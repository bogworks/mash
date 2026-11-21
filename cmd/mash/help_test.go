package main

import (
	"bytes"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// helpVerbs is every verb and sub-verb of mash; each takes -h and --help, prints its usage and changes nothing.
var helpVerbs = [][]string{
	{"serve"}, {"hook"}, {"send"}, {"read"}, {"inbox"}, {"who"}, {"prompt"}, {"status"}, {"doctor"}, {"version"},
	{"key"}, {"key", "create"}, {"join"},
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
					"MASH_SERVER": "nats://127.0.0.1:1",
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

// Help is only the first argument after the verb: free text and values that contain
// -h or --help, anywhere further along, are never help.
func TestHelpUsageOnlyFirstArgument(t *testing.T) {
	yes := [][]string{
		{"prompt", "--help"}, {"prompt", "-h"},
		{"serve", "-h"}, {"hook", "--help"},
	}
	for _, a := range yes {
		if _, ok := helpUsage(a); !ok {
			t.Errorf("%v: not help, want help", a)
		}
	}
	no := [][]string{
		{"send", tTo("y"), "--help"}, {"send", tTo("y"), "-h"}, // send is on the command table: its help is cmdcore's
		{"prompt", tTo("y"), "what", "does", "-h", "do"}, {"prompt", tTo("y"), "--help"}, {"prompt", tTo("y"), "-h"},
		{"nosuch", "--help"},
	}
	for _, a := range no {
		if _, ok := helpUsage(a); ok {
			t.Errorf("%v: taken as help", a)
		}
	}
}

// A message that contains the words is sent whole when it is one argument, or follows "--"; --help or -h as an option
// anywhere before "--" prints the help and sends nothing.
func TestSendTextWithHelpWordsIsSent(t *testing.T) {
	w := newWorld(t, "x", "y")
	for i, text := range [][]string{{"see the --help flag"}, {"--", "-h"}, {"--", "--help"}, {"what does -h do"}} {
		out, code := w.cli(t, "x", append([]string{"send", tTo("y")}, text...)...)
		if code != 0 || strings.HasPrefix(out, "Send a message") || !strings.Contains(out, "Mail sent (number ") {
			t.Fatalf("send %v: %d %q", text, code, out)
		}
		out, code = w.cli(t, "y", "read", strconv.Itoa(i+1))
		if want := text[len(text)-1]; code != 0 || !strings.Contains(out, want) {
			t.Fatalf("read #%d: %d %q, want text %q", i+1, code, out, want)
		}
	}
	for _, args := range [][]string{{"--help"}, {"-h"}, {tTo("y"), "--help"}, {tTo("y"), "words", "-h"}, {tTo("y"), "--kind", "fyi", "-h"}} {
		out, code := w.cli(t, "x", append([]string{"send"}, args...)...)
		if code != 0 || !strings.HasPrefix(out, "Send a message to an endpoint.\n\nUsage:\n  mash send ") {
			t.Fatalf("send %v: %d %q", args, code, out)
		}
	}
	if n := inboxCount(t, w, "y"); n != 4 {
		t.Fatalf("y has %d messages, want the 4 sent and nothing from help", n)
	}
}

// prompt needs a live responder, so the send path is tested as far as it goes: the words reach recipient lookup (an
// unknown recipient is an error, not usage), and help as the first argument prints usage.
func TestPromptTextWithHelpWordsIsNotHelp(t *testing.T) {
	w := newWorld(t, "x")
	for _, a := range [][]string{{"nosuch", "what", "does", "-h", "do"}, {"nosuch", "--help"}, {"nosuch", "-h"}} {
		out, code := w.cli(t, "x", append([]string{"prompt"}, a...)...)
		if code == 0 || strings.HasPrefix(out, "usage:") {
			t.Errorf("prompt %v: %d %q, want a lookup error, not usage", a, code, out)
		}
	}
	for _, h := range []string{"--help", "-h"} {
		out, code := w.cli(t, "x", "prompt", h)
		if code != 0 || !strings.HasPrefix(out, "Ask an endpoint a live question") {
			t.Errorf("prompt %s: %d %q", h, code, out)
		}
	}
}

// A help option before the command is moved to just after it, so a "--" later in the line cannot hide it.
func TestHelpBeforeCommandWithTerminator(t *testing.T) {
	want := runMash(nil, "version", "--help")
	got := runMash(nil, "-h", "version", "--")
	if got.code != 0 || got.out != want.out {
		t.Fatalf("mash -h version --: exit %d, %q; want what mash version --help prints (%q)", got.code, got.out, want.out)
	}
}
