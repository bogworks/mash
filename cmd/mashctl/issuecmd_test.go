package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	madmin "github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/issue"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/wranglerreg"
)

// seedRE is an nkeys seed: S, the type letter (U user, A account, O operator, X curve, N server, C cluster), 56 base32.
var seedRE = regexp.MustCompile(`S[UAOXNC][A-Z2-7]{56}`)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

const (
	issueHost  = "h1"
	issueGroup = "foo.c"
	issueWait  = 10 * time.Second
	issueTTL   = time.Minute
)

// issueEnv is an operator-mode server with the owner group foo.c's scoped signing key and template in the account JWT,
// a test wrangler registered for (h1, foo.c), and the issue service running on the admin connection.
type issueEnv struct {
	*applyEnv
	ls       listset.ListSet
	compiled *listset.Compiled
	lsPath   string
	wrangler nkeys.KeyPair
	wpub     string
	logs     *syncBuf
	nc       *nats.Conn // the wrangler's connection (the admin user: account setup that gives a wrangler only its publish permission is out of scope)
	adm      *madmin.Admin
	captured *syncBuf // every payload the account carried, as text
	stop     context.CancelFunc
	done     chan int
}

func issueListSet() listset.ListSet {
	return listset.ListSet{
		Range: "foo",
		Switches: []listset.SwitchEntry{
			{Group: issueGroup, Verb: listset.Mail, Switch: listset.Closed},
			{Group: issueGroup, Verb: listset.Prompt, Switch: listset.Closed},
			{Group: issueGroup, Verb: listset.Status, Switch: listset.Shared},
			{Group: issueGroup, Verb: listset.Heartbeat, Switch: listset.Closed},
		},
		Senders: []listset.SenderLists{
			{Sender: "foo.c.core", Allow: []string{"foo.c.lead"}},
			{Sender: "foo.c.lead"},
			{Sender: "foo.c.sdk"},
		},
	}
}

// mustT is its value, or a panic (which fails the test) when err is set: mustT(f()) for a two-value f.
func mustT[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// newIssueEnvNoService builds everything except the registry and the service.
func newIssueEnvNoService(t *testing.T) *issueEnv {
	t.Helper()
	e := &issueEnv{applyEnv: newApplyEnv(t, twoZones), logs: &syncBuf{}, captured: &syncBuf{}}
	e.env["MASH_ADMIN_CREDS"] = e.pl.AdminCredsPath()
	if _, errb, code := e.run("init"); code != 0 {
		t.Fatalf("init: %s", errb)
	}
	if err := os.Chmod(e.root, 0o700); err != nil { // the list set lives here: only its owner may write the directory
		t.Fatal(err)
	}
	e.ls = issueListSet()
	e.compiled = mustT(listset.Compile(e.ls))
	raw := mustT(json.Marshal(e.ls))
	e.lsPath = filepath.Join(e.root, "listset.json")
	if err := os.WriteFile(e.lsPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	// Publish the owner group's scoped key and template in the account JWT, as the policy gate will.
	gk := mustT(e.ks.GroupKey(issueGroup, true))
	gpub := mustT(gk.PublicKey())
	ac := e.claims()
	us := jwt.NewUserScope()
	us.Key = gpub
	for _, tpl := range e.compiled.Templates {
		if tpl.Group == issueGroup {
			us.Template.Permissions = tpl.Permissions
		}
	}
	ac.SigningKeys.AddScopedSigner(us)
	opkp, _, err := e.ks.Operator()
	if err != nil {
		t.Fatal(err)
	}
	tok := mustT(ac.Encode(opkp))
	sys := mustT(nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.env["MASH_SYS_CREDS"])))
	defer sys.Close()
	msg, err := sys.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(tok), issueWait)
	if err != nil || strings.Contains(string(msg.Data), `"error"`) {
		t.Fatalf("push: %v %v", err, msg)
	}

	e.wrangler = mustT(nkeys.CreateUser())
	e.wpub = mustT(e.wrangler.PublicKey())

	e.nc = mustT(nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.pl.AdminCredsPath())))
	t.Cleanup(e.nc.Close)
	if _, err := e.nc.Subscribe(">", func(m *nats.Msg) { fmt.Fprintf(e.captured, "%s %s\n", m.Subject, m.Data) }); err != nil {
		t.Fatal(err)
	}
	e.adm = mustT(madmin.New(e.nc, "alice"))
	return e
}

