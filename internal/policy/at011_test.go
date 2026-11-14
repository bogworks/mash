package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

// AT-011 / REQ-012: does narrowing a scoped signing key's template, then pushing the new account JWT, reach a client
// connection that is already open, without a reconnect? The answer is a property of the nats-server version, so the
// test measures it on every run and compares the outcome classes with the record in testdata/at011-result.json.

const (
	at011Record    = "testdata/at011-result.json"
	at011UpdateEnv = "MASH_AT011_UPDATE"

	// at011Bound is the longest the test waits, after the push is acknowledged, for the change to show on the sender's
	// connection. The server closes the connection at the push and the client reconnects (its reconnect wait is
	// at011Poll here): the measured maximum was 123 ms, over several runs under -race with eight busy loops running
	// on a 16-core host. The bound is more than twenty times that, so a loaded CI host does not flake. A change not
	// seen within the bound is recorded as "not_applied".
	at011Bound = 3 * time.Second
	// at011Poll is the interval at which the open connection publishes to the changed subject.
	at011Poll = 10 * time.Millisecond
	// at011Settle is how long the observer waits for a message that should not arrive.
	at011Settle = 300 * time.Millisecond
	// at011Slack widens the window of the disconnect before the push is acknowledged.
	at011Slack = 100 * time.Millisecond
	// at011Stream is the interval of the mail sent to the unchanged subject while the change is pushed.
	at011Stream = 2 * time.Millisecond

	// at011MailAck is how long a mail publish through JetStream waits for its acknowledgement.
	at011MailAck = 2 * time.Second
	// at011MailLead is how long mail flows before the push, so that the push lands in a running stream.
	at011MailLead = 150 * time.Millisecond
	// at011MailStream is the stream the mail is stored in: the shape of mash's inbox stream (store.inboxStreamConfig)
	// on the subject the key may publish to.
	at011MailStream = "inbox-at011"
	// at011MailScenario is the name of the mail scenario in the record.
	at011MailScenario = "mail_at_close"

	at011Wait         = 10 * time.Second
	at011ResolverSync = 5 * time.Minute
	at011Unchanged    = "agents.mail.a"
	at011Inbox        = "_INBOX.at011"
)

// at011Outcome is the outcome class of one scenario. Delays are logged, never recorded.
type at011Outcome struct {
	// Applied is "open_connection" when the change showed with the connection still the one opened before the push,
	// "after_reconnect" when it showed only after the server closed that connection and the client reconnected, and
	// "not_applied" when it did not show within the bound.
	Applied string `json:"applied,omitempty"`
	// Connection is "kept" when the connection stayed open with no reconnect, "closed" or "reconnected" otherwise.
	Connection string `json:"connection,omitempty"`
	// UnchangedSubjects is "no_loss_outside_the_disconnect" when every message to a subject the change did not touch
	// reached the observer, except possibly some sent after the push and before the client had reconnected.
	UnchangedSubjects string `json:"unchanged_subjects,omitempty"`
	// FreshConnection is how a connection opened right after the push saw the change: "changed" or "old".
	FreshConnection string `json:"fresh_connection,omitempty"`

	// The fields below are set only by the mail scenario, which publishes mail through JetStream at the instant the
	// server closes the connection. They are classes that hold on every run. Whether a message is lost at the close at
	// all is probabilistic and is logged, not recorded.

	// MailAckedAndMissing is "none" when every mail publish that was acknowledged is in the stream, as the contract of an
	// acknowledgement says it must be.
	MailAckedAndMissing string `json:"mail_acked_and_missing,omitempty"`
	// MailSilentLoss is "none" when every mail publish that is not in the stream made the publisher see an error: no
	// message was lost without the publisher being told (an acknowledgement that never came counts as an error).
	MailSilentLoss string `json:"mail_silent_loss,omitempty"`
}

type at011Record_ struct {
	NATSServerVersion string                  `json:"nats_server_version"`
	Outcome           map[string]at011Outcome `json:"outcome"`
}

type at011Scenario struct {
	name    string
	tags    []string
	before  jwt.Permissions
	after   jwt.Permissions
	changed string // the subject whose permission the change flips
	widen   bool   // the change allows the subject, rather than refusing it
}

func at011Perms(allow ...string) jwt.Permissions {
	var p jwt.Permissions
	p.Pub.Allow.Add(allow...)
	return p
}

