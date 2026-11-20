package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/joinfile"
)

const testCAPEM = "-----BEGIN CERTIFICATE-----\nMIIBhTCCASugAwIBAgIQIRi6zePL6mKjOipn+dNuaTAKBggqhkjOPQQDAjASMRAw\nDgYDVQQKEwdBY21lIENvMB4XDTE3MTAyMDE5NDMwNloXDTE4MTAyMDE5NDMwNlow\nEjEQMA4GA1UEChMHQWNtZSBDbzBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABD0d\n7VNhbWvZLWPuj/RtHFjvtJBEwOkhbN/BnnE8rnZR8+sbwnc/KhCk3FhnpHZnQz7B\n5aETbbIgmuvewdjvSBSjYzBhMA4GA1UdDwEB/wQEAwICpDATBgNVHSUEDDAKBggr\nBgEFBQcDATAPBgNVHRMBAf8EBTADAQH/MCkGA1UdEQQiMCCCDmxvY2FsaG9zdDo1\nNDUzgg4xMjcuMC4wLjE6NTQ1MzAKBggqhkjOPQQDAgNIADBFAiEA2zpJEPQyz6/l\nWf86aX6PepsntZv2GYlA5UpabfT2EZICICpJ5h/iI+i341gBptmWnIBEL9S9Y56E\nHpT4Mq8hWc3j\n-----END CERTIFICATE-----\n"

// joinMaterial is one endpoint's key and the user JWT an account issued for it.
type joinMaterial struct {
	seed  []byte
	pub   string
	token string
	creds []byte // the whole creds file
}

func newJoinMaterial(t *testing.T, name string) joinMaterial {
	t.Helper()
	acct, _ := nkeys.CreateAccount()
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	seed, _ := kp.Seed()
	uc := jwt.NewUserClaims(pub)
	uc.Name = name
	tok, err := uc.Encode(acct)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := jwt.FormatUserConfig(tok, seed)
	if err != nil {
		t.Fatal(err)
	}
	return joinMaterial{seed: seed, pub: pub, token: tok, creds: creds}
}

