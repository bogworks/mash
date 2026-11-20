package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A folder mash 0.5.5 left stops every mashctl command, init included, names itself, changes nothing and never shows
// the old values.
func TestLegacy055FolderRefused(t *testing.T) {
	const hint = "hint: https://github.com/bogworks/mash/releases/latest/download/mash-migrate-0.5.5.sh\n"
	for name, body := range map[string]string{
		"url":      `{"url":"nats://SECRET-URL:4222","owner":"alice"}`,
		"tls_ca":   `{"tls_ca":"/SECRET/ca.pem"}`,
		"old, new": `{"url":"nats://SECRET-URL:4222","servers":["tls://x:1"],"ca":"/c"}`,
	} {
		for _, args := range [][]string{{"version"}, {"status"}, {"init", "--config", "/nope.yaml"}, {"endpoint", "list"}} {
			t.Run(name+" "+args[0], func(t *testing.T) {
				home := t.TempDir()
				dir := filepath.Join(home, "cfg")
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				p := filepath.Join(dir, "config.json")
				if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
				if err := os.Chtimes(p, old, old); err != nil {
					t.Fatal(err)
				}
				env := map[string]string{"HOME": home, "MASH_CONFIG_DIR": dir}
				var out, errb bytes.Buffer
				code := runEnv(args, func(k string) string { return env[k] }, &out, &errb)
				want := "error: ~/cfg holds a mash 0.5.5 configuration\n" + hint
				if code != 1 || errb.String() != want || out.Len() != 0 {
					t.Fatalf("%d %q %q", code, errb.String(), out.String())
				}
				got, _ := os.ReadFile(p)
				fi, _ := os.Stat(p)
				if string(got) != body || !fi.ModTime().Equal(old) {
					t.Fatal("the file changed")
				}
				if ents, _ := os.ReadDir(dir); len(ents) != 1 {
					t.Fatalf("the folder changed: %v", ents)
				}
			})
		}
	}
}

// A clean folder is not refused.
func TestLegacy055CleanFolderNotRefused(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "cfg")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"servers":["tls://x:1"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "MASH_CONFIG_DIR": dir}
	var out, errb bytes.Buffer
	if code := runEnv([]string{"version"}, func(k string) string { return env[k] }, &out, &errb); code != 0 {
		t.Fatalf("%d %q", code, errb.String())
	}
}