func (e *issueEnv) registerWrangler() {
	e.t.Helper()
	if _, errb, code := e.run("wrangler", "init", "--test-account"); code != 0 {
		e.t.Fatal(errb)
	}
	if _, errb, code := e.run("wrangler", "add", "--host", issueHost, "--group", issueGroup, "--key", e.wpub); code != 0 {
		e.t.Fatal(errb)
	}
}

func (e *issueEnv) start() {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e.stop = cancel
	ready := make(chan struct{})
	var once sync.Once
	oldCtx, oldReady := issueServeContext, issueServeReady
	issueServeContext = func() (context.Context, context.CancelFunc) { return ctx, cancel }
	issueServeReady = func() { once.Do(func() { close(ready) }) }
	e.t.Cleanup(func() { issueServeContext, issueServeReady = oldCtx, oldReady })
	e.done = make(chan int, 1)
	go func() {
		var out bytes.Buffer
		e.done <- runEnv([]string{"issue", "serve", "--test-account", "--list-set", e.lsPath}, func(k string) string { return e.env[k] }, &out, e.logs)
	}()
	select {
	case <-ready:
	case code := <-e.done:
		e.t.Fatalf("service exited %d: %s", code, e.logs.String())
	case <-time.After(issueWait):
		e.t.Fatal("service did not start")
	}
	e.t.Cleanup(func() { cancel(); <-e.done })
}

func newIssueEnv(t *testing.T) *issueEnv {
	e := newIssueEnvNoService(t)
	e.registerWrangler()
	e.start()
	return e
}

// ireq is one request being built: its fields, the user key pair behind the public key, and the signer of its proof.
type ireq struct {
	fields map[string]any
	user   nkeys.KeyPair
	signer nkeys.KeyPair
}

func (e *issueEnv) newReq(address string) *ireq {
	user := mustT(nkeys.CreateUser())
	return e.newReqFor(address, user)
}

func (e *issueEnv) newReqFor(address string, user nkeys.KeyPair) *ireq {
	nonce := make([]byte, issue.MinNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		e.t.Fatal(err)
	}
	r := &ireq{user: user, signer: e.wrangler, fields: map[string]any{
		"version": 1, "address": address, "public_key": mustT(user.PublicKey()), "host": issueHost,
		"nonce": base64.RawURLEncoding.EncodeToString(nonce), "expires": time.Now().Add(issueTTL).Unix(), "wrangler": e.wpub,
	}}
	return r
}

// sign fills "proof" from the current fields with the request's signer.
func (r *ireq) sign(t *testing.T) *ireq {
	t.Helper()
	f := r.fields
	proof := mustT(issue.SignProof(r.signer, f["address"].(string), f["public_key"].(string), f["host"].(string),
		f["nonce"].(string), f["expires"].(int64)))
	f["proof"] = proof
	return r
}

// send publishes the request on the subject of its address's owner group and returns the raw reply.
func (e *issueEnv) send(r *ireq) (issue.Response, []byte) {
	e.t.Helper()
	raw := mustT(json.Marshal(r.fields))
	return e.sendRaw(issue.Subject("foo", "c"), raw)
}

func (e *issueEnv) sendRaw(subject string, raw []byte) (issue.Response, []byte) {
	e.t.Helper()
	m, err := e.nc.Request(subject, raw, issueWait)
	if err != nil {
		e.t.Fatal(err)
	}
	var resp issue.Response
	if err := json.Unmarshal(m.Data, &resp); err != nil {
		e.t.Fatalf("reply %q: %v", m.Data, err)
	}
	return resp, m.Data
}

func (e *issueEnv) records() map[string]store.Issued {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), issueWait)
	defer cancel()
	recs, _, err := e.adm.Issued(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	m := map[string]store.Issued{}
	for _, r := range recs {
		m[r.PublicKey] = r
	}
	return m
}

// refused sends r and fails unless it is refused with code, logged, and nothing was recorded.
func (e *issueEnv) refused(name string, r *ireq, code string) {
	e.t.Helper()
	before := len(e.records())
	resp, raw := e.send(r)
	if resp.Code != code || resp.JWT != "" || resp.Error == "" {
		e.t.Fatalf("%s: want code %s, got %s", name, code, raw)
	}
	if n := len(e.records()); n != before {
		e.t.Fatalf("%s: the record grew from %d to %d", name, before, n)
	}
	if !strings.Contains(e.logs.String(), "code="+code) {
		e.t.Fatalf("%s: no log line with code=%s:\n%s", name, code, e.logs.String())
	}
}