func at011Scenarios() []at011Scenario {
	const b = "agents.mail.b"
	both := at011Perms(at011Inbox, at011Unchanged, b)
	onlyA := at011Perms(at011Inbox, at011Unchanged)
	tagged := at011Perms(at011Inbox, "agents.mail.{{tag(allow)}}", "agents.status.{{tag(allow)}}")
	untagged := at011Perms(at011Inbox, "agents.mail.{{tag(allow)}}")
	tags := []string{"allow:a"}
	return []at011Scenario{
		{name: "fixed_narrow", before: both, after: onlyA, changed: b},
		{name: "fixed_widen", before: onlyA, after: both, changed: b, widen: true},
		{name: "tag_narrow", tags: tags, before: tagged, after: untagged, changed: "agents.status.a"},
		{name: "tag_widen", tags: tags, before: untagged, after: tagged, changed: "agents.status.a", widen: true},
	}
}

// at011Env is an operator-mode server with a full account resolver, the kind mashctl pushes to, and a system user.
type at011Env struct {
	t       *testing.T
	op      nkeys.KeyPair
	acct    nkeys.KeyPair
	acctPub string
	key     nkeys.KeyPair // the scoped signing key K
	keyPub  string
	srv     *server.Server
	sys     *nats.Conn
	js      bool // the account has JetStream
}

// at011Must returns v, or panics: a setup failure aborts the test and the panic names it.
func at011Must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("at011 setup: %v", err))
	}
	return v
}

func (e *at011Env) accountJWT(perms jwt.Permissions) string {
	ac := jwt.NewAccountClaims(e.acctPub)
	ac.Name = "MASH"
	if e.js {
		ac.Limits.JetStreamLimits = jwt.JetStreamLimits{DiskStorage: 64 << 20, Streams: 5, Consumer: 5}
	}
	scope := jwt.NewUserScope()
	scope.Key = e.keyPub
	scope.Role = "K"
	scope.Template.Permissions = perms
	ac.SigningKeys.AddScopedSigner(scope)
	return at011Must(ac.Encode(e.op))
}

func newAT011Env(t *testing.T, before jwt.Permissions) *at011Env {
	t.Helper()
	return newAT011EnvJS(t, before, false)
}

// newAT011EnvJS is newAT011Env with JetStream on the server and in the account's limits when js is set.
func newAT011EnvJS(t *testing.T, before jwt.Permissions, js bool) *at011Env {
	t.Helper()
	e := &at011Env{t: t, js: js}
	e.op = at011Must(nkeys.CreateOperator())
	opPub := at011Must(e.op.PublicKey())
	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "at011"
	opJWT := at011Must(oc.Encode(e.op))
	op := at011Must(jwt.DecodeOperatorClaims(opJWT))

	e.acct = at011Must(nkeys.CreateAccount())
	e.acctPub = at011Must(e.acct.PublicKey())
	e.key = at011Must(nkeys.CreateAccount())
	e.keyPub = at011Must(e.key.PublicKey())

	sysKP := at011Must(nkeys.CreateAccount())
	sysPub := at011Must(sysKP.PublicKey())
	sc := jwt.NewAccountClaims(sysPub)
	sc.Name = "SYS"

	resolver := at011Must(server.NewDirAccResolver(t.TempDir(), 0, at011ResolverSync, server.NoDelete))
	if err := resolver.Store(sysPub, at011Must(sc.Encode(e.op))); err != nil {
		t.Fatal(err)
	}
	if err := resolver.Store(e.acctPub, e.accountJWT(before)); err != nil {
		t.Fatal(err)
	}
	opts := &server.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		TrustedOperators: []*jwt.OperatorClaims{op}, AccountResolver: resolver, SystemAccount: sysPub,
	}
	if js {
		opts.JetStream, opts.StoreDir = true, t.TempDir()
	}
	s := at011Must(server.NewServer(opts))
	go s.Start()
	if !s.ReadyForConnections(at011Wait) {
		t.Fatal("nats-server did not start")
	}
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })
	e.srv = s

	sysUser := at011Must(nkeys.CreateUser())
	sysUserPub := at011Must(sysUser.PublicKey())
	tok := at011Must(jwt.NewUserClaims(sysUserPub).Encode(sysKP))
	e.sys = at011Must(nats.Connect(s.ClientURL(), nats.UserJWT(
		func() (string, error) { return tok, nil }, func(n []byte) ([]byte, error) { return sysUser.Sign(n) })))
	t.Cleanup(e.sys.Close)
	return e
}

