package main

import (
	"bytes"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helpVerbs is every verb and sub-verb of mash; each takes -h and --help, prints its usage and changes nothing.
var helpVerbs = [][]string{
	{"serve"}, {"hook"}, {"send"}, {"read"}, {"inbox"}, {"who"}, {"prompt"}, {"status"}, {"version"},
	{"plugin"}, {"plugin", "export"},
	{"admin"}, {"admin", "init"}, {"admin", "limits"}, {"admin", "permissions"}, {"admin", "provision"},
	{"admin", "set-meta"}, {"admin", "suspend"}, {"admin", "resume"}, {"admin", "suspended"},
	{"admin", "deprovision"}, {"admin", "list"},
}

func listTree(root string) []string {
	var found []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if p != root {
			found = append(found, p)
		}
		return nil
	})
	return found
}

func TestVerbHelpPrintsUsageAndChangesNothing(t *testing.T) {
	for _, v := range helpVerbs {
		for _, flag := range []string{"--help", "-h"} {
			name := strings.Join(v, " ") + " " + flag
			t.Run(name, func(t *testing.T) {
				home := t.TempDir()
				cwd := t.TempDir()
				t.Chdir(cwd)
				vars := map[string]string{
					"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, "c"),
					"XDG_DATA_HOME": filepath.Join(home, "d"), "XDG_STATE_HOME": filepath.Join(home, "s"),
					"MASH_URL": "nats://127.0.0.1:1",
				}
				env := func(k string) string { return vars[k] }
				pr, pw := io.Pipe() // an open pipe that is never closed
				defer pw.Close()
				args := append(append([]string{}, v...), flag)
				var out, errb bytes.Buffer
				done := make(chan int, 1)
				go func() { done <- run(args, env, pr, &out, &errb) }()
				var rc int
				select {
				case rc = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("help did not return in 5s (blocked)")
				}
				if rc != 0 {
					t.Fatalf("rc = %d, want 0; stderr %q", rc, errb.String())
				}
				if out.Len() == 0 || !strings.Contains(out.String(), v[len(v)-1]) {
					t.Errorf("stdout %q does not mention %q", out.String(), v[len(v)-1])
				}
				if errb.Len() != 0 {
					t.Errorf("stderr = %q, want empty", errb.String())
				}
				if f := listTree(home); len(f) != 0 {
					t.Errorf("help wrote %v under HOME", f)
				}
				if f := listTree(cwd); len(f) != 0 {
					t.Errorf("help wrote %v in cwd", f)
				}
			})
		}
	}
}

func TestHelpAfterDoubleDashIsNotHelp(t *testing.T) {
	if _, ok := helpUsage([]string{"send", "bob", "--", "--help"}); ok {
		t.Error("--help after -- was taken as help")
	}
	if _, ok := helpUsage([]string{"send", "bob", "--help"}); !ok {
		t.Error("--help not found")
	}
	if _, ok := helpUsage([]string{"nosuch", "--help"}); ok {
		t.Error("unknown verb took help")
	}
}