// connects reports whether jwtText with user's private key connects to the server.
func (e *issueEnv) connects(jwtText string, user nkeys.KeyPair) error {
	nc, err := nats.Connect(e.srv.ClientURL(), nats.UserJWT(func() (string, error) { return jwtText, nil },
		func(n []byte) ([]byte, error) { return user.Sign(n) }), nats.Timeout(issueWait))
	if err != nil {
		return err
	}
	nc.Close()
	return nil
}

func (e *issueEnv) wantTags(address string) []string {
	for _, u := range e.compiled.Users {
		if u.Sender == address {
			return u.Tags
		}
	}
	e.t.Fatalf("%s is not in the list set", address)
	return nil
}

// AT-012: the wrangler sends a public key, an address and a proof, and the JWT it gets connects with the key's private half.
func TestIssueServiceAT012IssuesAJWTThatConnects(t *testing.T) {
	e := newIssueEnv(t)
	r := e.newReq("foo.c.core").sign(t)
	resp, raw := e.send(r)
	if resp.JWT == "" {
		t.Fatalf("refused: %s\nlog: %s", raw, e.logs.String())
	}
	if err := e.connects(resp.JWT, r.user); err != nil {
		t.Fatalf("the issued JWT does not connect: %v", err)
	}
	uc := mustT(jwt.DecodeUserClaims(resp.JWT))
	if want := e.wantTags("foo.c.core"); !slices.Equal([]string(uc.Tags), want) || len(want) == 0 {
		t.Errorf("tags %v, want the table's %v", uc.Tags, want)
	}
	if uc.Name != "core" || uc.Subject != r.fields["public_key"] || uc.IssuerAccount != e.acctPub {
		t.Errorf("claims: name %q subject %q issuer account %q", uc.Name, uc.Subject, uc.IssuerAccount)
	}
	if uc.Expires != 0 || !reflectEmptyLimits(uc) {
		t.Errorf("the JWT carries an expiry or limits: %+v", uc.UserPermissionLimits)
	}
	gp := mustT(e.ks.GroupKey(issueGroup, false))
	if want := mustT(gp.PublicKey()); uc.Issuer != want {
		t.Errorf("signed by %s, not the owner group's key %s", uc.Issuer, want)
	}

	rec, ok := e.records()[r.fields["public_key"].(string)]
	if !ok {
		t.Fatal("not in the issue record")
	}
	if rec.Purpose != store.PurposeIssue || rec.Kind != store.IssuedEndpoint || rec.Address != "foo.c.core" ||
		rec.Zone != issueGroup || rec.Owner != "c" || !slices.Equal(rec.Tags, uc.Tags) || rec.RevokedAt != "" {
		t.Errorf("record %+v", rec)
	}
	if !strings.Contains(e.logs.String(), "listening") {
		t.Errorf("no startup line in the log: %s", e.logs.String())
	}
}

func reflectEmptyLimits(uc *jwt.UserClaims) bool {
	return reflect.DeepEqual(uc.UserPermissionLimits, jwt.UserPermissionLimits{})
}

// No private key anywhere: the NATS payloads the account carried and the service's log hold no seed.
func TestIssueServiceAT012NoPrivateKeyAnywhere(t *testing.T) {
	e := newIssueEnv(t)
	for _, a := range []string{"foo.c.core", "foo.c.lead"} {
		r := e.newReq(a).sign(t)
		if resp, raw := e.send(r); resp.JWT == "" {
			t.Fatalf("%s", raw)
		}
	}
	e.refused("unlisted", e.newReq("foo.c.ghost").sign(t), issue.CodeUnlisted)
	e.refused("unsigned", e.newReq("foo.c.core"), issue.CodeMalformed)
	if err := e.nc.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let the capture subscription catch up
	for name, text := range map[string]string{"nats payloads": e.captured.String(), "log": e.logs.String()} {
		if text == "" {
			t.Errorf("%s: nothing captured", name)
		}
		if m := seedRE.FindString(text); m != "" {
			t.Errorf("%s hold a seed-shaped string: %s...", name, m[:4])
		}
	}
	if !strings.Contains(e.captured.String(), "agents.issue.foo.c ") {
		t.Errorf("the capture did not see the issue traffic")
	}
	keyFiles, _ := filepath.Glob(filepath.Join(e.keyDir, "*", "*.nk"))
	rootFiles, _ := filepath.Glob(filepath.Join(e.keyDir, "*.nk"))
	for _, f := range append(keyFiles, rootFiles...) {
		seed := strings.TrimSpace(string(mustT(os.ReadFile(f))))
		if strings.Contains(e.captured.String(), seed) || strings.Contains(e.logs.String(), seed) {
			t.Errorf("the seed in %s appears in the capture or the log", f)
		}
	}
}

