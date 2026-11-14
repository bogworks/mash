//go:build unix

package ctlkeys

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// otherUser makes the process look like it runs as another uid: a file owned by a different user cannot be made
// without root.
func otherUser(t *testing.T) {
	t.Helper()
	old := geteuid
	geteuid = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { geteuid = old })
}

// A symlink to a perfectly good 0600 seed is refused by the open itself (O_NOFOLLOW), and the error names the
// symlink, not the seed.
func TestReadSeedRefusesSymlinkAtOpen(t *testing.T) {
	a, dir := newStore(t)
	a.InitAccount()
	real := filepath.Join(dir, "account.nk")
	link := filepath.Join(t.TempDir(), "link.nk")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	kp, err := readSeed(link, accountKind)
	if kp != nil || err == nil || !strings.Contains(err.Error(), "is a symlink") {
		t.Fatalf("got %v, %v", kp, err)
	}
	if kp, err := readSeed(real, accountKind); err != nil {
		t.Fatalf("the real file: %v", err)
	} else {
		kp.Wipe()
	}
}

func TestReadSeedRefusesAForeignOwner(t *testing.T) {
	a, dir := newStore(t)
	a.InitAccount()
	otherUser(t)
	p := filepath.Join(dir, "account.nk")
	kp, err := readSeed(p, accountKind)
	if kp != nil || err == nil || !strings.Contains(err.Error(), "owned by uid") {
		t.Fatalf("seed: got %v, %v", kp, err)
	}
	if _, _, err := a.Account(); err == nil {
		t.Fatal("Account read a foreign-owned seed")
	}
	if _, err := checkDir(dir); err == nil || !strings.Contains(err.Error(), "owned by uid") {
		t.Fatalf("dir: got %v", err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("Open accepted a foreign-owned directory")
	}
	if _, err := a.Lock(time.Millisecond); err == nil {
		t.Fatal("Lock ran in a foreign-owned directory")
	}
	// Create the lock file as the real user, then look at it as another one: the directory check passes only
	// when the owner matches, so test the file check alone.
	geteuid = os.Geteuid
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	fi, _ := os.Stat(filepath.Join(dir, lockName))
	otherUser(t)
	if err := ownerErr(fi, lockName, "lock file"); err == nil || !strings.Contains(err.Error(), "lock file") {
		t.Fatalf("lock file owner: got %v", err)
	}
}

func TestReadSeedKeepsSeedOutOfErrors(t *testing.T) {
	a, dir := newStore(t)
	a.InitAccount()
	p := filepath.Join(dir, "account.nk")
	b, _ := os.ReadFile(p)
	seed := strings.TrimSpace(string(b))
	// A user seed in an account file, with surrounding whitespace the bytes path trims.
	if err := os.WriteFile(p, []byte("\n  "+seed[:len(seed)-1]+"\t \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := readSeed(p, accountKind)
	if err == nil || strings.Contains(err.Error(), seed[:20]) {
		t.Fatalf("got %v", err)
	}
	if err := os.WriteFile(p, []byte("\n  "+seed+" \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kp, err := readSeed(p, accountKind)
	if err != nil {
		t.Fatalf("a seed with surrounding whitespace: %v", err)
	}
	kp.Wipe()
	if err := os.WriteFile(p, make([]byte, maxSeedFile+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSeed(p, accountKind); err == nil || !strings.Contains(err.Error(), "does not hold a valid") {
		t.Fatalf("oversized file: got %v", err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSeed(p, accountKind); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty file: got %v", err)
	}
}

func TestLockRefusesASymlinkedLockFile(t *testing.T) {
	a, dir := newStore(t)
	a.InitAccount()
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(target, filepath.Join(dir, lockName)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Lock(time.Millisecond); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Fatal("the lock created a file through the symlink")
	}
}

func TestCheckParentDir(t *testing.T) {
	d := t.TempDir()
	os.Chmod(d, 0o700)
	if err := CheckParentDir(d); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o757); err != nil {
		t.Fatal(err)
	}
	if err := CheckParentDir(d); err == nil || !strings.Contains(err.Error(), "writable by every user") {
		t.Fatalf("other-writable: got %v", err)
	}
	os.Chmod(d, 0o700)
	link := filepath.Join(t.TempDir(), "l")
	os.Symlink(d, link)
	if err := CheckParentDir(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink: got %v", err)
	}
	otherUser(t)
	if err := CheckParentDir(d); err == nil || !strings.Contains(err.Error(), "owned by uid") {
		t.Fatalf("foreign owner: got %v", err)
	}
}

func TestCheckStrictParentDir(t *testing.T) {
	d := t.TempDir()
	for _, mode := range []os.FileMode{0o700, 0o755, 0o750} {
		os.Chmod(d, mode)
		if err := CheckStrictParentDir(d); err != nil {
			t.Fatalf("mode %04o: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o770, 0o775, 0o720, 0o777, 0o757} {
		os.Chmod(d, mode)
		if err := CheckStrictParentDir(d); err == nil || !strings.Contains(err.Error(), "chmod go-w "+d) {
			t.Fatalf("mode %04o: got %v", mode, err)
		}
	}
	os.Chmod(d, 0o700)
	link := filepath.Join(t.TempDir(), "l")
	os.Symlink(d, link)
	if err := CheckStrictParentDir(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink: got %v", err)
	}
	if err := CheckStrictParentDir(filepath.Join(d, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: got %v", err)
	}
	otherUser(t)
	if err := CheckStrictParentDir(d); err == nil || !strings.Contains(err.Error(), "owned by uid") {
		t.Fatalf("foreign owner: got %v", err)
	}
}

func TestGroupKeyIsKeptBesideTheZoneKeysAndNeverOverwritten(t *testing.T) {
	a, err := Open(filepath.Join(t.TempDir(), "keys"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.GroupKey("foo.c", false); !errors.Is(err, ErrNoGroupKey) {
		t.Fatalf("missing: %v", err)
	}
	k1, err := a.GroupKey("foo.c", true)
	if err != nil {
		t.Fatal(err)
	}
	p1, _ := k1.PublicKey()
	k2, err := a.GroupKey("foo.c", true)
	if err != nil {
		t.Fatal(err)
	}
	if p2, _ := k2.PublicKey(); p1 != p2 {
		t.Error("a second create replaced the key")
	}
	fi, err := os.Stat(filepath.Join(a.dir, "groups", "foo.c.nk"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	for _, bad := range []string{"", "foo", "foo.c.core", "../x.c", "foo.", ".c", "Foo.c", "foo/x.c"} {
		if _, err := a.GroupKey(bad, true); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
