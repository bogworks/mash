package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/testenv"
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

// enrollCreate runs the library part of a create, as a run that stopped before it wrote the file would have.
func enrollCreate(h *fleetEnv, a sap.Address, pub string) (enroll.Result, error) {
	_, acct, err := h.keys().Account()
	if err != nil {
		return enroll.Result{}, err
	}
	_ = acct
	return enroll.Create(context.Background(), enroll.Deps{Keys: h.keys(), Admin: h.adminOf(), Account: h.accountPub()},
		enroll.Request{Addr: a, PublicKey: pub})
}

func TestEndpointCreateNaming(t *testing.T) {
	h := newFleetEnv(t)
	out := t.TempDir()
	_, pubFile, _ := epKey(t, out, "k")
	owner := testenv.Owner
	cases := []struct {
		name string
		args []string
		code int
		want string // in stderr
	}{
		{"canonical", []string{"--owner", owner, "--group", "dev", "--name", "a1", "--dry-run"}, 0, ""},
		{"shortcut", []string{owner + "/dev/a2", "--dry-run"}, 0, ""},
		{"group and name, configured owner", []string{"--group", "dev", "--name", "a3", "--dry-run"}, 0, ""},
		{"one token and group", []string{"a4", "--group", "dev", "--dry-run"}, 0, ""},
		{"one token, no group", []string{"a5", "--dry-run"}, 2, "an endpoint needs a group; write --group dev or alice/dev/design"},
		{"name only, no group", []string{"--name", "a6", "--dry-run"}, 2, "an endpoint needs a group"},
		{"two tokens", []string{"dev/a7", "--dry-run"}, 2, "two tokens are ambiguous; write alice/dev/design or --group dev design"},
		{"group disagrees", []string{owner + "/dev/a8", "--group", "ops", "--dry-run"}, 2, "different group"},
		{"name disagrees", []string{owner + "/dev/a9", "--name", "b", "--dry-run"}, 2, "different name"},
		{"owner disagrees", []string{owner + "/dev/a10", "--owner", "zed", "--dry-run"}, 2, "different owner"},
		{"agreeing forms", []string{owner + "/dev/a11", "--owner", owner, "--group", "dev", "--name", "a11", "--dry-run"}, 0, ""},
		{"bad token", []string{owner + "/dev/Bad--name", "--dry-run"}, 1, "instance"},
		{"empty token", []string{owner + "//a12", "--dry-run"}, 1, "name_invalid"},
		{"four tokens", []string{owner + "/dev/a13/x", "--dry-run"}, 1, "name_invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"endpoint", "create", "--public-key", pubFile, "--jwt-out", filepath.Join(out, "x.jwt")}, c.args...)
			so, se, code := h.run(args...)
			if code != c.code {
				t.Fatalf("code %d, want %d; stdout %q stderr %q", code, c.code, so, se)
			}
			if !strings.Contains(se, c.want) {
				t.Errorf("stderr %q lacks %q", se, c.want)
			}
		})
	}
	so, _, code := h.run("endpoint", "create", "a5", "--public-key", pubFile, "--json")
	var doc epDoc
	if err := json.Unmarshal([]byte(so), &doc); err != nil || code != 2 || doc.Refusal == nil || doc.Refusal.Code != "group_missing" {
		t.Fatalf("json %q code %d err %v", so, code, err)
	}
}

func TestEndpointCreateKeyFlags(t *testing.T) {
	h := newFleetEnv(t)
	out := t.TempDir()
	_, pubFile, _ := epKey(t, out, "k")
	a := ad("k1").String()
	so, se, code := h.run("endpoint", "create", a)
	if code != 2 || !strings.Contains(se, "--public-key") || !strings.Contains(se, "--make-key") {
		t.Fatalf("neither: %d %q %q", code, so, se)
	}
	so, _, _ = h.run("endpoint", "create", a, "--json")
	var doc epDoc
	if json.Unmarshal([]byte(so), &doc) != nil || doc.Refusal == nil || doc.Refusal.Code != "key_missing" {
		t.Fatalf("json %q", so)
	}
	if _, _, code = h.run("endpoint", "create", a, "--public-key", pubFile, "--make-key"); code != 2 {
		t.Fatalf("both: %d", code)
	}
	if _, ok := h.record(ad("k1")); ok {
		t.Fatal("a refused command made a record")
	}
	if _, _, code = h.run("endpoint", "create", a, "--public-key", "--make-key"); code != 2 {
		t.Fatalf("flag as value: %d", code)
	}
}