// A seed sent in the public key field is refused, and is not logged.
func TestIssueServiceDoesNotLogASeedSentAsAPublicKey(t *testing.T) {
	e := newIssueEnv(t)
	user := mustT(nkeys.CreateUser())
	seed := string(mustT(user.Seed()))
	r := e.newReqFor("foo.c.core", user)
	r.fields["public_key"] = seed
	r.fields["proof"] = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	e.refused("seed as key", r, issue.CodeBadKey)
	if strings.Contains(e.logs.String(), seed) {
		t.Fatal("the seed is in the log")
	}
}

func TestIssueServiceAT012Refusals(t *testing.T) {
	e := newIssueEnv(t)
	rec := func(r *ireq) string { return r.fields["public_key"].(string) }

	e.refused("unlisted address", e.newReq("foo.c.ghost").sign(t), issue.CodeUnlisted)

	e.refused("no proof", e.newReq("foo.c.core"), issue.CodeMalformed)

	bad := e.newReq("foo.c.core")
	bad.fields["proof"] = base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	e.refused("bad proof", bad, issue.CodeBadProof)

	exp := e.newReq("foo.c.core")
	exp.fields["expires"] = time.Now().Add(-10 * time.Second).Unix()
	e.refused("expired", exp.sign(t), issue.CodeExpired)

	far := e.newReq("foo.c.core")
	far.fields["expires"] = time.Now().Add(issue.MaxWindow + time.Minute).Unix()
	e.refused("expiry too far", far.sign(t), issue.CodeWindow)

	// A reused request: the first is signed, the second carries the same nonce.
	first := e.newReq("foo.c.core").sign(t)
	if resp, raw := e.send(first); resp.JWT == "" {
		t.Fatalf("%s", raw)
	}
	again := e.newReq("foo.c.lead")
	again.fields["nonce"] = first.fields["nonce"]
	e.refused("reused nonce", again.sign(t), issue.CodeReplay)
	// ...and the very same bytes again, as a replay on the wire.
	e.refused("replayed bytes", first, issue.CodeReplay)

	// A proof valid for foo.c.core replayed with foo.c.lead in the same owner group.
	swap := e.newReq("foo.c.core").sign(t)
	swap.fields["address"] = "foo.c.lead"
	e.refused("proof for another address", swap, issue.CodeBadProof)

	// A wrangler that is not registered at all, signing a correct proof.
	stranger := e.newReq("foo.c.core")
	stranger.signer = mustT(nkeys.CreateUser())
	stranger.fields["wrangler"] = mustT(stranger.signer.PublicKey())
	e.refused("unregistered wrangler", stranger.sign(t), issue.CodeUnregistered)

	// The registered wrangler, but for another host.
	host := e.newReq("foo.c.core")
	host.fields["host"] = "h2"
	e.refused("other host", host.sign(t), issue.CodeUnregistered)

	// A wrangler registered for (h1, foo.d) only asks for foo.c.
	other := mustT(nkeys.CreateUser())
	opub := mustT(other.PublicKey())
	if _, errb, code := e.run("wrangler", "add", "--host", issueHost, "--group", "foo.d", "--key", opub); code != 0 {
		t.Fatal(errb)
	}
	grp := e.newReq("foo.c.core")
	grp.signer = other
	grp.fields["wrangler"] = opub
	e.refused("other group", grp.sign(t), issue.CodeUnregistered)

	// A malformed key, and an oversize request.
	mal := e.newReq("foo.c.core")
	mal.fields["public_key"] = "UNOTAKEY"
	e.refused("malformed key", mal.sign(t), issue.CodeBadKey)
	big := e.newReq("foo.c.core")
	big.fields["padding"] = strings.Repeat("x", issue.MaxRequestBytes)
	e.refused("oversize", big.sign(t), issue.CodeTooLarge)
	if resp, _ := e.sendRaw(issue.Subject("foo", "c"), []byte("not json")); resp.Code != issue.CodeMalformed {
		t.Errorf("garbage: %+v", resp)
	}

	// The subject must be the owner group of the address.
	mism := e.newReq("foo.c.core").sign(t)
	if resp, _ := e.sendRaw(issue.Subject("foo", "d"), mustT(json.Marshal(mism.fields))); resp.Code != issue.CodeSubject {
		t.Errorf("subject mismatch: %+v", resp)
	}

	// A key already bound to another address: issued to foo.c.core, asked for foo.c.lead.
	k := e.newReq("foo.c.core").sign(t)
	if resp, raw := e.send(k); resp.JWT == "" {
		t.Fatalf("%s", raw)
	}
	bound := e.newReqFor("foo.c.lead", k.user)
	e.refused("key bound to another address", bound.sign(t), issue.CodeBound)
	// The same key for the same address is an ordinary re-issue.
	same := e.newReqFor("foo.c.core", k.user).sign(t)
	if resp, raw := e.send(same); resp.JWT == "" {
		t.Fatalf("re-issue refused: %s", raw)
	}

	// A revoked key.
	rv := e.newReq("foo.c.lead").sign(t)
	if resp, raw := e.send(rv); resp.JWT == "" {
		t.Fatalf("%s", raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), issueWait)
	defer cancel()
	if err := e.adm.MarkRevoked(ctx, rec(rv), time.Now()); err != nil {
		t.Fatal(err)
	}
	e.refused("revoked key", e.newReqFor("foo.c.lead", rv.user).sign(t), issue.CodeRevoked)
	if got := e.records()[rec(rv)]; got.RevokedAt == "" {
		t.Error("the refusal cleared revoked_at")
	}
}

