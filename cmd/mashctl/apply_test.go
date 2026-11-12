package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

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

// twoZones is a two-group policy file. The name stays for the tests that use it; the groups are the label net, since a
// policy file that carries a zone is refused (ADR 0091).
const twoZones = `default: deny
endpoints:
  x: {labels: {net: a}}
  y: {labels: {net: b}}
rules:
  - {name: a-to-b, action: allow, from: {net: a}, to: {net: b}}
`

func newApplyEnv(t *testing.T, policyYAML string) *applyEnv {
	t.Helper()
	return newApplyEnvTweak(t, policyYAML, nil)
}

// newApplyEnvTweak is newApplyEnv with a hook that runs after the server files are written and before the server
// starts, so a test can rewrite the operator or account JWTs on disk.
func newApplyEnvTweak(t *testing.T, policyYAML string, tweak func(e *applyEnv)) *applyEnv {
	t.Helper()
	return newApplyEnvOpts(t, policyYAML, tweak, nil)
}

// newApplyEnvOpts is newApplyEnvTweak with a hook that may change the server options (for example to add TLS) before
// the server starts.
func newApplyEnvOpts(t *testing.T, policyYAML string, tweak func(e *applyEnv), optsHook func(o *server.Options)) *applyEnv {
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
	if tweak != nil {
		tweak(e)
	}
	opts, err := server.ProcessConfigFile(pl.ConfPath())
	if err != nil {
		t.Fatal(err)
	}
	opts.Port = -1
	opts.NoLog, opts.NoSigs = true, true
	if optsHook != nil {
		optsHook(opts)
	}
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
		"HOME": root, "MASH_CONFIG_DIR": filepath.Join(root, "cfg"), "MASH_URL": srv.ClientURL(), "MASH_INSECURE_LOOPBACK": "1", "MASH_OWNER": "alice",
		"MASH_KEYS_DIR": e.keyDir, "MASH_SYS_CREDS": filepath.Join(pl.Dir, bootstrap.SysCreds), "MASH_POLICY": e.policy,
	}
	return e
}

// withDirectory gives the env a reachable, initialised directory and the admin credentials to read it: apply reads the
// directory first, and refuses when it cannot (ADR 0091).
func (e *applyEnv) withDirectory() *applyEnv {
	e.t.Helper()
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	if out, errb, code := e.run("init"); code != 0 {
		e.t.Fatalf("init: %d %s %s", code, out, errb)
	}
	return e
}

func (e *applyEnv) run(args ...string) (string, string, int) {
	e.t.Helper()
	var out, errb bytes.Buffer
	code := runEnv(args, func(k string) string { return e.env[k] }, &out, &errb)
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
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			e.t.Fatal(err)
		}
		if seed := strings.TrimSpace(string(b)); seed != "" && strings.Contains(s, seed) {
			e.t.Fatalf("seed of %s appears in output", filepath.Base(f))
		}
	}
}

// applyZonePolicy puts a zone policy of an older release on the server: the account JWT gains one scoped signing key,
// which is what `policy apply` used to publish and what the zone-removal guards detect (ADR 0091). It returns the
// account JWT as it was before.
func (e *applyEnv) applyZonePolicy() string {
	e.t.Helper()
	before := e.accountJWT()
	op, _, err := e.ks.Operator()
	if err != nil {
		e.t.Fatal(err)
	}
	cl, err := jwt.DecodeAccountClaims(before)
	if err != nil {
		e.t.Fatal(err)
	}
	sk, _ := nkeys.CreateAccount()
	pub, _ := sk.PublicKey()
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = pub, "red"
	cl.SigningKeys.AddScopedSigner(sc)
	if time.Now().Unix() <= cl.IssuedAt {
		time.Sleep(1100 * time.Millisecond) // the new copy must be the newer one
	}
	tok, err := cl.Encode(op)
	if err != nil {
		e.t.Fatal(err)
	}
	nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.env["MASH_SYS_CREDS"]))
	if err != nil {
		e.t.Fatal(err)
	}
	defer nc.Close()
	if _, err := nc.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(tok), 5*time.Second); err != nil {
		e.t.Fatal(err)
	}
	if len(scopedSigners(e.claims())) != 1 {
		e.t.Fatal("the zone policy did not reach the server")
	}
	return before
}

