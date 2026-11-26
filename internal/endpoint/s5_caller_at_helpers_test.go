package endpoint

// S5 acceptance helpers (contract 0.6.0-sdk-adoption: REQ-008, REQ-015, REQ-016, REQ-021; AT-008, AT-015, AT-016,
// AT-022). Under amendment 5 (A) and (i), discovery, presence and the prompt sender stay in mash's own code
// (internal/endpoint); there is no second caller. The tests in the s5_caller_at_*_test.go files reach main's discovery
// through one seam, declared here and set in s5_caller_wire_test.go: an Endpoint over the asker's connection, its
// presence probe (startPresence, awaitPresence, applyPresence), its recipient discovery (DiscoverRecipient) and its
// prompt sender. The seam has:
//
//   - the reply subjects main sends (secretInbox): _INBOX.<owner>.<flat>. and one token of 32 hex characters, 128
//     bits from crypto/rand (REQ-015, AT-016); the reply layout of CHG-15 (client-id and route tokens) fell away;
//   - the identity env.Creds signing the Agent-Sender header with display name env.Name, as main's promptMsg does.
//     Main draws the clock and the nonce inside promptMsg, so env.Inputs is not used by this seam: AT-008 masks ts,
//     nonce and sig from byte equality only, checks ts and the nonce for form, verifies the signature over the
//     captured bytes, and compares everything else byte for byte (amendment 5, N8);
//   - main's discovery verification (REQ-016): a registration is accepted only when its id_sig verifies and its
//     prompt subject, address, account, user_nkey and owner agree with env.Directory's record, through the S5 switch
//     (s5Switch, atomic; set by newS5Main for the test and restored with t.Cleanup);
//   - the presence window of main (PresenceQuiet, PresenceCap), which env.Window does not shorten.
//
// The random tokens are not injected: the tests swap the token seam of main's own secretInbox
// (tokenRand, service.go; default crypto/rand.Reader), "the fixed test random source" of REQ-015 and the replaced
// entropy source of AT-016, applied to main's real path. They never replace crypto/rand's global source: the embedded
// nats-server reads it, and that races under -race.
//
// Contract of each s5Caller method is on the interface below.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
	"github.com/bogworks/mash/internal/testenv"
)

// ---------------------------------------------------------------------------------------------------------------
// The seam.

// s5Inputs are the deterministic inputs of REQ-015 that the constructor takes. (The random source is the process's
// crypto/rand, seeded by the test through the token seam.)
type s5Inputs struct {
	Now   func() time.Time // the clock of the signed header; nil means time.Now
	Nonce string           // the nonce of the signed header; empty means a fresh one
}

// s5DirRecord is what the directory holds for one address.
type s5DirRecord struct {
	Address sap.Address
	User    string // user_nkey
	Account string
}

// s5Env is everything the constructor needs.
type s5Env struct {
	NC        *nats.Conn             // the asker's own connection: production permissions, CustomInboxPrefix = Asker.InboxPrefix()
	Asker     sap.Address            // the caller's own endpoint address
	Creds     []byte                 // contents of the asker's creds file (the key that signs)
	Identity  sap.Identity           // the same key as mash's own type, for a constructor that wants it
	Name      string                 // display name in Agent-Sender, as main passes it: the asker's flat key
	Directory map[string]s5DirRecord // flat key -> directory record, every endpoint of the mesh
	Inputs    s5Inputs
	Window    time.Duration // longest a discovery waits; 0 means the default
}

// s5Registration is one registration the caller accepted.
type s5Registration struct {
	Address       sap.Address
	PromptSubject string
	UserNKey      string
}

// s5Discovery is the result of one discovery.
type s5Discovery struct {
	Accepted    []s5Registration // verified registrations of the address asked for, nothing else
	NotVerified int              // answers rejected in this discovery as not verified (each counted once)
}

