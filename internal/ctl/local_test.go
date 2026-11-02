package ctl

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/bootstrap"
	"github.com/bogworks/mash/internal/config"
)

type localEnv struct {
	root string
	cfg  config.Config
}

func newLocalEnv(t *testing.T) *localEnv {
	t.Helper()
	root := t.TempDir()
	t.Setenv("MASH_KEYS_DIR", filepath.Join(root, "keys"))
	return &localEnv{root: root, cfg: config.Config{Dir: filepath.Join(root, "cfg")}}
}

func (e *localEnv) run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run("mashctl", e.cfg, args, &out, &errb)
	return code, out.String(), errb.String()
}

// seedsIn returns every seed text under root (key files and creds files).
func seedsIn(t *testing.T, root string) []string {
	t.Helper()
	var seeds []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, f := range strings.Fields(string(b)) {
			if _, err := nkeys.FromSeed([]byte(f)); err == nil {
				seeds = append(seeds, f)
			}
		}
		return nil
	})
	return seeds
}

func TestInitLocalWithNoConfig(t *testing.T) {
	e := newLocalEnv(t)
	code, out, errs := e.run("init", "--local", "--owner", "alice", "--port", "4333")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errs)
	}
	for _, want := range []string{"nats-server -c " + filepath.Join(e.root, "cfg", "server", "server.conf"), "mashctl init", "mashctl provision", "no TLS", "127.0.0.1:4333"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	seeds := seedsIn(t, e.root)
	if len(seeds) < 5 { // operator, system, account, mashadmin, sys
		t.Fatalf("found %d seeds, want at least 5", len(seeds))
	}
	for _, s := range seeds {
		if strings.Contains(out, s) || strings.Contains(errs, s) {
			t.Fatal("a seed was printed")
		}
	}
	c, err := os.ReadFile(filepath.Join(e.root, "cfg", "config.json"))
	if err != nil || !strings.Contains(string(c), "nats://127.0.0.1:4333") {
		t.Fatalf("config.json: %s %v", c, err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "cfg", "policy.yaml")); err != nil {
		t.Fatal(err)
	}

	code, _, errs = e.run("init", "--local", "--owner", "alice")
	if code != 1 || !strings.Contains(errs, "refusing to overwrite") {
		t.Fatalf("second run: %d %s", code, errs)
	}
}

