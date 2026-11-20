package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/natsfixture"
)

// initer is a second, empty key dir and config dir: the machine that sets mash up on the server of an applyEnv, from a
// setup file.
type initer struct {
	t      *testing.T
	src    *applyEnv
	root   string
	env    map[string]string
	opSeed string // 0600 seed files extracted from the first key dir
	acSeed string
	sys    string
	admin  string
	vals   map[string]any // the keys of the setup file; a nil value leaves the key out
}

var anySeed = regexp.MustCompile(`\bS[OAU][A-Z2-7]{50,}\b`)

func newIniter(t *testing.T, src *applyEnv) *initer {
	t.Helper()
	root := t.TempDir()
	a := &initer{t: t, src: src, root: root, env: map[string]string{
		"HOME": root, "MASH_INSECURE_PLAINTEXT": "1", "MASH_CONFIG_DIR": filepath.Join(root, "cfg"), "MASH_KEYS_DIR": filepath.Join(root, "keys"),
	}}
	in := filepath.Join(root, "in")
	if err := os.Mkdir(in, 0o700); err != nil {
		t.Fatal(err)
	}
	copyFile := func(from, name string) string {
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(in, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a.opSeed = copyFile(filepath.Join(src.keyDir, "operator.nk"), "op.seed")
	a.acSeed = copyFile(filepath.Join(src.keyDir, "account.nk"), "acct.seed")
	a.sys = copyFile(filepath.Join(src.pl.Dir, natsfixture.SysCreds), "sys.creds")
	a.admin = copyFile(src.pl.AdminCredsPath(), "admin.creds")
	a.vals = map[string]any{
		"servers": []string{src.srv.ClientURL()}, "owner": "alice", "admin_creds": a.admin, "system_creds": a.sys,
		"operator_key": a.opSeed, "account_key": a.acSeed,
	}
	return a
}

// setupPath writes the setup file (into the input folder) and returns its path.
func (a *initer) setupPath() string {
	a.t.Helper()
	var b strings.Builder
	keys := make([]string, 0, len(a.vals))
	for k := range a.vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := a.vals[k].(type) {
		case nil:
		case []string:
			fmt.Fprintf(&b, "%s:\n", k)
			for _, s := range v {
				fmt.Fprintf(&b, "  - %q\n", s)
			}
		default:
			fmt.Fprintf(&b, "%s: %q\n", k, v)
		}
	}
	p := filepath.Join(a.root, "in", "setup.yaml")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		a.t.Fatal(err)
	}
	return p
}

func (a *initer) run(args ...string) (string, string, int) {
	a.t.Helper()
	var out, errb bytes.Buffer
	code := runEnv(args, func(k string) string { return a.env[k] }, &out, &errb)
	if anySeed.MatchString(out.String() + errb.String()) {
		a.t.Fatalf("a seed appears in the output:\n%s\n%s", out.String(), errb.String())
	}
	return out.String(), errb.String(), code
}

// init runs `mashctl init --config` with the setup file the values describe.
func (a *initer) init(extra ...string) (string, string, int) {
	a.t.Helper()
	return a.run(append([]string{"init", "--config", a.setupPath()}, extra...)...)
}

