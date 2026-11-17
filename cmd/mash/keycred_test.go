package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// kcRun runs mash with a config directory that does not exist and no other environment.
func kcRun(t *testing.T, cfgDir string, args ...string) (int, string, string) {
	t.Helper()
	vars := map[string]string{"HOME": t.TempDir(), "MASH_CONFIG_DIR": cfgDir}
	var out, errb bytes.Buffer
	rc := run(args, func(k string) string { return vars[k] }, strings.NewReader(""), &out, &errb)
	return rc, out.String(), errb.String()
}

func TestKeyCreateCLI(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "design.seed")
	rc, out, errb := kcRun(t, filepath.Join(dir, "nocfg"), "key", "create", "--out", seed)
	if rc != 0 || errb != "" {
		t.Fatalf("rc %d, stderr %q", rc, errb)
	}
	pubRaw, _ := os.ReadFile(filepath.Join(dir, "design.pub"))
	pub := strings.TrimSpace(string(pubRaw))
	for _, want := range []string{"created key", "public key  " + pub, "seed        " + seed, "public file " + filepath.Join(dir, "design.pub"), "mashctl endpoint create ADDRESS --public-key"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// a second run refuses, exit 1
	rc, _, errb = kcRun(t, "", "key", "create", "--out", seed)
	if rc != 1 || !strings.Contains(errb, "file_exists") || !strings.Contains(errb, "next:") {
		t.Errorf("rc %d, stderr %q", rc, errb)
	}
}

func TestKeyCreateJSONGolden(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "k.seed")
	rc, out, _ := kcRun(t, "", "key", "create", "--out", seed, "--json", "--dry-run")
	if rc != 0 {
		t.Fatal(rc)
	}
	pubStart := strings.Index(out, `"public_key":"`) + len(`"public_key":"`)
	pub := out[pubStart : pubStart+56]
	want := `{"schema":"mash-key@1","outcome":"planned","public_key":"` + pub + `","seed_path":"` + seed + `","public_path":"` + filepath.Join(dir, "k.pub") + `","refusal":null}` + "\n"
	if out != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("dry run wrote %v", ents)
	}
	os.WriteFile(seed, []byte("x"), 0o600)
	rc, out, _ = kcRun(t, "", "key", "create", "--out", seed, "--json")
	if rc != 1 || !strings.Contains(out, `"outcome":"refused"`) || !strings.Contains(out, `"code":"file_exists"`) {
		t.Errorf("rc %d, %s", rc, out)
	}
}

func kcCredFixture(t *testing.T, name string) (dir, seedPath, jwtPath, seed, tok string) {
	t.Helper()
	dir = t.TempDir()
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	sb, _ := kp.Seed()
	seed = string(sb)
	acct, _ := nkeys.CreateAccount()
	uc := jwt.NewUserClaims(pub)
	uc.Name = name
	tok, _ = uc.Encode(acct)
	seedPath, jwtPath = filepath.Join(dir, "u.seed"), filepath.Join(dir, "u.jwt")
	os.WriteFile(seedPath, []byte(seed+"\n"), 0o600)
	os.WriteFile(jwtPath, []byte(tok+"\n"), 0o600)
	return
}

func TestCredentialCreateCLI(t *testing.T) {
	dir, seedPath, jwtPath, _, _ := kcCredFixture(t, "design")
	cfg := filepath.Join(dir, "cfg")
	rc, out, errb := kcRun(t, cfg, "credential", "create", "--seed", seedPath, "--jwt", jwtPath)
	if rc != 0 || errb != "" {
		t.Fatalf("rc %d, stderr %q", rc, errb)
	}
	creds := filepath.Join(cfg, "design.creds")
	if !strings.Contains(out, "created credentials") || !strings.Contains(out, "creds       "+creds) || !strings.Contains(out, "next: mash status") {
		t.Errorf("output:\n%s", out)
	}
	rc, _, errb = kcRun(t, cfg, "credential", "create", "--seed", seedPath, "--jwt", jwtPath)
	if rc != 1 || !strings.Contains(errb, "--force") {
		t.Errorf("rc %d, stderr %q", rc, errb)
	}
	rc, _, _ = kcRun(t, cfg, "credential", "create", "--seed", seedPath, "--jwt", jwtPath, "--force")
	if rc != 0 {
		t.Errorf("force rc %d", rc)
	}
	// a mismatched seed
	other := filepath.Join(dir, "o.seed")
	kp, _ := nkeys.CreateUser()
	sb, _ := kp.Seed()
	os.WriteFile(other, sb, 0o600)
	rc, _, errb = kcRun(t, cfg, "credential", "create", "--seed", other, "--jwt", jwtPath, "--out", filepath.Join(dir, "m.creds"))
	if rc != 1 || !strings.Contains(errb, "key_mismatch") {
		t.Errorf("rc %d, stderr %q", rc, errb)
	}
}

func TestCredentialEmptyNameNeedsOut(t *testing.T) {
	dir, seedPath, jwtPath, _, _ := kcCredFixture(t, "")
	rc, _, errb := kcRun(t, filepath.Join(dir, "cfg"), "credential", "create", "--seed", seedPath, "--jwt", jwtPath)
	if rc != 2 || !strings.Contains(errb, "--out") {
		t.Errorf("rc %d, stderr %q", rc, errb)
	}
}

