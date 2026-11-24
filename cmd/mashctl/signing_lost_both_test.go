package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"
)

func removeSigningFiles(t *testing.T, keysDir string) {
	t.Helper()
	for _, f := range []string{"signing.nk", "signing.pub"} {
		if err := os.Remove(filepath.Join(keysDir, f)); err != nil {
			t.Fatal(err)
		}
	}
}

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
	wantErr := "error: signing.nk and signing.pub are missing in " + keysDir + "\nhint: mashctl init --config <file>\n"
	if errb != wantErr || out != "" || strings.Contains(errb, skPub) {
		t.Errorf("stderr = %q, want %q", errb, wantErr)
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
	a.refuses(1, []string{"error: signing.nk and signing.pub are missing in", "hint: mashctl init --config <file>"}, map[string]any{"signing_key": nil})
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

// Both files gone and the field present: endpoint delete refuses before it revokes anything. (A revocation that opened
// the key store without config.json's signing_key_pub would not.)
func TestEndpointDeleteSigningLostBothRefuses(t *testing.T) {
	prev := revokeUsers
	revokeUsers = revokeUserKeys // the real revocation, through the system account
	t.Cleanup(func() { revokeUsers = prev })
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 0 {
		t.Fatalf("create: %d\n%s\n%s", code, out, errb)
	}
	before := len(e.claims().Revocations)
	removeSigningFiles(t, a.env["MASH_KEYS_DIR"])
	out, errb, code := a.run("endpoint", "delete", "alice.g.x", "--yes")
	if code != 1 {
		t.Fatalf("delete: exit %d\n%s\n%s", code, out, errb)
	}
	wantErr := "error: signing.nk and signing.pub are missing in " + a.env["MASH_KEYS_DIR"] + "\nhint: mashctl init --config <file>\n"
	if errb != wantErr || strings.Contains(errb, skPub) {
		t.Errorf("stderr = %q, want %q", errb, wantErr)
	}
	if n := len(e.claims().Revocations); n != before {
		t.Errorf("revocations %d -> %d: delete went ahead", before, n)
	}
}

// A rerun of init that gives no signing_key records the public key of the signing.nk the key store holds, in a
// config.json that lacks the field, keeping the file's layout; with the field present it changes nothing.
func TestInitWithoutSigningKeyRecordsTheStoredKey(t *testing.T) {
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)
	cfgDir := a.env["MASH_CONFIG_DIR"]
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
	a.vals["signing_key"] = nil
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init: %d\n%s\n%s", code, out, errb)
	}
	got, now := configSigningPub(t, cfgDir)
	if got != skPub {
		t.Fatalf("signing_key_pub = %q, want the stored key %s\n%s", got, skPub, now)
	}
	if !strings.HasPrefix(now, strings.TrimSuffix(old, "\n}\n")+",\n  ") {
		t.Fatalf("the file's layout changed:\n%s\nwas\n%s", now, old)
	}
	// and a fresh config.json is written with it
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init without config.json: %d\n%s\n%s", code, out, errb)
	}
	if got, raw := configSigningPub(t, cfgDir); got != skPub {
		t.Fatalf("new config.json: signing_key_pub = %q\n%s", got, raw)
	}
	// with the field present: nothing to do
	out, errb, code := a.init()
	if code != 0 || !strings.Contains(out, "Already initialized") {
		t.Fatalf("third init: %d\n%s\n%s", code, out, errb)
	}
}