// tree lists every file under root (except the input files) with its mode and content hash.
func tree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "in" || strings.HasPrefix(rel, "in"+string(filepath.Separator)) {
			return nil
		}
		fi, _ := d.Info()
		line := rel + " " + fi.Mode().String()
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			line += fmt.Sprintf(" %x", sha256.Sum256(b))
		}
		out = append(out, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func sameTree(t *testing.T, a, b []string) {
	t.Helper()
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Fatalf("directory trees differ:\n%s\n---\n%s", strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
}

func mustPub(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := nkeys.FromSeed(bytes.TrimSpace(b))
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return pub
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVerifyAccount(t *testing.T) {
	kp, _ := nkeys.CreateAccount()
	pub, _ := kp.PublicKey()
	now := time.Now().Unix()
	mk := func(f func(c *jwt.AccountClaims)) *jwt.AccountClaims {
		c := jwt.NewAccountClaims(pub)
		c.Name = "MASH"
		f(c)
		return c
	}
	other, _ := nkeys.CreateAccount()
	opub, _ := other.PublicKey()
	for name, tc := range map[string]struct {
		c    *jwt.AccountClaims
		want string
	}{
		"subject mismatch": {mk(func(c *jwt.AccountClaims) { c.Subject = opub }), "subject is"},
		"expired":          {mk(func(c *jwt.AccountClaims) { c.Expires = now - 10 }), "expired"},
		"not yet valid":    {mk(func(c *jwt.AccountClaims) { c.NotBefore = now + 1000 }), "not valid before"},
	} {
		if _, err := verifyAccount(tc.c, pub, now); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := verifyAccount(mk(func(c *jwt.AccountClaims) {}), pub, now); err != nil {
		t.Errorf("good account: %v", err)
	}
}

func TestJSLimits(t *testing.T) {
	c := jwt.NewAccountClaims("AAAA")
	if _, _, err := jsLimits(c); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("no storage: %v", err)
	}
	c.Limits.JetStreamLimits = jwt.JetStreamLimits{DiskStorage: -1, Streams: -1, Consumer: -1}
	if _, _, err := jsLimits(c); err != nil {
		t.Errorf("unlimited: %v", err)
	}
	c = jwt.NewAccountClaims("AAAA")
	c.Limits.JetStreamTieredLimits = map[string]jwt.JetStreamLimits{"R3": {DiskStorage: 1 << 30}}
	if _, _, err := jsLimits(c); err == nil || !strings.Contains(err.Error(), "R1") {
		t.Errorf("tiers without R1: %v", err)
	}
}

// strictEnv is a server whose operator has strict signing key usage and whose account has one signing key. In strict
// mode the operator identity key may not sign accounts, so the operator JWT also declares an operator signing key,
// and the system and MASH account JWTs are re-signed by it (the MASH one also gains the account signing key). The
// operator.jwt and the preloaded JWTs in server.conf are rewritten on disk (the server config is natsfixture's own)
// before the server starts. The system user in sys.creds is re-minted under a system account signing key. It returns the env, the account signing key, and the operator signing key, whose seed
// is what adopt must be given as --operator-key.
func strictEnv(t *testing.T) (*applyEnv, nkeys.KeyPair, nkeys.KeyPair) {
	t.Helper()
	sk, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	skPub, _ := sk.PublicKey()
	osk, err := nkeys.CreateOperator()
	if err != nil {
		t.Fatal(err)
	}
	oskPub, _ := osk.PublicKey()
	ssk, err := nkeys.CreateAccount() // a signing key of the system account: its identity key cannot sign users either
	if err != nil {
		t.Fatal(err)
	}
	sskPub, _ := ssk.PublicKey()
	e := newApplyEnvTweak(t, func(e *applyEnv) {
		opKP, _, err := e.ks.Operator()
		if err != nil {
			t.Fatal(err)
		}
		oc, err := jwt.DecodeOperatorClaims(strings.TrimSpace(string(mustRead(t, filepath.Join(e.pl.Dir, natsfixture.OperatorJWT)))))
		if err != nil {
			t.Fatal(err)
		}
		oc.StrictSigningKeyUsage = true
		oc.SigningKeys.Add(oskPub)
		otok, err := oc.Encode(opKP)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(e.pl.Dir, natsfixture.OperatorJWT), []byte(otok+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		conf := e.pl.ConfPath()
		cb := string(mustRead(t, conf))
		resign := func(pub, addKey string) {
			m := regexp.MustCompile(`(?m)^  ` + pub + `: "([^"]+)"$`).FindStringSubmatch(cb)
			if m == nil {
				t.Fatalf("JWT of %s not found in server.conf", pub)
			}
			ac, err := jwt.DecodeAccountClaims(m[1])
			if err != nil {
				t.Fatal(err)
			}
			if addKey != "" {
				ac.SigningKeys.Add(addKey)
			}
			tok, err := ac.Encode(osk)
			if err != nil {
				t.Fatal(err)
			}
			cb = strings.Replace(cb, m[1], tok, 1)
		}
		resign(e.pl.System, sskPub)
		resign(e.acctPub, skPub)
		signingKeyCreds(t, e.pl.System, ssk, filepath.Join(e.pl.Dir, natsfixture.SysCreds))
		if err := os.WriteFile(conf, []byte(cb), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	return e, sk, osk
}

// signingKeyCreds is a user of the account signed by its signing key sk.
func signingKeyCreds(t *testing.T, acctPub string, sk nkeys.KeyPair, path string) {
	t.Helper()
	ukp, _ := nkeys.CreateUser()
	pub, _ := ukp.PublicKey()
	seed, _ := ukp.Seed()
	uc := jwt.NewUserClaims(pub)
	uc.Name = "viasigner"
	uc.IssuerAccount = acctPub
	tok, err := uc.Encode(sk)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.FormatUserConfig(tok, seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInitDryRunThenRealRunThenAgain(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	url := e.srv.ClientURL()

	before := tree(t, a.root)
	out, errb, code := a.init("--dry-run")
	if want := "Would initialize mash on " + url + " for owner alice\nNothing was changed.\n"; code != 0 || errb != "" || out != want {
		t.Fatalf("dry run: %d\n%q\n%q", code, out, errb)
	}
	sameTree(t, before, tree(t, a.root))

	out, errb, code = a.init()
	if want := "Initialized mash on " + url + " for owner alice\n"; code != 0 || errb != "" || out != want {
		t.Fatalf("init: %d\n%q\n%q", code, out, errb)
	}
	keyDir := a.env["MASH_KEYS_DIR"]
	if got, want := mustPub(t, filepath.Join(keyDir, "operator.nk")), mustPub(t, filepath.Join(e.keyDir, "operator.nk")); got != want {
		t.Fatalf("operator key differs: %s %s", got, want)
	}
	if got, want := mustPub(t, filepath.Join(keyDir, "account.nk")), mustPub(t, filepath.Join(e.keyDir, "account.nk")); got != want {
		t.Fatalf("account key differs: %s %s", got, want)
	}
	cfgDir := a.env["MASH_CONFIG_DIR"]
	srvDir := filepath.Join(cfgDir, "server")
	for _, p := range []string{filepath.Join(keyDir, "operator.nk"), filepath.Join(keyDir, "account.nk"), filepath.Join(srvDir, "sys.creds"), filepath.Join(cfgDir, "config.json")} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
	var cfg struct {
		Servers     []string `json:"servers"`
		Owner       string   `json:"owner"`
		AdminCreds  string   `json:"admin_creds"`
		SystemCreds string   `json:"system_creds"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(cfgDir, "config.json")), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0] != url || cfg.Owner != "alice" || cfg.AdminCreds != a.admin || cfg.SystemCreds != filepath.Join(srvDir, "sys.creds") {
		t.Errorf("config.json: %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(cfgDir, "policy.yaml")); err == nil {
		t.Error("init wrote a policy file: the policy file is gone")
	}
	if _, err := os.Stat(filepath.Join(srvDir, "mashadmin.creds")); err == nil {
		t.Error("init wrote an admin creds file: it records the path of the one it was given")
	}
	if sys, _ := os.ReadFile(a.sys); !bytes.Equal(sys, mustRead(t, filepath.Join(srvDir, "sys.creds"))) {
		t.Error("sys.creds is not a copy")
	}

	// The storage is there and the config works for the other verbs.
	if out, errb, code = a.run("endpoint", "list"); code != 0 {
		t.Fatalf("endpoint list after init: %d\n%s\n%s", code, out, errb)
	}
	if n := len(scopedSigners(e.claims())); n != 0 {
		t.Fatalf("want no scoped signer after init, got %d", n)
	}

	// A second run, and a dry run after it, change nothing.
	snap := tree(t, a.root)
	for _, extra := range [][]string{nil, {"--dry-run"}} {
		out, errb, code = a.init(extra...)
		if want := "Already initialized on " + url + "; nothing changed\n"; code != 0 || errb != "" || out != want {
			t.Fatalf("again %v: %d\n%q\n%q", extra, code, out, errb)
		}
		sameTree(t, snap, tree(t, a.root))
	}
}

func TestInitJSONAndVerbose(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	url := e.srv.ClientURL()
	out, errb, code := a.init("--dry-run", "--json")
	var r struct {
		Server      []string `json:"server"`
		Owner       string   `json:"owner"`
		Changed     bool     `json:"changed"`
		DryRun      bool     `json:"dry_run"`
		WouldChange bool     `json:"would_change"`
	}
	if err := json.Unmarshal([]byte(out), &r); code != 0 || err != nil || errb != "" || r.Changed || !r.DryRun || !r.WouldChange || r.Owner != "alice" || len(r.Server) != 1 || r.Server[0] != url {
		t.Fatalf("dry run json: %d %v %q %q", code, err, out, errb)
	}
	out, errb, code = a.init("--json")
	r.DryRun, r.WouldChange = false, false
	if err := json.Unmarshal([]byte(out), &r); code != 0 || err != nil || errb != "" || !r.Changed || r.Owner != "alice" {
		t.Fatalf("json: %d %v %q %q", code, err, out, errb)
	}
	out, _, code = a.init("--json")
	if err := json.Unmarshal([]byte(out), &r); code != 0 || err != nil || r.Changed {
		t.Fatalf("json again: %d %v %q", code, err, out)
	}

	// --verbose follows the line with label: value lines for what was written, paths only.
	b := newIniter(t, e)
	out, errb, code = b.init("--verbose")
	if code != 0 || errb != "" || !strings.HasPrefix(out, "Initialized mash on "+url+" for owner alice\n") {
		t.Fatalf("verbose: %d %q %q", code, out, errb)
	}
	cfgDir, keyDir := b.env["MASH_CONFIG_DIR"], b.env["MASH_KEYS_DIR"]
	for _, want := range []string{
		"operator key: " + filepath.Join(keyDir, "operator.nk"), "account key: " + filepath.Join(keyDir, "account.nk"),
		"system creds: " + filepath.Join(cfgDir, "server", "sys.creds"), "config: " + filepath.Join(cfgDir, "config.json"),
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("verbose output lacks %q:\n%s", want, out)
		}
	}
}

// A relative path in the setup file is resolved against the folder of the file, not the working directory.
func TestInitRelativePathsAreRelativeToTheSetupFile(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	rel := func(p string) string { return filepath.Base(p) }
	a.vals["admin_creds"], a.vals["system_creds"] = rel(a.admin), rel(a.sys)
	a.vals["operator_key"], a.vals["account_key"] = rel(a.opSeed), rel(a.acSeed)
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	var cfg struct {
		AdminCreds string `json:"admin_creds"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json")), &cfg); err != nil || cfg.AdminCreds != a.admin {
		t.Errorf("admin_creds %q, want %q (%v)", cfg.AdminCreds, a.admin, err)
	}
}

// config.json that already holds the same settings is left as it is; one that holds others is refused, not replaced.
func TestInitExistingConfigJSON(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	cfgPath := filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	other := "{\"owner\": \"carol\"}\n"
	if err := os.WriteFile(cfgPath, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	before := tree(t, a.root)
	_, errb, code := a.init()
	if code != 1 || errb != "error: ~/cfg was set up with a different server; nothing was changed\nhint: mashctl init --config <file> --config-dir <a new folder>\n" {
		t.Fatalf("%d %q", code, errb)
	}
	sameTree(t, before, tree(t, a.root))
}

// The setup file: a missing key, an unknown key, a value that is not what the key takes.
func TestInitSetupFileErrors(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	for _, k := range []string{"servers", "owner", "admin_creds", "system_creds", "operator_key", "account_key"} {
		saved := a.vals[k]
		a.vals[k] = nil
		before := tree(t, a.root)
		path := a.setupPath()
		out, errb, code := a.run("init", "--config", path)
		if want := "error: " + path + ": missing key " + k + "\n"; code != 1 || out != "" || errb != want {
			t.Errorf("missing %s: %d %q %q", k, code, out, errb)
		}
		sameTree(t, before, tree(t, a.root))
		a.vals[k] = saved
	}
	a.vals["frobnicate"] = "x"
	path := a.setupPath()
	if out, errb, code := a.run("init", "--config", path); code != 1 || out != "" || errb != "error: "+path+": unknown key frobnicate\n" {
		t.Errorf("unknown key: %d %q %q", code, out, errb)
	}
	delete(a.vals, "frobnicate")
	a.vals["owner"] = "Not A Name"
	if _, errb, code := a.init(); code != 1 || !strings.Contains(errb, "owner is not a valid name") {
		t.Errorf("bad owner: %d %q", code, errb)
	}
	a.vals["owner"] = "alice"
	if _, errb, code := a.run("init", "--config", filepath.Join(a.root, "in", "nope.yaml")); code != 1 || !strings.HasPrefix(errb, "error: cannot read ") {
		t.Errorf("no file: %d %q", code, errb)
	}
	notYAML := filepath.Join(a.root, "in", "junk.yaml")
	os.WriteFile(notYAML, []byte("servers: [\n"), 0o600)
	if _, errb, code := a.run("init", "--config", notYAML); code != 1 || !strings.HasPrefix(errb, "error: "+notYAML+": not a YAML file") {
		t.Errorf("not yaml: %d %q", code, errb)
	}
}

// init needs --config, takes no other option and no argument; each is a command line error with the hint of init's help.
func TestInitCommandLineErrors(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"init"}, "error: mashctl init needs --config <file>\nhint: mashctl init --help\n"},
		{[]string{"init", "--local"}, "error: unknown option --local\nhint: mashctl init --help\n"},
		{[]string{"init", "--start", "--config", "x"}, "error: unknown option --start\nhint: mashctl init --help\n"},
		{[]string{"init", "--config", "x", "extra"}, "error: too many arguments\nhint: mashctl init --help\n"},
		{[]string{"init", "--config"}, "error: --config needs a value\nhint: mashctl init --help\n"},
	}
	for _, c := range cases {
		before := tree(t, a.root)
		out, errb, code := a.run(c.args...)
		if code != 2 || out != "" || errb != c.want {
			t.Errorf("%v: %d %q %q", c.args, code, out, errb)
		}
		sameTree(t, before, tree(t, a.root))
	}
}

// refuses runs init with the setup values changed by vals (a nil value leaves the key out) and checks it exits with
// want, names every substring in msg on stderr, and leaves the tree as it was.
func (a *initer) refuses(want int, msg []string, vals map[string]any) {
	a.t.Helper()
	saved := map[string]any{}
	for k, v := range vals {
		saved[k] = a.vals[k]
		a.vals[k] = v
	}
	defer func() {
		for k, v := range saved {
			a.vals[k] = v
		}
	}()
	before := tree(a.t, a.root)
	out, errb, code := a.init()
	if code != want {
		a.t.Fatalf("exit %d, want %d\n%s\n%s", code, want, out, errb)
	}
	if !strings.HasPrefix(errb, "error: ") {
		a.t.Errorf("stderr does not start with error: %q", errb)
	}
	for _, m := range msg {
		if !strings.Contains(errb, m) {
			a.t.Errorf("stderr lacks %q:\n%s", m, errb)
		}
	}
	sameTree(a.t, before, tree(a.t, a.root))
}

func TestInitRefusals(t *testing.T) {
	lookupWait = time.Second
	t.Cleanup(func() { lookupWait = 5 * time.Second })
	e := newApplyEnv(t)
	a := newIniter(t, e)
	in := filepath.Join(a.root, "in")

	t.Run("credentials in the URL", func(t *testing.T) {
		a.t = t
		for _, u := range []string{"tls://bob:hunter2@127.0.0.1:4222", "bob:hunter2@127.0.0.1:4222"} {
			a.refuses(1, []string{"servers must not hold credentials"}, map[string]any{"servers": []string{u}})
		}
	})
	t.Run("wrong seed prefix", func(t *testing.T) {
		a.t = t
		a.refuses(1, []string{"operator_key", "operator seed"}, map[string]any{"operator_key": a.acSeed})
		a.refuses(1, []string{"account_key", "account"}, map[string]any{"account_key": a.opSeed})
		bad := filepath.Join(in, "junk.seed")
		os.WriteFile(bad, []byte("not a seed\n"), 0o600)
		a.refuses(1, []string{"account_key"}, map[string]any{"account_key": bad})
	})
	t.Run("seed file mode", func(t *testing.T) {
		a.t = t
		open := filepath.Join(in, "open.seed")
		os.WriteFile(open, mustRead(t, a.acSeed), 0o644)
		a.refuses(1, []string{"mode 0644", "chmod 600"}, map[string]any{"account_key": open})
	})
	t.Run("creds file mode", func(t *testing.T) {
		a.t = t
		open := filepath.Join(in, "open.creds")
		os.WriteFile(open, mustRead(t, a.sys), 0o644)
		a.refuses(1, []string{"system_creds", "mode 0644"}, map[string]any{"system_creds": open})
		a.refuses(1, []string{"admin_creds", "mode 0644"}, map[string]any{"admin_creds": open})
	})
	t.Run("seed file is a symlink", func(t *testing.T) {
		a.t = t
		link := filepath.Join(in, "link.seed")
		if err := os.Symlink(a.acSeed, link); err != nil {
			t.Fatal(err)
		}
		a.refuses(1, []string{"account_key"}, map[string]any{"account_key": link})
	})
	t.Run("account not on the resolver", func(t *testing.T) {
		a.t = t
		kp, _ := nkeys.CreateAccount()
		seed, _ := kp.Seed()
		other := filepath.Join(in, "other.seed")
		os.WriteFile(other, append(seed, '\n'), 0o600)
		a.refuses(1, []string{"not on the resolver", "cannot create one"}, map[string]any{"account_key": other})
	})
	t.Run("system creds that cannot connect", func(t *testing.T) {
		a.t = t
		kp, _ := nkeys.CreateAccount()
		raw, err := natsfixture.MintAdminCreds(kp)
		if err != nil {
			t.Fatal(err)
		}
		bad := filepath.Join(in, "stranger.creds")
		os.WriteFile(bad, raw, 0o600)
		a.refuses(1, []string{"with the system creds"}, map[string]any{"system_creds": bad})
	})
	t.Run("creds of another account are not system creds", func(t *testing.T) {
		a.t = t
		ac := filepath.Join(in, "acct.creds")
		os.WriteFile(ac, mustRead(t, e.pl.AdminCredsPath()), 0o600)
		a.refuses(1, []string{"does not look like an operator-mode server with a full resolver"}, map[string]any{"system_creds": ac})
	})
	t.Run("admin creds of another account", func(t *testing.T) {
		a.t = t
		other, _ := nkeys.CreateAccount()
		otherPub, _ := other.PublicKey()
		p := filepath.Join(in, "other-admin.creds")
		signingKeyCreds(t, otherPub, other, p)
		a.refuses(1, []string{"admin_creds", "belong to another account (" + otherPub + ")"}, map[string]any{"admin_creds": p})
	})
	t.Run("admin creds the server rejects", func(t *testing.T) {
		a.t = t
		kp, _ := nkeys.CreateAccount()
		raw, err := natsfixture.MintAdminCreds(kp)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(in, "stranger-admin.creds")
		os.WriteFile(p, raw, 0o600)
		a.refuses(1, []string{"admin_creds", "belong to another account"}, map[string]any{"admin_creds": p})
	})
	t.Run("key store holds another key", func(t *testing.T) {
		a.t = t
		ks, err := ctlkeys.Open(a.env["MASH_KEYS_DIR"])
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := ks.InitOperator(); err != nil {
			t.Fatal(err)
		}
		a.refuses(1, []string{"error: ~/cfg was set up with a different operator key; nothing was changed\nhint: mashctl init --config <file> --config-dir <a new folder>\n"}, nil)
		os.Remove(filepath.Join(a.env["MASH_KEYS_DIR"], "operator.nk"))
		if _, _, err := ks.InitAccount(); err != nil {
			t.Fatal(err)
		}
		a.refuses(1, []string{"error: ~/cfg was set up with a different account key; nothing was changed\nhint: mashctl init --config <file> --config-dir <a new folder>\n"}, nil)
	})
	t.Run("different sys.creds already in place", func(t *testing.T) {
		a.t = t
		os.Remove(filepath.Join(a.env["MASH_KEYS_DIR"], "account.nk"))
		sd := filepath.Join(a.env["MASH_CONFIG_DIR"], "server")
		os.MkdirAll(sd, 0o700)
		os.WriteFile(filepath.Join(sd, "sys.creds"), []byte("other"), 0o600)
		a.refuses(1, []string{"error: ~/cfg was set up with a different system creds; nothing was changed\nhint: mashctl init --config <file> --config-dir <a new folder>\n"}, nil)
	})
}

func TestInitRefusesAServerWithoutOperatorMode(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	srv, err := server.NewServer(&server.Options{Port: -1, Host: "127.0.0.1", NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("plain server did not start")
	}
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	a.refuses(1, []string{"does not look like an operator-mode server with a full resolver"}, map[string]any{"servers": []string{srv.ClientURL()}})
}

// A server that does not answer is `cannot reach`, with the hint of doctor, exit 1, and nothing is written.
func TestInitUnreachableServer(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	a.vals["servers"] = []string{"nats://127.0.0.1:1"}
	before := tree(t, a.root)
	out, errb, code := a.init()
	if want := "error: cannot reach nats://127.0.0.1:1\nhint: mashctl doctor\n"; code != 1 || out != "" || errb != want {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	sameTree(t, before, tree(t, a.root))
	out, errb, code = a.init("--json")
	if want := `{"error":"cannot reach nats://127.0.0.1:1","hint":"mashctl doctor"}` + "\n"; code != 1 || out != want || errb != "" {
		t.Fatalf("json: %d %q %q", code, out, errb)
	}
}

// The removed verbs and options are unknown: nothing runs.
func TestAdoptAndInitLocalAreGone(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	before := tree(t, a.root)
	out, errb, code := a.run("adopt", "--server", e.srv.ClientURL(), "--operator-key", a.opSeed)
	if code != 2 || out != "" || !strings.HasPrefix(errb, "error: unknown command \"adopt\"\n") {
		t.Errorf("adopt: %d %q %q", code, out, errb)
	}
	for _, args := range [][]string{{"init", "--local"}, {"init", "--local", "--start", "--dir", filepath.Join(a.root, "srv")}} {
		if out, errb, code = a.run(args...); code != 2 || out != "" || !strings.HasPrefix(errb, "error: unknown option --") || !strings.HasSuffix(errb, "hint: mashctl init --help\n") {
			t.Errorf("%v: %d %q %q", args, code, out, errb)
		}
	}
	sameTree(t, before, tree(t, a.root))
}

func TestInitRefusesWhenTheServerCouldNotEnableJetStream(t *testing.T) {
	e := newApplyEnvOpts(t, nil, func(o *server.Options) { o.JetStreamMaxStore = 128 << 20 })
	a := newIniter(t, e)
	before := tree(t, a.root)
	out, errb, code := a.init()
	if code == 0 {
		t.Fatalf("init succeeded:\n%s", out)
	}
	for _, want := range []string{"could not enable JetStream", "75% of the free disk", "(0.2 GiB)", "about 0.3 GiB"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errb)
		}
	}
	sameTree(t, before, tree(t, a.root))
}

// The owner is the one in the setup file, not one a global option names.
func TestInitOwnerComesFromTheSetupFile(t *testing.T) {
	e := newApplyEnv(t)
	a := newIniter(t, e)
	out, errb, code := a.run("init", "--endpoint", "carol.dev.x", "--config", a.setupPath())
	if code != 0 {
		t.Fatalf("init: %d\n%s\n%s", code, out, errb)
	}
	var cfg struct {
		Servers []string `json:"servers"`
		Owner   string   `json:"owner"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json")), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0] != e.srv.ClientURL() || cfg.Owner != "alice" {
		t.Errorf("config.json %+v, want servers [%s] and owner alice (the setup file's)", cfg, e.srv.ClientURL())
	}
}

func TestInitStrictSigningKeyUsage(t *testing.T) {
	e, sk, osk := strictEnv(t)
	a := newIniter(t, e)
	seed, _ := osk.Seed()
	if err := os.WriteFile(a.opSeed, append(seed, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	// An admin user the account identity key signed is rejected by a strict server, dry run included, and nothing is written.
	acctKP, _, err := e.ks.Account()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := natsfixture.MintAdminCreds(acctKP)
	if err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(a.root, "in", "identity-signed.creds")
	if err := os.WriteFile(dead, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	a.refuses(1, []string{"admin_creds", "cannot connect", "nothing was changed"}, map[string]any{"admin_creds": dead})

	// A user signed by the account signing key is accepted.
	sc := filepath.Join(a.root, "in", "signer.creds")
	signingKeyCreds(t, e.acctPub, sk, sc)
	a.vals["admin_creds"] = sc
	if out, errb, code := a.init(); code != 0 {
		t.Fatalf("init with a signing key user: %d\n%s\n%s", code, out, errb)
	}
}

func TestShownFolder(t *testing.T) {
	for _, c := range []struct{ dir, home, want string }{
		{"/home/a/.config/mash", "/home/a", "~/.config/mash"},
		{"/home/a", "/home/a", "~"},
		{"/srv/mash", "/home/a", "/srv/mash"},
		{"/home/ab/x", "/home/a", "/home/ab/x"},
	} {
		if got := shownFolder(c.dir, c.home); got != c.want {
			t.Errorf("shownFolder(%q, %q) = %q, want %q", c.dir, c.home, got, c.want)
		}
	}
}