// s5Caller is the minimum surface of the mash caller constructor the acceptance tests use.
type s5Caller interface {
	// Discover publishes one $SRV.INFO.agents request and returns the verified registrations of target that
	// answered within the window, and how many answers (of any endpoint) were rejected as not verified in this
	// call. An invalid answer never ends the discovery early or fails it: a valid answer that arrives before, with
	// or after invalid ones is returned (SEC-12). Nothing found is an empty Accepted and a nil error.
	Discover(ctx context.Context, target sap.Address) (s5Discovery, error)

	// Prompt discovers target as Discover does, then prompts the verified registration's prompt subject with
	// text and returns the text of each response chunk in order. An unverified or absent target is an error (the
	// "not online" case of main) and nothing is published to any prompt subject. A text over MaxPromptBytes is
	// refused as main does: an error that names PromptTooLargeCode, and nothing is published.
	Prompt(ctx context.Context, target sap.Address, text string) (chunks []string, err error)

	// ReplySubjects lists the reply subjects this caller has used so far, in order of use, as far as it can tell
	// (it may not expose them: nil is allowed). Every one it lists must be a subject the server saw; the tests
	// take the complete set from the server.
	ReplySubjects() []string

	// NotVerified is the cumulative count of answers rejected as not verified (TO-12's total).
	NotVerified() int

	// Close ends the caller. It does not close env.NC.
	Close() error
}

// newS5Caller is set in s5_caller_wire_test.go: main's own discovery, presence and prompt sender over env.NC.
var newS5Caller func(t *testing.T, env s5Env) s5Caller

// needS5Caller fails the test when the seam has not been set. item names the AT item.
func needS5Caller(t *testing.T, item string) {
	t.Helper()
	if newS5Caller == nil {
		t.Fatal(item + ": the S5 seam is not wired (s5_caller_wire_test.go)")
	}
}

// ---------------------------------------------------------------------------------------------------------------
// The two-owner fixture of AT-022: owner acme with alice, bob and carol, owner zeta with mallory.

type s5Fix struct {
	*fixture
	alice, bob, carol, mallory sap.Address
}

const s5Group = "ops" // the owner-first address form: acme.ops.alice

func newS5Fix(t *testing.T) *s5Fix {
	t.Helper()
	e := testenv.Start(t)
	e.Owner = "acme"
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	z := &s5Fix{fixture: &fixture{e: e, adm: adm, creds: map[string]string{}, users: map[string]string{}}}
	z.alice = z.endpoint(t, "acme", "alice")
	z.bob = z.endpoint(t, "acme", "bob")
	z.carol = z.endpoint(t, "acme", "carol")
	z.mallory = z.endpoint(t, "zeta", "mallory")
	return z
}

// endpoint provisions one endpoint of owner with the production permissions (addAt mints with admin.Permissions) and
// fails the test unless its JWT carries exactly that output and nothing more.
func (z *s5Fix) endpoint(t *testing.T, owner, name string) sap.Address {
	t.Helper()
	prev := z.e.Owner
	z.e.Owner = owner
	a := z.addAt(t, s5Group, name, addOpts{})
	z.e.Owner = prev
	s5AssertExactPermissions(t, z.creds[a.Flat()], a)
	return a
}

// s5AssertExactPermissions decodes the user JWT in a creds file and fails unless its permissions are exactly the
// output of admin.Permissions(a): the same publish and subscribe allow lists, no deny list, no response permission.
func s5AssertExactPermissions(t *testing.T, credsPath string, a sap.Address) {
	t.Helper()
	raw, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	want := admin.Permissions(a)
	if want == nil {
		t.Fatalf("admin.Permissions(%s) is nil", a)
	}
	same := func(what string, got, exp []string) {
		g, w := append([]string(nil), got...), append([]string(nil), exp...)
		sort.Strings(g)
		sort.Strings(w)
		if strings.Join(g, "\n") != strings.Join(w, "\n") {
			t.Fatalf("%s: the JWT of %s carries %q, admin.Permissions gives %q", what, a, g, w)
		}
	}
	same("publish allow", uc.Pub.Allow, want.Pub.Allow)
	same("subscribe allow", uc.Sub.Allow, want.Sub.Allow)
	same("publish deny", uc.Pub.Deny, want.Pub.Deny)
	same("subscribe deny", uc.Sub.Deny, want.Sub.Deny)
	if uc.Resp != nil {
		t.Fatalf("the JWT of %s carries a response permission: %+v", a, uc.Resp)
	}
}

// dir is the directory as the caller's constructor receives it.
func (z *s5Fix) dir() map[string]s5DirRecord {
	m := map[string]s5DirRecord{}
	for _, a := range []sap.Address{z.alice, z.bob, z.carol, z.mallory} {
		m[a.Flat()] = s5DirRecord{Address: a, User: z.users[a.Flat()], Account: z.e.Account}
	}
	return m
}

