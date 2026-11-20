package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
)

func TestEndpointReissueArguments(t *testing.T) {
	h := newFleetEnv(t)
	_, pubFile, _ := epKey(t, t.TempDir(), "k")
	a := ad("x").String()
	for _, in := range []string{ad("x").Owner + "/g/x", "x", "g.x"} {
		so, se, code := h.run("endpoint", "reissue", in)
		wantUsage(t, so, se, code, "reissue", "not an endpoint address")
	}
	so, se, code := h.run("endpoint", "reissue", a, "--public-key", pubFile)
	if code != 2 || so != "" || se != "error: --public-key needs --out, the join file to write\nhint: mashctl endpoint reissue --help\n" {
		t.Errorf("public key without out: %d %q %q", code, so, se)
	}
	for _, f := range [][]string{{"--make-key"}, {"--jwt-out", "x"}, {"--all"}, {"--changed"}, {"--yes"}, {"--bogus"}} {
		so, se, code := h.run(append([]string{"endpoint", "reissue", a}, f...)...)
		wantUsage(t, so, se, code, "reissue", "unknown option")
	}
	if so, se, code := h.run("endpoint", "reissue", "--all", "--yes"); code != 2 || so != "" || !strings.Contains(se, "unknown option") {
		t.Errorf("bulk form: %d %q %q", code, so, se)
	}
}

func TestEndpointReissue(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	setFakeAcked(t, 3)
	out := t.TempDir()
	oldPub, oldFile, _ := epKey(t, out, "old")
	a := ad("rot")
	if _, se, code := h.run("endpoint", "create", a.String(), "--out", filepath.Join(out, "old.join"), "--public-key", oldFile); code != 0 {
		t.Fatalf("create: %q", se)
	}
	newPub, newFile, _ := epKey(t, out, "new")
	nj := filepath.Join(out, "new.join")
	before := epSnapshot(t, h, out)
	so, se, code := h.run("endpoint", "reissue", a.String(), "--out", nj, "--public-key", newFile, "--dry-run")
	if code != 0 || so != "Would reissue credentials for "+a.String()+"\nNothing was changed.\n" {
		t.Fatalf("dry-run: %d %q %q", code, so, se)
	}
	if epSnapshot(t, h, out) != before || len(*calls) != 0 {
		t.Fatal("dry-run changed state")
	}
	so, se, code = h.run("endpoint", "reissue", a.String(), "--out", nj, "--public-key", newFile, "--json")
	var doc map[string]any
	if json.Unmarshal([]byte(so), &doc) != nil || code != 0 || !sameKeys(doc, []string{"address", "join_file"}) || doc["join_file"] != nj || doc["address"] != a.String() {
		t.Fatalf("doc %d %q %q", code, so, se)
	}
	j, m := readJoin(t, nj)
	if !sameKeys(m, joinKeysJWT) || j.Creds != "" {
		t.Errorf("keys %v", m)
	}
	if uc, err := jwt.DecodeUserClaims(j.UserJWT); err != nil || uc.Subject != newPub {
		t.Errorf("jwt names another key: %v", err)
	}
	if rec, _ := h.record(a); rec.User != newPub {
		t.Errorf("record user %s", rec.User)
	}
	if len(*calls) != 1 || (*calls)[0].users[0] != oldPub {
		t.Errorf("revoke calls %+v", *calls)
	}
	if _, se, code := h.run("endpoint", "reissue", ad("none").String(), "--out", filepath.Join(out, "n2.join"), "--public-key", newFile); code != 1 || !strings.HasPrefix(se, "error: "+ad("none").String()+" does not exist\nhint: mashctl endpoint create ") {
		t.Errorf("missing: %d %q", code, se)
	}
	// an existing join file is never replaced
	if _, se, code := h.run("endpoint", "reissue", a.String(), "--out", nj, "--public-key", newFile); code != 1 || se != "error: "+nj+" already exists\n" {
		t.Errorf("existing path: %d %q", code, se)
	}
}