// `policy apply` and `--dry-run` refuse and push nothing: the zone policy stack was removed (ADR 0091) and there is no
// replacement until the label policy model ships. They check the file, its tests and the guards first.
func TestPolicyApplyRefusesAndPushesNothing(t *testing.T) {
	e := newApplyEnv(t, twoZones).withDirectory()
	before := e.accountJWT()
	for _, args := range [][]string{{"policy", "apply"}, {"policy", "apply", "--dry-run"}} {
		out, errb, code := e.run(args...)
		if code != 1 || !strings.Contains(errb, "zone policy stack was removed") || !strings.Contains(errb, "ADR 0091") ||
			!strings.Contains(errb, "no replacement until the label policy model ships") || !strings.Contains(errb, "nothing was changed") || strings.Contains(out, "nothing to push") {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
		e.noSeeds(out + errb)
		if e.accountJWT() != before {
			t.Fatalf("%v changed the server", args)
		}
	}
	if n := len(scopedSigners(e.claims())); n != 0 {
		t.Fatalf("%d scoped signers on the account", n)
	}
	if _, err := os.Lstat(filepath.Join(e.keyDir, "zones")); !os.IsNotExist(err) {
		t.Fatalf("a zones key directory was made: %v", err)
	}
}

// The Z1 guard stays and wins: an applied zone policy is detected from the account JWT's scoped signing keys and
// `policy apply` (and --dry-run) refuse with that more specific message, pushing nothing.
func TestPolicyApplyRefusesAZonePolicyOnTheServer(t *testing.T) {
	e := newApplyEnv(t, twoZones).withDirectory()
	e.applyZonePolicy()
	before := e.accountJWT()
	for _, args := range [][]string{{"policy", "apply"}, {"policy", "apply", "--dry-run"}} {
		out, errb, code := e.run(args...)
		if code != 1 || !strings.Contains(errb, "a zone policy is applied") || !strings.Contains(errb, "ADR 0091") || !strings.Contains(errb, "nothing was changed") || strings.Contains(errb, "no replacement") || strings.Contains(out, "nothing to push") {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
	}
	if e.accountJWT() != before {
		t.Fatal("a refused apply changed the server")
	}
}

// A policy file that still carries a zone is refused, naming ADR 0091 and what to remove, before any connection.
func TestPolicyApplyRefusesAFileThatCarriesAZone(t *testing.T) {
	for name, file := range map[string]string{
		"zone label":    "endpoints:\n  x: {labels: {zone: red}}\n",
		"subject_label": "subject_label: site\nendpoints:\n  x: {labels: {site: red}}\n",
	} {
		e := newApplyEnv(t, file).withDirectory()
		e.env["MASH_URL"] = "nats://127.0.0.1:1" // nothing listens: a refusal that connects would say so
		e.env["MASH_SYS_CREDS"] = filepath.Join(e.root, "absent.creds")
		for _, args := range [][]string{{"policy", "apply"}, {"policy", "apply", "--dry-run"}, {"policy", "check"}} {
			out, errb, code := e.run(args...)
			all := out + errb
			if code != 1 || !strings.Contains(all, "ADR 0091") || !strings.Contains(all, "remove") || strings.Contains(all, "connect") || strings.Contains(all, "system account credentials") {
				t.Fatalf("%s %v: %d\n%s", name, args, code, all)
			}
		}
	}
}

func TestZoneFlagsAreGone(t *testing.T) {
	for _, flag := range []string{"--replace-zone-key", "--remove-zone", "--keep-zone"} {
		for _, args := range [][]string{{flag, "a"}, {flag + "=a"}} {
			_, err := parseApplyArgs("apply", args)
			if err == nil || !strings.Contains(err.Error(), "ADR 0091") {
				t.Fatalf("%v: %v", args, err)
			}
		}
	}
	o, err := parseApplyArgs("apply", []string{"--servers", "3", "--dry-run"})
	if err != nil || o.servers != "3" || !o.dryRun {
		t.Fatalf("%v %v", o, err)
	}
	if _, err := parseApplyArgs("apply", []string{"--servers"}); err == nil {
		t.Fatal("a flag without a value parsed")
	}
}

func TestServersFlagAndEnvAreValidated(t *testing.T) {
	for _, args := range [][]string{{"--servers", "0"}, {"--servers=-1"}, {"--servers", "abc"}, {"--servers"}} {
		e := newApplyEnv(t, twoZones)
		if _, _, code := e.run(append([]string{"policy", "apply"}, args...)...); code != 2 {
			t.Fatalf("%v: exit %d, want 2", args, code)
		}
	}
	e := newApplyEnv(t, twoZones).withDirectory()
	e.env["MASH_SERVERS"] = "junk"
	if _, errb, code := e.run("policy", "apply"); code != 2 || !strings.Contains(errb, "MASH_SERVERS") {
		t.Fatalf("%d %s", code, errb)
	}
	e.env["MASH_SERVERS"] = "1"
	if out, errb, code := e.run("policy", "apply"); code != 1 || !strings.Contains(errb, "no replacement") {
		t.Fatalf("%d %s %s", code, out, errb)
	}
}

func TestApplyRefusals(t *testing.T) {
	e := newApplyEnv(t, twoZones).withDirectory()
	before := e.accountJWT()

	// a bad sys-creds path
	e.env["MASH_SYS_CREDS"] = filepath.Join(e.root, "nope.creds")
	_, errb, code := e.run("policy", "apply")
	if code != 1 || !strings.Contains(errb, "system account credentials") {
		t.Fatalf("bad creds: %d %q", code, errb)
	}
	// no creds anywhere
	e.env["MASH_SYS_CREDS"] = ""
	e.env["MASH_CONFIG_DIR"] = filepath.Join(e.root, "empty")
	adminCreds := e.env["MASH_ADMIN_CREDS"] // the default sys.creds is found next to the admin creds: take them away
	delete(e.env, "MASH_ADMIN_CREDS")
	out0, errb, code := e.run("policy", "drift")
	if code != 1 || !strings.Contains(out0+errb, "--sys-creds FILE or set MASH_SYS_CREDS") {
		t.Fatalf("no creds: %d %q %q", code, out0, errb)
	}
	e.env["MASH_ADMIN_CREDS"] = adminCreds
	// the user's own credentials are not system credentials
	e.env["MASH_SYS_CREDS"] = e.pl.Creds
	out, errb, code := e.run("policy", "apply")
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
	// usage: diff is a removed verb
	if _, errb, code := e.run("policy", "diff"); code != 2 || !strings.Contains(errb, "usage: mashctl policy") {
		t.Fatalf("diff: %d %q", code, errb)
	}
}

func TestSysCredsDefaultsFromTheInitServerDir(t *testing.T) {
	e := newApplyEnv(t, twoZones).withDirectory()
	// admin_creds in the environment points into the server directory: sys.creds is found next to it.
	e.env["MASH_SYS_CREDS"] = ""
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	e.env["MASH_INSECURE_LOOPBACK"] = "1"
	// the system creds were found: apply gets as far as its refusal
	out, errb, code := e.run("policy", "apply")
	if code != 1 || !strings.Contains(errb, "no replacement") {
		t.Fatalf("%d %s %s", code, out, errb)
	}
}

func TestFailingPolicyTestsRefuseApply(t *testing.T) {
	bad := twoZones + "tests:\n  - {from: x, to: y, expect: deny}\n"
	e := newApplyEnv(t, bad).withDirectory()
	before := e.accountJWT()

	for _, args := range [][]string{{"policy", "apply"}, {"policy", "apply", "--dry-run"}} {
		out, errb, code := e.run(args...)
		if code != 1 || !strings.Contains(out, "FAIL x->y expect deny got allow") || !strings.Contains(errb, "policy tests") {
			t.Fatalf("%v: %d %q %q", args, code, out, errb)
		}
	}
	if e.accountJWT() != before {
		t.Fatal("a refused apply changed the server")
	}

	// the same file with a test that holds passes the gate and meets the refusal
	good := twoZones + "tests:\n  - {from: x, to: y, expect: allow}\n"
	if err := os.WriteFile(e.policy, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := e.run("policy", "apply"); code != 1 || strings.Contains(out, "FAIL") || !strings.Contains(errb, "no replacement") {
		t.Fatalf("passing tests: %d %q %q", code, out, errb)
	}
}

// The preflight fails safe: a directory that cannot be read cannot be shown to hold no zoned record, so apply is
// refused and nothing is pushed. Here there are no admin credentials, then the directory has not been initialised.
func TestApplyRefusesWhenTheDirectoryCannotBeRead(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	before := e.accountJWT()
	out, errb, code := e.run("policy", "apply")
	if code != 1 || !strings.Contains(errb, "cannot read the directory to check for zoned records") || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("no admin creds: %d\n%s\n%s", code, out, errb)
	}
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds // reachable, but no directory bucket yet
	out, errb, code = e.run("policy", "apply")
	if code != 1 || !strings.Contains(errb, "cannot read the directory to check for zoned records") || !strings.Contains(errb, "mash admin init") {
		t.Fatalf("no directory: %d\n%s\n%s", code, out, errb)
	}
	if e.accountJWT() != before {
		t.Fatal("a refused apply pushed")
	}
}
