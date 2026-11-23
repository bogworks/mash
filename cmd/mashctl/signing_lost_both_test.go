package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
)

// configSigningPub reads signing_key_pub from config.json in cfgDir, and the whole file text.
func configSigningPub(t *testing.T, cfgDir string) (string, string) {
	t.Helper()
	raw := string(mustRead(t, filepath.Join(cfgDir, "config.json")))
	const k = `"signing_key_pub": "`
	i := strings.Index(raw, k)
	if i < 0 {
		return "", raw
	}
	rest := raw[i+len(k):]
	return rest[:strings.Index(rest, `"`)], raw
}

// init with signing_key records the PUBLIC key in config.json, and never a seed.
func TestInitRecordsSigningKeyPub(t *testing.T) {
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	seed, _ := sk.Seed()
	a := initStrict(t, e, sk, osk, true)
	got, raw := configSigningPub(t, a.env["MASH_CONFIG_DIR"])
	if got != skPub {
		t.Fatalf("signing_key_pub = %q, want %s\n%s", got, skPub, raw)
	}
	if strings.Contains(raw, string(seed)) || strings.Contains(raw, "SAA") {
		t.Fatalf("config.json holds seed text:\n%s", raw)
	}
	// without signing_key, no field
	e2, sk2, osk2 := strictEnv(t)
	b := initStrict(t, e2, sk2, osk2, false)
	if g, _ := configSigningPub(t, b.env["MASH_CONFIG_DIR"]); g != "" {
		t.Fatalf("signing_key_pub written without signing_key: %q", g)
	}
}

// Both signing.nk and signing.pub gone, the config says a signing key was configured: create refuses and records
// nothing; init with the same signing_key restores both and create works.
func TestEndpointCreateSigningLostBothRefuses(t *testing.T) {
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)
	keysDir := a.env["MASH_KEYS_DIR"]
	for _, f := range []string{"signing.nk", "signing.pub"} {
		if err := os.Remove(filepath.Join(keysDir, f)); err != nil {
			t.Fatal(err)
		}
	}
	out, errb, code := a.run("endpoint", "create", "alice.g.x")
	if code != 1 {
		t.Fatalf("create: exit %d\n%s\n%s", code, out, errb)
	}
	for _, m := range []string{"signing_key was configured", skPub, "both missing", "mashctl init --config <file>", "nothing was changed"} {
		if !strings.Contains(errb, m) {
			t.Errorf("stderr lacks %q:\n%s", m, errb)
		}
	}
	creds := filepath.Join(a.env["MASH_CONFIG_DIR"], "alice.g.x.creds")
	for _, f := range []string{creds, creds + ".pending"} {
		if _, err := os.Lstat(f); err == nil {
			t.Errorf("%s was left behind", f)
		}
	}
	// a rerun with the same signing_key restores both files
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init to restore: %d\n%s\n%s", code, out, errb)
	}
	for _, f := range []string{"signing.nk", "signing.pub"} {
		if _, err := os.Stat(filepath.Join(keysDir, f)); err != nil {
			t.Errorf("%s not restored: %v", f, err)
		}
	}
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 0 {
		t.Fatalf("create after restore: %d\n%s\n%s", code, out, errb)
	}
}

// A 0.5.7 config.json has no signing_key_pub: both files gone still means identity signing (documented
// compatibility), and a rerun of init with signing_key completes the config.
func TestSigningLostBothOldConfigKeepsIdentitySigning(t *testing.T) {
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)
	cfgDir := a.env["MASH_CONFIG_DIR"]
	// make the config an old one: drop the field
	p := filepath.Join(cfgDir, "config.json")
	_, raw := configSigningPub(t, cfgDir)
	var kept []string
	for _, l := range strings.Split(raw, "\n") {
		if !strings.Contains(l, "signing_key_pub") {
			kept = append(kept, l)
		}
	}
	old := strings.Replace(strings.Join(kept, "\n"), "\",\n}", "\"\n}", 1)
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	keysDir := a.env["MASH_KEYS_DIR"]
	for _, f := range []string{"signing.nk", "signing.pub"} {
		if err := os.Remove(filepath.Join(keysDir, f)); err != nil {
			t.Fatal(err)
		}
	}
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 0 {
		t.Fatalf("create: %d\n%s\n%s", code, out, errb)
	}
	creds := filepath.Join(cfgDir, "alice.g.x.creds")
	if uc := credsClaims(t, creds); uc.Issuer != e.acctPub || uc.IssuerAccount != "" {
		t.Fatalf("issuer %s issuer_account %q: want the identity key", uc.Issuer, uc.IssuerAccount)
	}
	// init completes the old config with the field
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init: %d\n%s\n%s", code, out, errb)
	}
	if got, raw := configSigningPub(t, cfgDir); got != skPub {
		t.Fatalf("signing_key_pub = %q after rerun\n%s", got, raw)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("config.json mode %v", fi.Mode())
	}
}

// With the field present and both files gone, init without signing_key refuses and changes nothing.
func TestInitSigningLostBothNeedsTheKey(t *testing.T) {
	e, sk, osk := strictEnv(t)
	a := initStrict(t, e, sk, osk, true)
	keysDir := a.env["MASH_KEYS_DIR"]
	for _, f := range []string{"signing.nk", "signing.pub"} {
		if err := os.Remove(filepath.Join(keysDir, f)); err != nil {
			t.Fatal(err)
		}
	}
	a.refuses(1, []string{"signing_key was configured", "both missing", "nothing was changed"}, map[string]any{"signing_key": nil})
}

// config.json records a different signing_key_pub than the setup file's signing_key: init refuses and changes
// nothing, config.json included.
func TestInitRefusesADifferentSigningKeyPubInConfig(t *testing.T) {
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)
	cfgDir := a.env["MASH_CONFIG_DIR"]
	other, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _ := other.PublicKey()
	p := filepath.Join(cfgDir, "config.json")
	_, raw := configSigningPub(t, cfgDir)
	edited := strings.Replace(raw, skPub, otherPub, 1)
	if edited == raw {
		t.Fatalf("config.json lacks %s:\n%s", skPub, raw)
	}
	if err := os.WriteFile(p, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	a.refuses(1, []string{"different signing key", "nothing was changed"}, nil)
	if after := string(mustRead(t, p)); after != edited {
		t.Fatalf("config.json changed:\n%s\nwant\n%s", after, edited)
	}
}
