//go:build unix

package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckCredsMode(t *testing.T) {
	for _, c := range []struct {
		name    string
		mode    os.FileMode
		missing bool
		refused bool
	}{
		{"0600", 0o600, false, false},
		{"0400", 0o400, false, false},
		{"0640", 0o640, false, true},
		{"0604", 0o604, false, true},
		{"missing", 0, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "x.creds")
			if !c.missing {
				if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(p, c.mode); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckCredsMode(p)
			if c.refused {
				if err == nil || !strings.Contains(err.Error(), "chmod 600 "+p) {
					t.Fatalf("want a refusal with the chmod hint, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCredsFollowSymlinksButRefuseDirsAndOpenLinkTargets(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.creds")
	if err := os.WriteFile(real, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.creds")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// A symlink is followed (Kubernetes secret mounts are symlinks); the target's mode is what is checked.
	if err := CheckCredsMode(link); err != nil {
		t.Errorf("symlink to a 0600 file: %v", err)
	}
	if b, err := ReadCredsFile(link); err != nil || string(b) != "secret" {
		t.Errorf("ReadCredsFile(link) = %q, %v", b, err)
	}
	if err := os.Chmod(real, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckCredsMode(link); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("symlink to a 0644 file: want a refusal, got %v", err)
	}
	if err := os.Chmod(real, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]func(string) error{
		"CheckCredsMode": CheckCredsMode,
		"ReadCredsFile":  func(p string) error { _, err := ReadCredsFile(p); return err },
	} {
		if err := f(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("%s: directory: want a refusal, got %v", name, err)
		}
	}
	if _, err := ReadCredsFile(filepath.Join(dir, "nope")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: want fs.ErrNotExist, got %v", err)
	}
}