// ---------------------------------------------------------------------------------------------------------------
// Connections that record the server's permission violations.

type s5Conn struct {
	nc      *nats.Conn
	mu      sync.Mutex
	errList []string
}

// connect opens a as the endpoint a with the connection's reply prefix a.InboxPrefix(), as mash's own clients do.
func (z *s5Fix) connect(t *testing.T, a sap.Address) *s5Conn {
	t.Helper()
	c := &s5Conn{}
	nc, err := nats.Connect(z.e.URL, append(natsauth.TLSOptions(z.e.CAFile, z.e.ClientCert, z.e.ClientKey), nats.Timeout(15*time.Second),
		nats.UserCredentials(z.creds[a.Flat()]), nats.CustomInboxPrefix(a.InboxPrefix()),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			c.mu.Lock()
			c.errList = append(c.errList, err.Error())
			c.mu.Unlock()
		}))...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	c.nc = nc
	return c
}

// errs is every error the server or the client library has reported to this connection so far.
func (c *s5Conn) errs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.errList...)
}

// violations are the permission violations the server has reported to this connection so far.
func (c *s5Conn) violations() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, e := range c.errList {
		if strings.Contains(strings.ToLower(e), "permissions violation") {
			out = append(out, e)
		}
	}
	return out
}

// violated waits up to d for a violation of kind ("subscription" or "publish") that names subject.
func (c *s5Conn) violated(kind, subject string, d time.Duration) bool {
	want, sub := "permissions violation for "+kind, strings.ToLower(subject)
	for end := time.Now().Add(d); ; time.Sleep(20 * time.Millisecond) {
		c.mu.Lock()
		for _, e := range c.errList {
			l := strings.ToLower(e)
			if strings.Contains(l, want) && strings.Contains(l, sub) {
				c.mu.Unlock()
				return true
			}
		}
		c.mu.Unlock()
		if time.Now().After(end) {
			return false
		}
	}
}

// settle makes sure the server has handled everything sent so far and that any violation it answered with has been
// delivered to the error handler.
func (c *s5Conn) settle(t *testing.T) {
	t.Helper()
	if err := c.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
}

// ---------------------------------------------------------------------------------------------------------------
// The subject capture: every message on the subjects of interest, seen by an unrestricted connection.

type s5Msg struct {
	Subject, Reply string
	Header         nats.Header // nil when the message had none
	Data           []byte
}

type s5Tap struct {
	mu   sync.Mutex
	msgs []s5Msg
	nc   *nats.Conn // the tap's own connection, flushed before the final read
}