// A request that carries tags, permissions, limits, a name and an expiry is signed, and none of it reaches the JWT.
func TestIssueServiceIgnoresTagsAndPermissionsInARequest(t *testing.T) {
	e := newIssueEnv(t)
	r := e.newReq("foo.c.lead")
	r.fields["tags"] = []string{"allow:c.core", "deny:c.sdk"}
	r.fields["permissions"] = map[string]any{"pub": map[string]any{"allow": []string{">"}}}
	r.fields["limits"] = map[string]any{"subs": 99999}
	r.fields["name"] = "root"
	r.fields["jwt_expires"] = time.Now().Add(time.Hour).Unix()
	r.sign(t)
	resp, raw := e.send(r)
	if resp.JWT == "" {
		t.Fatalf("%s", raw)
	}
	uc := mustT(jwt.DecodeUserClaims(resp.JWT))
	if want := e.wantTags("foo.c.lead"); !slices.Equal([]string(uc.Tags), want) {
		t.Errorf("tags %v, want the table's %v", uc.Tags, want)
	}
	if uc.Name != "lead" || uc.Expires != 0 || !reflectEmptyLimits(uc) {
		t.Errorf("the request reached the JWT: %+v", uc)
	}
	if err := e.connects(resp.JWT, r.user); err != nil {
		t.Fatal(err)
	}
}

// The service does not start without a test registry for this account.
func TestIssueServeRefusesToStartWithoutATestRegistry(t *testing.T) {
	e := newIssueEnvNoService(t)
	run := func() (int, string) {
		var out, errb bytes.Buffer
		code := runEnv([]string{"issue", "serve", "--test-account", "--list-set", e.lsPath}, func(k string) string { return e.env[k] }, &out, &errb)
		return code, errb.String()
	}
	const want = "no wrangler registry for this account: the static registry is for test accounts only; real wrangler registration is not decided yet"
	check := func(name string) {
		t.Helper()
		if code, errb := run(); code != 1 || !strings.Contains(errb, want) {
			t.Fatalf("%s: %d %s", name, code, errb)
		}
	}
	check("no registry file")

	e.registerWrangler()
	path := wranglerreg.Path(e.keyDir)
	f := mustT(wranglerreg.Load(path))

	f.TestAccount = false
	if err := f.Save(path); err != nil {
		t.Fatal(err)
	}
	check("not flagged test_account")

	f.TestAccount = true
	other := mustT(nkeys.CreateAccount())
	f.Account = mustT(other.PublicKey())
	if err := f.Save(path); err != nil {
		t.Fatal(err)
	}
	check("another account")

	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	check("a registry others can write")
}

// The registry file alone cannot turn the service on: the operator's own --test-account must agree with it.
func TestIssueServeRequiresTheOperatorsTestAccountFlag(t *testing.T) {
	e := newIssueEnvNoService(t)
	e.registerWrangler()
	var out, errb bytes.Buffer
	code := runEnv([]string{"issue", "serve", "--list-set", e.lsPath}, func(k string) string { return e.env[k] }, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "--test-account") || !strings.Contains(errb.String(), "test-account facility") {
		t.Fatalf("without the flag: %d %s", code, errb.String())
	}
	e.start() // with the flag and the matching file it starts
}

