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
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// addEnv is a server, a config directory with a key store (account key made) and no policy file: endpoints are
// declared in the directory by `mashctl add`.
type addEnv struct {
	t   *testing.T
	e   *testenv.Env
	dir string
	env map[string]string
}

func newAddEnv(t *testing.T) *addEnv {
	t.Helper()
	e := testenv.Start(t)
	dir := t.TempDir()
	// writeCredsNew refuses a group-writable parent, and t.TempDir is 0777 minus the umask (0775 under umask 002).
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	h := &addEnv{t: t, e: e, dir: dir, env: map[string]string{
		"HOME": t.TempDir(), "MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_LOOPBACK": "1",
		"MASH_CONFIG_DIR": dir, "MASH_KEYS_DIR": filepath.Join(dir, "keys"),
	}}
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

// record is name's directory record, or false when there is none.
func (h *addEnv) record(name string) (store.Record, bool) {
	h.t.Helper()
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	recs, err := adm.List(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return findRecord(recs, name)
}

func TestAddAndReissue(t *testing.T) {
	fakeKeyRevoked(t, false, nil) // an add-only reissue first asks whether its key is revoked
	h := newAddEnv(t)
	h.seed("a")
	h.seed("b")

	out, errb, code := h.run("add", "g", "--label", "net=green", "--tool", "--label", "team=y", "--note", "first", "--describe", "a tool", "--cap", "build")
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
	rec, _ := h.record("g")
	if !reflect.DeepEqual(rec.Labels, map[string]string{"net": "green", "kind": "tool", "team": "y"}) || rec.Note != "first" || rec.Description != "a tool" || !slices.Equal(rec.Caps, []string{"build"}) {
		t.Errorf("record %+v", rec)
	}

	// the same name again: an error, nothing changes
	if _, errb, code := h.run("add", "g"); code != 1 || !strings.Contains(errb, "already exists in the directory") {
		t.Fatalf("second add: %d %q", code, errb)
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
	if _, errb, code := h.run("reissue", "a", "nobody"); code != 1 || !strings.Contains(errb, "reissue nobody: no creds file for nobody") {
		t.Fatalf("no such endpoint: %d %q", code, errb)
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
	for _, args := range [][]string{{"reissue"}, {"reissue", "--changed", "a"}, {"reissue", "--all", "a"}, {"reissue", "--all", "--changed"}, {"reissue", "--bogus"}, {"reissue", "a", "--policy", "x.yaml"}} {
		if _, _, code := h.run(args...); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

// `add` needs no file anywhere: it makes the directory record, the inbox, the creds file and the issue entry, and
// nothing else. The creds carry the owner-line permission set.
func TestAddNeedsNoFile(t *testing.T) {
	h := newAddEnv(t)
	before := dirNames(t, h.dir)
	out, errb, code := h.run("add", "g", "--label", "net=green")
	if code != 0 || out != "added g creds "+h.creds("g")+"\n" {
		t.Fatalf("add: %d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	// the config directory gained the creds file and nothing else
	after := dirNames(t, h.dir)
	if want := append(before, "g.creds"); !slices.Equal(slices.Sorted(slices.Values(after)), slices.Sorted(slices.Values(want))) {
		t.Errorf("config dir %v -> %v", before, after)
	}
	uc, userPub := h.credsClaims("g")
	if !reflect.DeepEqual(uc.Permissions, admin.Permissions(h.e.Owner, "g")) {
		t.Errorf("permissions %+v", uc.Permissions)
	}
	rec, ok := h.record("g")
	if !ok || rec.User != userPub || rec.Labels["net"] != "green" {
		t.Fatalf("record %+v %v", rec, ok)
	}
	// the issue record holds the user, recorded as an issue
	adm, err := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issuedRecords(t, adm)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range issued {
		if r.PublicKey == userPub && r.EffectivePurpose() == store.PurposeIssue {
			found = true
		}
	}
	if !found {
		t.Errorf("the issue record has no entry for %s: %+v", userPub, issued)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// `--policy` is gone: add (and the other verbs that took it) refuse it as an unknown option and create nothing.
func TestPolicyFlagIsGone(t *testing.T) {
	h := newAddEnv(t)
	for _, args := range [][]string{
		{"add", "g", "--policy", "p.yaml"}, {"add", "g", "--policy=p.yaml"},
		{"retire", "g", "--policy", "p.yaml"}, {"label", "g", "k=v", "--policy", "p.yaml"},
		{"unlabel", "g", "k", "--policy", "p.yaml"}, {"reissue", "g", "--policy", "p.yaml"},
	} {
		_, errb, code := h.run(args...)
		if code != 2 || !strings.Contains(errb, `unknown option "--policy"`) {
			t.Errorf("%v: %d %q", args, code, errb)
		}
	}
	if _, err := os.Lstat(h.creds("g")); err == nil {
		t.Error("creds written")
	}
	if recs := h.records(); len(recs) != 0 {
		t.Errorf("records %v", recs)
	}
}

// ADR 0091: add mints with the account signing key and the endpoint's own permissions. There is no zone key to make or
// to find, no `policy apply` to wait for, and the creds authenticate on a server whose account lists no scoped signer.
func TestAddMintsCredsThatAuthenticateWithoutAZoneKey(t *testing.T) {
	h := newAddEnv(t)
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

func TestAddFailingProvisionWritesNothing(t *testing.T) {
	h := newAddEnv(t)
	_, u := h.e.Mint(t, "c")
	if _, errb, code := h.run("provision", "c", "--user", u, "--account", h.e.Account); code != 0 {
		t.Fatal(errb)
	}
	out, errb, code := h.run("add", "c")
	if code != 1 || !strings.Contains(errb, "already exists in the directory") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, err := os.Stat(h.creds("c")); !os.IsNotExist(err) {
		t.Error("creds written")
	}
	if r := h.records(); len(r) != 1 || !strings.HasPrefix(r[0], "c//"+u) {
		t.Errorf("the earlier record was disturbed: %v", r)
	}
}

// A creds file that cannot be written after the record is made takes the record back out.
func TestAddRollsBackWhenTheCredsFileCannotBeWritten(t *testing.T) {
	h := newAddEnv(t)
	// the creds directory cannot be written, so the file is the step that fails, after the record is made
	if err := os.Chmod(h.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(h.dir, 0o700) })
	out, errb, code := h.run("add", "c")
	if code != 1 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if _, err := os.Stat(h.creds("c")); !os.IsNotExist(err) {
		t.Error("creds file left behind")
	}
	if r := h.records(); len(r) != 0 {
		t.Errorf("records left: %v", r)
	}
}

func TestAddRefusals(t *testing.T) {
	h := newAddEnv(t)
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
	h.seed("a")
	for _, tc := range []struct {
		args []string
		want string
		code int
	}{
		{[]string{"add"}, "no endpoint name", 2},
		{[]string{"add", "Bad_Name"}, "NAME is not a valid endpoint name", 2},
		{[]string{"add", "0"}, "NAME is not a valid endpoint name", 2},
		{[]string{"add", "Zed"}, "NAME is not a valid endpoint name", 2},
		{[]string{"add", "a"}, "already exists in the directory", 1},
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
	if out, errb, code := h.run("add", "d"); code != 0 {
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
	if strings.Contains(b.String(), "--policy") || strings.Contains(b.String(), "policy.yaml") {
		t.Errorf("usage mentions the policy file flag:\n%s", b.String())
	}
}

// TestReissueChangedFindsCredsSignedByAnotherKey: a creds file an older release signed with a zone key (any key but the
// account's, carrying tags and no permissions) is stale, and --changed lists it and only it; its reissue rotates the user.
func TestReissueChangedFindsCredsSignedByAnotherKey(t *testing.T) {
	h := newAddEnv(t)
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

// `reissue --all` covers every endpoint of the directory, and a rotation is recorded in the issue record.
func TestReissueAllCoversTheDirectory(t *testing.T) {
	fakeKeyRevoked(t, false, nil)
	h := newAddEnv(t)
	h.seed("a")
	h.seed("b")
	out, errb, code := h.run("reissue", "--all")
	if code != 0 || out != "reissued a\nreissued b\n" {
		t.Fatalf("--all: %d %q %q", code, out, errb)
	}
	// an endpoint with a record but no creds file here is reported by name and the rest are still done
	os.Remove(h.creds("b"))
	out, errb, code = h.run("reissue", "--all")
	if code != 1 || out != "reissued a\n" || !strings.Contains(errb, "reissue b: no creds file for b") {
		t.Fatalf("--all with b's creds gone: %d %q %q", code, out, errb)
	}
}

// `reissue NAME` rotates a creds file that differs from what a mint now issues, and records the new user key.
func TestReissueRotatesAndRecords(t *testing.T) {
	h := newAddEnv(t)
	h.seedWith("a", func(uc *jwt.UserClaims) { uc.Tags.Add("allow:gone.x") })
	calls := fakeRevoke(t, nil)
	_, oldPub := h.credsClaims("a")
	out, errb, code := h.run("reissue", "a")
	if code != 0 || out != "reissued a (new user key, old key revoked)\n" {
		t.Fatalf("reissue: %d %q %q", code, out, errb)
	}
	_, newPub := h.credsClaims("a")
	if newPub == oldPub || len(*calls) != 1 || (*calls)[0].users[0] != oldPub {
		t.Fatalf("rotation: new %s old %s calls %+v", newPub, oldPub, *calls)
	}
	if rec, _ := h.record("a"); rec.User != newPub {
		t.Errorf("the record names %s, not the new key %s", rec.User, newPub)
	}
	adm, err := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := issuedRecords(t, adm)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range issued {
		if r.PublicKey == newPub && r.EffectivePurpose() == store.PurposeRotation {
			found = true
		}
	}
	if !found {
		t.Errorf("the issue record has no rotation entry for %s", newPub)
	}
}

func TestAddOrphanRefusalText(t *testing.T) {
	const fix = "looks like an interrupted add (a creds file or an inbox exists but the directory has no complete record of %s): run mashctl retire %s --force --delete-creds, then mashctl add %s again"
	h := newAddEnv(t)
	// creds file only
	os.WriteFile(h.creds("c"), []byte("old"), 0o600)
	_, errb, code := h.run("add", "c")
	if code != 1 || !strings.Contains(errb, "c "+fmt.Sprintf(fix, "c", "c", "c")) {
		t.Fatalf("creds orphan: %d %q", code, errb)
	}
}

// (f) mashctl add applies the name rule first: a refused name creates no creds file and no record.
func TestAddAppliesTheNameRule(t *testing.T) {
	fakeKeyRevoked(t, false, nil)
	h := newAddEnv(t)
	rule := filepath.Join(h.dir, "name-rule.json")
	if err := os.WriteFile(rule, []byte(`{"version":1,"rules":[{"id":"no-g","match":{"name":{"is":"g"}},"message":"g is retired"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.env["MASH_NAME_RULE_FILE"] = rule
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
	if out, errb, code := h.run("add", "h", "--label", "net=green"); code != 0 {
		t.Fatalf("allowed add: %d %q %q", code, out, errb)
	}
}

func issuedRecords(t *testing.T, adm *admin.Admin) ([]store.Issued, error) {
	t.Helper()
	recs, _, err := adm.Issued(context.Background())
	return recs, err
}
