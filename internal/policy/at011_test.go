package policy

import (
	"encoding/json"
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
	Applied string `json:"applied"`
	// Connection is "kept" when the connection stayed open with no reconnect, "closed" or "reconnected" otherwise.
	Connection string `json:"connection"`
	// UnchangedSubjects is "no_loss_outside_the_disconnect" when every message to a subject the change did not touch
	// reached the observer, except possibly some sent after the push and before the client had reconnected.
	UnchangedSubjects string `json:"unchanged_subjects"`
	// FreshConnection is how a connection opened right after the push saw the change: "changed" or "old".
	FreshConnection string `json:"fresh_connection"`
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
	scope := jwt.NewUserScope()
	scope.Key = e.keyPub
	scope.Role = "K"
	scope.Template.Permissions = perms
	ac.SigningKeys.AddScopedSigner(scope)
	return at011Must(ac.Encode(e.op))
}

func newAT011Env(t *testing.T, before jwt.Permissions) *at011Env {
	t.Helper()
	e := &at011Env{t: t}
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
	s := at011Must(server.NewServer(&server.Options{
		Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		TrustedOperators: []*jwt.OperatorClaims{op}, AccountResolver: resolver, SystemAccount: sysPub,
	}))
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
	})
	if t.Failed() {
		return
	}
	got := at011Record_{NATSServerVersion: server.VERSION, Outcome: measured}

	if os.Getenv(at011UpdateEnv) != "" {
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
	if !reflect.DeepEqual(want.Outcome, got.Outcome) {
		wj, _ := json.Marshal(want.Outcome)
		gj, _ := json.Marshal(got.Outcome)
		t.Fatalf("AT-011: the outcome on nats-server %s differs from the record in %s.\nrecorded: %s\nmeasured: %s\nWhat mash may rely on has changed: rerun with %s=1, commit the new record, and update ADR 0097 and the requirements that cite it.",
			got.NATSServerVersion, at011Record, wj, gj, at011UpdateEnv)
	}
}