// An overridden registry path is held to the same custody as the default one.
func TestIssueServeChecksTheCustodyOfAnOverriddenRegistry(t *testing.T) {
	e := newIssueEnvNoService(t)
	e.registerWrangler()
	serve := func(path string) (int, string) {
		var out, errb bytes.Buffer
		code := runEnv([]string{"issue", "serve", "--test-account", "--list-set", e.lsPath, "--registry", path}, func(k string) string { return e.env[k] }, &out, &errb)
		return code, errb.String()
	}
	src := wranglerreg.Path(e.keyDir)
	open := filepath.Join(e.root, "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(open, "w.json"), mustT(os.ReadFile(src)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if code, errb := serve(filepath.Join(open, "w.json")); code != 1 || !strings.Contains(errb, "chmod go-w") {
		t.Fatalf("world-writable parent: %d %s", code, errb)
	}
	link := filepath.Join(e.root, "link")
	if err := os.Symlink(e.keyDir, link); err != nil {
		t.Fatal(err)
	}
	if code, errb := serve(filepath.Join(link, wranglerreg.FileName)); code != 1 || !strings.Contains(errb, "is a symlink") {
		t.Fatalf("symlinked parent: %d %s", code, errb)
	}
	if err := os.Chmod(src, 0o660); err != nil {
		t.Fatal(err)
	}
	if code, errb := serve(src); code != 1 || !strings.Contains(errb, "others can write it") {
		t.Fatalf("group-writable file: %d %s", code, errb)
	}
}

func TestWranglerCommandsRoundTrip(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	path := wranglerreg.Path(e.keyDir)
	k1 := mustT(mustT(nkeys.CreateUser()).PublicKey())
	k2 := mustT(mustT(nkeys.CreateUser()).PublicKey())

	if _, errb, code := e.run("wrangler", "init"); code != 1 || !strings.Contains(errb, "test accounts only") {
		t.Fatalf("init without the flag: %d %s", code, errb)
	}
	if _, errb, code := e.run("wrangler", "list"); code != 1 || !strings.Contains(errb, "wrangler init --test-account") {
		t.Fatalf("list before init: %d %s", code, errb)
	}
	if out, errb, code := e.run("wrangler", "init", "--test-account"); code != 0 || !strings.Contains(out, e.acctPub) {
		t.Fatalf("init: %d %s %s", code, out, errb)
	}
	if out, _, code := e.run("wrangler", "init", "--test-account"); code != 0 || !strings.Contains(out, "already exists") {
		t.Fatalf("second init: %d %s", code, out)
	}
	for _, a := range [][]string{{"h1", "foo.c", k1}, {"h2", "foo.c", k2}, {"h1", "foo.d", k2}} {
		if _, errb, code := e.run("wrangler", "add", "--host", a[0], "--group", a[1], "--key="+a[2]); code != 0 {
			t.Fatalf("add %v: %s", a, errb)
		}
	}
	if _, errb, code := e.run("wrangler", "add", "--host", "h1", "--group", "foo.c", "--key", "SUAAA"); code != 1 || !strings.Contains(errb, "public key") {
		t.Fatalf("add a bad key: %d %s", code, errb)
	}
	out, _, code := e.run("wrangler", "list")
	if code != 0 || out != "h1\tfoo.c\t"+k1+"\nh2\tfoo.c\t"+k2+"\nh1\tfoo.d\t"+k2+"\n" {
		t.Fatalf("list: %d %q", code, out)
	}
	if fi := mustT(os.Stat(path)); fi.Mode().Perm() != 0o600 {
		t.Errorf("the registry has mode %04o", fi.Mode().Perm())
	}
	if _, errb, code := e.run("wrangler", "remove", "--host", "h1", "--group", "foo.c"); code != 0 {
		t.Fatal(errb)
	}
	if fi := mustT(os.Stat(path)); fi.Mode().Perm() != 0o600 {
		t.Errorf("after a rewrite the registry has mode %04o", fi.Mode().Perm())
	}
	if _, errb, code := e.run("wrangler", "remove", "--host", "h1", "--group", "foo.c"); code != 1 || !strings.Contains(errb, "no entry") {
		t.Fatalf("remove twice: %d %s", code, errb)
	}
	if out, _, _ := e.run("wrangler", "list"); out != "h2\tfoo.c\t"+k2+"\nh1\tfoo.d\t"+k2+"\n" {
		t.Fatalf("list after remove: %q", out)
	}
	if _, errb, code := e.run("wrangler", "remove", "--host", "h1"); code != 2 || !strings.Contains(errb, "usage: mashctl wrangler") {
		t.Fatalf("usage: %d %s", code, errb)
	}
	if _, errb, code := e.run("wrangler", "bogus"); code != 2 {
		t.Fatalf("unknown verb: %d %s", code, errb)
	}
	if _, errb, code := e.run("issue"); code != 2 || !strings.Contains(errb, "usage: mashctl issue serve") {
		t.Fatalf("issue usage: %d %s", code, errb)
	}
}

