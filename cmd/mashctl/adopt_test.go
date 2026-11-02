package main

import (
	"bytes"
	"crypto/sha256"
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
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/bootstrap"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/natsauth"
)

// adopter is a second, empty key dir and config dir: the machine that adopts the server of an applyEnv.
type adopter struct {
	t      *testing.T
	src    *applyEnv
	root   string
	env    map[string]string
	opSeed string // 0600 seed files extracted from the first key dir
	acSeed string
	sys    string
}

var anySeed = regexp.MustCompile(`\bS[OAU][A-Z2-7]{50,}\b`)

func newAdopter(t *testing.T, src *applyEnv) *adopter {
	t.Helper()
	root := t.TempDir()
	a := &adopter{t: t, src: src, root: root, env: map[string]string{
		"HOME": root, "MASH_CONFIG_DIR": filepath.Join(root, "cfg"), "MASH_KEYS_DIR": filepath.Join(root, "keys"),
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
	a.sys = copyFile(filepath.Join(src.pl.Dir, bootstrap.SysCreds), "sys.creds")
	return a
}

func (a *adopter) run(args ...string) (string, string, int) {
	a.t.Helper()
	var out, errb bytes.Buffer
	code := run(args, func(k string) string { return a.env[k] }, &out, &errb)
	if anySeed.MatchString(out.String() + errb.String()) {
		a.t.Fatalf("a seed appears in the output:\n%s\n%s", out.String(), errb.String())
	}
	return out.String(), errb.String(), code
}

func (a *adopter) adopt(extra ...string) (string, string, int) {
	args := []string{"adopt", "--server", a.src.srv.ClientURL(), "--owner", "alice", "--account", "MASH",
		"--operator-key", a.opSeed, "--account-key", a.acSeed, "--sys-creds", a.sys}
	return a.run(append(args, extra...)...)
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

const oneZone = `default: deny
endpoints:
  x: {labels: {zone: a}}
rules:
  - {name: a-to-a, action: allow, from: {zone: a}, to: {zone: a}}
`

func TestAdoptDryRunThenRealRunThenApply(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	a := newAdopter(t, e)

	before := tree(t, a.root)
	out, errb, code := a.adopt("--mint-admin", "--dry-run")
	if code != 0 || errb != "" {
		t.Fatalf("dry run: %d\n%s\n%s", code, out, errb)
	}
	for _, want := range []string{"All checks passed", "nothing is written", "would import the operator key", "would import the account key", "would copy the system creds", "would mint a mashadmin", "would write", "changes nothing on the server", "standalone", "jetstream", "`mashctl policy apply` is the first to use it"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
	sameTree(t, before, tree(t, a.root))

	out, errb, code = a.adopt("--mint-admin")
	if code != 0 || errb != "" {
		t.Fatalf("adopt: %d\n%s\n%s", code, out, errb)
	}
	keyDir := a.env["MASH_KEYS_DIR"]
	if got, want := mustPub(t, filepath.Join(keyDir, "operator.nk")), mustPub(t, filepath.Join(e.keyDir, "operator.nk")); got != want {
		t.Fatalf("operator key differs: %s %s", got, want)
	}
	if got, want := mustPub(t, filepath.Join(keyDir, "account.nk")), mustPub(t, filepath.Join(e.keyDir, "account.nk")); got != want {
		t.Fatalf("account key differs: %s %s", got, want)
	}
	srvDir := filepath.Join(a.env["MASH_CONFIG_DIR"], "server")
	for _, p := range []string{filepath.Join(keyDir, "operator.nk"), filepath.Join(keyDir, "account.nk"), filepath.Join(srvDir, "sys.creds"), filepath.Join(srvDir, "mashadmin.creds"), filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json"), filepath.Join(a.env["MASH_CONFIG_DIR"], "policy.yaml")} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
	cj, _ := os.ReadFile(filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json"))
	for _, want := range []string{e.srv.ClientURL(), `"owner": "alice"`, filepath.Join(srvDir, "mashadmin.creds")} {
		if !strings.Contains(string(cj), want) {
			t.Errorf("config.json lacks %q:\n%s", want, cj)
		}
	}
	pol, _ := os.ReadFile(filepath.Join(a.env["MASH_CONFIG_DIR"], "policy.yaml"))
	if string(pol) != bootstrap.OpenPolicy {
		t.Errorf("policy.yaml: %q", pol)
	}
	if sys, _ := os.ReadFile(a.sys); !bytes.Equal(sys, mustRead(t, filepath.Join(srvDir, "sys.creds"))) {
		t.Error("sys.creds is not a copy")
	}

	// The minted admin creds connect and can run the bucket step.
	nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(filepath.Join(srvDir, "mashadmin.creds")))
	if err != nil {
		t.Fatalf("mashadmin creds do not connect: %v", err)
	}
	nc.Close()
	out, errb, code = a.run("init")
	if code != 0 {
		t.Fatalf("mashctl init from the adopted config: %d\n%s\n%s", code, out, errb)
	}

	// A second run is idempotent: nothing changes.
	snap := tree(t, a.root)
	out, errb, code = a.adopt("--mint-admin")
	if code != 0 || errb != "" {
		t.Fatalf("second adopt: %d\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "already stored") || !strings.Contains(out, "not minted again") {
		t.Errorf("second run output:\n%s", out)
	}
	sameTree(t, snap, tree(t, a.root))

	// policy apply works from the adopted key dir against the same server.
	pf := filepath.Join(a.root, "one.yaml")
	if err := os.WriteFile(pf, []byte(oneZone), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errb, code = a.run("policy", "apply", "--policy", pf)
	if code != 0 {
		t.Fatalf("policy apply after adopt: %d\n%s\n%s", code, out, errb)
	}
	if !strings.Contains(out, "zone a: new") {
		t.Errorf("apply output:\n%s", out)
	}
	if n := len(scopedSigners(e.claims())); n != 1 {
		t.Fatalf("want 1 scoped signer after apply, got %d", n)
	}
}

func TestAdoptAdminCredsFileIsRecordedNotCopied(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	a := newAdopter(t, e)
	ac := filepath.Join(a.root, "in", "admin.creds")
	if err := os.WriteFile(ac, mustRead(t, e.pl.AdminCredsPath()), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, errb, code := a.adopt("--admin-creds", ac); code != 0 {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	cj := mustRead(t, filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json"))
	if !strings.Contains(string(cj), ac) {
		t.Errorf("config.json lacks the admin creds path:\n%s", cj)
	}
	if _, err := os.Stat(filepath.Join(a.env["MASH_CONFIG_DIR"], "server", "mashadmin.creds")); err == nil {
		t.Error("an admin creds file was written although one was given")
	}
	// An existing config.json is not touched; the settings are printed.
	cfgPath := filepath.Join(a.env["MASH_CONFIG_DIR"], "config.json")
	if err := os.WriteFile(cfgPath, []byte("{\"owner\": \"alice\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, code := a.adopt("--admin-creds", ac)
	if code != 0 || !strings.Contains(out, "exists and is left as it is") || !strings.Contains(out, e.srv.ClientURL()) {
		t.Errorf("existing config: %d\n%s", code, out)
	}
	if string(mustRead(t, cfgPath)) != "{\"owner\": \"alice\"}\n" {
		t.Error("config.json was changed")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// refuses runs adopt and checks it exits with want, names every substring in msg, and leaves the tree as it was.
func (a *adopter) refuses(want int, msg []string, args ...string) {
	a.t.Helper()
	before := tree(a.t, a.root)
	var out, errb string
	var code int
	if len(args) > 0 && args[0] == "adopt" {
		out, errb, code = a.run(args...)
	} else {
		out, errb, code = a.adopt(args...)
	}
	if code != want {
		a.t.Fatalf("exit %d, want %d\n%s\n%s", code, want, out, errb)
	}
	for _, m := range msg {
		if !strings.Contains(errb, m) {
			a.t.Errorf("stderr lacks %q:\n%s", m, errb)
		}
	}
	sameTree(a.t, before, tree(a.t, a.root))
}

func TestAdoptRefusals(t *testing.T) {
	lookupWait = time.Second
	t.Cleanup(func() { lookupWait = 5 * time.Second })
	e := newApplyEnv(t, twoZones)
	a := newAdopter(t, e)
	in := filepath.Join(a.root, "in")

	t.Run("missing flags", func(t *testing.T) {
		a.t = t
		a.refuses(2, []string{"--operator-key", "--account-key", "--sys-creds", "cannot create authority"}, "adopt", "--server", e.srv.ClientURL(), "--owner", "alice")
		a.refuses(2, []string{"--server"}, "adopt", "--operator-key", a.opSeed)
		a.refuses(2, []string{"alternatives"}, "--admin-creds", a.sys, "--mint-admin")
	})
	t.Run("wrong seed prefix", func(t *testing.T) {
		a.t = t
		a.refuses(1, []string{"--operator-key", "operator seed"}, "--operator-key", a.acSeed)
		a.refuses(1, []string{"--account-key", "account"}, "--account-key", a.opSeed)
		bad := filepath.Join(in, "junk.seed")
		os.WriteFile(bad, []byte("not a seed\n"), 0o600)
		a.refuses(1, []string{"--account-key"}, "--account-key", bad)
	})
	t.Run("seed file mode", func(t *testing.T) {
		a.t = t
		open := filepath.Join(in, "open.seed")
		os.WriteFile(open, mustRead(t, a.acSeed), 0o644)
		a.refuses(1, []string{"mode 0644", "chmod 600"}, "--account-key", open)
	})
	t.Run("account not on the resolver", func(t *testing.T) {
		a.t = t
		kp, _ := nkeys.CreateAccount()
		seed, _ := kp.Seed()
		other := filepath.Join(in, "other.seed")
		os.WriteFile(other, append(seed, '\n'), 0o600)
		a.refuses(1, []string{"not on the resolver", "cannot create one"}, "--account-key", other)
	})
	t.Run("system creds that cannot connect", func(t *testing.T) {
		a.t = t
		kp, _ := nkeys.CreateAccount()
		raw, err := bootstrap.MintAdminCreds(kp)
		if err != nil {
			t.Fatal(err)
		}
		bad := filepath.Join(in, "stranger.creds")
		os.WriteFile(bad, raw, 0o600)
		a.refuses(1, []string{"with the system creds"}, "--sys-creds", bad)
	})
	t.Run("creds of another account are not system creds", func(t *testing.T) {
		a.t = t
		ac := filepath.Join(in, "acct.creds")
		os.WriteFile(ac, mustRead(t, e.pl.AdminCredsPath()), 0o600)
		a.refuses(1, []string{"does not look like an operator-mode server with a full resolver"}, "--sys-creds", ac)
	})
	t.Run("account name mismatch", func(t *testing.T) {
		a.t = t
		a.refuses(1, []string{`named "MASH"`, "--account MASH"}, "--account", "other")
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
		a.refuses(1, []string{"already holds a different operator key"})
		os.Remove(filepath.Join(a.env["MASH_KEYS_DIR"], "operator.nk"))
		if _, _, err := ks.InitAccount(); err != nil {
			t.Fatal(err)
		}
		a.refuses(1, []string{"already holds a different account key"})
	})
	t.Run("different sys.creds already in place", func(t *testing.T) {
		a.t = t
		os.Remove(filepath.Join(a.env["MASH_KEYS_DIR"], "account.nk"))
		sd := filepath.Join(a.env["MASH_CONFIG_DIR"], "server")
		os.MkdirAll(sd, 0o700)
		os.WriteFile(filepath.Join(sd, "sys.creds"), []byte("other"), 0o600)
		a.refuses(1, []string{"different system creds"})
	})
}

func TestAdoptRefusesAServerWithoutOperatorMode(t *testing.T) {
	e := newApplyEnv(t, twoZones)
	a := newAdopter(t, e)
	srv, err := server.NewServer(&server.Options{Port: -1, Host: "127.0.0.1", NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("plain server did not start")
	}
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	before := tree(t, a.root)
	out, errb, code := a.run("adopt", "--server", srv.ClientURL(), "--owner", "alice", "--operator-key", a.opSeed, "--account-key", a.acSeed, "--sys-creds", a.sys)
	if code != 1 || !strings.Contains(errb, "does not look like an operator-mode server with a full resolver") {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	sameTree(t, before, tree(t, a.root))
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
		"wrong name":       {mk(func(c *jwt.AccountClaims) {}), "named"},
	} {
		acct := "MASH"
		if name == "wrong name" {
			acct = "x"
		}
		if _, err := verifyAccount(tc.c, pub, acct, now); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if w, err := verifyAccount(mk(func(c *jwt.AccountClaims) { c.Name = "" }), pub, "x", now); err != nil || len(w) != 1 {
		t.Errorf("unnamed: %v %v", w, err)
	}
	if _, err := verifyAccount(mk(func(c *jwt.AccountClaims) {}), pub, "MASH", now); err != nil {
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
