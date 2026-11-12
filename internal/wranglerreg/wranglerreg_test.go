package wranglerreg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nkeys"
)

func key(t *testing.T) string {
	kp, _ := nkeys.CreateUser()
	pub, _ := kp.PublicKey()
	return pub
}

func acct() string {
	kp, _ := nkeys.CreateAccount()
	pub, _ := kp.PublicKey()
	return pub
}

func TestStaticRefusalsAllWrapErrNoRegistry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	a := acct()
	if _, err := OpenStatic(path, a); !errors.Is(err, ErrNoRegistry) {
		t.Fatalf("missing: %v", err)
	}
	f := &File{Account: a, TestAccount: false}
	if err := f.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStatic(path, a); !errors.Is(err, ErrNoRegistry) {
		t.Fatalf("not a test account: %v", err)
	}
	f.TestAccount = true
	_ = f.Save(path)
	if _, err := OpenStatic(path, acct()); !errors.Is(err, ErrNoRegistry) {
		t.Fatalf("another account: %v", err)
	}
	s, err := OpenStatic(path, a)
	if err != nil {
		t.Fatal(err)
	}
	k := key(t)
	if err := f.Add("h", "foo.c", k); err != nil {
		t.Fatal(err)
	}
	_ = f.Save(path)
	if got := s.Lookup("h", "foo.c"); len(got) != 1 || got[0] != k {
		t.Fatalf("%v", got)
	}
	if len(s.Lookup("h", "foo.d")) != 0 || len(s.Lookup("h2", "foo.c")) != 0 {
		t.Fatal("matched another host or group")
	}
	// A registry that stops being one registers no one.
	f.TestAccount = false
	_ = f.Save(path)
	if len(s.Lookup("h", "foo.c")) != 0 {
		t.Fatal("served after the flag was cleared")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
}

func TestLoadRefusesASymlinkAndAWritableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	f := &File{Account: acct(), TestAccount: true}
	_ = f.Save(path)
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Skip(err)
	}
	if _, err := Load(link); err == nil {
		t.Error("followed a symlink")
	}
	_ = os.Chmod(path, 0o666)
	if _, err := Load(path); err == nil {
		t.Error("read a world-writable file")
	}
}

func TestAddValidates(t *testing.T) {
	f := &File{}
	for _, c := range [][3]string{{"", "foo.c", key(t)}, {"h", "foo", key(t)}, {"h", "foo.c.core", key(t)}, {"h", "foo.c", "UBAD"}} {
		if err := f.Add(c[0], c[1], c[2]); err == nil {
			t.Errorf("accepted %v", c)
		}
	}
}