// A removal takes effect on the running service at once: the registry is read at every request.
func TestIssueServiceHonoursARegistryRemovalWithoutARestart(t *testing.T) {
	e := newIssueEnv(t)
	if resp, raw := e.send(e.newReq("foo.c.core").sign(t)); resp.JWT == "" {
		t.Fatalf("%s", raw)
	}
	if _, errb, code := e.run("wrangler", "remove", "--host", issueHost, "--group", issueGroup); code != 0 {
		t.Fatal(errb)
	}
	e.refused("after removal", e.newReq("foo.c.lead").sign(t), issue.CodeUnregistered)
}

// The list set is read again when its file changes: a sender removed from it is refused.
func TestIssueServiceFollowsTheListSetFile(t *testing.T) {
	e := newIssueEnv(t)
	ls := e.ls
	ls.Senders = ls.Senders[:2]
	raw := mustT(json.Marshal(ls))
	if err := os.WriteFile(e.lsPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	e.refused("sender removed", e.newReq("foo.c.sdk").sign(t), issue.CodeUnlisted)
	if resp, raw := e.send(e.newReq("foo.c.lead").sign(t)); resp.JWT == "" {
		t.Fatalf("%s", raw)
	}
	if err := os.WriteFile(e.lsPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.refused("list set unreadable", e.newReq("foo.c.lead").sign(t), issue.CodeNoListSet)
}

// The signing key of an owner group that has none is refused, and nothing is created.
func TestIssueServiceDoesNotCreateAGroupKey(t *testing.T) {
	e := newIssueEnvNoService(t)
	if err := os.Remove(filepath.Join(e.keyDir, "groups", issueGroup+".nk")); err != nil {
		t.Fatal(err)
	}
	e.registerWrangler()
	e.start()
	e.refused("no group key", e.newReq("foo.c.core").sign(t), issue.CodeNoSigningKey)
	if _, err := os.Stat(filepath.Join(e.keyDir, "groups", issueGroup+".nk")); err == nil {
		t.Error("the service created a group key")
	}
}

// No permission template mash compiles reaches the issue subject, so an endpoint cannot publish to it.
func TestIssueSubjectIsNotReachableByATemplate(t *testing.T) {
	subj := strings.Split(issue.Subject("foo", "c"), ".")
	match := func(filter string) bool {
		f := strings.Split(filter, ".")
		for i, tok := range f {
			if tok == ">" {
				return len(subj) > i
			}
			if i >= len(subj) || (tok != "*" && tok != subj[i]) {
				return false
			}
		}
		return len(f) == len(subj)
	}
	for _, tpl := range mustT(listset.Compile(issueListSet())).Templates {
		for _, line := range append(append([]string{}, tpl.Permissions.Pub.Allow...), tpl.Permissions.Sub.Allow...) {
			if match(line) {
				t.Errorf("%s: %q reaches %s", tpl.Group, line, strings.Join(subj, "."))
			}
		}
	}
	for _, s := range madminSubjectsOf() {
		if match(s) {
			t.Errorf("the owner permission line %q reaches the issue subject", s)
		}
	}
}

func madminSubjectsOf() []string {
	p := madmin.Permissions("c", "core")
	return append(append([]string{}, p.Pub.Allow...), p.Sub.Allow...)
}

// pushIssueScope replaces the owner group's signing key in the account JWT: unscoped when unscoped is set, else scoped
// with a template that is not the list set's.
func (e *issueEnv) pushIssueScope(unscoped bool) {
	e.t.Helper()
	gk := mustT(e.ks.GroupKey(issueGroup, false))
	gpub := mustT(gk.PublicKey())
	ac := e.claims()
	delete(ac.SigningKeys, gpub)
	if unscoped {
		ac.SigningKeys.Add(gpub)
	} else {
		us := jwt.NewUserScope()
		us.Key = gpub
		us.Template.Permissions.Pub.Allow.Add(">")
		ac.SigningKeys.AddScopedSigner(us)
	}
	opkp, _, err := e.ks.Operator()
	if err != nil {
		e.t.Fatal(err)
	}
	time.Sleep(issueIatTick) // a pushed account JWT must be newer than the one it replaces
	tok := mustT(ac.Encode(opkp))
	sys := mustT(nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.env["MASH_SYS_CREDS"])))
	defer sys.Close()
	msg, err := sys.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(tok), issueWait)
	if err != nil || strings.Contains(string(msg.Data), `"error"`) {
		e.t.Fatalf("push: %v %v", err, msg)
	}
}