func writeJoin(t *testing.T, path string, j joinfile.File) {
	t.Helper()
	b, err := j.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// joinWorld is a folder to run in with a join file for acme.demo.bob in it and an empty configuration folder to join
// into. Its HOME is the folder, so the creds path is shown with a ~.
type joinWorld struct {
	t    *testing.T
	home string // the working folder and HOME
	cfg  string // the configuration folder, <home>/.config/mash
	m    joinMaterial
	env  map[string]string
}

func newJoinWorld(t *testing.T) *joinWorld {
	t.Helper()
	home := t.TempDir()
	t.Chdir(home)
	w := &joinWorld{t: t, home: home, cfg: filepath.Join(home, ".config", "mash"), m: newJoinMaterial(t, "acme.demo.bob")}
	w.env = map[string]string{"HOME": home}
	ca := testCAPEM
	writeJoin(t, "acme.demo.bob.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, CA: &ca, Creds: string(w.m.creds)})
	return w
}

func (w *joinWorld) run(args ...string) ran {
	w.t.Helper()
	return runMash(w.env, args...)
}

func (w *joinWorld) credsFile() string { return filepath.Join(w.cfg, "acme.demo.bob.creds") }

func (w *joinWorld) configJSON() map[string]any {
	w.t.Helper()
	b, err := os.ReadFile(filepath.Join(w.cfg, "config.json"))
	if err != nil {
		w.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		w.t.Fatal(err)
	}
	return m
}

// specCases are the x-output cases of the command at path in spec/mash.json: case name -> text, stream, exit.
type specCase struct {
	Case   string `json:"case"`
	Text   string `json:"text"`
	Stream string `json:"stream"`
	Exit   int    `json:"exit"`
}

func specCases(t *testing.T, path string) map[string]specCase {
	t.Helper()
	spec := loadConfSpec(t)
	out := map[string]specCase{}
	found := false
	confWalk(spec.Root, func(p string, c confCmd) {
		if p != path {
			return
		}
		found = true
		for _, m := range c.Metadata {
			if m.Name == "x-output" {
				var cs []specCase
				if err := json.Unmarshal(m.Value, &cs); err != nil {
					t.Fatal(err)
				}
				for _, c := range cs {
					out[c.Case] = c
				}
			}
		}
	})
	if !found || len(out) == 0 {
		t.Fatalf("spec/mash.json has no x-output for %q", path)
	}
	return out
}

// check compares a run with a case of the specification: the text on its stream, nothing on the other, and the exit.
func (c specCase) check(t *testing.T, r ran) {
	t.Helper()
	wantOut, wantErr := "", ""
	if c.Stream == "stdout" {
		wantOut = c.Text + "\n"
	} else {
		wantErr = c.Text + "\n"
	}
	if r.code != c.Exit || r.out != wantOut || r.errs != wantErr {
		t.Errorf("%s: want exit %d, stdout %q, stderr %q; got exit %d, stdout %q, stderr %q", c.Case, c.Exit, wantOut, wantErr, r.code, r.out, r.errs)
	}
}

// Each x-output case of join in spec/mash.json, word for word.
func TestJoinMatchesSpec(t *testing.T) {
	cases := specCases(t, "join")
	seen := map[string]bool{}
	do := func(name string, f func(w *joinWorld, c specCase)) {
		c, ok := cases[name]
		if !ok {
			t.Fatalf("spec/mash.json no longer has the join case %q: update this test", name)
		}
		seen[name] = true
		t.Run(name, func(t *testing.T) { f(newJoinWorld(t), c) })
	}
	do("Joined", func(w *joinWorld, c specCase) {
		c.check(t, w.run("join", "acme.demo.bob.join"))
	})
	do("Already installed", func(w *joinWorld, c specCase) {
		if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
			t.Fatal(r)
		}
		c.check(t, w.run("join", "acme.demo.bob.join"))
	})
	do("This folder already belongs to another mesh", func(w *joinWorld, c specCase) {
		os.MkdirAll(w.cfg, 0o700)
		os.WriteFile(filepath.Join(w.cfg, "config.json"), []byte(`{"servers":["tls://other:4222"]}`), 0o600)
		c.check(t, w.run("join", "acme.demo.bob.join"))
		if _, err := os.Stat(w.credsFile()); err == nil {
			t.Error("creds were written into another mesh's folder")
		}
	})
	do("This folder trusts a different certificate authority", func(w *joinWorld, c specCase) {
		if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
			t.Fatal(r)
		}
		m := newJoinMaterial(t, "acme.demo.carol")
		ca := strings.Replace(testCAPEM, "MIIBhTCC", "MIIBhTCD", 1)
		writeJoin(t, "acme.demo.bob.join", joinfile.File{MashJoin: 1, Address: "acme.demo.carol", Servers: []string{"tls://myserver:4222"}, CA: &ca, Creds: string(m.creds)})
		c.check(t, w.run("join", "acme.demo.bob.join"))
	})
	do("With --dry-run", func(w *joinWorld, c specCase) {
		c.check(t, w.run("join", "acme.demo.bob.join", "--dry-run"))
		if _, err := os.Stat(w.cfg); err == nil {
			t.Error("--dry-run made the configuration folder")
		}
	})
	do("The join file needs your seed", func(w *joinWorld, c specCase) {
		writeJoin(t, "acme.demo.bob.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, UserJWT: w.m.token})
		c.check(t, w.run("join", "acme.demo.bob.join"))
		if _, err := os.Stat(w.cfg); err == nil {
			t.Error("the configuration folder was made")
		}
	})
	for name := range cases {
		if !seen[name] {
			t.Errorf("spec/mash.json has the join case %q and this test does not run it", name)
		}
	}
}

func TestKeyCreateMatchesSpec(t *testing.T) {
	cases := specCases(t, "key create")
	chdirTemp(t)
	env := map[string]string{"HOME": t.TempDir()}
	c := cases["Created"]
	c.check(t, runMash(env, "key", "create", "--out", "bob.seed"))
	c = cases["File exists"]
	c.check(t, runMash(env, "key", "create", "--out", "bob.seed"))
	if len(cases) != 2 {
		t.Errorf("spec/mash.json has %d key create cases, this test runs 2", len(cases))
	}
}

func TestJoinInstalls(t *testing.T) {
	w := newJoinWorld(t)
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Fatal(r)
	}
	st, err := os.Stat(w.credsFile())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("creds file: %v, %v", st, err)
	}
	got, _ := os.ReadFile(w.credsFile())
	if !bytes.Equal(got, w.m.creds) {
		t.Error("the creds file is not the join file's creds")
	}
	ca, err := os.ReadFile(filepath.Join(w.cfg, "ca.pem"))
	if err != nil || string(ca) != testCAPEM {
		t.Errorf("ca.pem: %q, %v", ca, err)
	}
	cfg := w.configJSON()
	if s, _ := json.Marshal(cfg["servers"]); string(s) != `["tls://myserver:4222"]` || cfg["ca"] != "ca.pem" {
		t.Errorf("config.json: %v", cfg)
	}
	if st, _ := os.Stat(w.cfg); st.Mode().Perm() != 0o700 {
		t.Errorf("configuration folder mode %v", st.Mode())
	}
	left, _ := filepath.Glob(filepath.Join(w.cfg, ".join-*"))
	if len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// After a join the endpoint is installed: the configuration reads it as the one endpoint, with its servers and CA.
