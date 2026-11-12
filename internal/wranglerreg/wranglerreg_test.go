package wranglerreg

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
