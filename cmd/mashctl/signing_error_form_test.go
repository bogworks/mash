package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
)

// signingRefusalCases are the six signing refusals: how to bring the key store to each, the two lines mashctl prints
// (DIR is the key folder) and the exit code. Every command that signs prints them the same way; endpoint create is the one run here.
var signingRefusalCases = []struct {
	name  string
	setup func(t *testing.T, a *initer, keysDir string)
	msg   string
	hint  string
}{
	{"both missing", func(t *testing.T, a *initer, keysDir string) { removeSigningFiles(t, keysDir) },
		"signing.nk and signing.pub are missing from DIR", "mashctl init --config <file>"},
	{"pub missing", func(t *testing.T, a *initer, keysDir string) {
		if err := os.Remove(filepath.Join(keysDir, "signing.pub")); err != nil {
			t.Fatal(err)
		}
	}, "signing.pub is missing from DIR", "mashctl init --config <file>"},
	{"record mismatch", func(t *testing.T, a *initer, keysDir string) {
		cfgDir := a.env["MASH_CONFIG_DIR"]
		pub, raw := configSigningPub(t, cfgDir)
		other, err := nkeys.CreateAccount()
		if err != nil {
			t.Fatal(err)
		}
		otherPub, _ := other.PublicKey()
		if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(strings.Replace(raw, pub, otherPub, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "signing.pub in DIR does not match config.json", "mashctl doctor"},
	{"key mismatch", func(t *testing.T, a *initer, keysDir string) {
		other, err := nkeys.CreateAccount()
		if err != nil {
			t.Fatal(err)
		}
		seed, _ := other.Seed()
		if err := os.WriteFile(filepath.Join(keysDir, "signing.nk"), append(seed, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "signing.nk in DIR does not match signing.pub", "mashctl doctor"},
	{"unreadable", func(t *testing.T, a *initer, keysDir string) {
		if err := os.Chmod(filepath.Join(keysDir, "signing.nk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, "signing.nk in DIR cannot be read", "mashctl doctor"},
	{"not a valid key", func(t *testing.T, a *initer, keysDir string) {
		if err := os.WriteFile(filepath.Join(keysDir, "signing.nk"), []byte("not a seed\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "signing.nk in DIR is not a valid key", "mashctl doctor"},
}

var longKey = regexp.MustCompile(`[A-Z2-7]{56}`)

// Each signing refusal prints exactly `error:` and `hint:` on stderr, nothing on stdout, exit 1, and no public key.
func TestSigningRefusalsPrintErrorAndHint(t *testing.T) {
	for _, c := range signingRefusalCases {
		t.Run(c.name, func(t *testing.T) {
			e, sk, osk := strictEnv(t)
			a := initStrict(t, e, sk, osk, true)
			keysDir := a.env["MASH_KEYS_DIR"]
			c.setup(t, a, keysDir)
			out, errb, code := a.run("endpoint", "create", "alice.g.x")
			want := "error: " + strings.ReplaceAll(c.msg, "DIR", keysDir) + "\nhint: " + c.hint + "\n"
			if code != 1 || out != "" || errb != want {
				t.Fatalf("exit %d, stdout %q, stderr %q\nwant stderr %q", code, out, errb, want)
			}
			if longKey.MatchString(errb) {
				t.Errorf("stderr holds a public key: %q", errb)
			}
			// --json: one object on stdout, nothing on stderr
			out, errb, code = a.run("endpoint", "create", "alice.g.x", "--json")
			var o map[string]string
			if code != 1 || errb != "" || json.Unmarshal([]byte(out), &o) != nil {
				t.Fatalf("--json: exit %d, stdout %q, stderr %q", code, out, errb)
			}
			if o["error"] != strings.ReplaceAll(c.msg, "DIR", keysDir) || o["hint"] != c.hint || len(o) != 2 {
				t.Errorf("--json = %v", o)
			}
		})
	}
}

// init prints the same form for a lost signing.nk and signing.pub, with the key folder written with ~.
func TestInitSigningRefusalForm(t *testing.T) {
	e, sk, osk := strictEnv(t)
	a := initStrict(t, e, sk, osk, true)
	removeSigningFiles(t, a.env["MASH_KEYS_DIR"])
	home := a.env["HOME"]
	a.env["HOME"] = filepath.Dir(a.env["MASH_KEYS_DIR"])
	defer func() { a.env["HOME"] = home }()
	delete(a.vals, "signing_key")
	_, errb, code := a.init()
	want := "error: signing.nk and signing.pub are missing from ~/" + filepath.Base(a.env["MASH_KEYS_DIR"]) + "\nhint: mashctl init --config <file>\n"
	if code != 1 || errb != want {
		t.Fatalf("exit %d, stderr %q, want %q", code, errb, want)
	}
}