func TestJoinThenTheEndpointIsInstalled(t *testing.T) {
	w := newJoinWorld(t)
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Fatal(r)
	}
	env := envStrict(map[string]string{"HOME": w.home})
	cfg, err := config.Load(config.LookupFromFunc(env), w.home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Inert() || cfg.DisplayName() != "acme.demo.bob" || cfg.URL != "tls://myserver:4222" || cfg.TLSCA != filepath.Join(w.cfg, "ca.pem") {
		t.Errorf("config after join: name %q, url %q, ca %q", cfg.DisplayName(), cfg.URL, cfg.TLSCA)
	}
	if cfg.Creds != w.credsFile() {
		t.Errorf("creds %q, want %q", cfg.Creds, w.credsFile())
	}
	// mash status now finds the endpoint (it is not "no endpoint is installed here")
	r := w.run("status")
	if strings.Contains(r.errs, "no endpoint is installed here") || strings.Contains(r.out, "no endpoint is installed here") {
		t.Errorf("status after join: %+v", r)
	}
	// and --config-dir does the same as the variable
	other := filepath.Join(w.home, "elsewhere")
	if r := runMash(w.env, "join", "acme.demo.bob.join", "--config-dir", other); r.code != 0 {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(other, "acme.demo.bob.creds")); err != nil {
		t.Error(err)
	}
	if r := runMash(map[string]string{"HOME": w.home, "MASH_CONFIG_DIR": other}, "join", "acme.demo.bob.join"); r.code != 1 || !strings.Contains(r.errs, "already installed") {
		t.Errorf("MASH_CONFIG_DIR: %+v", r)
	}
}

func TestJoinJSON(t *testing.T) {
	w := newJoinWorld(t)
	r := w.run("join", "acme.demo.bob.join", "--json", "--dry-run")
	want := `{"endpoint":"acme.demo.bob","servers":["tls://myserver:4222"],"creds":"~/.config/mash/acme.demo.bob.creds","dry_run":true}` + "\n"
	if r.code != 0 || r.out != want || r.errs != "" {
		t.Errorf("--dry-run --json: %+v", r)
	}
	r = w.run("join", "acme.demo.bob.join", "--json")
	want = `{"endpoint":"acme.demo.bob","servers":["tls://myserver:4222"],"creds":"~/.config/mash/acme.demo.bob.creds"}` + "\n"
	if r.code != 0 || r.out != want || r.errs != "" {
		t.Errorf("--json: %+v", r)
	}
	// a failure as JSON: {error, hint} on stdout, nothing on stderr
	r = w.run("join", "acme.demo.bob.join", "--json")
	if r.code != 1 || r.errs != "" || r.out != `{"error":"acme.demo.bob is already installed here","hint":"mash join acme.demo.bob.join --replace"}`+"\n" {
		t.Errorf("--json failure: %+v", r)
	}
	// a path outside the home folder is shown as it is
	other := t.TempDir()
	r = w.run("join", "acme.demo.bob.join", "--json", "--config-dir", other)
	if !strings.Contains(r.out, `"creds":"`+filepath.Join(other, "acme.demo.bob.creds")+`"`) {
		t.Errorf("creds outside home: %+v", r)
	}
}

func TestJoinVerbose(t *testing.T) {
	w := newJoinWorld(t)
	r := w.run("join", "acme.demo.bob.join", "--detail")
	if r.code != 0 || r.out != "Joined as acme.demo.bob.\nservers: tls://myserver:4222\ncreds: ~/.config/mash/acme.demo.bob.creds\n" {
		t.Errorf("%+v", r)
	}
}

