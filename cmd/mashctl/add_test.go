package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/testenv"
)

// addEnv is a server, a config directory with a key store (account key made) and a policy file.
type addEnv struct {
	t      *testing.T
	e      *testenv.Env
	dir    string
	env    map[string]string
	policy string
}

const addPolicy = `# the fleet
default: deny

endpoints:
  a:                # a keeps its comment
    labels: {net: red, team: x}
  a2:
    labels: {net: red}
  b:
    labels: {net: blue}

rules:
  # team x may mail any tool
  - {name: x-tools, action: allow, from: {team: x}, to: {kind: tool}}

tests: []
`

func newAddEnv(t *testing.T, src string) *addEnv {
	t.Helper()
	e := testenv.Start(t)
	dir := t.TempDir()
	// writeCredsNew refuses a group-writable parent, and t.TempDir is 0777 minus the umask (0775 under umask 002).
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	h := &addEnv{t: t, e: e, dir: dir, policy: filepath.Join(dir, "policy.yaml"), env: map[string]string{
		"HOME": t.TempDir(), "MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_LOOPBACK": "1",
		"MASH_CONFIG_DIR": dir, "MASH_KEYS_DIR": filepath.Join(dir, "keys"),
	}}
	if err := os.WriteFile(h.policy, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := h.run("init"); code != 0 {
		t.Fatal("init")
	}
	ka, err := ctlkeys.Open(h.env["MASH_KEYS_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ka.InitAccount(); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *addEnv) run(args ...string) (string, string, int) {
	h.t.Helper()
	var out, errb bytes.Buffer
	code := runEnv(args, func(k string) string { return h.env[k] }, &out, &errb)
	return out.String(), errb.String(), code
}

func (h *addEnv) keys() *ctlkeys.Authority {
	ka, err := ctlkeys.Open(h.env["MASH_KEYS_DIR"])
	if err != nil {
		h.t.Fatal(err)
	}
	return ka
}

func (h *addEnv) creds(name string) string { return filepath.Join(h.dir, name+".creds") }

// seed writes a creds file for a declared endpoint, as an earlier add would have.
func (h *addEnv) seed(name string) {
	h.t.Helper()
	h.seedWith(name, nil)
}

func (h *addEnv) credsClaims(name string) (*jwt.UserClaims, string) {
	h.t.Helper()
	raw, err := os.ReadFile(h.creds(name))
	if err != nil {
		h.t.Fatal(err)
	}
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		h.t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		h.t.Fatal(err)
	}
	return uc, uc.Subject
}

var seedLike = regexp.MustCompile(`(^|[^A-Z2-7])SU[A-Z2-7]{20}`)

func noSecrets(t *testing.T, outs ...string) {
	t.Helper()
	for _, o := range outs {
		for _, bad := range []string{"BEGIN NATS USER JWT", "BEGIN USER NKEY SEED", "eyJ"} {
			if strings.Contains(o, bad) {
				t.Errorf("output holds %q: %q", bad, o)
			}
		}
		// A user seed is "SU" then base32; a bare "SU" also turns up inside a public key id (e.g. "UC7MSUKQ...") by chance.
		if seedLike.MatchString(o) {
			t.Errorf("output holds a seed: %q", o)
		}
	}
}

func (h *addEnv) records() []string {
	h.t.Helper()
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	recs, err := adm.List(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, r := range recs {
		out = append(out, r.Name+"/"+r.Zone+"/"+r.User)
	}
	return out
}

func TestAddAndReissue(t *testing.T) {
	fakeKeyRevoked(t, false, nil) // an add-only reissue first asks whether its key is revoked
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	h.seed("b")

	out, errb, code := h.run("add", "g", "--label", "net=green", "--tool", "--label", "team=y", "--note", "first")
	if code != 0 || out != "added g creds "+h.creds("g")+"\n" {
		t.Fatalf("add: %d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	fi, err := os.Stat(h.creds("g"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("creds file: %v %v", fi, err)
	}
	uc, userPub := h.credsClaims("g")
	if uc.Name != "g" {
		t.Errorf("name %q", uc.Name)
	}
	recs := h.records()
	if !slices.Contains(recs, "g//"+userPub) || len(recs) != 3 { // a and b are seeded with records
		t.Fatalf("records %v (user %s)", recs, userPub)
	}
	got, _ := os.ReadFile(h.policy)
	for _, want := range []string{"# the fleet", "# a keeps its comment", "# team x may mail any tool", "  g:", "kind: tool", "net: green", `team: "y"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("policy lacks %q:\n%s", want, got)
		}
	}

	// the same name again: an error, nothing changes
	before, _ := os.ReadFile(h.policy)
	if _, errb, code := h.run("add", "g"); code != 1 || !strings.Contains(errb, "already declared") {
		t.Fatalf("second add: %d %q", code, errb)
	}
	if after, _ := os.ReadFile(h.policy); !bytes.Equal(before, after) {
		t.Error("policy changed by a refused add")
	}

	// a second tool changes no endpoint's creds: they carry no tags and the same permissions for every endpoint
	_, userA := h.credsClaims("a")
	if out, errb, code = h.run("add", "g2", "--label", "net=blue", "--tool"); code != 0 || out != "added g2 creds "+h.creds("g2")+"\n" {
		t.Fatalf("add g2: %d %q %q", code, out, errb)
	}
	if out, _, code := h.run("reissue", "--changed"); code != 0 || out != "nothing to reissue\n" {
		t.Fatalf("--changed: %d %q", code, out)
	}
	// by name, even when nothing changed; the file is replaced and the key is still the same
	if out, _, code := h.run("reissue", "a"); code != 0 || out != "reissued a\n" {
		t.Fatalf("reissue a: %d %q", code, out)
	}
	if _, pub := h.credsClaims("a"); pub != userA {
		t.Error("reissue changed the user key")
	}
	if fi, _ := os.Stat(h.creds("a")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	// errors name the endpoint
	if _, errb, code := h.run("reissue", "a", "nobody"); code != 1 || !strings.Contains(errb, "reissue nobody: endpoint \"nobody\" is not declared") {
		t.Fatalf("undeclared: %d %q", code, errb)
	}
	os.Remove(h.creds("b"))
	if _, errb, code := h.run("reissue", "b"); code != 1 || !strings.Contains(errb, "reissue b: no creds file for b: retire and add it again") {
		t.Fatalf("no creds: %d %q", code, errb)
	}
	// an open-mode creds file is refused
	if err := os.Chmod(h.creds("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := h.run("reissue", "a"); code != 1 || !strings.Contains(errb, "mode 0644") {
		t.Fatalf("open mode: %d %q", code, errb)
	}
	for _, args := range [][]string{{"reissue"}, {"reissue", "--changed", "a"}, {"reissue", "--bogus"}} {
		if _, _, code := h.run(args...); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

// ADR 0091: add mints with the account signing key and the endpoint's own permissions. There is no zone key to make or
// to find, no `policy apply` to wait for, and the creds authenticate on a server whose account lists no scoped signer.
func TestAddMintsCredsThatAuthenticateWithoutAZoneKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	out, errb, code := h.run("add", "g", "--label", "net=green")
	if code != 0 || out != "added g creds "+h.creds("g")+"\n" {
		t.Fatalf("add: %d %q %q", code, out, errb)
	}
	if _, err := os.Lstat(filepath.Join(h.env["MASH_KEYS_DIR"], "zones")); !os.IsNotExist(err) {
		t.Fatalf("a zones directory exists in the key store: %v", err)
	}
	uc, _ := h.credsClaims("g")
	akp, apub, err := h.keys().Account()
	if err != nil {
		t.Fatal(err)
	}
	akp.Wipe()
	if uc.Issuer != apub || uc.IssuerAccount != "" || len(uc.Tags) != 0 || !reflect.DeepEqual(uc.Permissions, admin.Permissions(h.e.Owner, "g")) {
		t.Fatalf("claims: issuer %s (account %s) issuer_account %q tags %v", uc.Issuer, apub, uc.IssuerAccount, uc.Tags)
	}

	// a server whose account is the key store's account and holds no signing key at all
	opKP, _ := nkeys.CreateOperator()
	opPub, _ := opKP.PublicKey()
	oc := jwt.NewOperatorClaims(opPub)
	opJWT, err := oc.Encode(opKP)
	if err != nil {
		t.Fatal(err)
	}
	op, err := jwt.DecodeOperatorClaims(opJWT)
	if err != nil {
		t.Fatal(err)
	}
	sysKP, _ := nkeys.CreateAccount()
	sysPub, _ := sysKP.PublicKey()
	resolver := &server.MemAccResolver{}
	for _, pub := range []string{sysPub, apub} {
		tok, err := jwt.NewAccountClaims(pub).Encode(opKP)
		if err != nil {
			t.Fatal(err)
		}
		if err := resolver.Store(pub, tok); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true,
		TrustedOperators: []*jwt.OperatorClaims{op}, AccountResolver: resolver, SystemAccount: sysPub})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats-server did not start")
	}
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	nc, err := nats.Connect(srv.ClientURL(), nats.UserCredentials(h.creds("g")), nats.MaxReconnects(0), nats.Timeout(5*time.Second))
	if err != nil {
		t.Fatalf("the creds did not authenticate: %v", err)
	}
	nc.Close()
}

func TestAddRollsBackWhenPolicyChanged(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	afterProvision = func() {
		f, _ := os.OpenFile(h.policy, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString("# edited by hand\n")
		f.Close()
	}
	t.Cleanup(func() { afterProvision = nil })
	out, errb, code := h.run("add", "c")
	if code != 1 || !strings.Contains(errb, "changed since it was loaded") || !strings.Contains(errb, "Re-run: mashctl add c") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if _, err := os.Stat(h.creds("c")); !os.IsNotExist(err) {
		t.Error("creds file left behind")
	}
	if r := h.records(); len(r) != 0 {
		t.Errorf("records left: %v", r)
	}
	got, _ := os.ReadFile(h.policy)
	if strings.Contains(string(got), "  c:") || !strings.HasSuffix(string(got), "# edited by hand\n") {
		t.Errorf("policy:\n%s", got)
	}
}

func TestAddFailingProvisionWritesNothing(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	_, u := h.e.Mint(t, "c")
	if _, errb, code := h.run("provision", "c", "--user", u, "--account", h.e.Account); code != 0 {
		t.Fatal(errb)
	}
	before, _ := os.ReadFile(h.policy)
	out, errb, code := h.run("add", "c")
	if code != 1 || !strings.Contains(errb, "already provisioned") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, err := os.Stat(h.creds("c")); !os.IsNotExist(err) {
		t.Error("creds written")
	}
	if after, _ := os.ReadFile(h.policy); !bytes.Equal(before, after) {
		t.Error("policy changed")
	}
	if r := h.records(); len(r) != 1 || !strings.HasPrefix(r[0], "c//"+u) {
		t.Errorf("the earlier record was disturbed: %v", r)
	}
}

func TestAddRefusals(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	if err := os.WriteFile(h.creds("c"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := h.run("add", "c"); code != 1 || !strings.Contains(errb, "creds file "+h.creds("c")+" exists") {
		t.Fatalf("existing creds: %d %q", code, errb)
	}
	if b, _ := os.ReadFile(h.creds("c")); string(b) != "old" {
		t.Error("creds overwritten")
	}
	if r := h.records(); len(r) != 0 {
		t.Errorf("records %v", r)
	}
	for _, tc := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"add"}, "no endpoint name", 2},
		{[]string{"add", "Bad_Name"}, "NAME is not a valid endpoint name", 2},
		{[]string{"add", "null"}, `endpoint name "null" would read as a YAML null, not a name: choose another name`, 2},
		{[]string{"add", "true"}, `would read as a YAML bool`, 2},
		{[]string{"add", "0"}, "NAME is not a valid endpoint name", 2},
		{[]string{"add", "Zed"}, "NAME is not a valid endpoint name", 2},
		{[]string{"add", "a"}, "already declared", 1},
		{[]string{"add", "d", "--tool", "--label", "kind=x"}, "conflicts", 1},
		{[]string{"add", "d", "--label", "novalue"}, "--label wants k=v", 2},
		{[]string{"add", "d", "--bogus"}, "unknown option", 2},
		{[]string{"add", "d", "--zone", "red"}, "unknown option", 2},
	} {
		_, errb, code := h.run(tc.args...)
		if code != tc.code || !strings.Contains(errb, tc.want) {
			t.Errorf("%v: %d %q, want %d %q", tc.args, code, errb, tc.code, tc.want)
		}
	}
	// no account key: the ctlkeys message, nothing touched
	h.env["MASH_KEYS_DIR"] = filepath.Join(h.dir, "nokeys")
	if _, errb, code := h.run("add", "d"); code != 1 || !strings.Contains(errb, "mashctl init") {
		t.Fatalf("no account key: %d %q", code, errb)
	}
	// a missing creds directory is made 0700
	h.env["MASH_KEYS_DIR"] = filepath.Join(h.dir, "keys")
	h.env["MASH_CONFIG_DIR"] = filepath.Join(h.dir, "new", "sub")
	if out, errb, code := h.run("add", "d", "--policy", h.policy); code != 0 {
		t.Fatalf("add into a new config dir: %d %q %q", code, out, errb)
	}
	if fi, err := os.Stat(filepath.Join(h.dir, "new", "sub")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("creds dir: %v %v", fi, err)
	}
}

func TestAddUsageListsVerbs(t *testing.T) {
	var b bytes.Buffer
	runEnv([]string{"help"}, func(string) string { return "" }, &b, &b)
	for _, want := range []string{"mashctl add NAME", "mashctl reissue NAME"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}

// TestReissueChangedFindsCredsSignedByAnotherKey: a creds file an older release signed with a zone key (any key but the
// account's, carrying tags and no permissions) is stale, and --changed lists it and only it; its reissue rotates the user.
func TestReissueChangedFindsCredsSignedByAnotherKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("b")
	h.seedWith("a2", func(uc *jwt.UserClaims) {
		uc.Permissions = jwt.Permissions{}
		uc.Tags.Add("deny:_none")
	})
	calls := fakeRevoke(t, nil)
	_, oldPub := h.credsClaims("a2")
	out, errb, code := h.run("reissue", "--changed")
	if code != 0 || out != "reissued a2 (new user key, old key revoked)\n" {
		t.Fatalf("--changed: %d %q %q", code, out, errb)
	}
	uc, newPub := h.credsClaims("a2")
	if newPub == oldPub || len(uc.Tags) != 0 || !reflect.DeepEqual(uc.Permissions, admin.Permissions(h.e.Owner, "a2")) {
		t.Fatalf("after reissue: %v %v", newPub == oldPub, uc.Tags)
	}
	if len(*calls) != 1 {
		t.Errorf("revoke calls %+v", *calls)
	}
	if out, _, code := h.run("reissue", "--changed"); code != 0 || out != "nothing to reissue\n" {
		t.Fatalf("second --changed: %d %q", code, out)
	}
}

func TestAddOrphanRefusalText(t *testing.T) {
	const fix = "looks like an interrupted add (a directory record or creds file exists but the policy file has no endpoint %s): run mashctl retire %s --force --delete-creds, then mashctl add %s again"
	h := newAddEnv(t, addPolicy)
	// creds file only
	os.WriteFile(h.creds("c"), []byte("old"), 0o600)
	_, errb, code := h.run("add", "c")
	if code != 1 || !strings.Contains(errb, "c "+fmt.Sprintf(fix, "c", "c", "c")) {
		t.Fatalf("creds orphan: %d %q", code, errb)
	}
	// directory record only
	_, u := h.e.Mint(h.t, "d")
	if _, errb, code := h.run("provision", "d", "--user", u, "--account", h.e.Account); code != 0 {
		t.Fatal(errb)
	}
	_, errb, code = h.run("add", "d")
	if code != 1 || !strings.Contains(errb, "d "+fmt.Sprintf(fix, "d", "d", "d")) {
		t.Fatalf("record orphan: %d %q", code, errb)
	}
}

// A policy file whose tests fail gates add and reissue exactly as it gates apply: nothing is minted or written.
func TestFailingPolicyTestsRefuseAddAndReissue(t *testing.T) {
	fakeKeyRevoked(t, false, nil)                                   // an add-only reissue first asks whether its key is revoked
	const failing = "tests:\n  - {from: a, to: b, expect: allow}\n" // the rules deny a->b
	bad := strings.Replace(addPolicy, "tests: []\n", failing, 1)
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	h.seed("b")
	credsA, _ := os.ReadFile(h.creds("a"))
	if err := os.WriteFile(h.policy, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	polBefore, _ := os.ReadFile(h.policy)
	recs := h.records()

	out, errb, code := h.run("add", "g", "--label", "net=green", "--tool")
	if code != 1 || !strings.Contains(out, "FAIL a->b expect allow got deny") || !strings.Contains(errb, "refusing: 1 of 1 policy tests in "+h.policy+" fail (nothing was changed, no key or creds file was created") {
		t.Fatalf("add: %d %q %q", code, out, errb)
	}
	if _, err := os.Lstat(h.creds("g")); err == nil {
		t.Error("add wrote a creds file")
	}
	if got := h.records(); !slices.Equal(got, recs) {
		t.Errorf("records %v -> %v", recs, got)
	}
	if after, _ := os.ReadFile(h.policy); !bytes.Equal(polBefore, after) {
		t.Error("policy changed by a refused add")
	}

	for _, args := range [][]string{{"reissue", "a"}, {"reissue", "--changed"}} {
		out, errb, code = h.run(args...)
		if code != 1 || !strings.Contains(out, "FAIL a->b expect allow got deny") || !strings.Contains(errb, "refusing: 1 of 1 policy tests in "+h.policy+" fail (nothing was changed, no creds file was rewritten)") {
			t.Fatalf("%v: %d %q %q", args, code, out, errb)
		}
	}
	if got, _ := os.ReadFile(h.creds("a")); !bytes.Equal(got, credsA) {
		t.Error("a refused reissue rewrote the creds")
	}

	// the same file with a test that holds mints and reissues
	good := strings.Replace(addPolicy, "tests: []\n", "tests:\n  - {from: a, to: b, expect: deny}\n", 1)
	if err := os.WriteFile(h.policy, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := h.run("add", "g", "--label", "net=green", "--tool"); code != 0 {
		t.Fatalf("add with passing tests: %d %q %q", code, out, errb)
	}
	if out, errb, code := h.run("reissue", "a"); code != 0 || !strings.HasPrefix(out, "reissued a") {
		t.Fatalf("reissue with passing tests: %d %q %q", code, out, errb)
	}
}

// A file that does not check (a deny rule selecting nobody) is refused by add and reissue before anything is minted.
func TestAddAndReissueRefuseAFileThatDoesNotCheck(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	credsA, _ := os.ReadFile(h.creds("a"))
	bad := strings.Replace(addPolicy, "\ntests: []", "  - {name: typo, action: deny, from: {tema: x}, to: {}}\n\ntests: []", 1)
	if err := os.WriteFile(h.policy, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := h.run("add", "n", "--label", "net=red"); code != 1 || !strings.Contains(errb, "set allow_empty: true on the rule") {
		t.Fatalf("add: %d %q", code, errb)
	}
	if _, err := os.Lstat(h.creds("n")); err == nil {
		t.Error("add wrote a creds file")
	}
	if _, errb, code := h.run("reissue", "a"); code != 1 || !strings.Contains(errb, "set allow_empty: true on the rule") {
		t.Fatalf("reissue: %d %q", code, errb)
	}
	if got, _ := os.ReadFile(h.creds("a")); !bytes.Equal(got, credsA) {
		t.Error("creds rewritten")
	}
}

// (f) mashctl add applies the name rule first: a refused name creates no creds file, no record and no policy edit.
func TestAddAppliesTheNameRule(t *testing.T) {
	fakeKeyRevoked(t, false, nil)
	h := newAddEnv(t, addPolicy)
	rule := filepath.Join(h.dir, "name-rule.json")
	if err := os.WriteFile(rule, []byte(`{"version":1,"rules":[{"id":"no-g","match":{"name":{"is":"g"}},"message":"g is retired"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.env["MASH_NAME_RULE_FILE"] = rule
	before, _ := os.ReadFile(h.policy)
	_, errb, code := h.run("add", "g", "--label", "net=green")
	if code != 1 || !strings.Contains(errb, `name rule "no-g"`) || !strings.Contains(errb, "g is retired") {
		t.Fatalf("refused add: %d %q", code, errb)
	}
	if _, err := os.Stat(h.creds("g")); err == nil {
		t.Error("a refused add wrote creds")
	}
	if recs := h.records(); len(recs) != 0 {
		t.Errorf("a refused add wrote records: %v", recs)
	}
	if after, _ := os.ReadFile(h.policy); !bytes.Equal(before, after) {
		t.Error("a refused add changed the policy")
	}
	if out, errb, code := h.run("add", "h", "--label", "net=green"); code != 0 {
		t.Fatalf("allowed add: %d %q %q", code, out, errb)
	}
}