func TestInitLocalLeavesExistingConfig(t *testing.T) {
	e := newLocalEnv(t)
	if err := os.MkdirAll(e.cfg.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(e.cfg.Dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.cfg.Owner = "carol" // the owner comes from the config when no flag is given
	code, out, errs := e.run("init", "--local")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != "{}\n" {
		t.Fatal("config.json was touched")
	}
	for _, want := range []string{"not touched", `"url": "nats://127.0.0.1:4222"`, `"owner": "carol"`, "admin_creds"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestInitLocalRefusals(t *testing.T) {
	e := newLocalEnv(t)
	cases := []struct {
		name string
		args []string
		code int
		msg  string
	}{
		{"no owner", []string{"init", "--local"}, 1, "needs an owner"},
		{"bad owner", []string{"init", "--local", "--owner", "Bad_Name"}, 1, "not a valid name"},
		{"bad port", []string{"init", "--local", "--owner", "a", "--port", "99999"}, 1, "out of range"},
		{"bad flag", []string{"init", "--local", "--bogus"}, 2, "usage: mashctl init --local"},
		{"stray argument", []string{"init", "--local", "extra"}, 2, "usage: mashctl init --local"},
	}
	for _, c := range cases {
		code, _, errs := e.run(c.args...)
		if code != c.code || !strings.Contains(errs, c.msg) {
			t.Errorf("%s: exit %d %q", c.name, code, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(e.root, "cfg")); err == nil {
		t.Error("a refused run created files")
	}
	var out, errb bytes.Buffer
	if code := Run("mashctl", config.Config{}, []string{"init", "--local", "--owner", "a"}, &out, &errb); code != 1 || !strings.Contains(errb.String(), "no server directory") {
		t.Errorf("no config dir: %d %s", code, errb.String())
	}
}

func TestInitLocalDirFlagWithoutConfigDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MASH_KEYS_DIR", filepath.Join(root, "keys"))
	var out, errb bytes.Buffer
	dir := filepath.Join(root, "srv")
	if code := Run("mashctl", config.Config{}, []string{"init", "--local", "--owner", "a", "--dir", dir}, &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "server.conf")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no config directory") {
		t.Errorf("output does not say config.json was not written:\n%s", out.String())
	}
}

func TestInitLocalStartWithoutServerBinary(t *testing.T) {
	e := newLocalEnv(t)
	t.Setenv("PATH", "")
	code, _, errs := e.run("init", "--local", "--owner", "alice", "--start")
	if code != 1 || !strings.Contains(errs, "nats-server is not on PATH") || !strings.Contains(errs, "nothing was started") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if _, err := os.Stat(filepath.Join(e.root, "cfg", "server", "server.conf")); err != nil {
		t.Fatal("files should remain:", err)
	}
}

func TestPlainInitIsUnchanged(t *testing.T) {
	e := newLocalEnv(t)
	code, _, errs := e.run("init")
	if code != 1 || !strings.Contains(errs, "no owner") {
		t.Fatalf("plain init: %d %s", code, errs)
	}
	e.cfg.Owner = "alice"
	code, _, errs = e.run("init")
	if code != 1 || !strings.Contains(errs, "MASH_URL") {
		t.Fatalf("plain init: %d %s", code, errs)
	}
}

func TestUsageListsInitLocal(t *testing.T) {
	if u := Usage("mashctl"); !strings.Contains(u, "init --local [--dir D] [--owner O] [--port P] [--start]") {
		t.Fatal(u)
	}
}

// stubStart replaces the server start and bucket init seams; it restores them on cleanup.
func stubStart(t *testing.T) {
	t.Helper()
	s, i := startLocalServer, initLocalBuckets
	t.Cleanup(func() { startLocalServer, initLocalBuckets = s, i })
	startLocalServer = func(bootstrap.Params) (int, error) { return 4242, nil }
	initLocalBuckets = func(string, config.Config, io.Writer, io.Writer) int { return 0 }
}

func TestInitLocalStartConfigElsewhere(t *testing.T) {
	stubStart(t)
	for name, mod := range map[string]func(e *localEnv){
		"config.json elsewhere": func(e *localEnv) {
			e.cfg.URL, e.cfg.Owner, e.cfg.AdminCreds = "nats://other:4222", "alice", "/x/admin.creds"
		},
		"MASH_URL override": func(e *localEnv) {
			e.cfg.URL = "nats://other:4222"
			e.cfg.Owner = "alice"
			e.cfg.AdminCreds = filepath.Join(e.root, "cfg", "server", "mashadmin.creds")
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLocalEnv(t)
			mod(e)
			code, out, errs := e.run("init", "--local", "--owner", "alice", "--start")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, errs)
			}
			for _, want := range []string{"points elsewhere", `"url": "nats://127.0.0.1:4222"`, `"owner": "alice"`, "admin_creds", "MASH_URL"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "Ready") || strings.Contains(out, "provision <name>") {
				t.Errorf("claims readiness or recommends provision:\n%s", out)
			}
		})
	}
}

func TestInitLocalStartConfigMatches(t *testing.T) {
	stubStart(t)
	e := newLocalEnv(t)
	e.cfg.URL, e.cfg.Owner = "nats://127.0.0.1:4222", "alice"
	e.cfg.AdminCreds = filepath.Join(e.root, "cfg", "server", "mashadmin.creds")
	code, out, errs := e.run("init", "--local", "--owner", "alice", "--start")
	if code != 0 || !strings.Contains(out, "Ready") || strings.Contains(out, "points elsewhere") {
		t.Fatalf("exit %d: %s\n%s", code, out, errs)
	}
}

func TestInitLocalStartNoConfigIsReady(t *testing.T) {
	stubStart(t)
	e := newLocalEnv(t)
	code, out, errs := e.run("init", "--local", "--owner", "alice", "--start")
	if code != 0 || !strings.Contains(out, "Ready") || strings.Contains(out, "points elsewhere") {
		t.Fatalf("exit %d: %s\n%s", code, out, errs)
	}
}