func TestJoinDryRunWritesNothing(t *testing.T) {
	w := newJoinWorld(t)
	before := treeSnapshot(t, w.home)
	if r := w.run("join", "acme.demo.bob.join", "--dry-run"); r.code != 0 || r.out != "Would join as acme.demo.bob.\n" || r.errs != "" {
		t.Fatal(r)
	}
	if after := treeSnapshot(t, w.home); after != before {
		t.Errorf("--dry-run changed the folder:\n%s\n---\n%s", before, after)
	}
	// with a configuration folder that exists, the same
	os.MkdirAll(w.cfg, 0o700)
	before = treeSnapshot(t, w.home)
	w.run("join", "acme.demo.bob.join", "--dry-run")
	if after := treeSnapshot(t, w.home); after != before {
		t.Errorf("--dry-run changed the folder:\n%s\n---\n%s", before, after)
	}
	// a dry run reports what a real run would refuse
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Fatal(r)
	}
	if r := w.run("join", "acme.demo.bob.join", "--dry-run"); r.code != 1 || !strings.Contains(r.errs, "already installed") {
		t.Errorf("--dry-run over an installed endpoint: %+v", r)
	}
	if r := w.run("join", "acme.demo.bob.join", "--dry-run", "--replace"); r.code != 0 || r.out != "Would join as acme.demo.bob.\n" {
		t.Errorf("--dry-run --replace: %+v", r)
	}
}

func TestJoinReplace(t *testing.T) {
	w := newJoinWorld(t)
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Fatal(r)
	}
	fresh := newJoinMaterial(t, "acme.demo.bob")
	ca := testCAPEM
	writeJoin(t, "acme.demo.bob.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, CA: &ca, Creds: string(fresh.creds)})
	before, _ := os.ReadFile(w.credsFile())
	if r := w.run("join", "acme.demo.bob.join"); r.code != 1 {
		t.Fatal(r)
	}
	if now, _ := os.ReadFile(w.credsFile()); !bytes.Equal(now, before) {
		t.Error("the creds were replaced without --replace")
	}
	if r := w.run("join", "acme.demo.bob.join", "--replace"); r.code != 0 || r.out != "Joined as acme.demo.bob.\n" {
		t.Fatal(r)
	}
	now, _ := os.ReadFile(w.credsFile())
	if !bytes.Equal(now, fresh.creds) {
		t.Error("--replace did not replace the creds")
	}
	if st, _ := os.Stat(w.credsFile()); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
}

// One folder serves one mesh; a second endpoint of the same mesh joins the same folder.
func TestJoinSecondEndpointOfTheSameMesh(t *testing.T) {
	w := newJoinWorld(t)
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Fatal(r)
	}
	m := newJoinMaterial(t, "acme.demo.carol")
	ca := testCAPEM
	writeJoin(t, "carol.join", joinfile.File{MashJoin: 1, Address: "acme.demo.carol", Servers: []string{"tls://myserver:4222"}, CA: &ca, Creds: string(m.creds)})
	if r := w.run("join", "carol.join"); r.code != 0 || r.out != "Joined as acme.demo.carol.\n" {
		t.Fatal(r)
	}
	// a join file for another mesh is refused and changes nothing
	before := treeSnapshot(t, w.cfg)
	writeJoin(t, "dave.join", joinfile.File{MashJoin: 1, Address: "acme.demo.dave", Servers: []string{"tls://elsewhere:4222"}, Creds: string(m.creds)})
	r := w.run("join", "dave.join")
	if r.code != 1 || r.out != "" || !strings.HasSuffix(r.errs, " was set up with a different server;\n       nothing was changed\nhint: mash join dave.join --config-dir <another folder>\n") {
		t.Errorf("another mesh: %+v", r)
	}
	if after := treeSnapshot(t, w.cfg); after != before {
		t.Error("a refused join changed the folder")
	}
}

func TestJoinKeepsOtherConfigKeysAndAcceptsAServerString(t *testing.T) {
	w := newJoinWorld(t)
	os.MkdirAll(w.cfg, 0o700)
	os.WriteFile(filepath.Join(w.cfg, "config.json"), []byte(`{"host":"codex","servers":"tls://myserver:4222"}`), 0o640)
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Fatal(r)
	}
	cfg := w.configJSON()
	if cfg["host"] != "codex" || cfg["servers"] != "tls://myserver:4222" || cfg["ca"] != "ca.pem" {
		t.Errorf("config.json: %v", cfg)
	}
	if st, _ := os.Stat(filepath.Join(w.cfg, "config.json")); st.Mode().Perm() != 0o640 {
		t.Errorf("config.json mode %v", st.Mode())
	}
}

