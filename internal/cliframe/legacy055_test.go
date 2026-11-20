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
