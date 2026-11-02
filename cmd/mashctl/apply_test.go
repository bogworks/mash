package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	madmin "github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/bootstrap"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/natsauth"
)

// applyEnv is a local operator-mode server (bootstrap's configuration, run in process) and the environment mashctl
// needs to reach it.
type applyEnv struct {
	t       *testing.T
	root    string
	keyDir  string
	srv     *server.Server
	pl      *bootstrap.Plan
	env     map[string]string
	policy  string
	ks      *ctlkeys.Authority
	acctPub string
}

const twoZones = `default: deny
endpoints:
  x: {labels: {zone: a}}
  y: {labels: {zone: b}}
rules:
  - {name: a-to-b, action: allow, from: {zone: a}, to: {zone: b}}
`

// threeZones is twoZones plus an endpoint in zone c, which no rule lets anyone mail.
var threeZones = strings.Replace(twoZones, "  y: {labels: {zone: b}}\n", "  y: {labels: {zone: b}}\n  z: {labels: {zone: c}}\n", 1)

func newApplyEnv(t *testing.T, policyYAML string) *applyEnv {
	t.Helper()
	root := t.TempDir()
	e := &applyEnv{t: t, root: root, keyDir: filepath.Join(root, "keys")}
	ks, err := ctlkeys.Open(e.keyDir)
	if err != nil {
		t.Fatal(err)
	}
	e.ks = ks
	pl, err := bootstrap.Build(bootstrap.Params{Dir: filepath.Join(root, "server"), Owner: "alice", Port: 4222}, ks)
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Write(); err != nil {
		t.Fatal(err)
	}
	e.pl = pl
	e.acctPub = pl.Account
	opts, err := server.ProcessConfigFile(pl.ConfPath())
	if err != nil {
		t.Fatal(err)
	}
	opts.Port = -1
	opts.NoLog, opts.NoSigs = true, true
	srv, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("server did not start")
	}
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	e.srv = srv
	e.policy = filepath.Join(root, "policy.yaml")
	if err := os.WriteFile(e.policy, []byte(policyYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	e.env = map[string]string{
		"HOME": root, "MASH_CONFIG_DIR": filepath.Join(root, "cfg"), "MASH_URL": srv.ClientURL(), "MASH_OWNER": "alice",
		"MASH_KEYS_DIR": e.keyDir, "MASH_SYS_CREDS": filepath.Join(pl.Dir, bootstrap.SysCreds), "MASH_POLICY": e.policy,
	}
	return e
}

func (e *applyEnv) run(args ...string) (string, string, int) {
	e.t.Helper()
	var out, errb bytes.Buffer
	code := run(args, func(k string) string { return e.env[k] }, &out, &errb)
	return out.String(), errb.String(), code
}

// accountJWT reads the MASH account's JWT from the resolver through the system account.
func (e *applyEnv) accountJWT() string {
	e.t.Helper()
	nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	if err != nil {
		e.t.Fatal(err)
	}
	defer nc.Close()
	m, err := nc.Request("$SYS.REQ.ACCOUNT."+e.acctPub+".CLAIMS.LOOKUP", nil, 3*time.Second)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(m.Data)
}

func (e *applyEnv) claims() *jwt.AccountClaims {
	e.t.Helper()
	c, err := jwt.DecodeAccountClaims(e.accountJWT())
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func scopedSigners(c *jwt.AccountClaims) map[string]*jwt.UserScope {
	out := map[string]*jwt.UserScope{}
	for k, s := range c.SigningKeys {
		if us, ok := s.(*jwt.UserScope); ok && us != nil {
			out[k] = us
		}
	}
	return out
}

// noSeeds fails if any key's seed text appears in s.
func (e *applyEnv) noSeeds(s string) {
	e.t.Helper()
	files, _ := filepath.Glob(filepath.Join(e.keyDir, "*.nk"))
	more, _ := filepath.Glob(filepath.Join(e.keyDir, "zones", "*.nk"))
	for _, f := range append(files, more...) {
		b, err := os.ReadFile(f)
		if err != nil {
			e.t.Fatal(err)
		}
		if seed := strings.TrimSpace(string(b)); seed != "" && strings.Contains(s, seed) {
			e.t.Fatalf("seed of %s appears in output", filepath.Base(f))
		}
	}
}

// mint signs a user JWT for name under zone's key and returns a connect option for it.
func (e *applyEnv) mint(zone, name string) nats.Option {
	e.t.Helper()
	zk, err := e.ks.ZoneKey(zone, false)
	if err != nil {
		e.t.Fatal(err)
	}
	ukp, _ := nkeys.CreateUser()
	upub, _ := ukp.PublicKey()
	uc := jwt.NewUserClaims(upub)
	uc.Name = name
	uc.Tags.Add(madmin.NoDenyTag) // every zone key carries a deny template, so every user carries a deny tag (ADR 0056)
	uc.IssuerAccount = e.acctPub
	uc.UserPermissionLimits = jwt.UserPermissionLimits{} // a scoped key's users carry none of their own
	tok, err := uc.Encode(zk)
	if err != nil {
		e.t.Fatal(err)
	}
	return nats.UserJWT(func() (string, error) { return tok, nil }, func(n []byte) ([]byte, error) { return ukp.Sign(n) })
}

func TestApplyPushesZonesAndUsersGetExactlyTheirPermissions(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	before := e.accountJWT()

	out, errb, code := e.run("policy", "apply")
	if code != 0 || errb != "" {
		t.Fatalf("apply: %d\n%s\n%s", code, out, errb)
	}
	e.noSeeds(out + errb)
	for _, want := range []string{"zone a: new", "zone b: new", "created the signing key of zone a", "not a transaction", "acknowledged"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	got := e.claims()
	signers := scopedSigners(got)
	if len(signers) != 2 {
		t.Fatalf("want exactly 2 scoped signers (a, b), got %d", len(signers))
	}
	if e.accountJWT() == before {
		t.Fatal("the account JWT did not change")
	}
	if got.Limits.JetStreamLimits.Streams != 64 || got.Name != "MASH" {
		t.Errorf("limits or name were not preserved: %+v %q", got.Limits.JetStreamLimits, got.Name)
	}
	for _, zone := range []string{"a", "b"} {
		pub, err := e.ks.ZonePublic(zone)
		if err != nil {
			t.Fatal(err)
		}
		us := signers[pub]
		if us == nil || us.Role != zone {
			t.Fatalf("zone %s: no scoped signer with its key", zone)
		}
		var lines []string
		lines = append(lines, us.Template.Pub.Allow...)
		joined := strings.Join(lines, "\n")
		if strings.Contains(joined, "alice.b.*") != (zone == "a") {
			t.Errorf("zone %s pub allow wildcard to b wrong:\n%s", zone, joined)
		}
		if us.Template.Limits.Subs != -1 || us.Template.Limits.Data != -1 || us.Template.Limits.Payload != -1 {
			t.Errorf("zone %s template has per-user limits: %+v", zone, us.Template.Limits)
		}
	}

	// A user minted under zone a's key: zone b yes, zone c no.
	admin, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.pl.Creds))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	seen := make(chan string, 8)
	if _, err := admin.Subscribe("agents.mail.>", func(m *nats.Msg) { seen <- m.Subject }); err != nil {
		t.Fatal(err)
	}
	admin.Flush()

	violations := make(chan string, 8)
	ux, err := nats.Connect(e.srv.ClientURL(), e.mint("a", "x"), nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
		violations <- err.Error()
	}))
	if err != nil {
		t.Fatalf("a user minted under zone a's key does not connect: %v", err)
	}
	defer ux.Close()
	toB, toC := "agents.mail.cc.alice.b.y", "agents.mail.cc.alice.c.z"
	if err := ux.Publish(toB, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	ux.Flush()
	select {
	case s := <-seen:
		if s != toB {
			t.Fatalf("saw %s", s)
		}
	case v := <-violations:
		t.Fatalf("publish to zone b refused: %s", v)
	case <-time.After(3 * time.Second):
		t.Fatal("mail to zone b was not delivered")
	}
	if err := ux.Publish(toC, []byte("no")); err != nil {
		t.Fatal(err)
	}
	ux.Flush()
	select {
	case v := <-violations:
		if !strings.Contains(strings.ToLower(v), "permissions violation") {
			t.Fatalf("unexpected error %q", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("publish to zone c was not refused")
	}
	select {
	case s := <-seen:
		t.Fatalf("mail to zone c was delivered: %s", s)
	case <-time.After(200 * time.Millisecond):
	}

	// Second apply: nothing to push.
	jwtNow := e.accountJWT()
	out, errb, code = e.run("policy", "apply")
	if code != 0 || errb != "" || !strings.Contains(out, "account claims already up to date") {
		t.Fatalf("second apply: %d\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "acknowledged the unchanged JWT") {
		t.Fatalf("the no-change apply did not confirm the server:\n%s", out)
	}
	if e.accountJWT() != jwtNow {
		t.Fatal("the second apply changed the account JWT")
	}

	// The resolver acknowledges the identical JWT again with code 200 (the basis of the no-change confirmation).
	nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	acks, err := apply.NATS{Conn: nc}.Push(context.Background(), jwtNow, nil)
	if err != nil || len(acks) == 0 || !acks[0].OK() {
		t.Fatalf("re-pushing the identical JWT: %v %+v", err, acks)
	}
	if e.accountJWT() != jwtNow {
		t.Fatal("the identical re-push changed the account JWT")
	}
}

func TestDryRunAndDiffChangeNothing(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	before := e.accountJWT()
	for _, args := range [][]string{{"policy", "apply", "--dry-run"}, {"policy", "diff"}} {
		out, errb, code := e.run(args...)
		if code != 0 || errb != "" {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
		e.noSeeds(out)
		if !strings.Contains(out, "would create its signing key") || !strings.Contains(out, "pub allow") {
			t.Errorf("%v: no plan shown:\n%s", args, out)
		}
		if e.accountJWT() != before {
			t.Fatalf("%v changed the server", args)
		}
		if z, _ := e.ks.Zones(); len(z) != 0 {
			t.Fatalf("%v created zone keys: %v", args, z)
		}
	}
}

// dropZoneC applies threeZones, then rewrites the file as twoZones; it returns zone c's public key.
func dropZoneC(t *testing.T) (*applyEnv, string) {
	t.Helper()
	e := newApplyEnv(t, threeZones)
	if _, errb, code := e.run("policy", "apply"); code != 0 {
		t.Fatal(errb)
	}
	cpub, err := e.ks.ZonePublic("c")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.policy, []byte(twoZones), 0o600); err != nil {
		t.Fatal(err)
	}
	return e, cpub
}

func TestZoneAbsentFromFileIsRefusedUnlessRemovedOrKept(t *testing.T) {
	e, cpub := dropZoneC(t)
	before := e.accountJWT()
	for _, args := range [][]string{{"policy", "apply"}, {"policy", "apply", "--dry-run"}, {"policy", "diff"}} {
		out, errb, code := e.run(args...)
		want := 1
		if args[1] == "diff" {
			want = 0
		}
		if code != want || errb != "" || !strings.Contains(out, "REFUSED unless --remove-zone c or --keep-zone c") || strings.Contains(out, "account claims already up to date") {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
		if e.accountJWT() != before {
			t.Fatalf("%v changed the server", args)
		}
	}
	if _, ok := scopedSigners(e.claims())[cpub]; !ok {
		t.Fatal("zone c's key was removed")
	}
}

func TestKeepZoneProceedsWithTheWarning(t *testing.T) {
	e, cpub := dropZoneC(t)
	out, errb, code := e.run("policy", "apply", "--keep-zone", "c")
	if code != 0 || errb != "" || !strings.Contains(out, "zone c is outside the policy and its key stays active") || !strings.Contains(out, "account claims already up to date") {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	if _, ok := scopedSigners(e.claims())[cpub]; !ok {
		t.Fatal("zone c's key was removed")
	}
	// an unknown zone name is an error
	if out, _, code := e.run("policy", "apply", "--keep-zone", "nope"); code != 1 || strings.Contains(out, "account claims already up to date") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestRemoveZoneRemovesTheSignerAndKeepsTheKeyFile(t *testing.T) {
	e, cpub := dropZoneC(t)
	out, errb, code := e.run("policy", "apply", "--remove-zone", "c")
	if code != 0 || errb != "" || !strings.Contains(out, "users signed by it stop working") || !strings.Contains(out, "pushed") {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	if _, ok := scopedSigners(e.claims())[cpub]; ok {
		t.Fatal("zone c's signer is still in the account JWT")
	}
	if got, err := e.ks.ZonePublic("c"); err != nil || got != cpub {
		t.Fatalf("the local zone key file is gone: %v", err)
	}
	// nothing is outside the policy any more
	if out, _, code := e.run("policy", "apply"); code != 0 || !strings.Contains(out, "account claims already up to date") || strings.Contains(out, "outside the policy") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestServersFlagAndEnvAreValidated(t *testing.T) {
	for _, args := range [][]string{{"--servers", "0"}, {"--servers=-1"}, {"--servers", "abc"}, {"--servers"}} {
		e := newApplyEnv(t, twoZones)
		if _, _, code := e.run(append([]string{"policy", "diff"}, args...)...); code != 2 {
			t.Fatalf("%v: exit %d, want 2", args, code)
		}
	}
	e := newApplyEnv(t, twoZones)
	e.env["MASH_SERVERS"] = "junk"
	if _, errb, code := e.run("policy", "diff"); code != 2 || !strings.Contains(errb, "MASH_SERVERS") {
		t.Fatalf("%d %s", code, errb)
	}
	e.env["MASH_SERVERS"] = "1"
	if out, errb, code := e.run("policy", "apply"); code != 0 || !strings.Contains(out, "pushed: 1 server(s)") {
		t.Fatalf("%d %s %s", code, out, errb)
	}
}

func TestChangedZoneShowsAddedAndRemovedLines(t *testing.T) {
	e := newApplyEnv(t, threeZones)
	if _, errb, code := e.run("policy", "apply"); code != 0 {
		t.Fatal(errb)
	}
	changed := strings.Replace(threeZones, "to: {zone: b}}", "to: {zone: c}}", 1)
	if err := os.WriteFile(e.policy, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := e.run("policy", "diff")
	if code != 0 || !strings.Contains(out, "zone a: changed") || !strings.Contains(out, "+ agents.mail.*.alice.c.*") || !strings.Contains(out, "- agents.mail.*.alice.b.*") {
		t.Fatalf("diff:\n%s", out)
	}
	if !strings.Contains(out, "zone b: unchanged") {
		t.Fatalf("diff:\n%s", out)
	}
	if out, errb, code := e.run("policy", "apply"); code != 0 || !strings.Contains(out, "pushed") {
		t.Fatalf("apply: %d %s %s", code, out, errb)
	}
}

func TestApplyRefusals(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	before := e.accountJWT()

	// a bad sys-creds path
	e.env["MASH_SYS_CREDS"] = filepath.Join(e.root, "nope.creds")
	_, errb, code := e.run("policy", "apply")
	if code != 1 || !strings.Contains(errb, "system account credentials") {
		t.Fatalf("bad creds: %d %q", code, errb)
	}
	if z, _ := e.ks.Zones(); len(z) != 0 {
		t.Fatalf("zone keys created before the connection worked: %v", z)
	}
	// no creds anywhere
	e.env["MASH_SYS_CREDS"] = ""
	e.env["MASH_CONFIG_DIR"] = filepath.Join(e.root, "empty")
	_, errb, code = e.run("policy", "diff")
	if code != 1 || !strings.Contains(errb, "--sys-creds FILE or set MASH_SYS_CREDS") {
		t.Fatalf("no creds: %d %q", code, errb)
	}
	// the user's own credentials are not system credentials
	e.env["MASH_SYS_CREDS"] = e.pl.Creds
	out, errb, code := e.run("policy", "diff")
	if code != 1 {
		t.Fatalf("admin creds as sys creds: %d %q %q", code, out, errb)
	}
	e.env["MASH_SYS_CREDS"] = filepath.Join(e.pl.Dir, bootstrap.SysCreds)

	// a policy that does not check
	if err := os.WriteFile(e.policy, []byte("default: maybe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errb, code = e.run("policy", "apply")
	if code != 1 || !strings.Contains(errb, "does not check") {
		t.Fatalf("bad policy: %d %q", code, errb)
	}
	if e.accountJWT() != before {
		t.Fatal("a refused apply changed the server")
	}
	// usage
	if _, _, code := e.run("policy", "diff", "--dry-run"); code != 2 {
		t.Fatalf("diff --dry-run: %d", code)
	}
}

func TestSysCredsDefaultsFromTheInitServerDir(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	// admin_creds in the environment points into the server directory: sys.creds is found next to it.
	e.env["MASH_SYS_CREDS"] = ""
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	out, errb, code := e.run("policy", "diff")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errb)
	}
}

func TestApplyConfirmsTheServerSetAndParsesReplaceFlag(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	out, errb, code := e.run("policy", "apply")
	if code != 0 || !strings.Contains(out, "pushed: 1 server(s)") || strings.Contains(out, "could not be confirmed") {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	o, err := parseApplyArgs("apply", []string{"--replace-zone-key", "a", "--replace-zone-key=b"})
	if err != nil || len(o.replaceZones) != 2 || o.replaceZones[1] != "b" {
		t.Fatalf("%v %v", o, err)
	}
	o, err = parseApplyArgs("apply", []string{"--remove-zone", "a", "--keep-zone=b", "--servers", "3"})
	if err != nil || len(o.removeZones) != 1 || len(o.keepZones) != 1 || o.servers != "3" {
		t.Fatalf("%v %v", o, err)
	}
	if _, err := parseApplyArgs("apply", []string{"--replace-zone-key"}); err == nil {
		t.Fatal("a flag without a value parsed")
	}
}

func TestFailingPolicyTestsRefuseApplyAndWarnInDiff(t *testing.T) {
	bad := twoZones + "tests:\n  - {from: x, to: y, expect: deny}\n"
	e := newApplyEnv(t, bad)
	before := e.accountJWT()

	for _, args := range [][]string{{"policy", "apply"}, {"policy", "apply", "--dry-run"}} {
		out, errb, code := e.run(args...)
		if code != 1 || !strings.Contains(out, "FAIL x->y expect deny got allow") || !strings.Contains(errb, "policy tests") {
			t.Fatalf("%v: %d %q %q", args, code, out, errb)
		}
	}
	if z, _ := e.ks.Zones(); len(z) != 0 {
		t.Fatalf("zone keys created despite failing tests: %v", z)
	}
	if e.accountJWT() != before {
		t.Fatal("a refused apply changed the server")
	}

	out, errb, code := e.run("policy", "diff")
	if code != 0 || !strings.HasPrefix(out, "policy tests FAIL:") || !strings.Contains(out, "FAIL x->y expect deny got allow") || !strings.Contains(out, "zone") {
		t.Fatalf("diff: %d %q %q", code, out, errb)
	}

	// the same file with a test that holds applies
	good := twoZones + "tests:\n  - {from: x, to: y, expect: allow}\n"
	if err := os.WriteFile(e.policy, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := e.run("policy", "apply"); code != 0 {
		t.Fatalf("passing tests: %d %q %q", code, out, errb)
	}
}