func TestJoinUserJWTWithKey(t *testing.T) {
	w := newJoinWorld(t)
	writeJoin(t, "own.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, UserJWT: w.m.token})
	os.WriteFile("bob.seed", append(bytes.Clone(w.m.seed), '\n'), 0o600)
	if r := w.run("join", "own.join", "--key", "bob.seed"); r.code != 0 || r.out != "Joined as acme.demo.bob.\n" || r.errs != "" {
		t.Fatal(r)
	}
	got, _ := os.ReadFile(w.credsFile())
	if !bytes.Equal(got, w.m.creds) {
		t.Errorf("the creds file made from the JWT and the seed differs from NATS's own:\n%s", got)
	}
	if st, _ := os.Stat(w.credsFile()); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if _, err := os.Stat(filepath.Join(w.cfg, "ca.pem")); err == nil {
		t.Error("a ca.pem was written for a join file with no CA")
	}
	if cfg := w.configJSON(); cfg["ca"] != nil {
		t.Errorf("ca in config.json: %v", cfg)
	}
}

func TestJoinUserJWTSeedProblems(t *testing.T) {
	w := newJoinWorld(t)
	writeJoin(t, "own.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, UserJWT: w.m.token})
	stranger := newJoinMaterial(t, "x")
	os.WriteFile("stranger.seed", stranger.seed, 0o600)
	os.WriteFile("junk.seed", []byte("SUAJUNKJUNKJUNK"), 0o600)
	for _, tc := range []struct{ key, want string }{
		{"stranger.seed", "error: stranger.seed is not the key this join file was made for\n"},
		{"junk.seed", "error: junk.seed does not hold a user seed\n"},
		{"missing.seed", "error: cannot read missing.seed: no such file or directory\n"},
	} {
		r := w.run("join", "own.join", "--key", tc.key)
		if r.code != 1 || r.out != "" || r.errs != tc.want {
			t.Errorf("--key %s: %+v", tc.key, r)
		}
		if strings.Contains(r.errs, string(stranger.seed)) || strings.Contains(r.errs, string(w.m.seed)) {
			t.Errorf("a seed was printed: %q", r.errs)
		}
	}
	if _, err := os.Stat(w.cfg); err == nil {
		t.Error("a refused join made the configuration folder")
	}
}

func TestJoinRefusesBadFiles(t *testing.T) {
	w := newJoinWorld(t)
	good := joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, Creds: string(w.m.creds)}
	secret := string(w.m.seed)
	mod := func(f func(j *joinfile.File)) string {
		j := good
		f(&j)
		b, _ := json.Marshal(j)
		return string(b)
	}
	badCA := "not a certificate"
	for name, body := range map[string]string{
		"not json":      "hello " + secret,
		"empty":         "",
		"old format":    mod(func(j *joinfile.File) { j.MashJoin = 2 }),
		"no format":     mod(func(j *joinfile.File) { j.MashJoin = 0 }),
		"bad address":   mod(func(j *joinfile.File) { j.Address = "Acme_Demo_Bob" }),
		"two parts":     mod(func(j *joinfile.File) { j.Address = "acme.bob" }),
		"no servers":    mod(func(j *joinfile.File) { j.Servers = nil }),
		"no creds":      mod(func(j *joinfile.File) { j.Creds = "" }),
		"both":          mod(func(j *joinfile.File) { j.UserJWT = w.m.token }),
		"junk creds":    mod(func(j *joinfile.File) { j.Creds = "junk " + secret }),
		"bad ca":        mod(func(j *joinfile.File) { j.CA = &badCA }),
		"unknown field": strings.TrimSuffix(mod(func(j *joinfile.File) {}), "}") + `,"extra":1}`,
		"bad jwt":       mod(func(j *joinfile.File) { j.Creds = ""; j.UserJWT = "junk" }),
	} {
		os.WriteFile("bad.join", []byte(body), 0o600)
		r := w.run("join", "bad.join", "--key", "unused")
		if r.code != 1 || r.out != "" || !strings.HasPrefix(r.errs, "error: bad.join is ") || strings.Count(r.errs, "\n") != 1 {
			t.Errorf("%s: %+v", name, r)
		}
		if strings.Contains(r.errs, secret) || strings.Contains(r.errs, w.m.token) {
			t.Errorf("%s: a secret was printed: %q", name, r.errs)
		}
	}
	if r := w.run("join", "missing.join"); r.code != 1 || r.errs != "error: cannot read missing.join: no such file or directory\n" {
		t.Errorf("missing file: %+v", r)
	}
	if _, err := os.Stat(w.cfg); err == nil {
		t.Error("a refused join made the configuration folder")
	}
}

// The join file holds a seed; no output of any run holds it, nor the JWT.
func TestJoinNeverPrintsSecrets(t *testing.T) {
	w := newJoinWorld(t)
	secrets := []string{string(w.m.seed), w.m.token, string(w.m.creds)}
	for _, args := range [][]string{
		{"join", "acme.demo.bob.join"},
		{"join", "acme.demo.bob.join"},
		{"join", "acme.demo.bob.join", "--json", "--replace"},
		{"join", "acme.demo.bob.join", "--detail", "--replace"},
		{"join", "acme.demo.bob.join", "--dry-run", "--detail", "--json"},
		{"join", "acme.demo.bob.join", "--help"},
	} {
		r := w.run(args...)
		for _, s := range secrets {
			if strings.Contains(r.out+r.errs, s) {
				t.Errorf("%v printed a secret: %+v", args, r)
			}
		}
	}
}

// A run that fails while writing leaves no creds file and no temporary file, and is safe to run again.
func TestJoinFailedWriteLeavesNoPartialState(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("a read-only folder does not stop root")
	}
	w := newJoinWorld(t)
	os.MkdirAll(w.cfg, 0o700)
	if err := os.Chmod(w.cfg, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(w.cfg, 0o700)
	r := w.run("join", "acme.demo.bob.join")
	if r.code != 1 || !strings.HasPrefix(r.errs, "error: cannot write into ") || !strings.Contains(r.errs, "nothing was installed") {
		t.Errorf("%+v", r)
	}
	os.Chmod(w.cfg, 0o700)
	if ents, _ := os.ReadDir(w.cfg); len(ents) != 0 {
		t.Errorf("left behind: %v", ents)
	}
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Errorf("run again: %+v", r)
	}
}

