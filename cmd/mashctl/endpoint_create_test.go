package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/sap"
)

var updateEndpointCreateGolden = flag.Bool("update-endpoint-create", false, "rewrite cmd/mashctl/testdata/endpoint_create.golden.json")

// epKey makes a user key pair and writes its public half to a file in dir.
func epKey(t *testing.T, dir, name string) (pub, path string, seed []byte) {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ = kp.PublicKey()
	seed, _ = kp.Seed()
	path = filepath.Join(dir, name+".pub")
	if err := os.WriteFile(path, []byte(pub+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return pub, path, seed
}

func fileMode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// readJoin reads a join file: its fields as a struct, and as a map to check that no other key is there. A test that
// fails never prints the file.
func readJoin(t *testing.T, path string) (joinFile, map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var j joinFile
	var m map[string]any
	if json.Unmarshal(raw, &j) != nil || json.Unmarshal(raw, &m) != nil {
		t.Fatalf("%s is not JSON", path)
	}
	if !strings.HasSuffix(string(raw), "}\n") || strings.HasSuffix(string(raw), "\n\n") || !strings.HasPrefix(string(raw), "{\n  \"mash_join\": 1,\n") {
		t.Errorf("%s: wrong layout (two-space indent, one trailing newline)", path)
	}
	if fileMode(t, path) != 0o600 {
		t.Errorf("%s: mode %v, want 0600", path, fileMode(t, path))
	}
	return j, m
}

// joinKeys are the keys of a join file that holds creds, and of one that holds a user JWT.
var (
	joinKeysCreds = []string{"address", "ca", "creds", "mash_join", "servers"}
	joinKeysJWT   = []string{"address", "ca", "mash_join", "servers", "user_jwt"}
)

func sameKeys(m map[string]any, want []string) bool {
	if len(m) != len(want) {
		return false
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

// enrollCreateFor runs the library part of a create, as a run that stopped before it wrote the file would have.
func enrollCreateFor(h *fleetEnv, a sap.Address, pub string) (enroll.Result, error) {
	_, acct, err := h.keys().Account()
	if err != nil {
		return enroll.Result{}, err
	}
	_ = acct
	return enroll.Create(context.Background(), enroll.Deps{Keys: h.keys(), Admin: h.adminOf(), Account: h.accountPub()},
		enroll.Request{Addr: a, PublicKey: pub})
}

// wantUsage checks a wrong command line: exit 2, nothing on stdout, `error: ...` and the help hint, nothing else.
func wantUsage(t *testing.T, so, se string, code int, verb, msg string) {
	t.Helper()
	if code != 2 || so != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, so, se)
	}
	lines := strings.Split(strings.TrimRight(se, "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "error: ") || !strings.Contains(lines[0], msg) || lines[1] != "hint: mashctl endpoint "+verb+" --help" {
		t.Errorf("stderr %q, want error containing %q and the help hint", se, msg)
	}
}

func TestEndpointCreateAddressForms(t *testing.T) {
	h := newFleetEnv(t)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	owner := ad("x").Owner
	for _, in := range []string{owner + "/g/a1", "a2", "g.a3", owner + ".g.a4.extra", owner + "/g", ""} {
		so, se, code := h.run("endpoint", "create", in, "--dry-run")
		wantUsage(t, so, se, code, "create", "not an endpoint address")
	}
	if so, se, code := h.run("endpoint", "create", "--dry-run"); code != 2 || !strings.Contains(se, "error: missing the endpoint address") || so != "" {
		t.Errorf("no address: %d %q %q", code, so, se)
	}
	if so, se, code := h.run("endpoint", "create", ad("a").String(), ad("b").String()); code != 2 || !strings.Contains(se, "error: too many arguments") || so != "" {
		t.Errorf("two addresses: %d %q %q", code, so, se)
	}
	so, _, code := h.run("endpoint", "create", "a2", "--json")
	if code != 2 || !strings.HasPrefix(so, `{"error":"not an endpoint address`) || !strings.Contains(so, `"hint":"mashctl endpoint create --help"`) {
		t.Errorf("json: %d %q", code, so)
	}
	if len(h.records()) != 0 {
		t.Error("a wrong command line made a record")
	}
}

func TestEndpointCreateRemovedOptions(t *testing.T) {
	h := newFleetEnv(t)
	a := ad("gone").String()
	for _, args := range [][]string{
		{"--make-key"}, {"--jwt-out", "x.jwt"}, {"--creds-out", "x"}, {"--tool"}, {"--note", "n"}, {"--describe", "d"},
		{"--role", "r"}, {"--host", "h"}, {"--cap", "c"}, {"--label", "k=v"}, {"--owner", "o"}, {"--group", "g"}, {"--name", "n"},
		{"--bogus"},
	} {
		so, se, code := h.run(append([]string{"endpoint", "create", a}, args...)...)
		wantUsage(t, so, se, code, "create", "unknown option")
		if !strings.Contains(se, args[0]) {
			t.Errorf("%v: stderr %q does not name the option", args, se)
		}
	}
	if len(h.records()) != 0 {
		t.Error("a refused command made a record")
	}
}

func TestEndpointCreatePublicKeyNeedsOut(t *testing.T) {
	h := newFleetEnv(t)
	_, pubFile, _ := epKey(t, t.TempDir(), "k")
	so, se, code := h.run("endpoint", "create", ad("k1").String(), "--public-key", pubFile)
	if code != 2 || so != "" || se != "error: --public-key needs --out, the join file to write\nhint: mashctl endpoint create --help\n" {
		t.Fatalf("%d %q %q", code, so, se)
	}
	so, _, code = h.run("endpoint", "create", ad("k1").String(), "--public-key", pubFile, "--json")
	if code != 2 || !strings.HasPrefix(so, `{"error":"--public-key needs --out, the join file to write"`) {
		t.Errorf("json %d %q", code, so)
	}
	if _, _, code = h.run("endpoint", "create", ad("k1").String(), "--public-key", "--out", "x"); code != 2 {
		t.Errorf("flag as value: %d", code)
	}
	if len(h.records()) != 0 {
		t.Fatal("a refused command made a record")
	}
}

// With no option the key pair is made here and <address>.creds is written to the config folder.
func TestEndpointCreateDefaultInstalls(t *testing.T) {
	h := newFleetEnv(t)
	a := ad("same")
	so, se, code := h.run("endpoint", "create", a.String())
	if code != 0 || so != "Created endpoint "+a.String()+"\n" {
		t.Fatalf("code %d %q %q", code, so, se)
	}
	p := h.creds(a)
	if fileMode(t, p) != 0o600 {
		t.Fatalf("creds mode %v", fileMode(t, p))
	}
	raw, _ := os.ReadFile(p)
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		t.Fatal(err)
	}
	uc, _ := jwt.DecodeUserClaims(tok)
	kp, err := jwt.ParseDecoratedUserNKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	pub, _ := kp.PublicKey()
	if uc.Subject != pub || uc.Name != a.String() {
		t.Error("the creds do not match the endpoint")
	}
	if rec, ok := h.record(a); !ok || rec.User != pub {
		t.Errorf("record %+v %v", rec, ok)
	}
	if strings.Contains(so+se, string(seed)) || strings.Contains(so+se, tok) {
		t.Error("output holds a secret")
	}
	if !gone(t, p+".pending") {
		t.Error("the staged key was left")
	}
	// a second create of the address is refused and changes nothing
	so, se, code = h.run("endpoint", "create", a.String())
	if code != 1 || so != "" || se != "error: "+a.String()+" already exists\n" {
		t.Errorf("second: %d %q %q", code, so, se)
	}
	if got, _ := os.ReadFile(p); string(got) != string(raw) {
		t.Error("creds file changed")
	}
	// --json
	so, _, code = h.run("endpoint", "create", ad("same2").String(), "--json")
	var doc map[string]any
	if json.Unmarshal([]byte(so), &doc) != nil || code != 0 || !sameKeys(doc, []string{"address", "installed_here", "creds", "join_file"}) ||
		doc["installed_here"] != true || doc["join_file"] != nil || doc["address"] != ad("same2").String() ||
		doc["creds"] != h.creds(ad("same2")) {
		t.Errorf("json %d %q", code, so)
	}
	if _, err := os.Stat(h.creds(ad("same2"))); err != nil {
		t.Errorf("the creds path in the json is not a file: %v", err)
	}
	// --verbose
	so, _, code = h.run("endpoint", "create", ad("same3").String(), "--detail")
	if code != 0 || so != "Created endpoint "+ad("same3").String()+"\n" { // --detail adds nothing to create
		t.Errorf("verbose %d %q", code, so)
	}
}

// With --out the key pair is made and signed, nothing is installed here, and the join file holds the creds.
func TestEndpointCreateOut(t *testing.T) {
	h := newFleetEnv(t)
	out := t.TempDir()
	a := ad("far")
	jf := filepath.Join(out, "far.join")
	so, se, code := h.run("endpoint", "create", a.String(), "--out", jf)
	if code != 0 || so != "Created endpoint "+a.String()+"\nJoin file: "+jf+"\n" {
		t.Fatalf("code %d %q %q", code, so, se)
	}
	if _, err := os.Lstat(h.creds(a)); err == nil {
		t.Error("a creds file was installed here")
	}
	if !gone(t, jf+".pending") || !gone(t, h.creds(a)+".pending") {
		t.Error("a staged key was left")
	}
	j, m := readJoin(t, jf)
	if !sameKeys(m, joinKeysCreds) {
		t.Errorf("keys %v", m)
	}
	if j.MashJoin != 1 || j.Address != a.String() || len(j.Servers) != 1 || j.Servers[0] != h.env["MASH_SERVER"] || j.CA != nil || j.UserJWT != "" {
		t.Errorf("fields %+v", j.Address)
	}
	if _, ok := m["ca"]; !ok || m["ca"] != nil {
		t.Error("ca is not null")
	}
	tok, err := jwt.ParseDecoratedJWT([]byte(j.Creds))
	if err != nil {
		t.Fatal(err)
	}
	uc, _ := jwt.DecodeUserClaims(tok)
	kp, err := jwt.ParseDecoratedUserNKey([]byte(j.Creds))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	if uc.Subject != pub || uc.Name != a.String() {
		t.Error("the creds in the join file do not match the endpoint")
	}
	if rec, ok := h.record(a); !ok || rec.User != pub {
		t.Errorf("record %+v %v", rec, ok)
	}
	seed, _ := kp.Seed()
	noSecrets(t, so, se)
	if strings.Contains(so+se, string(seed)) || strings.Contains(so+se, tok) {
		t.Error("output holds a secret")
	}
	// the join file path is never overwritten
	before := strings.Join(h.records(), ",")
	so, se, code = h.run("endpoint", "create", ad("far2").String(), "--out", jf)
	if code != 1 || so != "" || se != "error: "+jf+" already exists\n" {
		t.Errorf("existing path: %d %q %q", code, so, se)
	}
	if strings.Join(h.records(), ",") != before {
		t.Error("a refusal changed the directory")
	}
	// --json and --verbose
	jf2 := filepath.Join(out, "far3.join")
	so, _, code = h.run("endpoint", "create", ad("far3").String(), "--out", jf2, "--json")
	var doc map[string]any
	if json.Unmarshal([]byte(so), &doc) != nil || code != 0 || !sameKeys(doc, []string{"address", "installed_here", "creds", "join_file"}) ||
		doc["installed_here"] != false || doc["creds"] != nil || doc["join_file"] != jf2 {
		t.Errorf("json %d %q", code, so)
	}
	jf3 := filepath.Join(out, "far4.join")
	so, _, code = h.run("endpoint", "create", ad("far4").String(), "--out", jf3, "--detail")
	if code != 0 || so != "Created endpoint "+ad("far4").String()+"\nJoin file: "+jf3+"\n" {
		t.Errorf("verbose %d %q", code, so)
	}
}

// testCAPEM is a self-signed certificate as PEM text.
func testCAPEM(t *testing.T) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// The configured CA file is read into the join file as PEM text.
func TestEndpointCreateOutCarriesCA(t *testing.T) {
	h := newFleetEnv(t)
	out := t.TempDir()
	ca := filepath.Join(out, "ca.pem")
	caPEM := testCAPEM(t)
	if err := os.WriteFile(ca, []byte(caPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := joinBaseOf(config.Config{URL: "tls://a:4222, tls://b:4222", TLSCA: ca})
	if err != nil || base.ca == nil || *base.ca != caPEM || len(base.servers) != 2 || base.servers[1] != "tls://b:4222" {
		t.Fatalf("base %+v %v", base, err)
	}
	j := base.withCreds("o.g.a", []byte("creds text\n"))
	raw, err := j.Encode()
	if err != nil || !strings.Contains(string(raw), `"ca": "-----BEGIN CERTIFICATE-----\n`) {
		t.Errorf("encoded %q %v", raw, err)
	}
	if _, err := joinBaseOf(config.Config{}); err == nil {
		t.Error("no server accepted")
	}
	h.env["MASH_CA"] = filepath.Join(out, "missing.pem")
	jf2 := filepath.Join(out, "b.join")
	so, se, code := h.run("endpoint", "create", ad("b").String(), "--out", jf2)
	if code != 1 || so != "" || !strings.HasPrefix(se, "error: cannot read the CA file") || len(h.records()) != 0 || !gone(t, jf2) {
		t.Errorf("missing CA file: %d %q %q", code, so, se)
	}
}

// With --public-key (and --out) the key is the endpoint's own: no seed is made, and the join file holds the user JWT.
func TestEndpointCreatePublicKey(t *testing.T) {
	h := newFleetEnv(t)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	out := t.TempDir()
	pub, pubFile, seed := epKey(t, out, "k")
	a := ad("design")
	jf := filepath.Join(out, "design.join")
	so, se, code := h.run("endpoint", "create", a.String(), "--out", jf, "--public-key", pubFile)
	if code != 0 || so != "Created endpoint "+a.String()+"\nJoin file: "+jf+"\n" {
		t.Fatalf("code %d: %q %q", code, so, se)
	}
	j, m := readJoin(t, jf)
	if !sameKeys(m, joinKeysJWT) || j.Creds != "" || j.Address != a.String() || j.MashJoin != 1 {
		t.Errorf("keys %v", m)
	}
	uc, err := jwt.DecodeUserClaims(j.UserJWT)
	if err != nil || uc.Subject != pub || uc.Name != a.String() {
		t.Fatalf("jwt: %v", err)
	}
	for _, s := range []string{so, se} {
		if strings.Contains(s, j.UserJWT) || strings.Contains(s, string(seed)) {
			t.Errorf("output holds a secret: %q", s)
		}
	}
	// no seed anywhere: not in the join file, the config folder, the key store or beside the join file
	for _, d := range []string{out, h.dir} {
		filepath.Walk(d, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			c, _ := os.ReadFile(p)
			if seedLike.Match(c) || strings.Contains(string(c), "BEGIN USER NKEY SEED") {
				t.Errorf("%s holds a seed", p)
			}
			return nil
		})
	}
	if _, err := os.Lstat(h.creds(a)); err == nil {
		t.Error("a creds file was installed here")
	}
	rec, ok := h.record(a)
	if !ok || rec.User != pub || len(rec.Labels) != 0 {
		t.Fatalf("record %+v %v", rec, ok)
	}
	iss, found, err := h.adminOf().IssuedByKey(context.Background(), pub)
	if err != nil || !found || iss.Address != a.Flat() {
		t.Fatalf("issue record %+v %v %v", iss, found, err)
	}
	// the key is used at one address
	_, se, code = h.run("endpoint", "create", ad("other").String(), "--out", filepath.Join(out, "o.join"), "--public-key", pubFile)
	if code != 1 || !strings.HasPrefix(se, "error: ") || !gone(t, filepath.Join(out, "o.join")) {
		t.Errorf("same key at a second address: %d %q", code, se)
	}
	// the address exists
	_, pub2File, _ := epKey(t, out, "k2")
	so, se, code = h.run("endpoint", "create", a.String(), "--out", filepath.Join(out, "t.join"), "--public-key", pub2File, "--json")
	if code != 1 || so != `{"error":"`+a.String()+` already exists"}`+"\n" || se != "" || !gone(t, filepath.Join(out, "t.join")) {
		t.Fatalf("taken: %d %q %q", code, so, se)
	}
	// an invalid key
	bad := filepath.Join(out, "bad.pub")
	if err := os.WriteFile(bad, []byte("not-a-key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, se, code = h.run("endpoint", "create", ad("z").String(), "--out", filepath.Join(out, "z.join"), "--public-key", bad)
	if code != 1 || !strings.HasPrefix(se, "error: ") || !strings.Contains(se, "does not hold a NATS user public key") {
		t.Errorf("bad key: %d %q", code, se)
	}
	// a missing key file
	_, se, code = h.run("endpoint", "create", ad("z").String(), "--out", filepath.Join(out, "z.join"), "--public-key", filepath.Join(out, "nope.pub"))
	if code != 1 || !strings.Contains(se, "cannot read the public key file") {
		t.Errorf("missing key file: %d %q", code, se)
	}
}

func TestEndpointCreateRefusals(t *testing.T) {
	h := newFleetEnv(t)
	out := t.TempDir()
	// the address exists
	h.seed(ad("here"))
	so, se, code := h.run("endpoint", "create", ad("here").String())
	if code != 1 || so != "" || se != "error: "+ad("here").String()+" already exists\n" {
		t.Errorf("exists: %d %q %q", code, so, se)
	}
	// the address was deleted
	h.seed(ad("deleted"))
	if _, se, code := h.run("endpoint", "delete", ad("deleted").String(), "--yes"); code != 0 {
		t.Fatalf("delete: %q", se)
	}
	for _, args := range [][]string{{}, {"--out", filepath.Join(out, "d.join")}} {
		so, se, code = h.run(append([]string{"endpoint", "create", ad("deleted").String()}, args...)...)
		if code != 1 || so != "" || se != "error: "+ad("deleted").String()+" was deleted and its address cannot be used again\n" {
			t.Errorf("deleted %v: %d %q %q", args, code, so, se)
		}
	}
	if !gone(t, filepath.Join(out, "d.join")) || !gone(t, h.creds(ad("deleted"))+".pending") {
		t.Error("a refused create left a file")
	}
	// no server configured: nothing is written
	delete(h.env, "MASH_SERVER")
	jf := filepath.Join(out, "n.join")
	for _, args := range [][]string{{}, {"--out", jf}} {
		so, se, code = h.run(append([]string{"endpoint", "create", ad("noserver").String()}, args...)...)
		if code != 1 || so != "" || !strings.HasPrefix(se, "error: no server is configured\nhint: mashctl endpoint create ") {
			t.Errorf("no server %v: %d %q %q", args, code, so, se)
		}
	}
	if !gone(t, jf) || !gone(t, h.creds(ad("noserver"))) || !gone(t, h.creds(ad("noserver"))+".pending") {
		t.Error("a refused create wrote a file")
	}
	// no config folder to install in
	h2 := newFleetEnv(t)
	noConfigDir(t, &refEnv{h: h2})
	so, se, code = h2.run("endpoint", "create", ad("nodir").String())
	if code != 1 || so != "" || !strings.HasPrefix(se, "error: no config folder to write the credentials file in\nhint: mashctl endpoint create ") {
		t.Errorf("no config folder: %d %q %q", code, so, se)
	}
}

func TestEndpointCreateRerunCompletes(t *testing.T) {
	h := newFleetEnv(t)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	out := t.TempDir()
	pub, pubFile, _ := epKey(t, out, "k")
	a := ad("again")
	if _, err := enrollCreateFor(h, a, pub); err != nil {
		t.Fatal(err)
	}
	jf := filepath.Join(out, "again.join")
	so, se, code := h.run("endpoint", "create", a.String(), "--out", jf, "--public-key", pubFile)
	if code != 0 {
		t.Fatalf("rerun: %d %q %q", code, so, se)
	}
	if j, _ := readJoin(t, jf); j.UserJWT == "" {
		t.Error("no user JWT")
	}
}

// epSnapshot is the directory records, the issue records and the files under dirs, as text.
func epSnapshot(t *testing.T, h *fleetEnv, dirs ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(strings.Join(h.records(), ",") + "\n")
	iss, _, err := h.adminOf().Issued(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range iss {
		b.WriteString(i.PublicKey + i.Address + i.IssuedAt + "\n")
	}
	for _, d := range dirs {
		filepath.Walk(d, func(p string, fi os.FileInfo, err error) error {
			// the .lock file of a reissue stays after the run; it holds no state
			if err == nil && !fi.IsDir() && !strings.HasSuffix(p, ".lock") {
				c, _ := os.ReadFile(p)
				b.WriteString(p + "=" + string(c) + "\n")
			}
			return nil
		})
	}
	return b.String()
}

func TestEndpointCreateDryRun(t *testing.T) {
	h := newFleetEnv(t)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	out := t.TempDir()
	_, pubFile, _ := epKey(t, out, "k")
	before := epSnapshot(t, h, h.dir, out)
	for _, args := range [][]string{
		{"endpoint", "create", ad("dry").String(), "--out", filepath.Join(out, "dry.join"), "--public-key", pubFile, "--dry-run"},
		{"endpoint", "create", ad("dry").String(), "--out", filepath.Join(out, "dry2.join"), "--dry-run"},
		{"endpoint", "create", ad("dry").String(), "--dry-run"},
	} {
		so, se, code := h.run(args...)
		if code != 0 || so != "Would create endpoint "+ad("dry").String()+"\n" {
			t.Fatalf("%v: %d %q %q", args, code, so, se)
		}
	}
	so, _, _ := h.run("endpoint", "create", ad("dry").String(), "--dry-run", "--json")
	var doc map[string]any
	if json.Unmarshal([]byte(so), &doc) != nil || doc["dry_run"] != true || doc["installed_here"] != true ||
		doc["creds"] != h.creds(ad("dry")) || doc["join_file"] != nil {
		t.Errorf("json %q", so)
	}
	so, _, _ = h.run("endpoint", "create", ad("dry").String(), "--out", filepath.Join(out, "dry3.join"), "--dry-run", "--json")
	doc = nil
	if json.Unmarshal([]byte(so), &doc) != nil || doc["dry_run"] != true || doc["installed_here"] != false ||
		doc["creds"] != nil || doc["join_file"] != filepath.Join(out, "dry3.join") {
		t.Errorf("json --out %q", so)
	}
	if after := epSnapshot(t, h, h.dir, out); after != before {
		t.Errorf("dry-run changed state:\n%s\n---\n%s", before, after)
	}
	h.seed(ad("taken"))
	if _, se, code := h.run("endpoint", "create", ad("taken").String(), "--dry-run"); code != 1 || se != "error: "+ad("taken").String()+" already exists\n" {
		t.Errorf("taken dry-run: %d %q", code, se)
	}
}

func TestEndpointCreateHelp(t *testing.T) {
	noenv := func(string) string { return "" }
	for _, v := range []string{"create", "reissue"} {
		for _, f := range []string{"-h", "--help"} {
			var out, errb strings.Builder
			if code := runEnv([]string{"endpoint", v, f}, noenv, &out, &errb); code != 0 || !strings.Contains(out.String(), "Example:") {
				t.Errorf("%s %s: %d %q", v, f, code, out.String())
			}
			for _, gone := range []string{"--make-key", "--jwt-out", "--creds-out", "--all", "--changed", "--label", "--tool"} {
				if strings.Contains(out.String(), gone) {
					t.Errorf("%s %s: help names the removed %s", v, f, gone)
				}
			}
		}
	}
}

func TestEndpointCreateGolden(t *testing.T) {
	h := newFleetEnv(t)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	out := t.TempDir()
	pub, pubFile, _ := epKey(t, out, "k")
	_, otherFile, _ := epKey(t, out, "other")
	jf := filepath.Join(out, "design.join")
	norm := func(s string) string {
		s = strings.ReplaceAll(s, pub, "UPUBLICKEY")
		s = strings.ReplaceAll(s, out, "OUT")
		return strings.ReplaceAll(s, h.dir, "CONFIG")
	}
	var b strings.Builder
	step := func(label string, args ...string) {
		so, _, code := h.run(args...)
		b.WriteString("# " + label + " exit " + string(rune('0'+code)) + "\n" + norm(so))
	}
	a := ad("design").String()
	step("planned", "endpoint", "create", a, "--out", jf, "--public-key", pubFile, "--dry-run", "--json")
	step("created", "endpoint", "create", a, "--out", jf, "--public-key", pubFile, "--json")
	step("exists", "endpoint", "create", a, "--out", filepath.Join(out, "other.join"), "--public-key", otherFile, "--json")
	step("not_an_address", "endpoint", "create", "design", "--public-key", pubFile, "--out", jf, "--json")
	step("path_taken", "endpoint", "create", ad("two").String(), "--out", jf, "--public-key", otherFile, "--json")
	step("installed_here", "endpoint", "create", ad("three").String(), "--json")
	golden := filepath.Join("testdata", "endpoint_create.golden.json")
	if *updateEndpointCreateGolden {
		if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file: %v", err)
	}
	if b.String() != string(want) {
		t.Fatalf("differs from %s (run with -update-endpoint-create after checking the diff)\n got: %s\nwant: %s", golden, b.String(), want)
	}
}