// push sends the account JWT as mashctl does, a claims update on the system account, and waits for the server's answer.
func (e *at011Env) push(token string) {
	e.t.Helper()
	msg, err := e.sys.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(token), at011Wait)
	if err != nil {
		e.t.Fatalf("claims update: %v", err)
	}
	if strings.Contains(string(msg.Data), `"error"`) {
		e.t.Fatalf("claims update refused: %s", msg.Data)
	}
}

// conn is one connected user: signed by the scoped key K with tags (when scoped) or by the account key.
type at011Conn struct {
	nc         *nats.Conn
	mu         sync.Mutex
	violations map[string]int // subject -> permission violations reported
	closed     atomic.Int32
	reconnects atomic.Int32
	disconnect atomic.Int32

	disconnectAt atomic.Int64 // unix nanoseconds of the first disconnect and the first reconnect, 0 for none
	reconnectAt  atomic.Int64
	disconnectEr string // the error the first disconnect reported
}

// sinceMS is the milliseconds from t0 to the time held in at, or -1 when it was never set.
func (c *at011Conn) sinceMS(at *atomic.Int64, t0 time.Time) int64 {
	n := at.Load()
	if n == 0 {
		return -1
	}
	return time.Unix(0, n).Sub(t0).Milliseconds()
}

func (c *at011Conn) disconnectError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disconnectEr
}

func (e *at011Env) connect(scoped bool, tags []string) *at011Conn {
	e.t.Helper()
	uk := at011Must(nkeys.CreateUser())
	upub := at011Must(uk.PublicKey())
	uc := jwt.NewUserClaims(upub)
	uc.Tags.Add(tags...)
	signer := e.acct
	if scoped {
		uc.IssuerAccount = e.acctPub
		// A scoped signer's users carry no permissions or limits of their own: the template supplies them.
		uc.UserPermissionLimits = jwt.UserPermissionLimits{}
		signer = e.key
	}
	tok := at011Must(uc.Encode(signer))
	c := &at011Conn{violations: map[string]int{}}
	nc, err := nats.Connect(e.srv.ClientURL(),
		nats.UserJWT(func() (string, error) { return tok, nil }, func(n []byte) ([]byte, error) { return uk.Sign(n) }),
		nats.ReconnectWait(at011Poll), nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if c.disconnect.Add(1) == 1 {
				c.disconnectAt.Store(time.Now().UnixNano())
				c.mu.Lock()
				c.disconnectEr = fmt.Sprint(err)
				c.mu.Unlock()
			}
		}),
		nats.ReconnectHandler(func(*nats.Conn) {
			if c.reconnects.Add(1) == 1 {
				c.reconnectAt.Store(time.Now().UnixNano())
			}
		}),
		nats.ClosedHandler(func(*nats.Conn) { c.closed.Add(1) }),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			const marker = `Permissions Violation for Publish to "`
			if i := strings.Index(err.Error(), marker); i >= 0 {
				subj := strings.TrimSuffix(err.Error()[i+len(marker):], `"`)
				c.mu.Lock()
				c.violations[subj]++
				c.mu.Unlock()
			}
		}))
	if err != nil {
		e.t.Fatalf("connect (scoped=%v): %v", scoped, err)
	}
	e.t.Cleanup(nc.Close)
	c.nc = nc
	return c
}

func (c *at011Conn) violationsOn(subject string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.violations[subject]
}

// publish sends one message and waits for the server to have processed it, so a refusal is on its way back.
func (c *at011Conn) publish(subject string, data []byte) {
	_ = c.nc.Publish(subject, data)
	_ = c.nc.Flush()
}

