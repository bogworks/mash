package endpoint

// S5 acceptance helpers (contract 0.6.0-sdk-adoption: REQ-008, REQ-015, REQ-016, REQ-021; AT-008, AT-015, AT-016,
// AT-022). The tests in the s5_caller_at_*_test.go files are written before the S5 constructor exists. They reach it
// through one seam, declared here, and skip (naming the AT item) until the sdk lane sets it.
//
// WHAT SDK MUST DO. In one _test.go file of this package (for example s5_caller_wire_test.go, written by sdk), set
//
//	func init() { newS5Caller = func(t *testing.T, env s5Env) s5Caller { ... } }
//
// to return the mash caller constructor S5 builds over env.NC. Under ruling (A), discovery, presence and the prompt
// sender stay in mash's own code (internal/endpoint); the SDK caller is used only where sdk later decides (reply
// inbox, stream). The constructor is whatever S5 builds around mash's discovery: these tests exercise mash's own
// discovery path (presence, applyPresence, the $SRV.INFO answer handling), not the SDK's Discover. It has:
//
//   - the reply prefix env.Asker.InboxPrefix() (caller.WithInboxPrefix), so every reply subject is
//     _INBOX.<owner>.<flat>.<client-id>.<route> (REQ-015, AT-016);
//   - the identity env.Creds (the asker's creds file contents) signing the Agent-Sender header with display name
//     env.Name. The header must be what main's promptMsg gives, byte for byte apart from the values of ts, nonce and
//     sig, and the prompt payload must be main's bare text (SAP 0.3 section 5.1). env.Inputs.Now and env.Inputs.Nonce
//     are offered for a constructor that can fix them; if it cannot (clock and nonce drawn inside), AT-008 masks
//     those three values and checks instead that the new signature verifies over the payload;
//   - mash's discovery verification: a registration is accepted only when its id_sig verifies and its prompt subject,
//     owner, address, account and user_nkey all agree with env.Directory's record for the address asked for
//     (REQ-016);
//   - discovery windows no longer than env.Window (0 means the defaults).
//
// The random tokens (client id, route, nonce) are NOT injected through the seam. The SDK reads crypto/rand directly,
// so the tests fix them with testing/cryptotest.SetGlobalRandom, which replaces crypto/rand for the whole process.
// That is "the fixed test random source" of REQ-015 and the replaced entropy source of AT-016, applied to the
// caller's real path. The constructor must therefore draw every random token (client id, route, nonce) from
// crypto/rand at call time, not at package init.
//
// Contract of each s5Caller method is on the interface below.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
// crypto/rand, fixed by the test with cryptotest.SetGlobalRandom.)
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

// newS5Caller is set by the sdk lane (see the comment at the top of this file): whatever S5 builds around mash's own
// discovery. Nil until then.
var newS5Caller func(t *testing.T, env s5Env) s5Caller