func TestJoinDifferentCAIsRefusedEvenWithReplace(t *testing.T) {
	w := newJoinWorld(t)
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Fatal(r)
	}
	m := newJoinMaterial(t, "acme.demo.carol")
	ca := strings.Replace(testCAPEM, "MIIBhTCC", "MIIBhTCD", 1)
	writeJoin(t, "carol.join", joinfile.File{MashJoin: 1, Address: "acme.demo.carol", Servers: []string{"tls://myserver:4222"}, CA: &ca, Creds: string(m.creds)})
	r := w.run("join", "carol.join")
	if r.code != 1 || !strings.Contains(r.errs, " was set up with a different ca;\n") || !strings.HasSuffix(r.errs, "hint: mash join carol.join --config-dir <another folder>\n") {
		t.Errorf("%+v", r)
	}
	if r := w.run("join", "carol.join", "--replace"); r.code != 1 || !strings.Contains(r.errs, " was set up with a different ca;") {
		t.Errorf("--replace: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(w.cfg, "ca.pem")); string(got) != testCAPEM {
		t.Error("ca.pem was replaced")
	}
	// a join file with no certificate authority, where the folder has one, is refused too
	writeJoin(t, "dan.join", joinfile.File{MashJoin: 1, Address: "acme.demo.dan", Servers: []string{"tls://myserver:4222"}, Creds: string(m.creds)})
	if r := w.run("join", "dan.join"); r.code != 1 || !strings.Contains(r.errs, " was set up with a different ca;") {
		t.Errorf("no ca: %+v", r)
	}
}

func TestJoinRelativeConfigDirIsRefused(t *testing.T) {
	w := newJoinWorld(t)
	r := w.run("join", "acme.demo.bob.join", "--config-dir", "rel")
	if r.code != 2 || r.out != "" || !strings.HasPrefix(r.errs, "error: --config-dir must be an absolute path") {
		t.Errorf("%+v", r)
	}
}