// at011Run measures one scenario. The returned delay is from the acknowledged push to the first publish on the open
// connection that the change affected (a refusal when narrowing, a delivery when widening), or the bound when none.
func at011Run(t *testing.T, sc at011Scenario) (at011Outcome, time.Duration) {
	t.Helper()
	e := newAT011Env(t, sc.before)
	observer := e.connect(false, nil)
	changed := make(chan struct{}, 1024)  // deliveries from the open connection
	freshGot := make(chan struct{}, 1024) // deliveries from the connection opened after the push
	unchanged := make(chan string, 4096)
	inbox := make(chan struct{}, 1024)
	for _, s := range []struct {
		subj string
		fn   func(*nats.Msg)
	}{
		{sc.changed, func(m *nats.Msg) {
			if string(m.Data) == "fresh" {
				freshGot <- struct{}{}
				return
			}
			changed <- struct{}{}
		}},
		{at011Unchanged, func(m *nats.Msg) { unchanged <- string(m.Data) }},
		{at011Inbox, func(*nats.Msg) { inbox <- struct{}{} }},
	} {
		at011Must(observer.nc.Subscribe(s.subj, s.fn))
	}
	if err := observer.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	drain := func() {
		for len(changed) > 0 {
			<-changed
		}
	}

	sender := e.connect(true, sc.tags)
	// Before the change: the unchanged subjects are allowed, and the changed one is allowed unless the change widens it.
	sender.publish(sc.changed, []byte("before"))
	sender.publish(at011Unchanged, []byte("before"))
	sender.publish(at011Inbox, nil)
	if sc.widen {
		time.Sleep(at011Settle)
		if len(changed) != 0 || sender.violationsOn(sc.changed) == 0 {
			t.Fatalf("%s: the changed subject was not refused before the change", sc.name)
		}
	} else {
		select {
		case <-changed:
		case <-time.After(at011Wait):
			t.Fatalf("%s: the changed subject was not delivered before the change", sc.name)
		}
	}
	drain()

	// An account JWT is replaced only by a newer one, and its issue time has a resolution of one second.
	time.Sleep(time.Second)
	token := e.accountJWT(sc.after)

	// A stream of mail to the unchanged subject runs through the transition.
	var sent atomic.Int64
	var sentAt []time.Time // sentAt[n-1] is when message n was sent; read after the stream has stopped
	stop := make(chan struct{})
	var streamDone sync.WaitGroup
	streamDone.Add(1)
	go func() {
		defer streamDone.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(at011Stream):
			}
			n := sent.Add(1)
			sentAt = append(sentAt, time.Now())
			_ = sender.nc.Publish(at011Unchanged, []byte(fmt.Sprint(n)))
		}
	}()

	e.push(token)
	pushed := time.Now()
	baseline := sender.violationsOn(sc.changed)

	// A connection opened right after the acknowledged push shows what the server now enforces.
	freshSaw := make(chan string, 1)
	go func() {
		fresh := e.connect(true, sc.tags)
		fresh.publish(sc.changed, []byte("fresh"))
		time.Sleep(at011Settle)
		saw := "old"
		if (len(freshGot) > 0) == sc.widen {
			saw = "changed"
		}
		freshSaw <- saw
	}()

	// Poll the open connection: publish to the changed subject until the change shows (a refusal when narrowing, a
	// delivery when widening) or the bound passes.
	var delay time.Duration
	applied := "not_applied"
	for time.Since(pushed) < at011Bound {
		sender.publish(sc.changed, []byte("after"))
		time.Sleep(at011Poll)
		hit := false
		if sc.widen {
			select {
			case <-changed:
				hit = true
			default:
			}
		} else {
			hit = sender.violationsOn(sc.changed) > baseline
		}
		if hit {
			delay, applied = time.Since(pushed), "open_connection"
			break
		}
	}
	if applied == "not_applied" {
		delay = at011Bound
	}
	time.Sleep(at011Settle) // the stream runs on a little past the change
	close(stop)
	streamDone.Wait()
	_ = sender.nc.Flush()

	// Mail lost in the transition can only be mail written to the socket the server was closing: it must have been sent
	// between the push and the moment the client reconnected. Anything else lost is a loss outside the disconnect.
	got := map[int]bool{}
	for drained := false; !drained; {
		select {
		case d := <-unchanged:
			if n, err := strconv.Atoi(d); err == nil {
				got[n] = true
			}
		case <-time.After(at011Settle):
			drained = true
		}
	}
	want := int(sent.Load())
	lost, outside := 0, 0
	reconnectedAt := time.Unix(0, sender.reconnectAt.Load())
	for n := 1; n <= want; n++ {
		if got[n] {
			continue
		}
		lost++
		at := sentAt[n-1]
		if sender.reconnectAt.Load() == 0 || at.Before(pushed.Add(-at011Slack)) || at.After(reconnectedAt) {
			outside++
		}
	}
	delivered := "no_loss_outside_the_disconnect"
	if outside > 0 || sender.violationsOn(at011Unchanged) != 0 {
		delivered = "loss_outside_the_disconnect"
	}

	conn := "kept"
	switch {
	case sender.closed.Load() > 0:
		conn = "closed"
	case sender.reconnects.Load() > 0 || sender.disconnect.Load() > 0:
		conn = "reconnected"
	}
	// A change seen only after the server closed the connection did not reach the open connection.
	if applied == "open_connection" && conn != "kept" {
		applied = "after_reconnect"
	}
	fresh := <-freshSaw
	out := at011Outcome{Applied: applied, Connection: conn, UnchangedSubjects: delivered, FreshConnection: fresh}
	t.Logf("AT011 %-13s applied=%s first-effect=%dms disconnect=%dms reconnect=%dms (%s) connection=%s unchanged=%s (sent=%d, lost=%d, lost outside the disconnect=%d) fresh=%s",
		sc.name, applied, delay.Milliseconds(), sender.sinceMS(&sender.disconnectAt, pushed), sender.sinceMS(&sender.reconnectAt, pushed),
		sender.disconnectError(), conn, delivered, want, lost, outside, fresh)
	return out, delay
}