const issueIatTick = 1100 * time.Millisecond

// The live service reads the account JWT: a group key published unscoped, or scoped with another template, signs nothing.
func TestIssueServiceRefusesAGroupKeyThatIsNotScopedWithTheTable(t *testing.T) {
	for _, c := range []struct {
		name     string
		unscoped bool
		code     string
	}{{"unscoped", true, issue.CodeNoScope}, {"another template", false, issue.CodeTemplate}} {
		t.Run(c.name, func(t *testing.T) {
			e := newIssueEnv(t)
			e.pushIssueScope(c.unscoped)
			e.refused(c.name, e.newReq("foo.c.core").sign(t), c.code)
		})
	}
}

// Concurrent `wrangler add` and `wrangler remove` are serialised: a removed wrangler is never restored by an add that
// read the registry first, and `wrangler list` during the edits prints a complete registry.
func TestWranglerCommandsAreSerialised(t *testing.T) {
	e := newIssueEnvNoService(t)
	if _, errb, code := e.run("wrangler", "init", "--test-account"); code != 0 {
		t.Fatalf("init: %s", errb)
	}
	x := mustT(mustT(nkeys.CreateUser()).PublicKey())
	for round := range 10 {
		host := fmt.Sprintf("x%d", round)
		if _, errb, code := e.run("wrangler", "add", "--host", host, "--group", "foo.c", "--key", x); code != 0 {
			t.Fatalf("add: %s", errb)
		}
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			if _, errb, code := e.run("wrangler", "remove", "--host", host, "--group", "foo.c"); code != 0 {
				t.Errorf("remove: %s", errb)
			}
		}()
		go func() {
			defer wg.Done()
			y := mustT(mustT(nkeys.CreateUser()).PublicKey())
			if _, errb, code := e.run("wrangler", "add", "--host", fmt.Sprintf("y%d", round), "--group", "foo.c", "--key", y); code != 0 {
				t.Errorf("add: %s", errb)
			}
		}()
		go func() {
			defer wg.Done()
			if out, errb, code := e.run("wrangler", "list"); code != 0 {
				t.Errorf("list during an edit: %s", errb)
			} else {
				for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
					if l != "" && len(strings.Split(l, "\t")) != 3 {
						t.Errorf("incomplete list line %q", l)
					}
				}
			}
		}()
		wg.Wait()
		out, _, _ := e.run("wrangler", "list")
		if strings.Contains(out, host+"\t") {
			t.Fatalf("round %d: the removed wrangler is back:\n%s", round, out)
		}
		if !strings.Contains(out, fmt.Sprintf("y%d\t", round)) {
			t.Fatalf("round %d: the add was lost:\n%s", round, out)
		}
	}
	if fi := mustT(os.Stat(wranglerreg.LockPath(wranglerreg.Path(e.keyDir)))); fi.Mode().Perm() != 0o600 {
		t.Errorf("lock file mode %v", fi.Mode())
	}
}

// The list set decides who may be issued a credential, so `issue serve` refuses to start on one that others can write,
// that is a symlink, or that sits in a directory a group or other user can write.
func TestIssueServeChecksTheCustodyOfTheListSet(t *testing.T) {
	e := newIssueEnvNoService(t)
	e.registerWrangler()
	serve := func(path string) (int, string) {
		var out, errb bytes.Buffer
		code := runEnv([]string{"issue", "serve", "--test-account", "--list-set", path}, func(k string) string { return e.env[k] }, &out, &errb)
		return code, errb.String()
	}
	if err := os.Chmod(e.lsPath, 0o664); err != nil {
		t.Fatal(err)
	}
	if code, errb := serve(e.lsPath); code != 1 || !strings.Contains(errb, "chmod go-w "+e.lsPath) {
		t.Fatalf("group-writable file: %d %s", code, errb)
	}
	if err := os.Chmod(e.lsPath, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e.root, "ls-link.json")
	if err := os.Symlink(e.lsPath, link); err != nil {
		t.Fatal(err)
	}
	if code, errb := serve(link); code != 1 || !strings.Contains(errb, "is a symlink") {
		t.Fatalf("symlinked file: %d %s", code, errb)
	}
	if err := os.Chmod(e.root, 0o770); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(e.root, 0o700)
	if code, errb := serve(e.lsPath); code != 1 || !strings.Contains(errb, "chmod go-w "+e.root) {
		t.Fatalf("group-writable directory: %d %s", code, errb)
	}
}