// join and key create are setup commands: no MCP tool, whatever the table says.
func TestJoinAndKeyCreateHaveNoMCPTool(t *testing.T) {
	for _, e := range commandEntries {
		switch e.CLIName() {
		case "join", "key create":
			if e.HasMCP() {
				t.Errorf("%s has an MCP tool", e.CLIName())
			}
		}
	}
	for _, tl := range newCommands(nil).MCPTools() {
		if tl.Name == "join" || strings.HasPrefix(tl.Name, "key") {
			t.Errorf("the command table offers the MCP tool %q", tl.Name)
		}
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "spec", "mash-mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{`"join"`, `"key create"`, `"key_create"`} {
		if strings.Contains(string(b), `"name": `+name) || strings.Contains(string(b), `"name":`+name) {
			t.Errorf("spec/mash-mcp.json lists %s", name)
		}
	}
}

func TestJoinHelp(t *testing.T) {
	w := newJoinWorld(t)
	r := w.run("join", "--help")
	if r.code != 0 || r.errs != "" || !strings.HasPrefix(r.out, "Install an endpoint's credentials on this machine from a join file.\n\nUsage:\n  mash join <file> [options]\n\nExamples:\n  mash join acme.demo.bob.join\n  mash join acme.demo.bob.join --key bob.seed\n\nArguments:\n") {
		t.Errorf("%+v", r)
	}
	if h := w.run("help", "join"); h != r {
		t.Errorf("mash help join differs from mash join --help")
	}
}

// join compares a join file's certificate authority with the one the folder is configured with, which config.json's "ca"
// may name as a file other than ca.pem.
func TestJoinComparesWithTheConfiguredCA(t *testing.T) {
	w := newJoinWorld(t)
	if err := os.MkdirAll(w.cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.cfg, "mesh-ca.pem"), []byte(testCAPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.cfg, "config.json"), []byte(`{"servers":["tls://myserver:4222"],"ca":"mesh-ca.pem"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// the same certificate authority: joined, and no second copy is written
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Fatalf("same ca: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(w.cfg, "ca.pem")); err == nil {
		t.Error("a ca.pem was written beside the configured file")
	}
	m := newJoinMaterial(t, "acme.demo.carol")
	other := strings.Replace(testCAPEM, "MIIBhTCC", "MIIBhTCD", 1)
	writeJoin(t, "carol.join", joinfile.File{MashJoin: 1, Address: "acme.demo.carol", Servers: []string{"tls://myserver:4222"}, CA: &other, Creds: string(m.creds)})
	for _, args := range [][]string{{"join", "carol.join"}, {"join", "carol.join", "--replace"}} {
		if r := w.run(args...); r.code != 1 || !strings.Contains(r.errs, " was set up with a different ca;") {
			t.Errorf("%v: %+v", args, r)
		}
	}
	writeJoin(t, "dan.join", joinfile.File{MashJoin: 1, Address: "acme.demo.dan", Servers: []string{"tls://myserver:4222"}, Creds: string(m.creds)})
	if r := w.run("join", "dan.join"); r.code != 1 || !strings.Contains(r.errs, " was set up with a different ca;") {
		t.Errorf("no ca: %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(w.cfg, "mesh-ca.pem")); string(got) != testCAPEM {
		t.Error("the configured ca file was replaced")
	}
}

// A folder that has no certificate authority and already holds an endpoint refuses a join file that brings one; the
// first join into an empty folder sets it.
func TestJoinCARefusedWhereTheFolderHasNoneAndAnEndpoint(t *testing.T) {
	w := newJoinWorld(t)
	m := newJoinMaterial(t, "acme.demo.carol")
	writeJoin(t, "carol.join", joinfile.File{MashJoin: 1, Address: "acme.demo.carol", Servers: []string{"tls://myserver:4222"}, Creds: string(m.creds)})
	if r := w.run("join", "carol.join"); r.code != 0 { // no ca into an empty folder
		t.Fatalf("carol: %+v", r)
	}
	for _, args := range [][]string{{"join", "acme.demo.bob.join"}, {"join", "acme.demo.bob.join", "--replace"}} {
		r := w.run(args...)
		if r.code != 1 || !strings.Contains(r.errs, " was set up with a different ca;\n       nothing was changed\n") {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if _, err := os.Stat(filepath.Join(w.cfg, "ca.pem")); err == nil {
		t.Error("ca.pem was written into a folder that holds an endpoint")
	}
}