func TestAT011TemplateChangeReachesOpenConnections(t *testing.T) {
	scenarios := at011Scenarios()
	measured := map[string]at011Outcome{}
	var mu sync.Mutex
	t.Run("measure", func(t *testing.T) {
		for _, sc := range scenarios {
			t.Run(sc.name, func(t *testing.T) {
				t.Parallel()
				out, _ := at011Run(t, sc)
				mu.Lock()
				measured[sc.name] = out
				mu.Unlock()
			})
		}
		t.Run(at011MailScenario, func(t *testing.T) {
			t.Parallel()
			out := at011RunMail(t)
			mu.Lock()
			measured[at011MailScenario] = out
			mu.Unlock()
		})
	})
	if t.Failed() {
		return
	}
	got := at011Record_{NATSServerVersion: server.VERSION, Outcome: measured}

	// A run narrowed with -run measures some scenarios only: it is compared on those and never writes the record.
	full := len(measured) == len(scenarios)+1

	if os.Getenv(at011UpdateEnv) != "" {
		if !full {
			t.Fatalf("%s needs every scenario to run", at011UpdateEnv)
		}
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.FromSlash(at011Record), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("recorded %s", at011Record)
		return
	}

	raw, err := os.ReadFile(filepath.FromSlash(at011Record))
	if err != nil {
		t.Fatalf("no AT-011 record: %v; run with %s=1 to write it, then update ADR 0097", err, at011UpdateEnv)
	}
	var want at011Record_
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("%s: %v", at011Record, err)
	}
	if want.NATSServerVersion != got.NATSServerVersion {
		t.Fatalf("AT-011: nats-server is %s, the record in %s was taken on %s. A server version change can change whether a template change reaches open connections. Rerun with %s=1 to measure again, commit the new record, and update ADR 0097 (and REQ-012/REQ-020 if the outcome changed).",
			got.NATSServerVersion, at011Record, want.NATSServerVersion, at011UpdateEnv)
	}
	compared := want.Outcome
	if !full {
		compared = map[string]at011Outcome{}
		for name := range measured {
			compared[name] = want.Outcome[name]
		}
	}
	if !reflect.DeepEqual(compared, got.Outcome) {
		wj, _ := json.Marshal(compared)
		gj, _ := json.Marshal(got.Outcome)
		t.Fatalf("AT-011: the outcome on nats-server %s differs from the record in %s.\nrecorded: %s\nmeasured: %s\nWhat mash may rely on has changed: rerun with %s=1, commit the new record, and update ADR 0097 and the requirements that cite it.",
			got.NATSServerVersion, at011Record, wj, gj, at011UpdateEnv)
	}
}

// at011MailRec is one mail publish: what the publisher saw, then whether the message is in the stream.
type at011MailRec struct {
	id    int
	api   string // "legacy_sync", "sync" or "async"
	state string // "acked", "error" or "unresolved" (an async publish whose acknowledgement neither came nor failed)
	kind  string // the error kind, for state "error"
}

