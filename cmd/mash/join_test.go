package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	do("Finishing a half-done join", func(w *joinWorld, c specCase) {
		w.halfDone(true, true)
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
		ca := genCA(t, "Other CA")
		writeJoin(t, "acme.demo.bob.join", joinfile.File{MashJoin: 1, Address: "acme.demo.carol", Servers: []string{"tls://myserver:4222"}, CA: &ca, Creds: string(m.creds)})
		c.check(t, w.run("join", "acme.demo.bob.join"))
	})
	do("The join file can be read by others", func(w *joinWorld, c specCase) {
		if err := os.Chmod("acme.demo.bob.join", 0o644); err != nil {
			t.Fatal(err)
		}
		c.check(t, w.run("join", "acme.demo.bob.join"))
		if _, err := os.Stat(w.cfg); err == nil {
			t.Error("the configuration folder was made")
		}
	})
	do("The join file is not a plain file of your own", func(w *joinWorld, c specCase) {
		if err := os.Rename("acme.demo.bob.join", "real.join"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("real.join", "acme.demo.bob.join"); err != nil {
			t.Fatal(err)
		}
		c.check(t, w.run("join", "acme.demo.bob.join"))
		if _, err := os.Stat(w.cfg); err == nil {
			t.Error("the configuration folder was made")
		}
	})
	do("With --dry-run", func(w *joinWorld, c specCase) {
		c.check(t, w.run("join", "acme.demo.bob.join", "--dry-run"))
		if _, err := os.Stat(w.cfg); err == nil {
			t.Error("--dry-run made the configuration folder")
		}
	})
	do("With --detail", func(w *joinWorld, c specCase) {
		c.check(t, w.run("join", "acme.demo.bob.join", "--detail"))
	})
	do("The join file needs your seed", func(w *joinWorld, c specCase) {
		writeJoin(t, "acme.demo.bob.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, UserJWT: w.m.token})
		c.check(t, w.run("join", "acme.demo.bob.join"))
		if _, err := os.Stat(w.cfg); err == nil {
			t.Error("the configuration folder was made")
		}
	})
	do("The folder cannot be written", func(w *joinWorld, c specCase) {
		if os.Geteuid() == 0 {
			t.Skip("root can write into a read-only folder")
		}
		os.MkdirAll(w.cfg, 0o700)
		if err := os.Chmod(w.cfg, 0o500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(w.cfg, 0o700)
		c.check(t, w.run("join", "acme.demo.bob.join"))
	})
	do("Could not finish and could not undo", func(w *joinWorld, c specCase) {
		// A --replace run never has a later step that can fail, so the rollback is driven directly: the backup of the old
		// creds cannot be put back, and the message is the one the run would print.
		creds := w.credsFile()
		backup := filepath.Join(w.cfg, ".join-backup-x")
		os.MkdirAll(w.cfg, 0o700)
		os.WriteFile(creds, []byte("new"), 0o600)
		os.WriteFile(backup, []byte("old"), 0o600)
		failJoinSteps(t, "acme.demo.bob.creds", "")
		left, kept := joinUndo(nil, backup, creds, nil)
		e := joinSvc{home: w.home}.undoFailure(left, kept)
		got := ran{code: e.Code, errs: "error: " + e.Msg + "\nhint: " + e.Hint + "\n"}
		c.check(t, got)
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
	// the usual --json is the address and nothing else
	r := w.run("join", "acme.demo.bob.join", "--json", "--dry-run")
	want := `{"endpoint":"acme.demo.bob","dry_run":true}` + "\n"
	if r.code != 0 || r.out != want || r.errs != "" {
		t.Errorf("--dry-run --json: %+v", r)
	}
	r = w.run("join", "acme.demo.bob.join", "--json")
	want = `{"endpoint":"acme.demo.bob"}` + "\n"
	if r.code != 0 || r.out != want || r.errs != "" {
		t.Errorf("--json: %+v", r)
	}
	// a failure as JSON: {error, hint} on stdout, nothing on stderr
	r = w.run("join", "acme.demo.bob.join", "--json")
	if r.code != 1 || r.errs != "" || r.out != `{"error":"acme.demo.bob is already installed here","hint":"mash join acme.demo.bob.join --replace"}`+"\n" {
		t.Errorf("--json failure: %+v", r)
	}
}

func TestJoinDetailJSON(t *testing.T) {
	w := newJoinWorld(t)
	// --detail --json adds the servers and the creds file's absolute path, not abbreviated with ~
	r := w.run("join", "acme.demo.bob.join", "--detail", "--json")
	want := `{"endpoint":"acme.demo.bob","servers":["tls://myserver:4222"],"creds":"` + filepath.Join(w.cfg, "acme.demo.bob.creds") + `"}` + "\n"
	if r.code != 0 || r.out != want || r.errs != "" || !filepath.IsAbs(w.cfg) {
		t.Errorf("--detail --json: %+v\nwant %s", r, want)
	}
	other := t.TempDir()
	r = w.run("join", "acme.demo.bob.join", "--detail", "--json", "--config-dir", other)
	if !strings.Contains(r.out, `"creds":"`+filepath.Join(other, "acme.demo.bob.creds")+`"`) {
		t.Errorf("creds outside home: %+v", r)
	}
}

func TestJoinVerbose(t *testing.T) {
	w := newJoinWorld(t)
	r := w.run("join", "acme.demo.bob.join", "--detail")
	if r.code != 0 || r.out != "Joined as acme.demo.bob.\nservers: tls://myserver:4222\ncreds:   ~/.config/mash/acme.demo.bob.creds\n" {
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
	dave := newJoinMaterial(t, "acme.demo.dave")
	writeJoin(t, "dave.join", joinfile.File{MashJoin: 1, Address: "acme.demo.dave", Servers: []string{"tls://elsewhere:4222"}, Creds: string(dave.creds)})
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
	w.joinFor("none.join", w.m, "acme.demo.bob", nil) // a folder that names servers takes no ca (#1062)
	if r := w.run("join", "none.join"); r.code != 0 {
		t.Fatal(r)
	}
	cfg := w.configJSON()
	if cfg["host"] != "codex" || cfg["servers"] != "tls://myserver:4222" || cfg["ca"] != nil {
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
	ca := genCA(t, "Rotated CA")
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
	dm := newJoinMaterial(t, "acme.demo.dan")
	writeJoin(t, "dan.join", joinfile.File{MashJoin: 1, Address: "acme.demo.dan", Servers: []string{"tls://myserver:4222"}, Creds: string(dm.creds)})
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
	other := genCA(t, "Other CA")
	writeJoin(t, "carol.join", joinfile.File{MashJoin: 1, Address: "acme.demo.carol", Servers: []string{"tls://myserver:4222"}, CA: &other, Creds: string(m.creds)})
	for _, args := range [][]string{{"join", "carol.join"}, {"join", "carol.join", "--replace"}} {
		if r := w.run(args...); r.code != 1 || !strings.Contains(r.errs, " was set up with a different ca;") {
			t.Errorf("%v: %+v", args, r)
		}
	}
	dm := newJoinMaterial(t, "acme.demo.dan")
	writeJoin(t, "dan.join", joinfile.File{MashJoin: 1, Address: "acme.demo.dan", Servers: []string{"tls://myserver:4222"}, Creds: string(dm.creds)})
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

// genCA is a new self-signed certificate authority, as PEM.
func genCA(t *testing.T, cn string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// genKeyPEM is a private key block, which must never be installed as a CA.
func genKeyPEM(t *testing.T) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
}

func (w *joinWorld) joinFor(file string, m joinMaterial, address string, ca *string) {
	w.t.Helper()
	writeJoin(w.t, file, joinfile.File{MashJoin: 1, Address: address, Servers: []string{"tls://myserver:4222"}, CA: ca, Creds: string(m.creds)})
}

// A join file as mashctl writes it (the shared joinfile package, mode 0600) installs, and the certificate is kept as
// ca.pem, which x509 parses and config.json names.
func TestJoinRoundTripFromTheSharedWriter(t *testing.T) {
	w := newJoinWorld(t)
	ca := genCA(t, "Round Trip CA")
	data, err := joinfile.File{MashJoin: joinfile.Format, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, CA: &ca, Creds: string(w.m.creds)}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("rt.join", data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := w.run("join", "rt.join"); r.code != 0 || r.out != "Joined as acme.demo.bob.\n" {
		t.Fatal(r)
	}
	raw, err := os.ReadFile(filepath.Join(w.cfg, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	blk, rest := pem.Decode(raw)
	if blk == nil || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("ca.pem: %q", raw)
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil || cert.Subject.CommonName != "Round Trip CA" {
		t.Errorf("ca.pem certificate: %v, %v", cert, err)
	}
	if w.configJSON()["ca"] != "ca.pem" {
		t.Errorf("config.json: %v", w.configJSON())
	}
	if got, _ := os.ReadFile(w.credsFile()); !bytes.Equal(got, w.m.creds) {
		t.Error("creds differ")
	}
	// the test CA of the other tests is a certificate x509 reads too
	if blk, _ := pem.Decode([]byte(testCAPEM)); blk == nil {
		t.Fatal("testCAPEM")
	} else if _, err := x509.ParseCertificate(blk.Bytes); err != nil {
		t.Errorf("testCAPEM: %v", err)
	}
}

// A CA that is anything but certificates is refused before any file is written.
func TestJoinRefusesACAThatIsNotOnlyCertificates(t *testing.T) {
	good := genCA(t, "Good CA")
	for name, ca := range map[string]string{
		"private key appended": good + genKeyPEM(t),
		"key first":            genKeyPEM(t) + good,
		"garbage":              "this is not a certificate\n",
		"trailing garbage":     good + "trailing text\n",
		"leading garbage":      "junk\n" + good,
		"garbage between":      good + "junk\n" + good,
		"bad certificate":      strings.Replace(good, "MII", "MIJ", 1),
		"empty":                "",
		"header":               strings.Replace(good, "-----BEGIN CERTIFICATE-----\n", "-----BEGIN CERTIFICATE-----\nProc-Type: 4,ENCRYPTED\n\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			w := newJoinWorld(t)
			w.joinFor("bad.join", w.m, "acme.demo.bob", &ca)
			r := w.run("join", "bad.join")
			if r.code != 1 || r.out != "" || r.errs != "error: bad.join is not a join file this mash can read\n" {
				t.Errorf("%+v", r)
			}
			if _, err := os.Stat(w.cfg); err == nil {
				t.Error("a refused join made the configuration folder")
			}
		})
	}
}

// The JWT's name is the endpoint it was issued for; a join file whose address differs is refused before anything is
// written. The error names the JWT's name and the address the join file gives (#1242).
func TestJoinRefusesACredsNameThatIsNotTheAddress(t *testing.T) {
	w := newJoinWorld(t)
	other := newJoinMaterial(t, "acme.demo.carol")
	w.joinFor("mix.join", other, "acme.demo.bob", nil)
	writeJoin(t, "mixjwt.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, UserJWT: other.token})
	os.WriteFile("carol.seed", other.seed, 0o600)
	hint := func(string) string {
		return "hint: mashctl endpoint reissue acme.demo.bob --out acme.demo.bob.new.join\n"
	}
	for _, args := range [][]string{{"join", "mix.join"}, {"join", "mixjwt.join", "--key", "carol.seed"}} {
		r := w.run(args...)
		if r.code != 1 || r.out != "" || r.errs != "error: "+args[1]+" names acme.demo.bob but is for acme.demo.carol\n"+hint(args[1]) {
			t.Errorf("%v: %+v", args, r)
		}
	}
	// a JWT with no name says so, and does not print an empty name
	none := newJoinMaterial(t, "")
	w.joinFor("noname.join", none, "acme.demo.bob", nil)
	if r := w.run("join", "noname.join"); r.code != 1 || r.out != "" || r.errs != "error: noname.join has no name but names acme.demo.bob\n"+hint("noname.join") {
		t.Errorf("no name: %+v", r)
	}
	if _, err := os.Stat(w.cfg); err == nil {
		t.Error("a refused join made the configuration folder")
	}
}

// A creds file that is a symlink in the folder is not written through: refused without --replace, and replaced (the link,
// not its target) with it.
func TestJoinOverACredsSymlink(t *testing.T) {
	w := newJoinWorld(t)
	os.MkdirAll(w.cfg, 0o700)
	w.joinFor("acme.demo.bob.join", w.m, "acme.demo.bob", nil) // the folder holds creds and no ca.pem: no CA may come in
	target := filepath.Join(w.home, "elsewhere.creds")
	os.WriteFile(target, []byte("precious\n"), 0o600)
	if err := os.Symlink(target, w.credsFile()); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, w.cfg)
	r := w.run("join", "acme.demo.bob.join")
	if r.code != 1 || r.errs != "error: acme.demo.bob is already installed here\nhint: mash join acme.demo.bob.join --replace\n" {
		t.Errorf("%+v", r)
	}
	if treeSnapshot(t, w.cfg) != before {
		t.Error("a refused join changed the folder")
	}
	if r := w.run("join", "acme.demo.bob.join", "--replace"); r.code != 0 {
		t.Fatal(r)
	}
	if st, err := os.Lstat(w.credsFile()); err != nil || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm() != 0o600 {
		t.Errorf("creds after --replace: %v, %v", st, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "precious\n" {
		t.Errorf("the symlink's target was written: %q", got)
	}
}

// A different CA, or none where the folder has one, is refused with and without --replace, and changes nothing.
func TestJoinRefusesAnotherCAWithOrWithoutReplace(t *testing.T) {
	want := "error: ~/.config/mash was set up with a different ca;\n       nothing was changed\nhint: mash join %s --config-dir <another folder>\n"
	for _, replace := range []bool{false, true} {
		w := newJoinWorld(t)
		if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
			t.Fatal(r)
		}
		fresh := newJoinMaterial(t, "acme.demo.bob")
		other := genCA(t, "Other CA")
		w.joinFor("other.join", fresh, "acme.demo.bob", &other)
		w.joinFor("none.join", fresh, "acme.demo.bob", nil)
		for _, f := range []string{"other.join", "none.join"} {
			before := treeSnapshot(t, w.cfg)
			args := []string{"join", f}
			if replace {
				args = append(args, "--replace")
			}
			r := w.run(args...)
			if r.code != 1 || r.out != "" || r.errs != fmt.Sprintf(want, f) {
				t.Errorf("%v: %+v", args, r)
			}
			if treeSnapshot(t, w.cfg) != before {
				t.Errorf("%v changed the folder", args)
			}
		}
	}
}

// The folder's CA is the file config.json names, compared by certificate content, not by bytes.
func TestJoinComparesTheConfiguredCAByContent(t *testing.T) {
	w := newJoinWorld(t)
	ca := genCA(t, "Mine")
	os.MkdirAll(w.cfg, 0o700)
	os.WriteFile(filepath.Join(w.cfg, "config.json"), []byte(`{"servers":["tls://myserver:4222"],"ca":"mine.pem"}`), 0o600)
	os.WriteFile(filepath.Join(w.cfg, "mine.pem"), []byte("\n"+strings.ReplaceAll(ca, "\n", "\r\n")+"\n\n"), 0o644)
	w.joinFor("a.join", w.m, "acme.demo.bob", &ca)
	if r := w.run("join", "a.join"); r.code != 0 {
		t.Fatalf("same certificate, other bytes: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(w.cfg, "ca.pem")); err == nil {
		t.Error("ca.pem was written beside the configured CA file")
	}
	// a different certificate than the configured file's is refused, though ca.pem would be free
	other := genCA(t, "Other")
	carol := newJoinMaterial(t, "acme.demo.carol")
	w.joinFor("c.join", carol, "acme.demo.carol", &other)
	r := w.run("join", "c.join")
	if r.code != 1 || !strings.Contains(r.errs, "was set up with a different ca;") {
		t.Errorf("%+v", r)
	}
	// and none where the folder has one
	w.joinFor("n.join", carol, "acme.demo.carol", nil)
	if r := w.run("join", "n.join"); r.code != 1 || !strings.Contains(r.errs, "was set up with a different ca;") {
		t.Errorf("%+v", r)
	}
}

// --replace replaces the creds for the same address and nothing else.
func TestJoinReplaceChangesOnlyTheCreds(t *testing.T) {
	w := newJoinWorld(t)
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		t.Fatal(r)
	}
	caBefore, _ := os.ReadFile(filepath.Join(w.cfg, "ca.pem"))
	cfgBefore, _ := os.ReadFile(filepath.Join(w.cfg, "config.json"))
	fresh := newJoinMaterial(t, "acme.demo.bob")
	ca := testCAPEM
	w.joinFor("re.join", fresh, "acme.demo.bob", &ca)
	if r := w.run("join", "re.join", "--replace"); r.code != 0 || r.out != "Joined as acme.demo.bob.\n" {
		t.Fatal(r)
	}
	if got, _ := os.ReadFile(w.credsFile()); !bytes.Equal(got, fresh.creds) {
		t.Error("creds were not replaced")
	}
	if got, _ := os.ReadFile(filepath.Join(w.cfg, "ca.pem")); !bytes.Equal(got, caBefore) {
		t.Error("ca.pem changed")
	}
	if got, _ := os.ReadFile(filepath.Join(w.cfg, "config.json")); !bytes.Equal(got, cfgBefore) {
		t.Error("config.json changed")
	}
	if ents, _ := os.ReadDir(w.cfg); len(ents) != 3 { // creds, ca.pem, config.json: no temporary or backup left
		t.Errorf("folder: %v", ents)
	}
}

// Joins into one folder run one after the other, and each checks the mesh and the CA under the lock: of two joins that
// bring different servers or different CAs into an empty folder, exactly one wins.
func TestJoinConcurrentJoinsOfDifferentMeshesLeaveOne(t *testing.T) {
	for _, kind := range []string{"server", "ca"} {
		w := newJoinWorld(t)
		caA, caB := genCA(t, "A"), genCA(t, "B")
		m1, m2 := newJoinMaterial(t, "acme.demo.one"), newJoinMaterial(t, "acme.demo.two")
		serversB := []string{"tls://myserver:4222"}
		if kind == "server" {
			serversB = []string{"tls://elsewhere:4222"}
			caB = caA
		}
		writeJoin(t, "one.join", joinfile.File{MashJoin: 1, Address: "acme.demo.one", Servers: []string{"tls://myserver:4222"}, CA: &caA, Creds: string(m1.creds)})
		writeJoin(t, "two.join", joinfile.File{MashJoin: 1, Address: "acme.demo.two", Servers: serversB, CA: &caB, Creds: string(m2.creds)})
		var wg sync.WaitGroup
		codes := make([]int, 2)
		for i, f := range []string{"one.join", "two.join"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i] = w.run("join", f).code
			}()
		}
		wg.Wait()
		if codes[0]+codes[1] != 1 {
			t.Errorf("%s: exit codes %v, want exactly one success", kind, codes)
		}
		ents, _ := os.ReadDir(w.cfg)
		creds := 0
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".creds") {
				creds++
			}
		}
		if creds != 1 || len(ents) != 3 {
			t.Errorf("%s: folder %v", kind, ents)
		}
	}
}

// `mash version --json` carries the whole commit; the text form shows seven.
func TestVersionJSONCarriesTheWholeCommit(t *testing.T) {
	full := "3f9a41c2e0b7d5a1c4e08b2d77f3a95e1b6c4098"
	old := commit // the -ldflags value, which run reads
	commit = full
	defer func() { commit = old }()
	var out, errb bytes.Buffer
	if code := run([]string{"version", "--json"}, envFrom(nil), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var o map[string]string
	if err := json.Unmarshal(out.Bytes(), &o); err != nil || o["commit"] != full || len(o["commit"]) != 40 {
		t.Errorf("version --json: %q %v", out.String(), err)
	}
	out.Reset()
	if code := run([]string{"version"}, envFrom(nil), strings.NewReader(""), &out, &errb); code != 0 || !strings.HasSuffix(out.String(), " (3f9a41c)\n") {
		t.Errorf("version: %d %q", code, out.String())
	}
}

// failJoinSteps makes the rename or remove fail for the paths that end in the given suffixes, for the test's length.
func failJoinSteps(t *testing.T, renameSuffix, removeSuffix string) {
	t.Helper()
	oldRename, oldRemove, oldLink := joinRename, joinRemove, joinLink
	t.Cleanup(func() { joinRename, joinRemove, joinLink = oldRename, oldRemove, oldLink })
	joinLink = func(from, to string) error { // fails the restore of a backup, not the put-back of the run's own file
		if renameSuffix != "" && strings.HasSuffix(to, renameSuffix) && strings.Contains(from, ".join-backup-") {
			return &os.LinkError{Op: "link", Old: from, New: to, Err: errors.New("injected failure")}
		}
		return oldLink(from, to)
	}
	joinRename = func(from, to string) error {
		if renameSuffix != "" && strings.HasSuffix(to, renameSuffix) {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("injected failure")}
		}
		return oldRename(from, to)
	}
	joinRemove = func(p string) error {
		if removeSuffix != "" && strings.Contains(p, removeSuffix) { // also the rollback's capture name
			return &os.PathError{Op: "remove", Path: p, Err: errors.New("injected failure")}
		}
		return oldRemove(p)
	}
}

func TestJoinRollsBackWhenCAOrConfigRenameFails(t *testing.T) {
	for _, suffix := range []string{"ca.pem", "config.json"} {
		t.Run(suffix, func(t *testing.T) {
			w := newJoinWorld(t)
			failJoinSteps(t, suffix, "")
			r := w.run("join", "acme.demo.bob.join")
			if r.code != 1 || !strings.HasPrefix(r.errs, "error: cannot write into ") || !strings.Contains(r.errs, "nothing was installed") {
				t.Errorf("%+v", r)
			}
			if _, err := os.Lstat(w.credsFile()); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("creds were left installed: %v", err)
			}
			if ents, _ := os.ReadDir(w.cfg); len(ents) != 0 {
				t.Errorf("left behind: %v", ents)
			}
		})
	}
}

// Surrounding white space and several certificates are fine.
func TestJoinAcceptsPaddedAndMultipleCertificates(t *testing.T) {
	a, b := genCA(t, "A CA"), genCA(t, "B CA")
	for name, ca := range map[string]string{"padded": "\n \t\r\n" + a + "\n\n  ", "two": a + "\n" + b} {
		t.Run(name, func(t *testing.T) {
			w := newJoinWorld(t)
			w.joinFor("ok.join", w.m, "acme.demo.bob", &ca)
			if r := w.run("join", "ok.join"); r.code != 0 {
				t.Errorf("%+v", r)
			}
		})
	}
}

func TestJoinNamesWhatARollbackCouldNotUndo(t *testing.T) {
	w := newJoinWorld(t)
	failJoinSteps(t, "config.json", "acme.demo.bob.creds")
	r := w.run("join", "acme.demo.bob.join")
	if r.code != 1 || !strings.HasPrefix(r.errs, "error: could not finish and could not undo: ") || !strings.Contains(r.errs, "~/.config/mash/acme.demo.bob.creds.join-undo-") {
		t.Errorf("%+v", r)
	}
	if strings.Contains(r.errs, "old creds kept") {
		t.Errorf("no backup exists, yet: %q", r.errs)
	}
	if strings.Contains(r.errs, "BEGIN") || strings.Contains(r.errs, string(w.m.seed)) {
		t.Errorf("secret on stderr: %q", r.errs)
	}
	if m, _ := filepath.Glob(w.credsFile() + ".join-undo-*"); len(m) != 1 {
		t.Errorf("the creds could not be removed and must still be there under the capture name: %v", m)
	}
	for _, name := range []string{"config.json", "ca.pem"} {
		if _, err := os.Lstat(filepath.Join(w.cfg, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s was left: %v", name, err)
		}
	}
}

func TestJoinUndoKeepsTheOldCredsAsDotOld(t *testing.T) {
	setup := func(t *testing.T) (w *joinWorld, creds, backup string) {
		w = newJoinWorld(t)
		creds = w.credsFile()
		backup = filepath.Join(w.cfg, ".join-backup-x")
		os.MkdirAll(w.cfg, 0o700)
		os.WriteFile(creds, []byte("NEW"), 0o600)
		os.WriteFile(backup, []byte("OLD"), 0o600)
		failJoinSteps(t, "acme.demo.bob.creds", "")
		return
	}
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	t.Run("renamed to .old", func(t *testing.T) {
		w, creds, backup := setup(t)
		left, kept := joinUndo(nil, backup, creds, nil)
		if len(left) != 1 || left[0] != creds || kept != creds+".old" {
			t.Fatalf("left %v, kept %q", left, kept)
		}
		if read(kept) != "OLD" || read(creds) != "NEW" {
			t.Errorf(".old %q, creds %q", read(kept), read(creds))
		}
		if _, err := os.Lstat(backup); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the backup name is still there: %v", err)
		}
		msg := joinSvc{home: w.home}.undoFailure(left, kept).Msg
		if strings.Contains(msg, "OLD") || strings.Contains(msg, "NEW") || !strings.Contains(msg, "~/.config/mash/acme.demo.bob.creds left; old creds kept at ~/.config/mash/acme.demo.bob.creds.old") {
			t.Errorf("%q", msg)
		}
	})
	t.Run("an existing .old is never overwritten", func(t *testing.T) {
		w, creds, backup := setup(t)
		os.WriteFile(creds+".old", []byte("EARLIER"), 0o600)
		left, kept := joinUndo(nil, backup, creds, nil)
		if len(left) != 1 || kept != backup {
			t.Fatalf("left %v, kept %q", left, kept)
		}
		if read(creds+".old") != "EARLIER" || read(backup) != "OLD" {
			t.Errorf(".old %q, backup %q", read(creds+".old"), read(backup))
		}
		msg := joinSvc{home: w.home}.undoFailure(left, kept).Msg
		if !strings.Contains(msg, "old creds kept at ~/.config/mash/.join-backup-x") || strings.Contains(msg, "OLD") {
			t.Errorf("%q", msg)
		}
	})
	t.Run("several leftovers", func(t *testing.T) {
		w := newJoinWorld(t)
		msg := joinSvc{home: w.home}.undoFailure([]string{filepath.Join(w.cfg, "ca.pem"), w.credsFile()}, "").Msg
		if msg != "could not finish and could not undo: ~/.config/mash/ca.pem, ~/.config/mash/acme.demo.bob.creds left" {
			t.Errorf("%q", msg)
		}
	})
}

// --replace over a creds path that is not an installed endpoint (a symlink) is a first-style join: the creds path is
// replaced, never written through, and ca.pem and config.json are written.
func TestJoinReplaceOverANonEndpointWritesCAAndConfig(t *testing.T) {
	w := newJoinWorld(t)
	os.MkdirAll(w.cfg, 0o700)
	target := filepath.Join(w.home, "elsewhere.creds")
	os.WriteFile(target, []byte("precious\n"), 0o600)
	if err := os.Symlink(target, w.credsFile()); err != nil {
		t.Fatal(err)
	}
	if r := w.run("join", "acme.demo.bob.join", "--replace"); r.code != 0 {
		t.Fatal(r)
	}
	if st, err := os.Lstat(w.credsFile()); err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		t.Errorf("creds: %v, %v", st, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "precious\n" {
		t.Errorf("target written: %q", got)
	}
	for _, f := range []string{"ca.pem", "config.json"} {
		if _, err := os.Stat(filepath.Join(w.cfg, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

// A seed hidden in a creds value that a later duplicate key overwrites, or a second object after the first, is not a
// join file, whatever its mode: it is refused as malformed and nothing is installed.
func TestJoinRefusesDuplicateKeysAndTrailingObjects(t *testing.T) {
	w := newJoinWorld(t)
	secret := string(w.m.seed)
	seeded, _ := json.Marshal(string(w.m.creds))
	head := `{"mash_join":1,"address":"acme.demo.bob","servers":["tls://myserver:4222"],`
	for name, body := range map[string]string{
		"duplicate creds": head + `"creds":` + string(seeded) + `,"creds":"x"}` + "\n",
		"trailing object": head + `"creds":` + string(seeded) + "}\n{}",
	} {
		if err := os.WriteFile("dup.join", []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod("dup.join", 0o644); err != nil {
			t.Fatal(err)
		}
		r := w.run("join", "dup.join")
		if r.code != 1 || r.out != "" || !strings.HasPrefix(r.errs, "error: dup.join is ") || strings.Count(r.errs, "\n") != 1 || strings.Contains(r.errs, secret) {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if _, err := os.Stat(w.cfg); err == nil {
		t.Error("a refused join made the configuration folder")
	}
}

// halfDone leaves the folder as a join killed after the creds step does: the creds a normal join installs, and then
// config.json and ca.pem removed as asked. It returns what a complete install holds, for comparing.
func (w *joinWorld) halfDone(noCfg, noCA bool) (cfg, ca []byte) {
	w.t.Helper()
	if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
		w.t.Fatal(r)
	}
	cfg, _ = os.ReadFile(filepath.Join(w.cfg, "config.json"))
	ca, _ = os.ReadFile(filepath.Join(w.cfg, "ca.pem"))
	if noCfg {
		os.Remove(filepath.Join(w.cfg, "config.json"))
	}
	if noCA {
		os.Remove(filepath.Join(w.cfg, "ca.pem"))
	}
	return cfg, ca
}

func (w *joinWorld) noJoinLeftovers() {
	w.t.Helper()
	if m, _ := filepath.Glob(filepath.Join(w.cfg, ".join-*")); len(m) != 0 {
		w.t.Errorf("leftovers: %v", m)
	}
}

const finishedLine = "Joined as acme.demo.bob (finished an earlier join)\n"

func TestJoinFinishesAHalfDoneJoin(t *testing.T) {
	for _, tc := range []struct {
		name      string
		noCfg, ca bool
	}{{"no config.json and no ca.pem", true, true}, {"only config.json missing", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			w := newJoinWorld(t)
			wantCfg, wantCA := w.halfDone(tc.noCfg, tc.ca)
			before, _ := os.ReadFile(w.credsFile())
			stBefore, _ := os.Stat(w.credsFile())
			r := w.run("join", "acme.demo.bob.join")
			if r.code != 0 || r.out != finishedLine || r.errs != "" {
				t.Fatalf("%+v", r)
			}
			if gotCfg, _ := os.ReadFile(filepath.Join(w.cfg, "config.json")); !bytes.Equal(gotCfg, wantCfg) {
				t.Errorf("config.json: %s, want %s", gotCfg, wantCfg)
			}
			if gotCA, _ := os.ReadFile(filepath.Join(w.cfg, "ca.pem")); !bytes.Equal(gotCA, wantCA) {
				t.Errorf("ca.pem: %s", gotCA)
			}
			now, _ := os.ReadFile(w.credsFile())
			stNow, _ := os.Stat(w.credsFile())
			if !bytes.Equal(now, before) || !os.SameFile(stBefore, stNow) {
				t.Error("the creds were touched")
			}
			w.noJoinLeftovers()
			// the folder is whole now: another run is the plain refusal
			if r := w.run("join", "acme.demo.bob.join"); r.code != 1 || !strings.Contains(r.errs, "already installed here") {
				t.Errorf("after finishing: %+v", r)
			}
		})
	}
}

func TestJoinFinishDetailAndDryRun(t *testing.T) {
	w := newJoinWorld(t)
	w.halfDone(true, true)
	r := w.run("join", "acme.demo.bob.join", "--dry-run")
	if r.code != 0 || r.out != "Would join as acme.demo.bob.\n" {
		t.Errorf("--dry-run: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(w.cfg, "config.json")); err == nil {
		t.Error("--dry-run wrote config.json")
	}
	r = w.run("join", "acme.demo.bob.join", "--detail")
	if r.code != 0 || !strings.HasPrefix(r.out, strings.TrimSuffix(finishedLine, "\n")+"\nservers: tls://myserver:4222\ncreds:   ") {
		t.Errorf("--detail: %+v", r)
	}
}

func TestJoinHalfDoneWithOtherCredsIsRefused(t *testing.T) {
	w := newJoinWorld(t)
	w.halfDone(true, true)
	before, _ := os.ReadFile(w.credsFile())
	other := newJoinMaterial(t, "acme.demo.bob")
	ca := testCAPEM
	writeJoin(t, "acme.demo.bob.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, CA: &ca, Creds: string(other.creds)})
	r := w.run("join", "acme.demo.bob.join")
	if r.code != 1 || r.out != "" {
		t.Errorf("%+v", r)
	}
	for _, f := range []string{"config.json", "ca.pem"} {
		if _, err := os.Stat(filepath.Join(w.cfg, f)); err == nil {
			t.Errorf("%s was written", f)
		}
	}
	if now, _ := os.ReadFile(w.credsFile()); !bytes.Equal(now, before) {
		t.Error("creds changed")
	}
	// only config.json missing: the plain refusal
	w2 := newJoinWorld(t)
	w2.halfDone(true, false)
	writeJoin(t, "acme.demo.bob.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, CA: &ca, Creds: string(other.creds)})
	r = w2.run("join", "acme.demo.bob.join")
	if r.code != 1 || r.errs != "error: acme.demo.bob is already installed here\nhint: mash join acme.demo.bob.join --replace\n" {
		t.Errorf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(w2.cfg, "config.json")); err == nil {
		t.Error("config.json was written")
	}
}

func TestJoinCompleteInstallIsStillRefused(t *testing.T) {
	w := newJoinWorld(t)
	w.halfDone(false, false)
	r := w.run("join", "acme.demo.bob.join")
	if r.code != 1 || r.errs != "error: acme.demo.bob is already installed here\nhint: mash join acme.demo.bob.join --replace\n" {
		t.Errorf("%+v", r)
	}
}

func TestJoinReplaceFinishesAHalfDoneJoin(t *testing.T) {
	w := newJoinWorld(t)
	wantCfg, wantCA := w.halfDone(true, true)
	fresh := newJoinMaterial(t, "acme.demo.bob")
	ca := testCAPEM
	writeJoin(t, "acme.demo.bob.join", joinfile.File{MashJoin: 1, Address: "acme.demo.bob", Servers: []string{"tls://myserver:4222"}, CA: &ca, Creds: string(fresh.creds)})
	r := w.run("join", "acme.demo.bob.join", "--replace")
	if r.code != 0 || r.out != "Joined as acme.demo.bob.\n" {
		t.Fatalf("%+v", r)
	}
	if now, _ := os.ReadFile(w.credsFile()); !bytes.Equal(now, fresh.creds) {
		t.Error("the creds were not replaced")
	}
	if got, _ := os.ReadFile(filepath.Join(w.cfg, "config.json")); !bytes.Equal(got, wantCfg) {
		t.Errorf("config.json: %s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(w.cfg, "ca.pem")); !bytes.Equal(got, wantCA) {
		t.Errorf("ca.pem: %s", got)
	}
	w.noJoinLeftovers()
}

func TestJoinDoesNotFinishOverUnsafeCreds(t *testing.T) {
	for _, kind := range []string{"symlink", "mode 0644"} {
		t.Run(kind, func(t *testing.T) {
			w := newJoinWorld(t)
			w.halfDone(true, true)
			creds, _ := os.ReadFile(w.credsFile())
			if kind == "symlink" {
				target := filepath.Join(w.home, "elsewhere.creds")
				os.WriteFile(target, creds, 0o600)
				os.Remove(w.credsFile())
				if err := os.Symlink(target, w.credsFile()); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Chmod(w.credsFile(), 0o644); err != nil {
				t.Fatal(err)
			}
			r := w.run("join", "acme.demo.bob.join")
			if r.code != 1 || !strings.Contains(r.errs, "already installed here") || strings.Contains(r.out, "finished") {
				t.Errorf("%+v", r)
			}
			for _, f := range []string{"config.json", "ca.pem"} {
				if _, err := os.Stat(filepath.Join(w.cfg, f)); err == nil {
					t.Errorf("%s was written", f)
				}
			}
		})
	}
}

func TestJoinFinishRollsBackOnlyWhatItWrote(t *testing.T) {
	w := newJoinWorld(t)
	w.halfDone(true, true)
	before, _ := os.ReadFile(w.credsFile())
	failJoinSteps(t, "config.json", "")
	r := w.run("join", "acme.demo.bob.join")
	if r.code != 1 || !strings.HasPrefix(r.errs, "error: cannot write into ") {
		t.Errorf("%+v", r)
	}
	if now, err := os.ReadFile(w.credsFile()); err != nil || !bytes.Equal(now, before) {
		t.Errorf("creds: %v", err)
	}
	for _, f := range []string{"config.json", "ca.pem"} {
		if _, err := os.Stat(filepath.Join(w.cfg, f)); err == nil {
			t.Errorf("%s left behind", f)
		}
	}
	w.noJoinLeftovers()
}

// A folder whose config.json already names servers was set up before: a join file that brings a ca is refused with the
// same line as for a folder that holds an endpoint, and nothing is changed. An empty folder, or a config.json with no
// servers, still takes it.
func TestJoinCARefusedWhereTheFolderNamesServersAndHasNoCA(t *testing.T) {
	w := newJoinWorld(t)
	if err := os.MkdirAll(w.cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(w.cfg, "config.json")
	cfgBytes := []byte("{\n  \"servers\": [\"tls://myserver:4222\"]\n}\n")
	if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"join", "acme.demo.bob.join"}, {"join", "acme.demo.bob.join", "--replace"}} {
		r := w.run(args...)
		if r.code != 1 || !strings.Contains(r.errs, " was set up with a different ca;\n       nothing was changed\n") {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if got, _ := os.ReadFile(cfgPath); !bytes.Equal(got, cfgBytes) {
		t.Errorf("config.json changed: %q", got)
	}
	ents, _ := os.ReadDir(w.cfg)
	if len(ents) != 1 {
		t.Errorf("the folder changed: %v", ents)
	}
}

func TestJoinCAAcceptedByAFreshFolderOrOneWithoutServers(t *testing.T) {
	for name, cfg := range map[string]string{"fresh": "", "no servers": "{\"other\": 1}\n"} {
		t.Run(name, func(t *testing.T) {
			w := newJoinWorld(t)
			if cfg != "" {
				os.MkdirAll(w.cfg, 0o700)
				if err := os.WriteFile(filepath.Join(w.cfg, "config.json"), []byte(cfg), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if r := w.run("join", "acme.demo.bob.join"); r.code != 0 {
				t.Fatalf("%+v", r)
			}
			if _, err := os.Stat(filepath.Join(w.cfg, "ca.pem")); err != nil {
				t.Errorf("ca.pem: %v", err)
			}
		})
	}
}

// A rollback with no backup removes the creds path only if it is still the file the join made.
func TestJoinRollbackLeavesACredsFileSomeoneReplaced(t *testing.T) {
	for _, replace := range []bool{true, false} {
		name := "untouched"
		if replace {
			name = "replaced"
		}
		t.Run(name, func(t *testing.T) {
			w := newJoinWorld(t)
			oldRename := joinRename
			t.Cleanup(func() { joinRename = oldRename })
			joinRename = func(from, to string) error {
				if strings.HasSuffix(to, "config.json") {
					if replace { // the window: after the install, before the rollback
						creds := w.credsFile()
						if err := os.Remove(creds); err != nil {
							t.Error(err)
						}
						if err := os.WriteFile(creds, []byte("someone else's"), 0o600); err != nil {
							t.Error(err)
						}
					}
					return &os.LinkError{Op: "rename", Old: from, New: to, Err: errors.New("injected failure")}
				}
				return oldRename(from, to)
			}
			r := w.run("join", "acme.demo.bob.join")
			got, err := os.ReadFile(w.credsFile())
			if !replace {
				if r.code != 1 || !strings.Contains(r.errs, "nothing was installed") || !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("%+v %v", r, err)
				}
				return
			}
			if err != nil || string(got) != "someone else's" {
				t.Errorf("the replaced file did not survive: %q %v", got, err)
			}
			if r.code != 1 || !strings.Contains(r.errs, "could not finish and could not undo: ~/.config/mash/acme.demo.bob.creds left") {
				t.Errorf("%+v", r)
			}
		})
	}
}

// The rollback captures the creds path by rename before it removes or restores, so a file put there between the
// decision and the act survives. replaceAt runs just before the capture rename.
func joinUndoWorld(t *testing.T, withBackup bool) (creds, backup string, wrote *joinWrote) {
	t.Helper()
	dir := t.TempDir()
	creds = filepath.Join(dir, "x.creds")
	if err := os.WriteFile(creds, []byte("JOINS"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrote = recordJoinWrote(creds, []byte("JOINS"))
	if withBackup {
		backup = filepath.Join(dir, ".join-backup-x")
		if err := os.WriteFile(backup, []byte("OLD"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func hookJoinCapture(t *testing.T, before func(), after func()) {
	t.Helper()
	old := joinRename
	t.Cleanup(func() { joinRename = old })
	joinRename = func(from, to string) error {
		if !strings.Contains(to, ".join-undo-") {
			return old(from, to)
		}
		if before != nil {
			before()
		}
		err := old(from, to)
		if after != nil {
			after()
		}
		return err
	}
}

func swapFile(t *testing.T, path, content string) func() {
	return func() {
		if err := os.Remove(path); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Error(err)
		}
	}
}

func readStr(p string) string { b, _ := os.ReadFile(p); return string(b) }

func TestJoinUndoCaptureKeepsAForeignFile(t *testing.T) {
	t.Run("no backup, replaced before the capture", func(t *testing.T) {
		creds, _, wrote := joinUndoWorld(t, false)
		hookJoinCapture(t, swapFile(t, creds, "FOREIGN"), nil)
		left, kept := joinUndo(nil, "", creds, wrote)
		if readStr(creds) != "FOREIGN" || kept != "" || len(left) != 1 || left[0] != creds {
			t.Errorf("creds %q left %v kept %q", readStr(creds), left, kept)
		}
		if m, _ := filepath.Glob(creds + ".join-undo-*"); len(m) != 0 {
			t.Errorf("capture name left: %v", m)
		}
	})
	t.Run("no backup, a newcomer arrives after a foreign capture", func(t *testing.T) {
		creds, _, wrote := joinUndoWorld(t, false)
		hookJoinCapture(t, swapFile(t, creds, "FOREIGN"), func() { os.WriteFile(creds, []byte("NEWCOMER"), 0o600) })
		left, _ := joinUndo(nil, "", creds, wrote)
		m, _ := filepath.Glob(creds + ".join-undo-*")
		if readStr(creds) != "NEWCOMER" || len(m) != 1 || readStr(m[0]) != "FOREIGN" || len(left) != 1 || left[0] != m[0] {
			t.Errorf("creds %q captured %v left %v", readStr(creds), m, left)
		}
	})
	t.Run("--replace, replaced before the capture", func(t *testing.T) {
		creds, backup, wrote := joinUndoWorld(t, true)
		hookJoinCapture(t, swapFile(t, creds, "FOREIGN"), nil)
		left, kept := joinUndo(nil, backup, creds, wrote)
		if readStr(creds) != "FOREIGN" || len(left) != 1 || kept == "" || readStr(kept) != "OLD" {
			t.Errorf("creds %q left %v kept %q", readStr(creds), left, kept)
		}
	})
	t.Run("--replace, a newcomer arrives after the capture", func(t *testing.T) {
		creds, backup, wrote := joinUndoWorld(t, true)
		hookJoinCapture(t, nil, func() { os.WriteFile(creds, []byte("NEWCOMER"), 0o600) })
		left, kept := joinUndo(nil, backup, creds, wrote)
		if readStr(creds) != "NEWCOMER" || len(left) != 1 || kept == "" || readStr(kept) != "OLD" {
			t.Errorf("creds %q left %v kept %q", readStr(creds), left, kept)
		}
		if m, _ := filepath.Glob(creds + ".join-undo-*"); len(m) != 0 {
			t.Errorf("capture name left: %v", m)
		}
	})
}

func TestJoinUndoCaptureNormalPaths(t *testing.T) {
	t.Run("no backup removes the join's own file", func(t *testing.T) {
		creds, _, wrote := joinUndoWorld(t, false)
		left, kept := joinUndo(nil, "", creds, wrote)
		ents, _ := os.ReadDir(filepath.Dir(creds))
		if len(left) != 0 || kept != "" || len(ents) != 0 {
			t.Errorf("left %v kept %q dir %v", left, kept, ents)
		}
	})
	t.Run("--replace restores the backup", func(t *testing.T) {
		creds, backup, wrote := joinUndoWorld(t, true)
		left, kept := joinUndo(nil, backup, creds, wrote)
		ents, _ := os.ReadDir(filepath.Dir(creds))
		if len(left) != 0 || kept != "" || readStr(creds) != "OLD" || len(ents) != 1 {
			t.Errorf("left %v kept %q creds %q dir %v", left, kept, readStr(creds), ents)
		}
	})
	t.Run("--replace with the creds already gone restores the backup", func(t *testing.T) {
		creds, backup, wrote := joinUndoWorld(t, true)
		os.Remove(creds)
		left, kept := joinUndo(nil, backup, creds, wrote)
		if len(left) != 0 || kept != "" || readStr(creds) != "OLD" {
			t.Errorf("left %v kept %q creds %q", left, kept, readStr(creds))
		}
	})
}
