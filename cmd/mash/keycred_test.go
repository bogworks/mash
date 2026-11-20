package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
)

// kcRun runs mash in the current folder with a config directory that does not exist and no other environment.
func kcRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	vars := map[string]string{"HOME": t.TempDir(), "MASH_CONFIG_DIR": filepath.Join(t.TempDir(), "nocfg")}
	var out, errb bytes.Buffer
	rc := run(args, envStrict(vars), strings.NewReader(""), &out, &errb)
	return rc, out.String(), errb.String()
}

// chdirTemp makes the test run in a new folder and returns it.
func chdirTemp(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Chdir(d)
	return d
}

func readTrim(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// The two files: the seed (mode 0600) and the public key beside it with .pub added to the whole seed path.
func TestKeyCreateWritesTheSeedAndThePublicKey(t *testing.T) {
	dir := chdirTemp(t)
	rc, out, errb := kcRun(t, "key", "create", "--out", "bob.seed")
	if rc != 0 || errb != "" || out != "Created bob.seed and bob.seed.pub.\n" {
		t.Fatalf("rc %d, stdout %q, stderr %q", rc, out, errb)
	}
	st, err := os.Stat(filepath.Join(dir, "bob.seed"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("seed mode: %v, %v", st, err)
	}
	kp, err := nkeys.FromSeed([]byte(readTrim(t, "bob.seed")))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	if got := readTrim(t, "bob.seed.pub"); got != pub || !strings.HasPrefix(pub, "U") {
		t.Errorf("public file %q, want %q", got, pub)
	}
	for _, n := range []string{"bob.pub", "bob.seed.pub.pub"} {
		if _, err := os.Stat(n); err == nil {
			t.Errorf("%s was made", n)
		}
	}
	// neither the seed nor the public key is ever printed
	if strings.Contains(out+errb, pub) || strings.Contains(out+errb, readTrim(t, "bob.seed")) {
		t.Errorf("a key was printed: %q %q", out, errb)
	}
}

func TestKeyCreateDefaultsToMashSeed(t *testing.T) {
	chdirTemp(t)
	rc, out, errb := kcRun(t, "key", "create")
	if rc != 0 || errb != "" || out != "Created mash.seed and mash.seed.pub.\n" {
		t.Fatalf("rc %d, stdout %q, stderr %q", rc, out, errb)
	}
	for _, n := range []string{"mash.seed", "mash.seed.pub"} {
		if _, err := os.Stat(n); err != nil {
			t.Error(err)
		}
	}
}

func TestKeyCreateRefusesToOverwrite(t *testing.T) {
	dir := chdirTemp(t)
	if err := os.WriteFile("bob.seed", []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, out, errb := kcRun(t, "key", "create", "--out", "bob.seed")
	if rc != 1 || out != "" || errb != "error: bob.seed already exists; nothing was written\n" {
		t.Fatalf("rc %d, stdout %q, stderr %q", rc, out, errb)
	}
	if readTrim(t, "bob.seed") != "mine" {
		t.Error("the existing seed was changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "bob.seed.pub")); err == nil {
		t.Error("a public key file was written")
	}
	// the public key's file in the way is named
	if err := os.Remove("bob.seed"); err != nil { // a file this test made
		t.Fatal(err)
	}
	if err := os.WriteFile("bob.seed.pub", []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, errb = kcRun(t, "key", "create", "--out", "bob.seed")
	if rc != 1 || out != "" || errb != "error: bob.seed.pub already exists; nothing was written\n" {
		t.Fatalf("rc %d, stdout %q, stderr %q", rc, out, errb)
	}
	if _, err := os.Stat("bob.seed"); err == nil {
		t.Error("a seed was written beside an existing public key file")
	}
}

func TestKeyCreateJSON(t *testing.T) {
	chdirTemp(t)
	rc, out, errb := kcRun(t, "key", "create", "--out", "bob.seed", "--json")
	if rc != 0 || errb != "" || out != `{"seed":"bob.seed","public_key":"bob.seed.pub"}`+"\n" {
		t.Fatalf("rc %d, stdout %q, stderr %q", rc, out, errb)
	}
	rc, out, errb = kcRun(t, "key", "create", "--out", "bob.seed", "--json")
	var obj map[string]string
	if rc != 1 || errb != "" || json.Unmarshal([]byte(out), &obj) != nil ||
		obj["error"] != "bob.seed already exists; nothing was written" || len(obj) != 1 {
		t.Fatalf("a refusal as JSON: rc %d, stdout %q, stderr %q", rc, out, errb)
	}
}

// The flags the specification does not have are gone.
func TestKeyCreateHasOnlyTheSpecifiedOptions(t *testing.T) {
	chdirTemp(t)
	for _, flag := range []string{"--pub-out", "--dry-run", "--force"} {
		rc, out, errb := kcRun(t, "key", "create", flag, "x")
		if rc != 2 || out != "" || !strings.HasPrefix(errb, "error: unknown option "+flag) || !strings.HasSuffix(errb, "hint: mash key create --help\n") {
			t.Errorf("%s: rc %d, stdout %q, stderr %q", flag, rc, out, errb)
		}
	}
	if ents, _ := os.ReadDir("."); len(ents) != 0 {
		t.Errorf("a refused command line wrote %v", ents)
	}
}

func TestKeyCreateMakesParentDirectory(t *testing.T) {
	dir := chdirTemp(t)
	rc, out, errb := kcRun(t, "key", "create", "--out", "keys/bob.seed")
	if rc != 0 || errb != "" || out != "Created keys/bob.seed and keys/bob.seed.pub.\n" {
		t.Fatalf("rc %d, stdout %q, stderr %q", rc, out, errb)
	}
	if st, err := os.Stat(filepath.Join(dir, "keys")); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("keys directory: %v, %v", st, err)
	}
}

func TestKeyHelp(t *testing.T) {
	for _, args := range [][]string{{"key", "--help"}, {"help", "key"}} {
		rc, out, errb := kcRun(t, args...)
		if rc != 0 || errb != "" || !strings.HasPrefix(out, "Make your own key pair, for when the seed must never leave this machine.\n\nUsage:\n  mash key create [options]\n\nGlobal options:\n") {
			t.Errorf("%v: rc %d, stdout %q, stderr %q", args, rc, out, errb)
		}
	}
	a := runMash(map[string]string{"HOME": t.TempDir()}, "key", "create", "--help")
	b := runMash(map[string]string{"HOME": t.TempDir()}, "help", "key", "create")
	if a.code != 0 || a != b || !strings.Contains(a.out, "Usage:\n  mash key create [options]\n") {
		t.Errorf("key create --help and help key create: %+v, %+v", a, b)
	}
}

func TestKeyNeedsACommand(t *testing.T) {
	rc, out, errb := kcRun(t, "key")
	if rc != 2 || out != "" || errb != "error: mash key needs a command: create\nhint: mash key --help\n" {
		t.Errorf("rc %d, stdout %q, stderr %q", rc, out, errb)
	}
	rc, out, errb = kcRun(t, "key", "new")
	if rc != 2 || out != "" || errb != "error: mash key has no command new\nhint: mash key --help\n" {
		t.Errorf("rc %d, stdout %q, stderr %q", rc, out, errb)
	}
}

// mash credential is gone: it is an unknown command, with no alias.
func TestCredentialIsRemoved(t *testing.T) {
	rc, out, errb := kcRun(t, "credential", "create", "--seed", "a", "--jwt", "b")
	if rc != 2 || out != "" || errb != "error: mash has no command credential\nhint: mash --help\n" {
		t.Errorf("rc %d, stdout %q, stderr %q", rc, out, errb)
	}
	if strings.Contains(usage, "mash credential") {
		t.Errorf("mash --help still names credential:\n%s", usage)
	}
}