func TestEndpointCreatePublicKey(t *testing.T) {
	h := newFleetEnv(t)
	out := t.TempDir()
	pub, pubFile, seed := epKey(t, out, "k")
	a := ad("design")
	jf := filepath.Join(out, "design.jwt")
	so, se, code := h.run("endpoint", "create", a.String(), "--public-key", pubFile, "--jwt-out", jf, "--label", "team=x", "--tool", "--describe", "d", "--cap", "c1")
	if code != 0 {
		t.Fatalf("code %d: %q %q", code, so, se)
	}
	if !strings.HasPrefix(so, "created endpoint "+testenv.Owner+"/g/design\n") || !strings.Contains(so, "next: on the endpoint's machine, run: mash credential create --seed design.seed --jwt design.jwt") {
		t.Errorf("stdout %q", so)
	}
	if m := fileMode(t, jf); m != 0o600 {
		t.Errorf("jwt mode %v", m)
	}
	raw, _ := os.ReadFile(jf)
	tok := strings.TrimSpace(string(raw))
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil || uc.Subject != pub {
		t.Fatalf("jwt: %v", err)
	}
	for _, s := range []string{so, se} {
		if strings.Contains(s, tok) || strings.Contains(s, string(seed)) {
			t.Errorf("output holds a secret: %q", s)
		}
	}
	rec, ok := h.record(a)
	if !ok || rec.User != pub || rec.Labels["kind"] != "tool" || rec.Labels["team"] != "x" {
		t.Fatalf("record %+v %v", rec, ok)
	}
	iss, found, err := h.adminOf().IssuedByKey(context.Background(), pub)
	if err != nil || !found || iss.Address != a.Flat() {
		t.Fatalf("issue record %+v %v %v", iss, found, err)
	}
	// the JWT file is never overwritten, and the refusal changes nothing
	before := strings.Join(h.records(), ",")
	_, se, code = h.run("endpoint", "create", ad("other").String(), "--public-key", pubFile, "--jwt-out", jf)
	if code != 1 || !strings.Contains(se, "file_exists") {
		t.Errorf("existing file: %d %q", code, se)
	}
	if got, _ := os.ReadFile(jf); string(got) != string(raw) {
		t.Error("the jwt file changed")
	}
	if strings.Join(h.records(), ",") != before {
		t.Error("a refusal changed the directory")
	}
	// address taken
	_, pub2File, _ := epKey(t, out, "k2")
	so, se, code = h.run("endpoint", "create", a.String(), "--public-key", pub2File, "--jwt-out", filepath.Join(out, "t.jwt"), "--json")
	var doc epDoc
	if json.Unmarshal([]byte(so), &doc) != nil || code != 1 || doc.Outcome != "refused" || doc.Refusal.Code != enroll.CodeAddressTaken {
		t.Fatalf("taken: %d %q %q", code, so, se)
	}
	if _, err := os.Stat(filepath.Join(out, "t.jwt")); err == nil {
		t.Error("a refused create wrote a file")
	}
	// an invalid key
	bad := filepath.Join(out, "bad.pub")
	if err := os.WriteFile(bad, []byte("not-a-key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, se, code = h.run("endpoint", "create", ad("z").String(), "--public-key", bad, "--jwt-out", filepath.Join(out, "z.jwt"))
	if code != 1 || !strings.Contains(se, "public_key_invalid") {
		t.Errorf("bad key: %d %q", code, se)
	}
}

func TestEndpointCreateRerunCompletes(t *testing.T) {
	h := newFleetEnv(t)
	out := t.TempDir()
	pub, pubFile, _ := epKey(t, out, "k")
	a := ad("again")
	if _, err := enrollCreate(h, a, pub); err != nil {
		t.Fatal(err)
	}
	jf := filepath.Join(out, "again.jwt")
	so, se, code := h.run("endpoint", "create", a.String(), "--public-key", pubFile, "--jwt-out", jf)
	if code != 0 {
		t.Fatalf("rerun: %d %q %q", code, so, se)
	}
	if fileMode(t, jf) != 0o600 {
		t.Error("mode")
	}
}

func TestEndpointCreateMakeKey(t *testing.T) {
	h := newFleetEnv(t)
	a := ad("same")
	so, se, code := h.run("endpoint", "create", a.String(), "--make-key", "--json")
	if code != 0 {
		t.Fatalf("code %d %q %q", code, so, se)
	}
	var doc epDoc
	if err := json.Unmarshal([]byte(so), &doc); err != nil || doc.Outcome != "created" || doc.CredsWritten == nil || !*doc.CredsWritten || doc.JWTPath != "" {
		t.Fatalf("doc %q %v", so, err)
	}
	p := h.creds(a)
	if doc.CredsPath != p || fileMode(t, p) != 0o600 {
		t.Fatalf("creds path %q", doc.CredsPath)
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
	if uc.Subject != pub || doc.UserPublicKey != pub {
		t.Error("public key does not match")
	}
	if strings.Contains(so+se, string(seed)) || strings.Contains(so+se, tok) {
		t.Error("output holds a secret")
	}
	_, _, code = h.run("endpoint", "create", a.String(), "--make-key")
	if code != 1 {
		t.Errorf("second: %d", code)
	}
	if got, _ := os.ReadFile(p); string(got) != string(raw) {
		t.Error("creds file changed")
	}
	so, _, _ = h.run("endpoint", "create", ad("same2").String(), "--make-key")
	if !strings.Contains(so, "next: start a session as this endpoint: MASH_OWNER="+testenv.Owner+" MASH_GROUP=g MASH_NAME=same2") {
		t.Errorf("next line %q", so)
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
			if err == nil && !fi.IsDir() {
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
	out := t.TempDir()
	_, pubFile, _ := epKey(t, out, "k")
	before := epSnapshot(t, h, h.dir, out)
	for _, args := range [][]string{
		{"endpoint", "create", ad("dry").String(), "--public-key", pubFile, "--jwt-out", filepath.Join(out, "dry.jwt"), "--dry-run"},
		{"endpoint", "create", ad("dry").String(), "--make-key", "--dry-run", "--json"},
	} {
		so, se, code := h.run(args...)
		if code != 0 {
			t.Fatalf("%v: %d %q %q", args, code, so, se)
		}
		if !strings.Contains(so, "would create endpoint") && !strings.Contains(so, `"planned"`) {
			t.Errorf("stdout %q", so)
		}
	}
	if after := epSnapshot(t, h, h.dir, out); after != before {
		t.Errorf("dry-run changed state:\n%s\n---\n%s", before, after)
	}
	h.seed(ad("taken"))
	if _, _, code := h.run("endpoint", "create", ad("taken").String(), "--public-key", pubFile, "--dry-run"); code != 1 {
		t.Errorf("taken dry-run: %d", code)
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
		}
	}
}

func TestEndpointCreateGolden(t *testing.T) {
	h := newFleetEnv(t)
	out := t.TempDir()
	pub, pubFile, _ := epKey(t, out, "k")
	_, otherFile, _ := epKey(t, out, "other")
	jf := filepath.Join(out, "design.jwt")
	norm := func(s string) string {
		s = strings.ReplaceAll(s, pub, "UPUBLICKEY")
		return strings.ReplaceAll(s, out, "OUT")
	}
	var b strings.Builder
	step := func(label string, args ...string) {
		so, _, code := h.run(args...)
		b.WriteString("# " + label + " exit " + string(rune('0'+code)) + "\n" + norm(so))
	}
	a := ad("design").String()
	step("planned", "endpoint", "create", a, "--public-key", pubFile, "--jwt-out", jf, "--dry-run", "--json")
	step("created", "endpoint", "create", a, "--public-key", pubFile, "--jwt-out", jf, "--label", "team=x", "--describe", "d", "--cap", "c1", "--note", "n", "--json")
	step("address_taken", "endpoint", "create", a, "--public-key", otherFile, "--jwt-out", filepath.Join(out, "other.jwt"), "--json")
	step("group_missing", "endpoint", "create", "design", "--public-key", pubFile, "--json")
	step("file_exists", "endpoint", "create", ad("two").String(), "--public-key", otherFile, "--jwt-out", jf, "--json")
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

func TestEndpointReissue(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	out := t.TempDir()
	oldPub, oldFile, _ := epKey(t, out, "old")
	a := ad("rot")
	if _, se, code := h.run("endpoint", "create", a.String(), "--public-key", oldFile, "--jwt-out", filepath.Join(out, "old.jwt")); code != 0 {
		t.Fatalf("create: %q", se)
	}
	newPub, newFile, _ := epKey(t, out, "new")
	nj := filepath.Join(out, "new.jwt")
	before := epSnapshot(t, h, out)
	if _, _, code := h.run("endpoint", "reissue", a.String(), "--public-key", newFile, "--jwt-out", nj, "--dry-run"); code != 0 {
		t.Fatal("dry-run")
	}
	if epSnapshot(t, h, out) != before || len(*calls) != 0 {
		t.Fatal("dry-run changed state")
	}
	so, se, code := h.run("endpoint", "reissue", a.String(), "--public-key", newFile, "--jwt-out", nj, "--json")
	if code != 0 {
		t.Fatalf("%d %q %q", code, so, se)
	}
	var doc epDoc
	if err := json.Unmarshal([]byte(so), &doc); err != nil || doc.Outcome != "reissued" || doc.UserPublicKey != newPub || doc.JWTWritten == nil || !*doc.JWTWritten {
		t.Fatalf("doc %q %v", so, err)
	}
	if rec, _ := h.record(a); rec.User != newPub {
		t.Errorf("record user %s", rec.User)
	}
	if len(*calls) != 1 || (*calls)[0].users[0] != oldPub {
		t.Errorf("revoke calls %+v", *calls)
	}
	if fileMode(t, nj) != 0o600 {
		t.Error("mode")
	}
	if _, _, code := h.run("endpoint", "reissue", ad("none").String(), "--public-key", newFile, "--jwt-out", filepath.Join(out, "n2.jwt")); code != 1 {
		t.Errorf("missing: %d", code)
	}
	if _, _, code := h.run("endpoint", "reissue", "--all", "--make-key", "--yes"); code != 2 {
		t.Errorf("bulk: %d", code)
	}
	// same host: the creds file is replaced, mode 0600
	if _, _, code := h.run("endpoint", "create", ad("host").String(), "--make-key"); code != 0 {
		t.Fatal("create host")
	}
	p := h.creds(ad("host"))
	first, _ := os.ReadFile(p)
	if _, se, code := h.run("endpoint", "reissue", ad("host").String(), "--make-key"); code != 0 {
		t.Fatalf("reissue host: %d %q", code, se)
	}
	second, _ := os.ReadFile(p)
	if string(first) == string(second) || fileMode(t, p) != 0o600 {
		t.Error("creds not replaced or wrong mode")
	}
	k1, _ := jwt.ParseDecoratedUserNKey(first)
	k2, _ := jwt.ParseDecoratedUserNKey(second)
	p1, _ := k1.PublicKey()
	p2, _ := k2.PublicKey()
	if rec, _ := h.record(ad("host")); rec.User != p2 || p1 == p2 {
		t.Error("record does not name the new key")
	}
}