// needS5Caller skips the test while the constructor has not landed. item names the AT item, so a skipped line says
// what is not yet checked.
func needS5Caller(t *testing.T, item string) {
	t.Helper()
	if newS5Caller == nil {
		t.Skip(item + ": S5 constructor not landed: sdk wires newS5Caller")
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
}

func (z *s5Fix) tap(t *testing.T, subjects ...string) *s5Tap {
	t.Helper()
	tp := &s5Tap{}
	nc := z.e.Conn(t, z.e.AdminCreds)
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

func (an *s5Answerer) publish(t *testing.T, reply string, data []byte) {
	t.Helper()
	if err := an.c.nc.Publish(reply, data); err != nil {
		t.Fatal(err)
	}
	if err := an.c.nc.Flush(); err != nil {
		t.Fatal(err)
	}
}

// s5RaceMode says where the valid answer comes relative to the invalid ones.
type s5RaceMode string

const (
	s5Before s5RaceMode = "invalid-before-valid" // carol's invalid answers, then (after a pause) bob's valid one
	s5After  s5RaceMode = "invalid-after-valid"  // bob's valid answer, then (after a pause) carol's invalid ones
	s5None   s5RaceMode = "invalid-only"         // no valid answer at all
)

const s5RacePause = 250 * time.Millisecond // inside the discovery's stall, past any delivery delay on one server

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

const s5Base32 = "abcdefghijklmnopqrstuvwxyz234567"

// s5Layout splits a reply subject of the ruled layout _INBOX.<owner>.<flat>.<client-id>.<route> and reports a
// problem, or "" when it is as ruled. The route token must be at least 26 base32 characters (130 bits).
func s5Layout(asker sap.Address, subject string) (clientID, route, problem string) {
	pre := asker.InboxPrefix() + "."
	if !strings.HasPrefix(subject, pre) {
		return "", "", fmt.Sprintf("%q is outside the owner-scoped prefix %q", subject, pre)
	}
	toks := strings.Split(strings.TrimPrefix(subject, pre), ".")
	if len(toks) != 2 {
		return "", "", fmt.Sprintf("%q has %d tokens after the prefix, want 2 (client id, route)", subject, len(toks))
	}
	clientID, route = toks[0], toks[1]
	if clientID == "" {
		return "", "", fmt.Sprintf("%q has an empty client id token", subject)
	}
	if len(route) < 26 {
		return "", "", fmt.Sprintf("%q: route token has %d characters, want at least 26 (128 bits)", subject, len(route))
	}
	if strings.Trim(route, s5Base32) != "" {
		return "", "", fmt.Sprintf("%q: route token is not lowercase base32", subject)
	}
	return clientID, route, ""
}

// s5RandomRoute is a guess: a random base32 token of a route's length.
func s5RandomRoute() string {
	var b [26]byte
	_, _ = rand.Read(b[:])
	for i := range b {
		b[i] = s5Base32[int(b[i])%32]
	}
	return string(b[:])
}

// s5IncrementBase32 reads tok as a lowercase base32 number and returns tok+n (same width, wrapping).
func s5IncrementBase32(tok string, n int) string {
	b := []byte(tok)
	for ; n > 0; n-- {
		for i := len(b) - 1; i >= 0; i-- {
			j := strings.IndexByte(s5Base32, b[i])
			if j < 31 {
				b[i] = s5Base32[j+1]
				break
			}
			b[i] = s5Base32[0]
		}
	}
	return string(b)
}

// s5Guesses lists the subjects a same-owner observer who saw one reply subject would try: the NUID successors and
// simple increments of the per-request token under the prefix with and without the client-id token, and the client
// id with random route tokens.
func s5Guesses(seen string) []string {
	dot := strings.LastIndex(seen, ".")
	if dot < 0 {
		return nil
	}
	head, route := seen[:dot], seen[dot+1:]
	var cid, prefix string
	if d2 := strings.LastIndex(head, "."); d2 >= 0 {
		prefix, cid = head[:d2], head[d2+1:]
	}
	var toks []string
	if len(route) >= 22 {
		toks = append(toks, nextNUIDs(route[:22], 1, 700)...)
	}
	for n := 1; n <= 200; n++ {
		toks = append(toks, s5IncrementBase32(route, n))
	}
	var out []string
	for _, tk := range toks {
		out = append(out, head+"."+tk) // the client-id token seen, a guessed route
		if prefix != "" {
			out = append(out, prefix+"."+tk) // one token under the flat prefix (main's layout)
		}
	}
	for i := 0; i < 100; i++ {
		out = append(out, head+"."+s5RandomRoute())
	}
	if cid != "" && prefix != "" { // a guessed client id with the route seen
		out = append(out, prefix+"."+s5IncrementBase32(cid, 1)+"."+route)
	}
	return out
}

// s5Evil is the reply chunk an attacker publishes: a response chunk that, accepted, would show up in the output.
var s5Evil = []byte(`{"type":"response","data":"EVIL"}`)

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

// sdkIdentity is identity for the callers that take the SDK identity (Prompt).
func sdkIdentity(t *testing.T, creds string) sapsdk.Identity {
	t.Helper()
	id, err := sapsdk.IdentityFromCredsAt(creds, mustRead(t, creds))
	if err != nil {
		t.Fatal(err)
	}
	return id
}
