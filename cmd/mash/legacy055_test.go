package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A folder mash 0.5.5 left stops every command, names itself, changes nothing and never shows the old values.
func TestLegacy055FolderRefused(t *testing.T) {
	const hint = "hint: https://github.com/bogworks/mash/releases/latest/download/mash-migrate-0.5.5.sh\n"
	for name, body := range map[string]string{
		"url":      `{"url":"nats://SECRET-URL:4222","owner":"alice"}`,
		"tls_ca":   `{"tls_ca":"/SECRET/ca.pem"}`,
		"old, new": `{"url":"nats://SECRET-URL:4222","servers":["tls://x:1"],"ca":"/c"}`,
	} {
		for _, args := range [][]string{{"version"}, {"status"}, {"who"}, {"join", "acme.demo.bob.join"}} {
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
				code := run(args, func(k string) string { return env[k] }, strings.NewReader(""), &out, &errb)
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

// A clean folder, and a folder with no config.json, are not refused.
func TestLegacy055CleanFolderNotRefused(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "cfg")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "MASH_CONFIG_DIR": dir}
	check := func() {
		var out, errb bytes.Buffer
		code := run([]string{"version"}, func(k string) string { return env[k] }, strings.NewReader(""), &out, &errb)
		if code != 0 || strings.Contains(errb.String(), "0.5.5") {
			t.Fatalf("%d %q", code, errb.String())
		}
	}
	check()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"servers":["tls://x:1"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	check()
}

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

const legacy055Two = "error: ~/cfg holds a mash 0.5.5 configuration\n" +
	"hint: https://github.com/bogworks/mash/releases/latest/download/mash-migrate-0.5.5.sh\n"

// A hook never blocks a session: the two lines on stderr, exit 0, stdout empty, stdin not read, no spool.
func TestLegacy055HookExitsZero(t *testing.T) {
	home, dir := legacy055Folder(t)
	state := filepath.Join(home, "state")
	env := map[string]string{"HOME": home, "MASH_CONFIG_DIR": dir, "MASH_STATE_DIR": state}
	var out, errb bytes.Buffer
	code := run([]string{"hook"}, func(k string) string { return env[k] }, strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb)
	if code != 0 || errb.String() != legacy055Two || out.Len() != 0 {
		t.Fatalf("%d %q %q", code, errb.String(), out.String())
	}
	if _, err := os.Stat(state); err == nil {
		t.Fatal("the spool was touched")
	}
}

// serve stops (exit 1) with the same two lines; --help is refused too.
func TestLegacy055ServeAndHelpRefused(t *testing.T) {
	for _, args := range [][]string{{"serve"}, {"--help"}, {"send", "--help"}} {
		home, dir := legacy055Folder(t)
		env := map[string]string{"HOME": home, "MASH_CONFIG_DIR": dir}
		var out, errb bytes.Buffer
		code := run(args, func(k string) string { return env[k] }, strings.NewReader(""), &out, &errb)
		if code != 1 || errb.String() != legacy055Two || out.Len() != 0 {
			t.Fatalf("%v: %d %q %q", args, code, errb.String(), out.String())
		}
	}
}

// A default folder is named ~/.config/mash.
func TestLegacy055DefaultFolderShown(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "mash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"tls_ca":"/c"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home}
	var out, errb bytes.Buffer
	code := run([]string{"version"}, func(k string) string { return env[k] }, strings.NewReader(""), &out, &errb)
	if code != 1 || !strings.HasPrefix(errb.String(), "error: ~/.config/mash holds a mash 0.5.5 configuration\n") {
		t.Fatalf("%d %q", code, errb.String())
	}
}