func (z *s5Fix) tap(t *testing.T, subjects ...string) *s5Tap {
	t.Helper()
	tp := &s5Tap{}
	nc := z.e.Conn(t, z.e.AdminCreds)
	tp.nc = nc
	for _, s := range subjects {
		if _, err := nc.Subscribe(s, func(m *nats.Msg) {
			cp := s5Msg{Subject: m.Subject, Reply: m.Reply, Data: append([]byte(nil), m.Data...)}
			if len(m.Header) > 0 {
				cp.Header = nats.Header(http.Header(m.Header).Clone())
			}
			tp.mu.Lock()
			tp.msgs = append(tp.msgs, cp)
			tp.mu.Unlock()
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return tp
}

func (tp *s5Tap) all() []s5Msg {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return append([]s5Msg(nil), tp.msgs...)
}

// where returns the captured messages for which keep is true.
func (tp *s5Tap) where(keep func(s5Msg) bool) []s5Msg {
	var out []s5Msg
	for _, m := range tp.all() {
		if keep(m) {
			out = append(out, m)
		}
	}
	return out
}

// requestsFrom returns the discovery and prompt requests sent by asker, found by their reply subject under the
// asker's own prefix, so another endpoint's traffic on the same subjects is not counted.
func (tp *s5Tap) requestsFrom(asker sap.Address) (discovery, prompt []s5Msg) {
	for _, m := range tp.all() {
		if !strings.HasPrefix(m.Reply, asker.InboxPrefix()+".") {
			continue
		}
		if m.Subject == "$SRV.INFO."+sap.ServiceName {
			discovery = append(discovery, m)
		} else if strings.HasPrefix(m.Subject, "agents.prompt.") {
			prompt = append(prompt, m)
		}
	}
	return
}

// waitRequestsFrom is requestsFrom for a caller that knows how many requests it expects: the tap is its own
// connection and may not have received the last request when the traffic's reply returns, so it polls until at
// least wantDiscovery and wantPrompt are there (5 s at most), then flushes, settles 200 ms and reads again so an extra
// request is counted; it returns what it has, for the caller's exact-count check.
func (tp *s5Tap) waitRequestsFrom(t *testing.T, asker sap.Address, wantDiscovery, wantPrompt int) (discovery, prompt []s5Msg) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for {
		discovery, prompt = tp.requestsFrom(asker)
		if time.Now().After(end) {
			return
		}
		if len(discovery) >= wantDiscovery && len(prompt) >= wantPrompt {
			// The minimum is here; an extra request may still be on its way, and the callers' exact-count checks
			// must see it: flush the tap's connection, settle, and read again.
			if err := tp.nc.Flush(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(200 * time.Millisecond)
			return tp.requestsFrom(asker)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// replySubjectsFrom is every reply subject asker used, in the order the server saw them.
func (tp *s5Tap) replySubjectsFrom(asker sap.Address) []string {
	d, p := tp.requestsFrom(asker)
	var out []string
	for _, m := range append(d, p...) {
		out = append(out, m.Reply)
	}
	return out
}

func s5Wait(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(d); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// ---------------------------------------------------------------------------------------------------------------
// Deterministic inputs (AT-008, REQ-015).

// s5Fixed gives a clock fixed at the current second (so a live responder, which accepts a header within 30 s of its
// own clock, still accepts it) and a fixed nonce.
func s5Fixed() s5Inputs {
	now := time.Now().UTC().Truncate(time.Second)
	return s5Inputs{Now: func() time.Time { return now }, Nonce: "s5-fixed-nonce-0001"}
}

// env builds the seam's input for asker over a fresh connection of its own.
func (z *s5Fix) env(t *testing.T, asker sap.Address, in s5Inputs) (s5Env, *s5Conn) {
	t.Helper()
	c := z.connect(t, asker)
	raw, err := os.ReadFile(z.creds[asker.Flat()])
	if err != nil {
		t.Fatal(err)
	}
	return s5Env{NC: c.nc, Asker: asker, Creds: raw, Identity: identity(t, z.creds[asker.Flat()]), Name: asker.Flat(),
		Directory: z.dir(), Inputs: in, Window: 2 * time.Second}, c
}

// caller builds the constructor's caller for asker (the test must have called needS5Caller).
func (z *s5Fix) caller(t *testing.T, asker sap.Address, in s5Inputs) (s5Caller, *s5Conn) {
	t.Helper()
	env, c := z.env(t, asker, in)
	cl := newS5Caller(t, env)
	t.Cleanup(func() { _ = cl.Close() })
	return cl, c
}

// ---------------------------------------------------------------------------------------------------------------
// Real endpoints and the genuine registration.

// serve runs the endpoint a (lease, SAP service, mail loop) until the test ends. Prompts are answered with the text
// "pong from <instance>" unless onPrompt is given.
func (z *s5Fix) serveEcho(t *testing.T, a sap.Address, onPrompt func(*Endpoint, PromptEvent)) (*Endpoint, *events) {
	t.Helper()
	if onPrompt == nil {
		onPrompt = func(ep *Endpoint, p PromptEvent) { _ = ep.Reply(p.RequestID, "pong from "+a.Instance, true) }
	}
	return z.serveAt(t, a, onPrompt)
}

// genuineRegistration runs a's real service just long enough to read the registration it answers a discovery with,
// and stops it: the bytes the forged variants are made from, and the valid answer the race tests replay.
func (z *s5Fix) genuineRegistration(t *testing.T, a sap.Address) []byte {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, z.cfgAt(a))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := ep.StartService(ctx, func(PromptEvent) {}); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, (&events{}).deliver); close(done) }()
	defer func() { cancel(); <-done; ep.Close() }()
	// asked from alice's connection: the service may publish only to inboxes of its own owner
	m, err := z.connect(t, z.alice).nc.Request("$SRV.INFO."+sap.ServiceName, nil, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return m.Data
}

// ---------------------------------------------------------------------------------------------------------------
// Forged registrations (AT-015's negative list), made from the genuine answer with the real signing code.

type s5Answer struct {
	Name string
	Data []byte
}

// s5Forgeries is the negative list of AT-015, each answer naming bob's address:
//
//	unsigned       no id_sig
//	forged-sig     id_sig with a flipped byte
//	wrong-key      validly signed, by carol's key, for bob's address
//	subject        validly signed by bob's key but the prompt subject is carol's (differs from bob's record)
//	owner          validly signed by bob's key but metadata owner is another owner
//	account        validly signed by bob's key but metadata account is another account
//	user-nkey      validly signed by a key that is nobody's in the directory, in the user_nkey field
func (z *s5Fix) forgeries(t *testing.T, genuine []byte) []s5Answer {
	t.Helper()
	bobID, err := sapsdk.IdentityFromCredsAt(z.creds[z.bob.Flat()], mustRead(t, z.creds[z.bob.Flat()]))
	if err != nil {
		t.Fatal(err)
	}
	carolID, err := sapsdk.IdentityFromCredsAt(z.creds[z.carol.Flat()], mustRead(t, z.creds[z.carol.Flat()]))
	if err != nil {
		t.Fatal(err)
	}
	bobSubj, carolSubj := mustSubj(z.bob).Prompt, mustSubj(z.carol).Prompt

	// forge edits the genuine answer's metadata and its prompt endpoint subject, then signs with signer.
	forge := func(name string, signer *sapsdk.Identity, mod func(md map[string]string, subject *string)) s5Answer {
		var info map[string]any
		if err := json.Unmarshal(genuine, &info); err != nil {
			t.Fatal(err)
		}
		md := map[string]string{}
		for k, v := range info["metadata"].(map[string]any) {
			md[k], _ = v.(string)
		}
		subject := bobSubj
		if mod != nil {
			mod(md, &subject)
		}
		if signer != nil { // the signer's key and account replace the genuine ones; the signature covers them
			md["user_nkey"], md["account"] = signer.User, signer.Account
			sig, err := signer.IDSig(md["agent"], md["owner"], subject)
			if err != nil {
				t.Fatal(err)
			}
			md["id_sig"] = sig
		}
		info["metadata"] = md
		for _, ep := range info["endpoints"].([]any) {
			if e := ep.(map[string]any); e["name"] == "prompt" {
				e["subject"] = subject
			}
		}
		data, err := json.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		return s5Answer{Name: name, Data: data}
	}

	unsigned := forge("unsigned", nil, func(md map[string]string, _ *string) { delete(md, "id_sig") })
	forged := forge("forged-sig", nil, func(md map[string]string, _ *string) {
		raw, _ := base64.RawURLEncoding.DecodeString(md["id_sig"])
		raw[0] ^= 0xff
		md["id_sig"] = base64.RawURLEncoding.EncodeToString(raw)
	})
	wrongKey := forge("wrong-key", &carolID, nil)
	subject := forge("subject", &bobID, func(_ map[string]string, s *string) { *s = carolSubj })
	owner := forge("owner", &bobID, func(md map[string]string, _ *string) { md["owner"] = "zeta" })

	otherAcct, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	otherAcctPub, _ := otherAcct.PublicKey()
	bobOtherAcct, err := sapsdk.NewIdentity(seedOf(t, z.creds[z.bob.Flat()]), otherAcctPub)
	if err != nil {
		t.Fatal(err)
	}
	account := forge("account", &bobOtherAcct, nil)

	stranger, _ := nkeys.CreateUser()
	strangerSeed, _ := stranger.Seed()
	strangerID, err := sapsdk.NewIdentity(strangerSeed, z.e.Account)
	if err != nil {
		t.Fatal(err)
	}
	userNKey := forge("user-nkey", &strangerID, nil)

	all := []s5Answer{unsigned, forged, wrongKey, subject, owner, account, userNKey}
	for _, a := range all { // the real verification code agrees with how each answer was made
		var info struct {
			Metadata map[string]string `json:"metadata"`
		}
		if err := json.Unmarshal(a.Data, &info); err != nil {
			t.Fatal(err)
		}
		md := info.Metadata
		ok := sapsdk.VerifyIDSig(md["user_nkey"], md["account"], md["agent"], md["owner"], s5PromptSubjectOf(t, a.Data), md["id_sig"])
		if wantValid := a.Name != "unsigned" && a.Name != "forged-sig"; ok != wantValid {
			t.Fatalf("forgery %s: id_sig verifies=%v, want %v", a.Name, ok, wantValid)
		}
	}
	return all
}

// refused is the forgeries of AT-015's negative list: the discovery refuses and counts every one, the owner forgery
// included (REQ-016, behind the S5 switch).
func (z *s5Fix) refused(t *testing.T, genuine []byte) []s5Answer {
	t.Helper()
	return z.forgeries(t, genuine)
}

func s5PromptSubjectOf(t *testing.T, data []byte) string {
	t.Helper()
	var info struct {
		Endpoints []struct{ Name, Subject string } `json:"endpoints"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	for _, e := range info.Endpoints {
		if e.Name == "prompt" {
			return e.Subject
		}
	}
	t.Fatalf("no prompt endpoint in %s", data)
	return ""
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// seedOf is the user seed of a creds file.
func seedOf(t *testing.T, credsPath string) []byte {
	t.Helper()
	kp, err := jwt.ParseDecoratedNKey(mustRead(t, credsPath))
	if err != nil {
		t.Fatal(err)
	}
	seed, err := kp.Seed()
	if err != nil {
		t.Fatal(err)
	}
	return seed
}

// ---------------------------------------------------------------------------------------------------------------
// Answerers: a peer that watches discovery requests and publishes what the test tells it to.

// answerer is endpoint a's own connection subscribed to the discovery subject; every request's reply subject is sent
// to replies. Publishing is allowed to a on _INBOX.<owner>.> only, which is all the real endpoints can do.
type s5Answerer struct {
	c       *s5Conn
	replies chan string
}

func (z *s5Fix) answerer(t *testing.T, a sap.Address) *s5Answerer {
	t.Helper()
	an := &s5Answerer{c: z.connect(t, a), replies: make(chan string, 64)}
	if _, err := an.c.nc.Subscribe("$SRV.INFO."+sap.ServiceName, func(m *nats.Msg) {
		if m.Reply != "" {
			select {
			case an.replies <- m.Reply:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := an.c.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	return an
}

// s5RaceMode says where the valid answer comes relative to the invalid ones.
type s5RaceMode string

const (
	s5Before s5RaceMode = "invalid-before-valid" // carol's invalid answers, then (after a pause) bob's valid one
	s5After  s5RaceMode = "invalid-after-valid"  // bob's valid answer, then (after a pause) carol's invalid ones
	s5None   s5RaceMode = "invalid-only"         // no valid answer at all
)

// s5RacePause separates the two publishes of a race. pub flushes, so the server has the first answer before the second
// is published and delivers them in that order; the pause only has to keep the second out of the same read. It must be
// well inside PresenceQuiet (300 ms): the presence window closes that long after the last answer it has received, and
// a pause near it (it was 250 ms) lets a loaded -race run close the window before the second answer lands, so the
// probe never counts it.
const s5RacePause = 40 * time.Millisecond

// race starts carol (the same-owner peer) answering every discovery request with invalid, in one publish each ("one
// at a time", the caller picks the slice) or all together, and bob's connection answering with valid per mode. It
// returns when the goroutine has been told to stop at the end of the test.
func (z *s5Fix) race(t *testing.T, mode s5RaceMode, invalid []s5Answer, valid []byte) {
	t.Helper()
	carol := z.answerer(t, z.carol)
	bob := z.connect(t, z.bob)
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	pub := func(c *s5Conn, reply string, data []byte) {
		_ = c.nc.Publish(reply, data)
		_ = c.nc.Flush()
	}
	go func() {
		for {
			select {
			case <-stop:
				return
			case reply := <-carol.replies:
				sendInvalid := func() {
					for _, a := range invalid {
						pub(carol.c, reply, a.Data)
					}
				}
				switch mode {
				case s5Before:
					sendInvalid()
					time.Sleep(s5RacePause)
					pub(bob, reply, valid)
				case s5After:
					pub(bob, reply, valid)
					time.Sleep(s5RacePause)
					sendInvalid()
				case s5None:
					sendInvalid()
				}
			}
		}
	}()
}

// ---------------------------------------------------------------------------------------------------------------
// Reply-subject layout checks (REQ-015, AT-016).

// s5Layout splits a reply subject of main's layout, _INBOX.<owner>.<flat>.<token> (secretInbox), and reports a
// problem, or "" when it is as main's is: the owner-scoped prefix and one token of 32 lowercase hex characters, which
// is 128 bits.
func s5Layout(asker sap.Address, subject string) (token, problem string) {
	pre := asker.InboxPrefix() + "."
	if !strings.HasPrefix(subject, pre) {
		return "", fmt.Sprintf("%q is outside the owner-scoped prefix %q", subject, pre)
	}
	token = strings.TrimPrefix(subject, pre)
	if strings.Contains(token, ".") {
		return "", fmt.Sprintf("%q has more than one token after the prefix", subject)
	}
	if len(token) < 32 {
		return "", fmt.Sprintf("%q: the token has %d characters, want 32 hex (128 bits)", subject, len(token))
	}
	if !s5Hex32.MatchString(token) {
		return "", fmt.Sprintf("%q: the token is not 32 lowercase hex characters", subject)
	}
	return token, ""
}

// s5RandomToken is a guess: a random token of the form of main's.
func s5RandomToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// s5IncrementHex reads tok as a lowercase hex number and returns tok+n (same width, wrapping).
func s5IncrementHex(tok string, n int) string {
	const digits = "0123456789abcdef"
	b := []byte(tok)
	for ; n > 0; n-- {
		for i := len(b) - 1; i >= 0; i-- {
			j := strings.IndexByte(digits, b[i])
			if j < 15 {
				b[i] = digits[j+1]
				break
			}
			b[i] = digits[0]
		}
	}
	return string(b)
}

// s5Guesses lists the subjects a same-owner observer who saw one reply subject would try: the NUID successors of its
// first 22 characters (the form a NUID-only inbox would have), simple increments of the token, and random tokens of
// its form, all under the prefix.
func s5Guesses(seen string) []string {
	dot := strings.LastIndex(seen, ".")
	if dot < 0 {
		return nil
	}
	prefix, token := seen[:dot], seen[dot+1:]
	var toks []string
	if len(token) >= 22 {
		toks = append(toks, nextNUIDs(token[:22], 1, 700)...)
	}
	for n := 1; n <= 200; n++ {
		toks = append(toks, s5IncrementHex(token, n))
	}
	for i := 0; i < 100; i++ {
		toks = append(toks, s5RandomToken())
	}
	var out []string
	for _, tk := range toks {
		out = append(out, prefix+"."+tk)
	}
	return out
}

// s5Evil is the reply chunk an attacker publishes: a response chunk that, accepted, would show up in the output.
var s5Evil = []byte(`{"type":"response","data":"EVIL"}`)

// s5Hex32 is the token of main's reply subject: 32 lowercase hex characters, 128 bits.
var s5Hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

// observe starts a same-owner peer (carol) that watches $SRV.> and, for each discovery request, floods the guesses of
// s5Guesses for up to 3 s with the evil chunk (and a terminator), and with a replay of replay (a genuine answer) when
// it is not nil. It returns the number of attacked requests, readable at any time.
func (z *s5Fix) observe(t *testing.T, replay []byte) func() int {
	t.Helper()
	c := z.connect(t, z.carol)
	spy, err := c.nc.SubscribeSync("$SRV.>")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	var mu sync.Mutex
	attacked := 0
	go func() {
		for {
			m, err := spy.NextMsg(100 * time.Millisecond)
			select {
			case <-stop:
				return
			default:
			}
			if err != nil || !strings.HasPrefix(m.Subject, "$SRV.INFO") || !strings.HasPrefix(m.Reply, z.alice.InboxPrefix()+".") {
				continue
			}
			mu.Lock()
			attacked++
			mu.Unlock()
			guesses := s5Guesses(m.Reply)
			for end := time.Now().Add(3 * time.Second); time.Now().Before(end); {
				select {
				case <-stop:
					return
				default:
				}
				for _, g := range guesses {
					_ = c.nc.Publish(g, s5Evil)
					_ = c.nc.Publish(g, nil)
					if replay != nil {
						_ = c.nc.Publish(g, replay)
					}
				}
				_ = c.nc.Flush()
			}
		}
	}()
	return func() int { mu.Lock(); defer mu.Unlock(); return attacked }
}

// s5Join is the response text of a prompt as main's Prompt writes it.
func s5Join(chunks []string) string { return strings.Join(chunks, "") }

// s5Contains reports whether any message data in msgs holds s.
func s5Contains(msgs []s5Msg, s string) bool {
	for _, m := range msgs {
		if bytes.Contains(m.Data, []byte(s)) {
			return true
		}
	}
	return false
}
