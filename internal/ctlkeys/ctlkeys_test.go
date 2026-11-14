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
		dir: 0o700, filepath.Join(dir, "account.nk"): 0o600,
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

func TestConcurrentInitAccount(t *testing.T) {
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
			pub, _, err := a.InitAccount()
			if err != nil {
				t.Error(err)
				return
			}
			pubs[i] = pub
		}()
	}
	wg.Wait()
	for _, p := range pubs {
		if p == "" || p != pubs[0] {
			t.Fatalf("keys differ: %v", pubs)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(left) != 0 {
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
	if got := mode(t, filepath.Join(dir, "account.nk")); got.Perm() != 0o600 {
		t.Fatalf("account key mode %v", got)
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
}

// TestStoreHasNoZoneKeys: ADR 0091 removed the per-zone scoped signing keys, and a zones directory an older release
// left is neither created, read nor touched.
func TestStoreHasNoZoneKeys(t *testing.T) {
	a, dir := newStore(t)
	if _, _, err := a.InitAccount(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "zones")); !os.IsNotExist(err) {
		t.Fatalf("a zones directory was created: %v", err)
	}
	old := filepath.Join(dir, "zones")
	if err := os.Mkdir(old, 0o755); err != nil { // loose on purpose: it is not checked
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "prod.nk"), []byte("left by an older release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Account(); err != nil {
		t.Fatalf("an old zones directory broke the store: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(old, "prod.nk")); string(b) != "left by an older release\n" {
		t.Fatal("the old zone key file was changed")
	}
}