// at011ErrKind names an error a publisher saw, so the kinds can be counted.
func at011ErrKind(err error) string {
	for _, k := range []struct {
		err  error
		name string
	}{
		{nats.ErrTimeout, "nats.ErrTimeout"},
		{context.DeadlineExceeded, "context.DeadlineExceeded"},
		{jetstream.ErrNoStreamResponse, "ErrNoStreamResponse"},
		{nats.ErrNoResponders, "ErrNoResponders"},
		{nats.ErrConnectionClosed, "ErrConnectionClosed"},
		{nats.ErrDisconnected, "ErrDisconnected"},
		{nats.ErrConnectionReconnecting, "ErrConnectionReconnecting"},
		{jetstream.ErrAsyncPublishTimeout, "ErrAsyncPublishTimeout"},
	} {
		if errors.Is(err, k.err) {
			return k.name
		}
	}
	return "other: " + err.Error()
}

// at011RunMail measures what a publisher sees when mail is published through JetStream at the instant the server
// closes its connection on a template change. The change is the unrelated-subject narrowing of fixed_narrow: the mail
// subject stays allowed, so only the close matters. Three kinds of publisher run on the one connection, each in a tight
// loop through the push: the legacy synchronous publish, the synchronous publish of the jetstream package (what mash
// uses), and its asynchronous publish in bursts, which keeps several messages in flight at the close. Afterwards the
// stream is read and every publish is classified by what the publisher saw and whether the message is stored.
func at011RunMail(t *testing.T) at011Outcome {
	t.Helper()
	sc := at011Scenarios()[0] // fixed_narrow: the mail subject, at011Unchanged, is in both templates
	e := newAT011EnvJS(t, sc.before, true)
	owner := e.connect(false, nil)
	ljsOwner := at011Must(owner.nc.JetStream())
	cfg := &nats.StreamConfig{
		Name: at011MailStream, Subjects: []string{at011Unchanged}, Storage: nats.FileStorage, Retention: nats.LimitsPolicy,
		MaxMsgs: -1, MaxBytes: 32 << 20, MaxMsgsPerSubject: -1, MaxMsgSize: 1 << 20, Discard: nats.DiscardOld,
		Duplicates: 2 * time.Minute, DenyDelete: true, DenyPurge: true, AllowDirect: true, Replicas: 1,
	}
	var addErr error
	for deadline := time.Now().Add(at011Wait); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, addErr = ljsOwner.AddStream(cfg); addErr == nil {
			break
		}
	}
	if addErr != nil {
		t.Fatalf("create the stream: %v", addErr)
	}

	sender := e.connect(true, nil)
	ljs := at011Must(sender.nc.JetStream())
	js := at011Must(jetstream.New(sender.nc, jetstream.WithPublishAsyncTimeout(at011MailAck)))
	if _, err := ljs.Publish(at011Unchanged, []byte("0")); err != nil {
		t.Fatalf("a mail publish before the change: %v", err)
	}

	var next atomic.Int64
	var mu sync.Mutex
	var recs []at011MailRec
	record := func(r at011MailRec) {
		mu.Lock()
		recs = append(recs, r)
		mu.Unlock()
	}
	finish := func(id int, api string, err error) {
		if err != nil {
			record(at011MailRec{id: id, api: api, state: "error", kind: at011ErrKind(err)})
			return
		}
		record(at011MailRec{id: id, api: api, state: "acked"})
	}

	// An account JWT is replaced only by a newer one, and its issue time has a resolution of one second.
	time.Sleep(time.Second)
	token := e.accountJWT(sc.after)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	type future struct {
		id int
		f  jetstream.PubAckFuture
	}
	var futures []future // read after the async publisher has stopped
	loop := func(every time.Duration, body func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				body()
				time.Sleep(every)
			}
		}()
	}
	for range 2 {
		loop(time.Millisecond, func() {
			id := int(next.Add(1))
			_, err := ljs.Publish(at011Unchanged, []byte(strconv.Itoa(id)), nats.AckWait(at011MailAck))
			finish(id, "legacy_sync", err)
		})
		loop(time.Millisecond, func() {
			id := int(next.Add(1))
			ctx, cancel := context.WithTimeout(context.Background(), at011MailAck)
			_, err := js.Publish(ctx, at011Unchanged, []byte(strconv.Itoa(id)))
			cancel()
			finish(id, "sync", err)
		})
	}
	loop(2*time.Millisecond, func() {
		for range 20 {
			id := int(next.Add(1))
			f, err := js.PublishAsync(at011Unchanged, []byte(strconv.Itoa(id)))
			if err != nil {
				finish(id, "async", err)
				continue
			}
			futures = append(futures, future{id, f})
		}
	})

	time.Sleep(at011MailLead)
	e.push(token)
	pushed := time.Now()
	for sender.reconnects.Load() == 0 && time.Since(pushed) < at011Bound {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(at011Settle) // mail runs on a little past the reconnect
	close(stop)
	wg.Wait()
	for _, f := range futures {
		select {
		case <-f.f.Ok():
			finish(f.id, "async", nil)
		case err := <-f.f.Err():
			finish(f.id, "async", err)
		case <-time.After(at011MailAck + at011Wait):
			record(at011MailRec{id: f.id, api: "async", state: "unresolved"})
		}
	}

	// Read the stream back: how many times each counter is in it.
	info, err := ljsOwner.StreamInfo(at011MailStream)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	stored := map[int]int{}
	var storedMsgs atomic.Int64
	var smu sync.Mutex
	sub, err := ljsOwner.Subscribe(at011Unchanged, func(m *nats.Msg) {
		if n, err := strconv.Atoi(string(m.Data)); err == nil {
			smu.Lock()
			stored[n]++
			smu.Unlock()
		}
		storedMsgs.Add(1)
	}, nats.OrderedConsumer(), nats.DeliverAll(), nats.BindStream(at011MailStream))
	if err != nil {
		t.Fatalf("read the stream: %v", err)
	}
	defer sub.Unsubscribe()
	for deadline := time.Now().Add(at011Wait); storedMsgs.Load() < int64(info.State.Msgs); {
		if time.Now().After(deadline) {
			t.Fatalf("read %d of %d messages in the stream", storedMsgs.Load(), info.State.Msgs)
		}
		time.Sleep(5 * time.Millisecond)
	}
	smu.Lock()
	defer smu.Unlock()

	var ackedStored, ackedMissing, errStored, errMissing, unresolvedMissing, duplicates int
	kinds := map[string]int{}
	byAPI := map[string]int{}
	mu.Lock()
	defer mu.Unlock()
	for _, r := range recs {
		byAPI[r.api]++
		n := stored[r.id]
		if n > 1 {
			duplicates++
		}
		switch {
		case r.state == "acked" && n > 0:
			ackedStored++
		case r.state == "acked":
			ackedMissing++
		case r.state == "error" && n > 0:
			errStored++
			kinds[r.kind+" (stored)"]++
		case r.state == "error":
			errMissing++
			kinds[r.kind+" (missing)"]++
		case n == 0:
			unresolvedMissing++
		}
	}
	silent := ackedMissing + unresolvedMissing
	conn := "kept"
	if sender.closed.Load() > 0 {
		conn = "closed"
	} else if sender.reconnects.Load() > 0 || sender.disconnect.Load() > 0 {
		conn = "reconnected"
	}
	t.Logf("AT011MAIL connection=%s published=%d (%v) acked_stored=%d acked_missing=%d error_stored=%d error_missing=%d silent_loss=%d duplicates=%d lost=%d error_kinds=%v disconnect=%dms reconnect=%dms",
		conn, len(recs), byAPI, ackedStored, ackedMissing, errStored, errMissing, silent, duplicates, errMissing+silent, kinds,
		sender.sinceMS(&sender.disconnectAt, pushed), sender.sinceMS(&sender.reconnectAt, pushed))

	if conn != "reconnected" {
		t.Errorf("the connection was %s: the push did not close it, so the close was not measured", conn)
	}
	out := at011Outcome{MailAckedAndMissing: "none", MailSilentLoss: "none"}
	if ackedMissing > 0 {
		t.Errorf("%d acknowledged mail publishes are not in the stream", ackedMissing)
		out.MailAckedAndMissing = "present"
	}
	if silent > 0 {
		t.Errorf("%d mail publishes were lost without the publisher seeing an error", silent)
		out.MailSilentLoss = "present"
	}
	return out
}