func TestCredentialJSON(t *testing.T) {
	dir, seedPath, jwtPath, _, _ := kcCredFixture(t, "design")
	rc, out, _ := kcRun(t, "", "credential", "create", "--seed", seedPath, "--jwt", jwtPath, "--out", filepath.Join(dir, "x.creds"), "--json")
	if rc != 0 || !strings.HasPrefix(out, `{"schema":"mash-credential@1","outcome":"created","public_key":"U`) || !strings.Contains(out, `"name":"design","refusal":null}`) {
		t.Errorf("rc %d, %s", rc, out)
	}
}

// INV-001: neither command prints a seed or a JWT, in text or JSON mode, on stdout or stderr.
func TestKeycredNeverPrintsSecrets(t *testing.T) {
	for _, js := range []bool{false, true} {
		for _, again := range []bool{false, true} { // the second pass is a refusal
			dir, seedPath, jwtPath, seed, tok := kcCredFixture(t, "design")
			cfg := filepath.Join(dir, "cfg")
			keySeed := filepath.Join(dir, "n.seed")
			cmds := [][]string{
				{"key", "create", "--out", keySeed},
				{"credential", "create", "--seed", seedPath, "--jwt", jwtPath},
				{"credential", "create", "--seed", seedPath, "--jwt", jwtPath, "--out", filepath.Join(dir, "m.creds"), "--dry-run"},
			}
			for _, c := range cmds {
				if js {
					c = append(c, "--json")
				}
				n := 1
				if again {
					n = 2
				}
				for i := 0; i < n; i++ {
					_, out, errb := kcRun(t, cfg, c...)
					all := out + errb
					if i == 0 && c[0] == "key" {
						b, _ := os.ReadFile(keySeed)
						if s := strings.TrimSpace(string(b)); s == "" || strings.Contains(all, s) {
							t.Errorf("%v: key seed in output or not written", c)
						}
					}
					if strings.Contains(all, seed) || strings.Contains(all, tok) {
						t.Errorf("%v json=%v: output holds a secret:\n%s", c, js, all)
					}
				}
			}
		}
	}
}

func TestKeycredUsageErrors(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown key sub", []string{"key", "nope"}, "create"},
		{"unknown credential sub", []string{"credential", "nope"}, "create"},
		{"key no sub", []string{"key"}, "create"},
		{"key missing out", []string{"key", "create"}, "--out is required"},
		{"credential missing seed", []string{"credential", "create", "--jwt", "j"}, "--seed is required"},
		{"credential missing jwt", []string{"credential", "create", "--seed", "s"}, "--jwt is required"},
		{"dash value", []string{"key", "create", "--out", "--json"}, "looks like a flag (write --out=--json"},
		{"unknown flag", []string{"key", "create", "--out", filepath.Join(dir, "a"), "--bogus"}, "--bogus"},
		{"force on key", []string{"key", "create", "--out", filepath.Join(dir, "a"), "--force"}, "--force"},
	}
	for _, c := range cases {
		rc, _, errb := kcRun(t, "", c.args...)
		if rc != 2 || !strings.Contains(errb, c.want) {
			t.Errorf("%s: rc %d, stderr %q, want 2 and %q", c.name, rc, errb, c.want)
		}
	}
	// the inline form takes a value that starts with '-'
	t.Chdir(dir)
	rc, _, _ := kcRun(t, "", "key", "create", "--out=-x.seed")
	if rc != 0 {
		t.Errorf("--out=-x.seed rc %d", rc)
	}
}

func TestKeycredHelp(t *testing.T) {
	for _, a := range [][]string{{"key"}, {"key", "create"}, {"credential"}, {"credential", "create"}} {
		for _, h := range []string{"--help", "-h"} {
			rc, out, errb := kcRun(t, "", append(append([]string{}, a...), h)...)
			if rc != 0 || errb != "" || !strings.Contains(out, "Example") || !strings.Contains(out, "usage: mash "+a[0]) {
				t.Errorf("%v %s: rc %d out %q err %q", a, h, rc, out, errb)
			}
		}
	}
}

func TestFlagValueRule(t *testing.T) {
	i := 0
	if v, err := flagValue([]string{"--out", "f"}, &i, "--out", "", false); v != "f" || err != nil || i != 1 {
		t.Errorf("%q %v %d", v, err, i)
	}
	i = 0
	if v, err := flagValue([]string{"--out", "-"}, &i, "--out", "", false); v != "-" || err != nil {
		t.Errorf("bare dash: %q %v", v, err)
	}
	i = 0
	if _, err := flagValue([]string{"--out", "--help"}, &i, "--out", "", false); err == nil || !strings.Contains(err.Error(), "write --out=--help") {
		t.Errorf("err %v", err)
	}
	i = 0
	if v, err := flagValue([]string{"--out=-x"}, &i, "--out", "-x", true); v != "-x" || err != nil {
		t.Errorf("inline: %q %v", v, err)
	}
	i = 0
	if _, err := flagValue([]string{"--out"}, &i, "--out", "", false); err == nil {
		t.Error("missing value accepted")
	}
}