// Without --out the key pair is made here and the creds file in the config folder is replaced.
func TestEndpointReissueInstalledHere(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	setFakeAcked(t, 3)
	a := ad("host")
	if _, se, code := h.run("endpoint", "create", a.String()); code != 0 {
		t.Fatalf("create host: %q", se)
	}
	p := h.creds(a)
	first, _ := os.ReadFile(p)
	so, se, code := h.run("endpoint", "reissue", a.String())
	if code != 0 || so != "Reissued credentials for "+a.String()+"\n" || se != "" && !strings.HasPrefix(se, "mashctl: target") {
		t.Fatalf("reissue host: %d %q %q", code, so, se)
	}
	second, _ := os.ReadFile(p)
	if string(first) == string(second) || fileMode(t, p) != 0o600 {
		t.Error("creds not replaced or wrong mode")
	}
	k1, _ := jwt.ParseDecoratedUserNKey(first)
	k2, _ := jwt.ParseDecoratedUserNKey(second)
	p1, _ := k1.PublicKey()
	p2, _ := k2.PublicKey()
	if rec, _ := h.record(a); rec.User != p2 || p1 == p2 {
		t.Error("record does not name the new key")
	}
	if len(*calls) != 1 || (*calls)[0].users[0] != p1 {
		t.Errorf("revoke calls %+v", *calls)
	}
	if !gone(t, p+".pending") {
		t.Error("the staged key was left")
	}
	so, _, _ = h.run("endpoint", "reissue", a.String(), "--json")
	var doc map[string]any
	if json.Unmarshal([]byte(so), &doc) != nil || !sameKeys(doc, []string{"address", "join_file"}) || doc["join_file"] != nil {
		t.Errorf("json %q", so)
	}
	so, _, _ = h.run("endpoint", "reissue", a.String(), "--verbose")
	rec, _ := h.record(a)
	if !strings.HasPrefix(so, "Reissued credentials for "+a.String()+"\nservers: ") || !strings.Contains(so, "\ncredentials file: "+p+"\nservers acknowledged: 3\npublic key: "+rec.User+"\n") {
		t.Errorf("verbose %q", so)
	}
	if so, _, code := h.run("endpoint", "reissue", a.String(), "--dry-run"); code != 0 || so != "Would reissue credentials for "+a.String()+"\nNothing was changed.\n" {
		t.Errorf("dry-run %d %q", code, so)
	}
}

// With --out and no --public-key the key pair is made here, the join file holds the creds, and nothing is installed.
func TestEndpointReissueOut(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	out := t.TempDir()
	a := ad("far")
	h.seed(a)
	creds := h.creds(a)
	installed, _ := os.ReadFile(creds)
	oldRec, _ := h.record(a)
	jf := filepath.Join(out, "far.join")
	so, se, code := h.run("endpoint", "reissue", a.String(), "--out", jf)
	if code != 0 || so != "Reissued credentials for "+a.String()+"\nJoin file: "+jf+"\n" {
		t.Fatalf("%d %q %q", code, so, se)
	}
	if got, _ := os.ReadFile(creds); string(got) != string(installed) {
		t.Error("the creds file here was changed")
	}
	j, m := readJoin(t, jf)
	if !sameKeys(m, joinKeysCreds) || j.UserJWT != "" {
		t.Errorf("keys %v", m)
	}
	kp, err := jwt.ParseDecoratedUserNKey([]byte(j.Creds))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	if rec, _ := h.record(a); rec.User != pub || pub == oldRec.User {
		t.Error("record does not name the new key")
	}
	if len(*calls) != 1 || (*calls)[0].users[0] != oldRec.User {
		t.Errorf("revoke calls %+v", *calls)
	}
	if !gone(t, jf+".pending") || !gone(t, creds+".pending") {
		t.Error("a staged key was left")
	}
	seed, _ := kp.Seed()
	tok, _ := jwt.ParseDecoratedJWT([]byte(j.Creds))
	if strings.Contains(so+se, string(seed)) || strings.Contains(so+se, tok) {
		t.Error("output holds a secret")
	}
	if _, se, code := h.run("endpoint", "reissue", a.String(), "--out", jf); code != 1 || se != "error: "+jf+" already exists\n" {
		t.Errorf("existing path: %d %q", code, se)
	}
}

// A run killed after it staged its key and wrote the join file is finished by the same command.
func TestEndpointReissueOutKilledResumes(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	out := t.TempDir()
	a := ad("kill")
	h.seed(a)
	jf := filepath.Join(out, "kill.join")
	if code := killAt(h, &epBeforeVerify, "endpoint", "reissue", a.String(), "--out", jf); code != -1 {
		t.Fatalf("not stopped: %d", code)
	}
	if _, err := os.Lstat(jf + ".pending"); err != nil {
		t.Fatal("the staged key was not kept")
	}
	first, _ := os.ReadFile(jf)
	so, se, code := h.run("endpoint", "reissue", a.String(), "--out", jf)
	if code != 0 || so != "Reissued credentials for "+a.String()+"\nJoin file: "+jf+"\n" {
		t.Fatalf("rerun: %d %q %q", code, so, se)
	}
	if got, _ := os.ReadFile(jf); string(got) != string(first) {
		t.Error("the join file of the first run was replaced")
	}
	if !gone(t, jf+".pending") {
		t.Error("the staged key was kept after the readback")
	}
	if len(*calls) != 1 {
		t.Errorf("the old key was revoked %d times", len(*calls))
	}
}
