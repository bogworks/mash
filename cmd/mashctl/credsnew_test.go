//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/ctlkeys"
)

func privateDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestWriteCredsNewInstalls(t *testing.T) {
	d := privateDir(t)
	p := filepath.Join(d, "sub", "a.creds") // the parent is made 0700
	undo, err := writeCredsNew(p, []byte("creds"))
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v", fi)
	}
	if fi, _ := os.Stat(filepath.Dir(p)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("made directory has mode %v", fi.Mode().Perm())
	}
	if _, err := writeCredsNew(p, []byte("other")); err == nil || !strings.Contains(err.Error(), "creds file "+p+" exists") {
		t.Fatalf("second write: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "creds" {
		t.Fatalf("existing file changed: %q", b)
	}
	undo()
	if _, err := os.Stat(filepath.Dir(p)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("undo left the directory: %v", err)
	}
}

func TestWriteCredsNewRefusesAnUntrustedParent(t *testing.T) {
	d := privateDir(t)
	loose := filepath.Join(d, "loose")
	if err := os.Mkdir(loose, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o757); err != nil {
		t.Fatal(err)
	}
	if _, err := writeCredsNew(filepath.Join(loose, "a.creds"), []byte("x")); err == nil || !strings.Contains(err.Error(), "writable by every user") {
		t.Fatalf("other-writable parent: %v", err)
	}
	real := filepath.Join(d, "real")
	os.Mkdir(real, 0o700)
	link := filepath.Join(d, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := writeCredsNew(filepath.Join(link, "a.creds"), []byte("x")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked parent: %v", err)
	}
	for _, dir := range []string{loose, real} {
		if ents, _ := os.ReadDir(dir); len(ents) != 0 {
			t.Fatalf("%s: left %v", dir, ents)
		}
	}
}

func TestWriteCredsNewLeavesNothingOnAFailedSync(t *testing.T) {
	d := privateDir(t)
	p := filepath.Join(d, "new", "a.creds")
	restore := ctlkeys.SetSyncDirHook(func(string) error { return errors.New("injected") })
	defer restore()
	if _, err := writeCredsNew(p, []byte("x")); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(p)); !errors.Is(err, os.ErrNotExist) {
		ents, _ := os.ReadDir(filepath.Dir(p))
		t.Fatalf("left the directory behind: %v %v", err, ents)
	}
	// With the parent already there, the failed install leaves no creds file and no temp file.
	if err := os.Mkdir(filepath.Join(d, "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	p = filepath.Join(d, "old", "a.creds")
	if _, err := writeCredsNew(p, []byte("x")); err == nil {
		t.Fatal("no error")
	}
	if ents, _ := os.ReadDir(filepath.Dir(p)); len(ents) != 0 {
		t.Fatalf("left %v", ents)
	}
}