// The registry file is held to the key store's custody: its directory may not be a symlink or writable by every user,
// and the file may not be writable by group or others.
func TestRegistryCustodyOfTheFileAndItsDirectory(t *testing.T) {
	a := acct()
	write := func(dir string) string {
		p := filepath.Join(dir, FileName)
		if err := (&File{Account: a, TestAccount: true}).Save(p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("sound", func(t *testing.T) {
		if _, err := OpenStatic(write(t.TempDir()), a); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("world-writable directory", func(t *testing.T) {
		dir := t.TempDir()
		p := write(dir)
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenStatic(p, a); !errors.Is(err, ErrNoRegistry) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("symlinked directory", func(t *testing.T) {
		real, link := t.TempDir(), filepath.Join(t.TempDir(), "link")
		write(real)
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenStatic(filepath.Join(link, FileName), a); !errors.Is(err, ErrNoRegistry) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("group-writable file", func(t *testing.T) {
		p := write(t.TempDir())
		if err := os.Chmod(p, 0o660); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenStatic(p, a); !errors.Is(err, ErrNoRegistry) {
			t.Fatalf("%v", err)
		}
	})
}

func newReg(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	a := acct()
	if _, err := Init(path, a); err != nil {
		t.Fatal(err)
	}
	return path, a
}

func has(t *testing.T, path, host string) bool {
	t.Helper()
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.Entries {
		if e.Host == host {
			return true
		}
	}
	return false
}

// The exact lost update: a second Update does not read the file until the first has saved, so both edits are there.
func TestUpdateSecondReadWaitsForTheFirstToSave(t *testing.T) {
	path, _ := newReg(t)
	inside, release := make(chan struct{}), make(chan struct{})
	firstDone, secondRead, secondDone := make(chan error, 1), make(chan struct{}), make(chan error, 1)
	go func() {
		firstDone <- Update(path, func(f *File) error {
			close(inside)
			<-release
			return f.Add("h1", "foo.c", key(t))
		})
	}()
	<-inside
	go func() {
		secondDone <- Update(path, func(f *File) error {
			close(secondRead)
			if !slices.ContainsFunc(f.Entries, func(e Entry) bool { return e.Host == "h1" }) {
				t.Error("the second Update read the file before the first saved")
			}
			return f.Add("h2", "foo.c", key(t))
		})
	}()
	select {
	case <-secondRead:
		t.Fatal("the second Update ran inside the first's critical section")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if !has(t, path, "h1") || !has(t, path, "h2") {
		t.Fatal("an edit was lost")
	}
}

// Concurrent adds of distinct keys and a remove: the file ends with every add except the removed one.
func TestUpdateConcurrentAddsAndARemove(t *testing.T) {
	path, _ := newReg(t)
	if err := Update(path, func(f *File) error { return f.Add("victim", "foo.c", key(t)) }); err != nil {
		t.Fatal(err)
	}
	const n = 20
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Update(path, func(f *File) error { return f.Add(fmt.Sprintf("h%d", i), "foo.c", key(t)) }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := Update(path, func(f *File) error {
			if f.Remove("victim", "foo.c") == 0 {
				return errors.New("nothing removed")
			}
			return nil
		}); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != n || has(t, path, "victim") {
		t.Fatalf("%d entries, victim present %v", len(f.Entries), has(t, path, "victim"))
	}
}

// A removed key does not come back when an add races the remove.
func TestUpdateRemovedKeyNeverReappears(t *testing.T) {
	for i := range 50 {
		path, _ := newReg(t)
		if err := Update(path, func(f *File) error { return f.Add("x", "foo.c", key(t)) }); err != nil {
			t.Fatal(err)
		}
		y := key(t)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := Update(path, func(f *File) error { f.Remove("x", "foo.c"); return nil }); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := Update(path, func(f *File) error { return f.Add("y", "foo.c", y) }); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
		if has(t, path, "x") || !has(t, path, "y") {
			t.Fatalf("round %d: x present %v, y present %v", i, has(t, path, "x"), has(t, path, "y"))
		}
	}
}

func TestUpdateErrorLeavesFileAndReleasesLock(t *testing.T) {
	path, _ := newReg(t)
	before, _ := os.ReadFile(path)
	boom := errors.New("boom")
	if err := Update(path, func(f *File) error { f.Entries = nil; _ = f.Add("h", "foo.c", key(t)); return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("a failed edit changed the file")
	}
	if err := Update(path, func(f *File) error { return nil }); err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	if err := Update(filepath.Join(t.TempDir(), FileName), func(*File) error { return nil }); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing registry: %v", err)
	}
}

func TestLockFileCustody(t *testing.T) {
	path, a := newReg(t)
	lock := LockPath(path)
	if fi, err := os.Stat(lock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("lock file: %v %v", fi, err)
	}
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(*File) error { t.Error("ran in a world-writable directory"); return nil }); err == nil {
		t.Error("edited in a world-writable directory")
	}
	if _, err := Init(path, a); err == nil {
		t.Error("Init in a world-writable directory")
	}
	_ = os.Chmod(dir, 0o700)
	// A symlinked lock file is refused.
	_ = os.Remove(lock)
	if err := os.Symlink(path, lock); err != nil {
		t.Skip(err)
	}
	if err := Update(path, func(*File) error { t.Error("ran with a symlinked lock"); return nil }); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlinked lock file: %v", err)
	}
	// A symlinked directory is refused.
	real, link := t.TempDir(), filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	if err := Update(filepath.Join(link, FileName), func(*File) error { return nil }); err == nil {
		t.Error("edited through a symlinked directory")
	}
}

// Readers take no lock: Lookup and Load during an edit answer at once, with a complete old or new file.
func TestReadersDuringAnEditSeeACompleteFile(t *testing.T) {
	path, a := newReg(t)
	k0 := key(t)
	if err := Update(path, func(f *File) error { return f.Add("h0", "foo.c", k0) }); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStatic(path, a)
	if err != nil {
		t.Fatal(err)
	}
	inside, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- Update(path, func(f *File) error {
			close(inside)
			<-release
			return f.Add("h1", "foo.c", key(t))
		})
	}()
	<-inside
	start := time.Now()
	if got := s.Lookup("h0", "foo.c"); len(got) != 1 || got[0] != k0 {
		t.Errorf("lookup during an edit: %v", got)
	}
	if f, err := Load(path); err != nil || len(f.Entries) != 1 {
		t.Errorf("load during an edit: %v %v", f, err)
	}
	if time.Since(start) > time.Second {
		t.Error("a reader waited for the writer")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Hammer readers against a stream of writers: every read is a complete file.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := Load(path); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := range 30 {
		if err := Update(path, func(f *File) error { return f.Add(fmt.Sprintf("w%d", i), "foo.c", key(t)) }); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
}
