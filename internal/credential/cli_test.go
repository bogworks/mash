package credential

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

func userJWT(t *testing.T, subject string) string {
	t.Helper()
	acct, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	uc := jwt.NewUserClaims(subject)
	tok, err := uc.Encode(acct)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestKeyCmdWritesSeed0600AndPrintsOnlyThePublicKey(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ep.seed")
	var so, se bytes.Buffer
	if rc := KeyCmd("mash", []string{"--out", out}, &so, &se); rc != 0 {
		t.Fatalf("rc %d: %s", rc, se.String())
	}
	if m := mode(t, out); m != 0o600 {
		t.Fatalf("mode %o", m)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	seed := strings.TrimSpace(string(raw))
	kp, err := nkeys.FromSeed([]byte(seed))
	if err != nil {
		t.Fatalf("the file is not a seed: %v", err)
	}
	pub, _ := kp.PublicKey()
	if strings.Contains(so.String()+se.String(), seed) {
		t.Fatal("the seed is in the output")
	}
	if !strings.Contains(so.String(), "public key: "+pub+"\n") {
		t.Fatalf("output %q lacks the seed's public key %s", so.String(), pub)
	}
	if !strings.Contains(so.String(), "mashctl credential issue --address ADDRESS --key "+pub) {
		t.Fatalf("no next step: %q", so.String())
	}
}

func TestKeyCmdRefusesToOverwriteAndNeedsOut(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ep.seed")
	if err := os.WriteFile(out, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	if rc := KeyCmd("mash", []string{"--out", out}, &so, &se); rc == 0 {
		t.Fatal("overwrote an existing file")
	}
	if b, _ := os.ReadFile(out); string(b) != "keep" {
		t.Fatalf("file changed: %q", b)
	}
	if strings.Contains(so.String(), "public key") {
		t.Fatalf("printed a key for a seed it did not keep: %q", so.String())
	}
	so.Reset()
	se.Reset()
	if rc := KeyCmd("mash", nil, &so, &se); rc != 2 || !strings.Contains(se.String(), "usage: mash credential key") {
		t.Fatalf("no --out: rc %d %q", rc, se.String())
	}
	if rc := KeyCmd("mash", []string{"--help"}, &so, &se); rc != 0 || !strings.Contains(so.String(), "usage: mash credential key") {
		t.Fatalf("help: rc %d %q", rc, so.String())
	}
}

func TestInstallCmdAssemblesAndRefusesWrongSubject(t *testing.T) {
	dir := t.TempDir()
	seedPath, jwtPath, creds := filepath.Join(dir, "s"), filepath.Join(dir, "j"), filepath.Join(dir, "c.creds")
	var so, se bytes.Buffer
	if rc := KeyCmd("mash", []string{"--out", seedPath}, &so, &se); rc != 0 {
		t.Fatal(se.String())
	}
	raw, _ := os.ReadFile(seedPath)
	kp, _ := nkeys.FromSeed(bytes.TrimSpace(raw))
	pub, _ := kp.PublicKey()
	if err := os.WriteFile(jwtPath, []byte(userJWT(t, pub)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	so.Reset()
	se.Reset()
	if rc := InstallCmd("mash", []string{"--seed", seedPath, "--jwt", jwtPath, "--out", creds}, &so, &se); rc != 0 {
		t.Fatalf("rc %d: %s", rc, se.String())
	}
	if m := mode(t, creds); m != 0o600 {
		t.Fatalf("creds mode %o", m)
	}
	if strings.Contains(so.String()+se.String(), strings.TrimSpace(string(raw))) {
		t.Fatal("the seed is in the output")
	}
	body, _ := os.ReadFile(creds)
	if tok, err := jwt.ParseDecoratedJWT(body); err != nil || tok == "" {
		t.Fatalf("creds do not hold the JWT: %v", err)
	}
	if nk, err := jwt.ParseDecoratedUserNKey(body); err != nil {
		t.Fatalf("creds hold no seed: %v", err)
	} else if p, _ := nk.PublicKey(); p != pub {
		t.Fatalf("creds seed is for %s", p)
	}
	// An existing CREDS is not overwritten without --force.
	if rc := InstallCmd("mash", []string{"--seed", seedPath, "--jwt", jwtPath, "--out", creds}, &so, &se); rc == 0 {
		t.Fatal("overwrote without --force")
	}
	if rc := InstallCmd("mash", []string{"--seed", seedPath, "--jwt", jwtPath, "--out", creds, "--force"}, &so, &se); rc != 0 {
		t.Fatalf("--force: rc %d: %s", rc, se.String())
	}
	if m := mode(t, creds); m != 0o600 {
		t.Fatalf("creds mode after force %o", m)
	}
	// A JWT for another key is refused and nothing is written.
	other, _ := nkeys.CreateUser()
	opub, _ := other.PublicKey()
	if err := os.WriteFile(jwtPath, []byte(userJWT(t, opub)), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.creds")
	se.Reset()
	if rc := InstallCmd("mash", []string{"--seed", seedPath, "--jwt", jwtPath, "--out", bad}, &so, &se); rc == 0 || !strings.Contains(se.String(), "another public key") {
		t.Fatalf("wrong subject: rc %d %q", rc, se.String())
	}
	if _, err := os.Stat(bad); err == nil {
		t.Fatal("wrote creds for a wrong subject")
	}
	if rc := InstallCmd("mash", []string{"--seed", seedPath}, &so, &se); rc != 2 {
		t.Fatalf("missing flags: rc %d", rc)
	}
}

func TestInstallCmdRefusesAReadableSeedFile(t *testing.T) {
	dir := t.TempDir()
	seed, pub, _ := NewKey()
	sp := filepath.Join(dir, "s")
	if err := os.WriteFile(sp, seed, 0o644); err != nil {
		t.Fatal(err)
	}
	jp := filepath.Join(dir, "j")
	if err := os.WriteFile(jp, []byte(userJWT(t, pub)), 0o600); err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	if rc := InstallCmd("mash", []string{"--seed", sp, "--jwt", jp, "--out", filepath.Join(dir, "c")}, &so, &se); rc == 0 {
		t.Fatal("used a seed file others can read")
	}
	if strings.Contains(se.String(), string(seed)) {
		t.Fatal("seed in error")
	}
}

func TestWriteFile0600Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile0600(path, []byte("NEW"), true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "NEW" {
		t.Fatalf("%q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp file left: %v", ents)
	}
	if err := WriteFile0600(path, []byte("X"), false); err == nil {
		t.Fatal("overwrite=false replaced a file")
	}
	if b, _ := os.ReadFile(path); string(b) != "NEW" {
		t.Fatalf("%q", b)
	}
}

func TestWriteFile0600FailureLeavesTheOriginal(t *testing.T) {
	// A target that is a directory: the rename fails, the temp file is removed.
	dir := t.TempDir()
	target := filepath.Join(dir, "d")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("K"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile0600(target, []byte("NEW"), true); err == nil {
		t.Fatal("wrote over a directory")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp file left: %v", ents)
	}
	// A read-only directory: no temp file can be made, the old file is intact.
	if os.Getuid() == 0 {
		return
	}
	ro := t.TempDir()
	path := filepath.Join(ro, "f")
	if err := os.WriteFile(path, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0o700)
	if err := WriteFile0600(path, []byte("NEW"), true); err == nil {
		t.Fatal("wrote into a read-only directory")
	}
	if b, _ := os.ReadFile(path); string(b) != "OLD" {
		t.Fatalf("original changed: %q", b)
	}
}

func TestRevokeResultExitCodes(t *testing.T) {
	for outcome, want := range map[string]int{
		OutcomeRevoked: 0, OutcomeUnchanged: 0, OutcomeRefused: 1, OutcomePartial: 5, OutcomeError: 6, "bogus": 6,
	} {
		if got := (RevokeResult{Outcome: outcome}).ExitCode(); got != want {
			t.Errorf("%s: %d, want %d", outcome, got, want)
		}
	}
}
