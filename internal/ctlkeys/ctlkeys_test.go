//go:build unix

package ctlkeys

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
)

func newStore(t *testing.T) (*Authority, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "keys")
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return a, dir
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestInitAccount(t *testing.T) {
	a, dir := newStore(t)
	pub, created, err := a.InitAccount()
	if err != nil || !created || !nkeys.IsValidPublicAccountKey(pub) {
		t.Fatalf("init: %q %v %v", pub, created, err)
	}
	for p, want := range map[string]os.FileMode{
		dir: 0o700, filepath.Join(dir, "zones"): 0o700, filepath.Join(dir, "account.nk"): 0o600,
	} {
		if got := mode(t, p); got != want {
			t.Errorf("%s mode %04o, want %04o", p, got, want)
		}
	}
	before, _ := os.ReadFile(filepath.Join(dir, "account.nk"))
	pub2, created2, err := a.InitAccount()
	if err != nil || created2 || pub2 != pub {
		t.Fatalf("second init: %q %v %v", pub2, created2, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "account.nk"))
	if string(before) != string(after) || !strings.HasSuffix(string(after), "\n") {
		t.Fatal("seed changed or lacks newline")
	}
	kp, pub3, err := a.Account()
	if err != nil || pub3 != pub || kp == nil {
		t.Fatalf("account: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestOpenAndAccountCreateNothing(t *testing.T) {
	a, dir := newStore(t)
	if _, _, err := a.Account(); !errors.Is(err, ErrNoAccountKey) {
		t.Fatalf("want ErrNoAccountKey, got %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir created: %v", err)
	}
	if z, err := a.Zones(); err != nil || len(z) != 0 {
		t.Fatalf("zones on empty store: %v %v", z, err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Account(); !errors.Is(err, ErrNoAccountKey) {
		t.Fatalf("want ErrNoAccountKey, got %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "account.nk")); !os.IsNotExist(err) {
		t.Fatal("account.nk created")
	}
}

func TestZoneKey(t *testing.T) {
	a, dir := newStore(t)
	if _, err := a.ZoneKey("prod", false); !errors.Is(err, ErrNoZoneKey) {
		t.Fatalf("want ErrNoZoneKey, got %v", err)
	}
	if _, err := a.ZonePublic("prod"); !errors.Is(err, ErrNoZoneKey) {
		t.Fatalf("want ErrNoZoneKey, got %v", err)
	}
	kp, err := a.ZoneKey("prod", true)
	if err != nil {
		t.Fatal(err)
	}
	p1, _ := kp.PublicKey()
	kp2, err := a.ZoneKey("prod", true)
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := kp2.PublicKey()
	pp, err := a.ZonePublic("prod")
	if err != nil || p1 != p2 || p1 != pp || !nkeys.IsValidPublicAccountKey(p1) {
		t.Fatalf("keys differ: %s %s %s %v", p1, p2, pp, err)
	}
	if got := mode(t, filepath.Join(dir, "zones", "prod.nk")); got != 0o600 {
		t.Fatalf("mode %04o", got)
	}
}

func TestBadZoneNames(t *testing.T) {
	a, dir := newStore(t)
	for _, z := range []string{"", "A", "a.b", "../x", "a/b", "*", ">", strings.Repeat("a", 33)} {
		if _, err := a.ZoneKey(z, true); err == nil {
			t.Errorf("zone %q accepted", z)
		}
		if _, err := a.ZoneKey(z, false); err == nil {
			t.Errorf("zone %q accepted (no create)", z)
		}
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("something was written: %v", err)
	}
}

func TestRefusals(t *testing.T) {
	t.Run("group-readable seed", func(t *testing.T) {
		a, dir := newStore(t)
		a.InitAccount()
		p := filepath.Join(dir, "account.nk")
		if err := os.Chmod(p, 0o640); err != nil {
			t.Fatal(err)
		}
		_, _, err := a.Account()
		if err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "chmod 600") {
			t.Fatalf("got %v", err)
		}
		if _, _, err := a.InitAccount(); err == nil {
			t.Fatal("init accepted a loose seed")
		}
	})
	t.Run("group-readable dir", func(t *testing.T) {
		a, dir := newStore(t)
		a.InitAccount()
		if err := os.Chmod(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		_, err := Open(dir)
		if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "chmod 700") {
			t.Fatalf("got %v", err)
		}
		if _, _, err := a.Account(); err == nil {
			t.Fatal("account read from a loose dir")
		}
	})
	t.Run("group-readable zones dir", func(t *testing.T) {
		a, dir := newStore(t)
		a.InitAccount()
		if err := os.Chmod(filepath.Join(dir, "zones"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := a.ZoneKey("prod", true); err == nil {
			t.Fatal("zone key made in a loose dir")
		}
	})
	t.Run("symlink seed", func(t *testing.T) {
		a, dir := newStore(t)
		a.InitAccount()
		target := filepath.Join(t.TempDir(), "real.nk")
		os.Rename(filepath.Join(dir, "account.nk"), target)
		if err := os.Symlink(target, filepath.Join(dir, "account.nk")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.Account(); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("symlink dir", func(t *testing.T) {
		real := t.TempDir()
		link := filepath.Join(t.TempDir(), "keys")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(link); err == nil {
			t.Fatal("symlinked dir accepted")
		}
	})
	t.Run("garbage seed", func(t *testing.T) {
		a, dir := newStore(t)
		a.InitAccount()
		const garbage = "SAXXXXNOTASEEDATALL"
		if err := os.WriteFile(filepath.Join(dir, "account.nk"), []byte(garbage+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := a.Account()
		if err == nil || strings.Contains(err.Error(), garbage) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("user seed", func(t *testing.T) {
		a, dir := newStore(t)
		a.InitAccount()
		u, _ := nkeys.CreateUser()
		seed, _ := u.Seed()
		if err := os.WriteFile(filepath.Join(dir, "account.nk"), append(seed, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := a.Account()
		if err == nil || strings.Contains(err.Error(), string(seed)) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestZonesSorted(t *testing.T) {
	a, dir := newStore(t)
	for _, z := range []string{"zeta", "alpha", "mid-1"} {
		if _, err := a.ZoneKey(z, true); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "zones", "ignored.txt"), nil, 0o600)
	got, err := a.Zones()
	if err != nil || strings.Join(got, ",") != "alpha,mid-1,zeta" {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestConcurrentZoneKey(t *testing.T) {
	_, dir := newStore(t)
	var wg sync.WaitGroup
	pubs := make([]string, 8)
	for i := range pubs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A separate Authority per goroutine, so only the filesystem arbitrates (the two-process case).
			a, err := Open(dir)
			if err != nil {
				t.Error(err)
				return
			}
			kp, err := a.ZoneKey("prod", true)
			if err != nil {
				t.Error(err)
				return
			}
			pubs[i], _ = kp.PublicKey()
		}()
	}
	wg.Wait()
	for _, p := range pubs {
		if p == "" || p != pubs[0] {
			t.Fatalf("keys differ: %v", pubs)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "zones", "*.tmp-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestDirFor(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		name string
		cfg  config.Config
		env  map[string]string
		want string
		err  bool
	}{
		{"absolute", config.Config{Dir: "/c"}, map[string]string{"MASH_KEYS_DIR": "/k/../k2"}, "/k2", false},
		{"relative", config.Config{Dir: "/c"}, map[string]string{"MASH_KEYS_DIR": "k"}, "", true},
		{"default", config.Config{Dir: "/c"}, nil, "/c/keys", false},
		{"neither", config.Config{}, nil, "", true},
		{"env without cfg", config.Config{}, map[string]string{"MASH_KEYS_DIR": "/k"}, "/k", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := DirFor(c.cfg, env(c.env))
			if (err != nil) != c.err || got != c.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestCreateSyncsDirectories(t *testing.T) {
	var synced []string
	old := syncDirHook
	syncDirHook = func(p string) error { synced = append(synced, p); return old(p) }
	defer func() { syncDirHook = old }()
	a, dir := newStore(t)
	if _, _, err := a.InitAccount(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ZoneKey("alpha", true); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "zones"))
	if len(ents) != 1 {
		t.Fatalf("zones dir entries: %v", ents)
	}
	if got := mode(t, filepath.Join(dir, "zones", ents[0].Name())); got.Perm() != 0o600 {
		t.Fatalf("zone key mode %v", got)
	}
	if len(synced) < 3 {
		t.Fatalf("expected directory syncs, got %v", synced)
	}
}

func TestSyncDirFailureIsAnError(t *testing.T) {
	old := syncDirHook
	syncDirHook = func(string) error { return errors.New("injected") }
	defer func() { syncDirHook = old }()
	a, _ := newStore(t)
	if _, _, err := a.InitAccount(); err == nil {
		t.Fatal("InitAccount reported success despite a failing directory sync")
	}
	if _, err := a.ZoneKey("beta", true); err == nil {
		t.Fatal("ZoneKey(create) reported success despite a failing directory sync")
	}
}
