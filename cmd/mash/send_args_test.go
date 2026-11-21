package main

import (
	"bytes"
	"strings"
	"testing"
)

// Help for the table's commands is generated (cmdcore); prompt, serve and hook are rendered in the same layout from their specification entry.
func TestVerbHelpAndStrayArguments(t *testing.T) {
	env := func(string) string { return "" }
	for _, verb := range []string{"status", "doctor", "version", "who", "read", "inbox", "send", "prompt"} {
		for _, h := range []string{"--help", "-h"} {
			var out, errb bytes.Buffer
			want := "Usage:\n  mash " + verb
			if code := run([]string{verb, h}, env, strings.NewReader(""), &out, &errb); code != 0 || !strings.Contains(out.String(), want) || errb.Len() != 0 {
				t.Errorf("%s %s: %d %q %q", verb, h, code, out.String(), errb.String())
			}
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"status", "--bogus"}, env, strings.NewReader(""), &out, &errb); code != 2 || out.Len() != 0 || errb.String() != "error: unknown option --bogus\nhint: mash status --help\n" {
		t.Errorf("status --bogus: %d %q %q", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"version", "--bogus"}, env, strings.NewReader(""), &out, &errb); code != 2 || out.Len() != 0 || errb.String() != "error: unknown option --bogus\nhint: mash version --help\n" {
		t.Errorf("version --bogus: %d %q %q", code, out.String(), errb.String())
	}
}

// --json on a verb with no JSON form is a usage error, and nothing is attempted: {"error","hint"} on stdout, exit 2.
func TestNoJSONFormIsRefused(t *testing.T) {
	env := func(string) string { return "" }
	for _, verb := range []string{"prompt", "serve", "hook"} {
		for _, args := range [][]string{{verb, "--json"}, {"--json", verb}, {verb, "x", "--json"}} {
			var out, errb bytes.Buffer
			want := "{\"error\":\"mash " + verb + " has no --json form\",\"hint\":\"mash " + verb + " --help\"}\n"
			if code := run(args, env, strings.NewReader(""), &out, &errb); code != 2 || errb.Len() != 0 || out.String() != want {
				t.Errorf("%v: %d %q %q, want stdout %q and nothing on stderr", args, code, out.String(), errb.String(), want)
			}
		}
	}
	// help still comes first, and the verbs that have a JSON form are not refused
	var out, errb bytes.Buffer
	if code := run([]string{"read", "--json", "--help"}, env, strings.NewReader(""), &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "Show one message in full.\n\nUsage:\n  mash read <number>") {
		t.Errorf("read --json --help: %d %q %q", code, out.String(), errb.String())
	}
	for _, args := range [][]string{{"read", "2", "--json"}, {"inbox", "--json"}, {"version", "--json"}, {"who", "--json"}, {"send", "--json"}, {"status", "--json"}, {"doctor", "--json"}} {
		out.Reset()
		errb.Reset()
		run(args, env, strings.NewReader(""), &out, &errb)
		if strings.Contains(errb.String()+out.String(), "has no --json form") {
			t.Errorf("%v refused: %q", args, errb.String())
		}
	}
}
