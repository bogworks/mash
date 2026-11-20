package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

const legacy055Two = "error: ~/cfg holds a mash 0.5.5 configuration\n" +
	"hint: https://github.com/bogworks/mash/releases/latest/download/mash-migrate-0.5.5.sh\n"

func legacy055Folder(t *testing.T) (home, dir string) {
	home = t.TempDir()
	dir = filepath.Join(home, "cfg")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"url":"nats://x:4222"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, dir
}

// endpoint create and every --help are refused.
func TestLegacy055CreateAndHelpRefused(t *testing.T) {
	for _, args := range [][]string{{"endpoint", "create", "acme.demo.bob"}, {"--help"}, {"status", "--help"}, {"endpoint", "--help"}} {
		home, dir := legacy055Folder(t)
		env := map[string]string{"HOME": home, "MASH_CONFIG_DIR": dir}
		var out, errb bytes.Buffer
		code := runEnv(args, func(k string) string { return env[k] }, &out, &errb)
		if code != 1 || errb.String() != legacy055Two || out.Len() != 0 {
			t.Fatalf("%v: %d %q %q", args, code, errb.String(), out.String())
		}
	}
}

// With HOME unset the folder is still found, as cmd/mash finds it: a named folder, or the process home directory.
func TestLegacy055HomeUnset(t *testing.T) {
	_, dir := legacy055Folder(t)
	t.Setenv("HOME", "")
	t.Run("config dir named", func(t *testing.T) {
		env := map[string]string{"MASH_CONFIG_DIR": dir}
		var out, errb bytes.Buffer
		code := runEnv([]string{"version"}, func(k string) string { return env[k] }, &out, &errb)
		if code != 1 || errb.String() != "error: "+dir+" holds a mash 0.5.5 configuration\n"+legacy055Two[strings.Index(legacy055Two, "hint:"):] {
			t.Fatalf("%d %q", code, errb.String())
		}
	})
	t.Run("flag", func(t *testing.T) {
		var out, errb bytes.Buffer
		code := runEnv([]string{"version", "--config-dir", dir}, func(string) string { return "" }, &out, &errb)
		if code != 1 || !strings.Contains(errb.String(), "holds a mash 0.5.5 configuration") {
			t.Fatalf("%d %q", code, errb.String())
		}
	})
	t.Run("no folder named", func(t *testing.T) {
		var out, errb bytes.Buffer
		code := runEnv([]string{"version"}, func(string) string { return "" }, &out, &errb)
		if code != 0 || strings.Contains(errb.String(), "0.5.5") {
			t.Fatalf("%d %q", code, errb.String())
		}
	})
}
