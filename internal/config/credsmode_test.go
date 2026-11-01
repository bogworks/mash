//go:build unix

package config

import (
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
