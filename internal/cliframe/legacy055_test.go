package cliframe

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRefuseLegacy055(t *testing.T) {
	const hint = "hint: https://github.com/bogworks/mash/releases/latest/download/mash-migrate-0.5.5.sh\n"
	cases := []struct {
		name, body string
		refuse     bool
	}{
		{"url", `{"url":"nats://SECRET-URL:4222","owner":"alice"}`, true},
		{"tls_ca", `{"tls_ca":"/SECRET/ca.pem"}`, true},
		{"old and new", `{"url":"nats://SECRET-URL:4222","servers":["tls://x:1"],"ca":"/c"}`, true},
		{"clean", `{"servers":["tls://x:1"],"owner":"alice"}`, false},
		{"not json", `nope`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, "cfg")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, "config.json")
			if err := os.WriteFile(p, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
			if err := os.Chtimes(p, old, old); err != nil {
				t.Fatal(err)
			}
			env := func(k string) string { return map[string]string{"MASH_CONFIG_DIR": dir, "HOME": home}[k] }
			var out, errb bytes.Buffer
			code, refused := RefuseLegacy055(&out, &errb, env, home, false)
			if refused != tc.refuse {
				t.Fatalf("refused %v, want %v", refused, tc.refuse)
			}
			if tc.refuse {
				want := "error: ~/cfg holds a mash 0.5.5 configuration\n" + hint
				if code != 1 || errb.String() != want || out.Len() != 0 {
					t.Fatalf("%d %q %q", code, errb.String(), out.String())
				}
			}
			if strings.Contains(out.String()+errb.String(), "SECRET") {
				t.Fatal("an old value was printed")
			}
			b, _ := os.ReadFile(p)
			fi, _ := os.Stat(p)
			if string(b) != tc.body || !fi.ModTime().Equal(old) {
				t.Fatal("the file changed")
			}
		})
	}
	t.Run("no folder, no file", func(t *testing.T) {
		home := t.TempDir()
		env := func(k string) string {
			return map[string]string{"MASH_CONFIG_DIR": filepath.Join(home, "none"), "HOME": home}[k]
		}
		if _, refused := RefuseLegacy055(&bytes.Buffer{}, &bytes.Buffer{}, env, home, false); refused {
			t.Fatal("refused")
		}
		if err := os.Mkdir(filepath.Join(home, "empty"), 0o700); err != nil {
			t.Fatal(err)
		}
		env = func(k string) string {
			return map[string]string{"MASH_CONFIG_DIR": filepath.Join(home, "empty"), "HOME": home}[k]
		}
		if _, refused := RefuseLegacy055(&bytes.Buffer{}, &bytes.Buffer{}, env, home, false); refused {
			t.Fatal("refused")
		}
	})
}

// The hint is written from ReleaseBase, which a release build may set.
func TestLegacy055HintUsesReleaseBase(t *testing.T) {
	old := ReleaseBase
	t.Cleanup(func() { ReleaseBase = old })
	ReleaseBase = "https://example.test/fork/dl"
	home := t.TempDir()
	dir := filepath.Join(home, "cfg")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"url":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string { return map[string]string{"MASH_CONFIG_DIR": dir, "HOME": home}[k] }
	var out, errb bytes.Buffer
	if code, ok := RefuseLegacy055(&out, &errb, env, home, false); !ok || code != 1 ||
		!strings.Contains(errb.String(), "hint: https://example.test/fork/dl/mash-migrate-0.5.5.sh\n") {
		t.Fatalf("%d %v %q", code, ok, errb.String())
	}
}

// The folder is shown as ~/... when it is under the home directory (the default and an XDG_CONFIG_HOME one alike), and
// as the real path when it is not.
func TestLegacy055FolderShown(t *testing.T) {
	mk := func(t *testing.T, dir string) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"url":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("default", func(t *testing.T) {
		home := t.TempDir()
		mk(t, filepath.Join(home, ".config", "mash"))
		got, ok := Legacy055(func(k string) string { return map[string]string{"HOME": home}[k] }, home)
		if !ok || got != "~/.config/mash" {
			t.Fatalf("%q %v", got, ok)
		}
	})
	t.Run("xdg under home", func(t *testing.T) {
		home := t.TempDir()
		x := filepath.Join(home, "xdg")
		mk(t, filepath.Join(x, "mash"))
		got, ok := Legacy055(func(k string) string { return map[string]string{"HOME": home, "XDG_CONFIG_HOME": x}[k] }, home)
		if !ok || got != "~/xdg/mash" {
			t.Fatalf("%q %v", got, ok)
		}
	})
	t.Run("xdg outside home", func(t *testing.T) {
		home, x := t.TempDir(), t.TempDir()
		mk(t, filepath.Join(x, "mash"))
		got, ok := Legacy055(func(k string) string { return map[string]string{"HOME": home, "XDG_CONFIG_HOME": x}[k] }, home)
		if !ok || got != filepath.Join(x, "mash") {
			t.Fatalf("%q %v", got, ok)
		}
	})
}
