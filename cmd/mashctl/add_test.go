package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/mint"
	"github.com/bogworks/mash/internal/policy"
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
    labels: {zone: red, team: x}
  a2:
    labels: {zone: red}
  b:
    labels: {zone: blue}

rules:
  # team x may mail any tool
  - {name: x-tools, action: allow, from: {team: x}, to: {kind: tool}}

tests: []
`

func newAddEnv(t *testing.T, src string) *addEnv {
	t.Helper()
	e := testenv.Start(t)
	dir := t.TempDir()
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
	code := run(args, func(k string) string { return h.env[k] }, &out, &errb)
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
	data, err := os.ReadFile(h.policy)
	if err != nil {
		h.t.Fatal(err)
	}
	_, c, err := compileBytes(data, h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	m, err := mint.Mint(h.keys(), c, name, nil, true)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(h.creds(name), m.Creds, 0o600); err != nil {
		h.t.Fatal(err)
	}
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

func noSecrets(t *testing.T, outs ...string) {
	t.Helper()
	for _, o := range outs {
		for _, bad := range []string{"SU", "BEGIN NATS USER JWT", "BEGIN USER NKEY SEED", "eyJ"} {
			if strings.Contains(o, bad) {
				t.Errorf("output holds %q: %q", bad, o)
			}
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
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	h.seed("b")

	out, errb, code := h.run("add", "g", "--zone", "green", "--tool", "--label", "team=y", "--note", "first")
	if code != 0 || !strings.HasPrefix(out, "added g (zone green) creds "+h.creds("g")+"\n") {
		t.Fatalf("add: %d %q %q", code, out, errb)
	}
	// a green zone is new: the account JWT needs the new key; team x (a) gets a tag for the new tool
	if !strings.Contains(out, "account update needed: run mashctl policy apply") || !strings.Contains(out, "reissue needed: a (run: mashctl reissue a)") {
		t.Errorf("follow-up lines: %q", out)
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
	if len(recs) != 1 || recs[0] != "g/green/"+userPub {
		t.Fatalf("records %v (user %s)", recs, userPub)
	}
	got, _ := os.ReadFile(h.policy)
	for _, want := range []string{"# the fleet", "# a keeps its comment", "# team x may mail any tool", "  g:", "kind: tool", "zone: green", `team: "y"`} {
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

	// a is in team x: a second tool changes its allow tags again
	oldTags, userA := h.credsClaims("a")
	out, errb, code = h.run("add", "g2", "--zone", "blue", "--tool")
	if code != 0 {
		t.Fatalf("add g2: %d %q", code, errb)
	}
	if !strings.Contains(out, "reissue needed: a (run: mashctl reissue a)") {
		t.Fatalf("expected a to need a reissue: %q", out)
	}
	noSecrets(t, out, errb)
	if strings.Contains(out, "account update needed") {
		t.Errorf("blue exists already and its template did not change: %q", out)
	}

	// --changed finds a and only a (b has no allow exception)
	out, errb, code = h.run("reissue", "--changed")
	if code != 0 || !strings.HasPrefix(out, "reissued a (tags: allow:") || strings.Count(out, "reissued") != 1 {
		t.Fatalf("--changed: %d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	uc, pub := h.credsClaims("a")
	if pub != userA {
		t.Error("reissue changed the user key")
	}
	if slices.Equal([]string(uc.Tags), []string(oldTags.Tags)) {
		t.Errorf("tags unchanged: %v", uc.Tags)
	}
	if fi, _ := os.Stat(h.creds("a")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	if out, _, code := h.run("reissue", "--changed"); code != 0 || out != "nothing to reissue\n" {
		t.Fatalf("second --changed: %d %q", code, out)
	}
	// by name, even when nothing changed; the file is replaced and the key is still the same
	if out, _, code := h.run("reissue", "b"); code != 0 || out != "reissued b (tags: deny:_none)\n" {
		t.Fatalf("reissue b: %d %q", code, out)
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

func TestReissueNeedsZoneKeyAndNewIssuer(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	// a's creds were issued by a zone key that is no longer the zone's: --changed notices
	if err := os.Remove(filepath.Join(h.env["MASH_KEYS_DIR"], "zones", "red.nk")); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := h.run("reissue", "a"); code != 1 || !strings.Contains(errb, "reissue a:") || !strings.Contains(errb, "no signing key for zone") {
		t.Fatalf("%d %q", code, errb)
	}
	if _, err := h.keys().ZoneKey("red", true); err != nil {
		t.Fatal(err)
	}
	if out, _, code := h.run("reissue", "--changed"); code != 0 || !strings.HasPrefix(out, "reissued a ") {
		t.Fatalf("--changed after a new zone key: %d %q", code, out)
	}
}

func TestAddRefusesExistingZoneWithLostKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	keyFile := filepath.Join(h.env["MASH_KEYS_DIR"], "zones", "red.nk")
	if err := os.Remove(keyFile); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(h.policy)
	recs := h.records()
	out, errb, code := h.run("add", "n", "--zone", "red")
	if code != 1 || out != "" || !strings.Contains(errb, "the signing key for zone red is missing") || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if after, _ := os.ReadFile(h.policy); !bytes.Equal(before, after) {
		t.Error("policy changed")
	}
	if _, err := os.Stat(h.creds("n")); !os.IsNotExist(err) {
		t.Error("creds written")
	}
	if !slices.Equal(recs, h.records()) {
		t.Errorf("directory changed: %v -> %v", recs, h.records())
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Error("a new zone key was created")
	}
}

func TestAddNewZoneReusesLeftoverKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	kp, err := h.keys().ZoneKey("green", true) // left by an earlier failed add
	if err != nil {
		t.Fatal(err)
	}
	want, _ := kp.PublicKey()
	out, errb, code := h.run("add", "g", "--zone", "green")
	if code != 0 || !strings.Contains(out, "account update needed: run mashctl policy apply") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	got, err := h.keys().ZonePublic("green")
	if err != nil || got != want {
		t.Fatalf("zone key changed: %q vs %q (%v)", got, want, err)
	}
	if uc, _ := h.credsClaims("g"); uc.Issuer != want {
		t.Errorf("creds not signed by the leftover key: %s", uc.Issuer)
	}
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
		{[]string{"add", "Bad_Name"}, "endpoint name", 1},
		{[]string{"add", "a"}, "already declared", 1},
		{[]string{"add", "d", "--zone", "red", "--label", "zone=blue"}, "conflicts", 1},
		{[]string{"add", "d", "--label", "novalue"}, "--label wants k=v", 2},
		{[]string{"add", "d", "--bogus"}, "unknown option", 2},
		{[]string{"add", "d", "--zone", "Not Valid"}, "zone", 1},
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
	run([]string{"help"}, func(string) string { return "" }, &b, &b)
	for _, want := range []string{"mashctl add NAME", "mashctl reissue NAME"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("usage lacks %q", want)
		}
	}
}

// bigPolicy is a default-deny policy of n peers in zone red, each with a unique id label, and one allow rule per
// peer for sender id=big. Without a declared big the rules match nothing; once big is added (--label id=big) it is
// named in n exceptions and its user JWT carries a tag per peer.
func bigPolicy(n int, withBig bool) string {
	var b strings.Builder
	b.WriteString("default: deny\n\nendpoints:\n")
	if withBig {
		b.WriteString("  big:\n    labels: {zone: red, id: big}\n")
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "  p%03d:\n    labels: {zone: red, id: p%03d}\n", i, i)
	}
	b.WriteString("\nrules:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "  - {name: big-p%03d, action: allow, from: {id: big}, to: {id: p%03d}}\n", i, i)
	}
	b.WriteString("\ntests: []\n")
	return b.String()
}

func TestBudgetRefusal(t *testing.T) {
	if err := budgetRefusal("x", policy.BudgetReport{OK: true}, nil); err != nil {
		t.Errorf("ok report: %v", err)
	}
	err := budgetRefusal("x", policy.BudgetReport{}, errors.New("policy: the user JWT of x would be 9000 bytes with 300 tags"))
	want := "adding x would make the policy unusable: the user JWT of x would be 9000 bytes with 300 tags; nothing was changed"
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	if err := budgetRefusal("x", policy.BudgetReport{}, nil); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("not OK without an error: %v", err)
	}
}

func TestBudgetWarning(t *testing.T) {
	ok := policy.BudgetReport{OK: true, UserLimit: 4000, AccountLimit: 1 << 20, WorstUserJWTBytes: 3200, AccountJWTBytes: 1000, WorstUser: "w"}
	if w := budgetWarning(ok); w != "" {
		t.Errorf("exactly 80%%: %q", w)
	}
	ok.WorstUserJWTBytes = 3300
	if w := budgetWarning(ok); !strings.HasPrefix(w, "warning: ") || !strings.Contains(w, "user JWT of w") || strings.Contains(w, "account") {
		t.Errorf("user: %q", w)
	}
	ok.AccountJWTBytes = (1 << 20) * 9 / 10
	if w := budgetWarning(ok); !strings.Contains(w, "account JWT") || !strings.Contains(w, "user JWT") {
		t.Errorf("both: %q", w)
	}
}

func TestAddRefusedOverBudget(t *testing.T) {
	src := bigPolicy(260, false)
	// confirm the setup is really over the limit before asserting add's refusal
	_, c, err := compileBytes([]byte(bigPolicy(260, true)), "o")
	if err != nil {
		t.Fatal(err)
	}
	if rep, _ := policy.Budget(c); rep.OK {
		t.Fatalf("setup is not over budget: %+v", rep)
	}

	h := newAddEnv(t, src)
	before, _ := os.ReadFile(h.policy)
	_, errb, code := h.run("add", "big", "--zone", "red", "--label", "id=big")
	if code != 1 || !strings.Contains(errb, "adding big would make the policy unusable") ||
		!strings.Contains(errb, "user JWT of big") || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("add: %d %q", code, errb)
	}
	if after, _ := os.ReadFile(h.policy); !bytes.Equal(before, after) {
		t.Error("policy changed")
	}
	if _, err := os.Lstat(h.creds("big")); err == nil {
		t.Error("creds file written")
	}
	if r := h.records(); len(r) != 0 {
		t.Errorf("records %v", r)
	}
	if ents, _ := os.ReadDir(filepath.Join(h.dir, "keys")); len(ents) > 0 {
		for _, e := range ents {
			if strings.Contains(e.Name(), "red") {
				t.Errorf("zone key file %s", e.Name())
			}
		}
	}
}

func TestReissueRefusedOverBudget(t *testing.T) {
	h := newAddEnv(t, bigPolicy(260, true))
	h.seed("big")
	before, _ := os.ReadFile(h.creds("big"))
	_, errb, code := h.run("reissue", "big")
	if code != 1 || !strings.Contains(errb, "reissue big: its user JWT would be") || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("reissue: %d %q", code, errb)
	}
	if after, _ := os.ReadFile(h.creds("big")); !bytes.Equal(before, after) {
		t.Error("creds changed")
	}
}

// TestReissueChangedFindsCredsWithoutDenyNone: a creds file minted before every user carried deny:_none (simulated by
// minting with the compiled tags minus the placeholder) is stale, and --changed lists it and only it.
func TestReissueChangedFindsCredsWithoutDenyNone(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("b")
	data, err := os.ReadFile(h.policy)
	if err != nil {
		t.Fatal(err)
	}
	_, c, err := compileBytes(data, h.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	for i := range c.Endpoints {
		c.Endpoints[i].Tags = slices.DeleteFunc(slices.Clone(c.Endpoints[i].Tags), func(s string) bool { return s == admin.NoDenyTag })
	}
	m, err := mint.Mint(h.keys(), c, "a2", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.creds("a2"), m.Creds, 0o600); err != nil {
		t.Fatal(err)
	}
	if old, _ := h.credsClaims("a2"); slices.Contains([]string(old.Tags), admin.NoDenyTag) {
		t.Fatalf("the simulated old creds carry the placeholder: %v", old.Tags)
	}
	out, errb, code := h.run("reissue", "--changed")
	if code != 0 || out != "reissued a2 (tags: deny:_none)\n" {
		t.Fatalf("--changed: %d %q %q", code, out, errb)
	}
	if uc, _ := h.credsClaims("a2"); !slices.Contains([]string(uc.Tags), admin.NoDenyTag) {
		t.Errorf("tags after reissue: %v", uc.Tags)
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