// The field records another key than the key store holds, and the setup file names none: init refuses.
func TestInitRefusesAStoredKeyThatIsNotTheRecordedOne(t *testing.T) {
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := initStrict(t, e, sk, osk, true)
	cfgDir := a.env["MASH_CONFIG_DIR"]
	other, _ := nkeys.CreateAccount()
	otherPub, _ := other.PublicKey()
	p := filepath.Join(cfgDir, "config.json")
	_, raw := configSigningPub(t, cfgDir)
	edited := strings.Replace(raw, skPub, otherPub, 1)
	if err := os.WriteFile(p, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	// the files hold skPub, the field says otherPub
	a.refuses(1, []string{"config.json", otherPub, skPub, "nothing was changed"}, map[string]any{"signing_key": nil})
	// and a signing.pub that differs from the field is refused by mashctl too
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 1 || !strings.Contains(errb, "error: signing.pub in "+a.env["MASH_KEYS_DIR"]+" does not match config.json\nhint: mashctl doctor\n") {
		t.Fatalf("create: %d\n%s\n%s", code, out, errb)
	}
}

// A rerun without signing_key, signing.nk in the key store and signing.pub gone: init refuses before any write and
// names the same fix as the key store's own refusal. Without it init would report success and every later command
// would refuse. Both with a config.json that records the field and with one that does not (written before 0.5.8).
func TestInitWithoutSigningKeyRefusesWhenSigningPubIsGone(t *testing.T) {
	for _, withField := range []bool{true, false} {
		name := "config records the field"
		if !withField {
			name = "config without the field"
		}
		t.Run(name, func(t *testing.T) {
			e, sk, osk := strictEnv(t)
			a := initStrict(t, e, sk, osk, true)
			keysDir := a.env["MASH_KEYS_DIR"]
			if err := os.Remove(filepath.Join(keysDir, "signing.pub")); err != nil {
				t.Fatal(err)
			}
			if !withField {
				cfg := filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json")
				var m map[string]any
				if err := json.Unmarshal(mustRead(t, cfg), &m); err != nil {
					t.Fatal(err)
				}
				delete(m, "signing_key_pub")
				b, _ := json.MarshalIndent(m, "", "  ")
				if err := os.WriteFile(cfg, b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			a.refuses(1, []string{"error: signing.pub is missing from ~/keys", "hint: mashctl init --config <file>"}, map[string]any{"signing_key": nil})
		})
	}
}

// A new config.json is written last: an init that stops after the key import leaves none, and a rerun finishes.
func TestInterruptedInitWritesNewConfigLast(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only folder that stops init")
	}
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := newIniter(t, e)
	if err := os.WriteFile(a.opSeed, mustRead(t, seedFile(t, t.TempDir(), "op", osk)), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := filepath.Join(a.root, "in", "signer.creds")
	signingKeyCreds(t, e.acctPub, sk, sc)
	a.vals["admin_creds"] = sc
	a.vals["signing_key"] = seedFile(t, filepath.Join(a.root, "in"), "signing.seed", sk)
	// a read-only server folder: init stops when it installs the system creds, after the keys are imported
	cfgDir := a.env["MASH_CONFIG_DIR"]
	server := filepath.Join(cfgDir, "server")
	if err := os.MkdirAll(server, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(server, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(server, 0o700) })
	if out, errb, code := a.init(); code == 0 {
		t.Fatalf("init went through a read-only folder:\n%s\n%s", out, errb)
	}
	keysDir := a.env["MASH_KEYS_DIR"]
	for _, f := range []string{"signing.nk", "signing.pub"} {
		if _, err := os.Stat(filepath.Join(keysDir, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("the interrupted init left a config.json (err %v)", err)
	}
	if err := os.Chmod(server, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("rerun: %d\n%s\n%s", code, out, errb)
	}
	if got, raw := configSigningPub(t, cfgDir); got != skPub {
		t.Fatalf("signing_key_pub = %q, want %s\n%s", got, skPub, raw)
	}
	removeSigningFiles(t, keysDir)
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 1 || !strings.Contains(errb, "signing.nk and signing.pub are missing in") {
		t.Fatalf("create after lost files: %d\n%s\n%s", code, out, errb)
	}
}

// A config.json that exists without signing_key_pub gets it before the key import: an init that stops after
// the import has recorded it, so the key store with both files removed refuses create; a rerun finishes.
func TestInterruptedInitOnExistingConfigHasRecordedTheSigningKey(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only folder that stops init")
	}
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := newIniter(t, e)
	if err := os.WriteFile(a.opSeed, mustRead(t, seedFile(t, t.TempDir(), "op", osk)), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := filepath.Join(a.root, "in", "signer.creds")
	signingKeyCreds(t, e.acctPub, sk, sc)
	a.vals["admin_creds"] = sc
	// a first init without signing_key: config.json exists and has no signing_key_pub
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("first init: %d\n%s\n%s", code, out, errb)
	}
	cfgDir := a.env["MASH_CONFIG_DIR"]
	if g, _ := configSigningPub(t, cfgDir); g != "" {
		t.Fatalf("signing_key_pub before: %q", g)
	}
	// sys.creds gone and the server folder read-only: the next init stops when it installs them, after the import
	server := filepath.Join(cfgDir, "server")
	if err := os.Remove(filepath.Join(server, "sys.creds")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(server, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(server, 0o700) })
	a.vals["signing_key"] = seedFile(t, filepath.Join(a.root, "in"), "signing.seed", sk)
	if out, errb, code := a.init(); code == 0 {
		t.Fatalf("init went through a read-only folder:\n%s\n%s", out, errb)
	}
	keysDir := a.env["MASH_KEYS_DIR"]
	if got, raw := configSigningPub(t, cfgDir); got != skPub {
		t.Fatalf("the interrupted init left signing_key_pub = %q, want %s\n%s", got, skPub, raw)
	}
	removeSigningFiles(t, keysDir)
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 1 || !strings.Contains(errb, "signing.nk and signing.pub are missing in") {
		t.Fatalf("create after the interrupted init and lost files: %d\n%s\n%s", code, out, errb)
	}
	if err := os.Chmod(server, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("rerun: %d\n%s\n%s", code, out, errb)
	}
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 0 {
		t.Fatalf("create after the rerun: %d\n%s\n%s", code, out, errb)
	}
}

func TestFailedKeyImportOnExistingConfigHasRecordedTheSigningKey(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only folder that stops the import")
	}
	e, sk, osk := strictEnv(t)
	skPub, _ := sk.PublicKey()
	a := newIniter(t, e)
	if err := os.WriteFile(a.opSeed, mustRead(t, seedFile(t, t.TempDir(), "op", osk)), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := filepath.Join(a.root, "in", "signer.creds")
	signingKeyCreds(t, e.acctPub, sk, sc)
	a.vals["admin_creds"] = sc
	// a first init without signing_key: config.json exists and has no signing_key_pub
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("first init: %d\n%s\n%s", code, out, errb)
	}
	cfgDir := a.env["MASH_CONFIG_DIR"]
	if g, _ := configSigningPub(t, cfgDir); g != "" {
		t.Fatalf("signing_key_pub before: %q", g)
	}
	// the key folder is read-only: the import of signing.nk fails, after the field was written
	keysDir := a.env["MASH_KEYS_DIR"]
	if err := os.Chmod(keysDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(keysDir, 0o700) })
	a.vals["signing_key"] = seedFile(t, filepath.Join(a.root, "in"), "signing.seed", sk)
	if out, errb, code := a.init(); code == 0 {
		t.Fatalf("init went through a read-only key folder:\n%s\n%s", out, errb)
	}
	if got, raw := configSigningPub(t, cfgDir); got != skPub {
		t.Fatalf("the failed import left signing_key_pub = %q, want %s\n%s", got, skPub, raw)
	}
	if _, err := os.Lstat(filepath.Join(keysDir, "signing.nk")); !os.IsNotExist(err) {
		t.Fatalf("signing.nk after the failed import: %v", err)
	}
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 1 || !strings.Contains(errb, "signing.nk and signing.pub are missing in") {
		t.Fatalf("create after the failed import: %d\n%s\n%s", code, out, errb)
	}
	if err := os.Chmod(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("rerun: %d\n%s\n%s", code, out, errb)
	}
	if out, errb, code := a.run("endpoint", "create", "alice.g.x"); code != 0 {
		t.Fatalf("create after the rerun: %d\n%s\n%s", code, out, errb)
	}
}

func TestAddSigningPub(t *testing.T) {
	_, sk, _ := strictEnv(t)
	pub, _ := sk.PublicKey()
	other, _ := nkeys.CreateAccount()
	otherPub, _ := other.PublicKey()
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// the layout and the bytes of every other member are kept, & is not escaped
	in := "{\n    \"servers\": [\"tls://a?x=1&y=2\"],\n    \"owner\": \"acme\"\n}\n"
	p := write("a.json", in)
	if err := addSigningPub(p, pub); err != nil {
		t.Fatal(err)
	}
	want := "{\n    \"servers\": [\"tls://a?x=1&y=2\"],\n    \"owner\": \"acme\",\n    \"signing_key_pub\": \"" + pub + "\"\n}\n"
	if got := string(mustRead(t, p)); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if err := addSigningPub(p, pub); err != nil {
		t.Fatalf("same key again: %v", err)
	}
	if err := addSigningPub(p, otherPub); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("another key: %v", err)
	}
	// a one-line object, an empty object
	p = write("b.json", `{"owner": "acme"}`)
	if err := addSigningPub(p, pub); err != nil || string(mustRead(t, p)) != `{"owner": "acme", "signing_key_pub": "`+pub+`"}` {
		t.Fatalf("one line: %v %s", err, mustRead(t, p))
	}
	p = write("c.json", "{}\n")
	if err := addSigningPub(p, pub); err != nil || string(mustRead(t, p)) != "{\n  \"signing_key_pub\": \""+pub+"\"\n}\n" {
		t.Fatalf("empty: %v %s", err, mustRead(t, p))
	}
	// an invalid or non-string stored value is an error, never overwritten
	for _, bad := range []string{`"nonsense"`, `7`, `["x"]`} {
		p = write("d.json", "{\n  \"signing_key_pub\": "+bad+"\n}\n")
		before := mustRead(t, p)
		if err := addSigningPub(p, pub); err == nil {
			t.Errorf("%s: no error", bad)
		}
		if string(mustRead(t, p)) != string(before) {
			t.Errorf("%s: file changed", bad)
		}
	}
	// an empty stored value is filled in, the other members kept
	p = write("e.json", "{\n  \"owner\": \"a&b\",\n  \"signing_key_pub\": \"\"\n}\n")
	if err := addSigningPub(p, pub); err != nil || !strings.Contains(string(mustRead(t, p)), pub) || !strings.Contains(string(mustRead(t, p)), "a&b") {
		t.Fatalf("empty value: %v %s", err, mustRead(t, p))
	}
	// a symbolic link is refused and left alone
	real := write("real.json", in)
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := addSigningPub(link, pub); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink: %v", err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 || string(mustRead(t, real)) != in {
		t.Fatal("the link or its target changed")
	}
	// invalid JSON is refused
	p = write("f.json", "{not json")
	if err := addSigningPub(p, pub); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}
