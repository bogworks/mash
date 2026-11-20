package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// runSplit is w.cli with the streams apart.
func runSplit(t *testing.T, w *world, as string, args ...string) (out, errb string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, envFrom(w.env[as]), strings.NewReader(""), &o, &e)
	return o.String(), withoutPlainWarning(e.String()), code
}

// `mash send` on the command table: the receipt in its three forms, the usage errors with nothing sent, and the failures
// in the rules' forms with the exit code, plain and with --json.
func TestSendCommandBothForms(t *testing.T) {
	w := newWorld(t, "a", "b")
	out, errb, code := runSplit(t, w, "b", "send", tTo("a"), "hello", "--json")
	if code != 0 || errb != "" || out != `{"to":"o.dev.a","number":1,"kind":"task","urgent":false,"duplicate":false,"retagged":false}`+"\n" {
		t.Fatalf("--json: %d %q %q", code, out, errb)
	}
	out, _, code = runSplit(t, w, "b", "send", tTo("a"), "--kind", "fyi", "--urgent", "second", "-v")
	if code != 0 || out != "Urgent fyi mail sent.\nto:      o.dev.a\nnumber:  2\nkind:    fyi\nurgent:  yes\n" {
		t.Fatalf("-v: %d %q", code, out)
	}
	// reply-to and relay go out signed, and a short question reply is tagged fyi (the receipt says the kind sent)
	out, _, code = runSplit(t, w, "a", "send", tTo("b"), "--reply-to", "1", "--kind", "question", "ok", "--json")
	if code != 0 || !strings.Contains(out, `"kind":"fyi"`) || !strings.Contains(out, `"retagged":true`) {
		t.Fatalf("reply-to: %d %q", code, out)
	}
	if rd, _ := w.cli(t, "b", "read", "1"); !strings.Contains(rd, "answers #1") || !strings.Contains(rd, "kind: fyi") {
		t.Fatalf("read: %s", rd)
	}
	// the text from --file and from standard input, one trailing newline dropped
	file := t.TempDir() + "/m.txt"
	if err := os.WriteFile(file, []byte("from a file\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, code = runSplit(t, w, "b", "send", tTo("a"), "--file", file); code != 0 {
		t.Fatalf("--file: %d", code)
	}
	if rd, _ := w.cli(t, "a", "read", "3"); !strings.Contains(rd, "> from a file\n") || strings.Contains(rd, "\r") {
		t.Fatalf("read #3: %q", rd)
	}
	var o, e bytes.Buffer
	if code := run([]string{"send", tTo("a"), "--file", "-"}, envFrom(w.env["b"]), strings.NewReader("piped\n"), &o, &e); code != 0 {
		t.Fatalf("--file -: %d %s", code, e.String())
	}
	if rd, _ := w.cli(t, "a", "read", "4"); !strings.Contains(rd, "> piped\n") {
		t.Fatalf("read #4: %q", rd)
	}

	// usage errors: exit 2, one error line and the help hint, nothing sent
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"send"}, "error: mash send needs <address>\n"},
		{[]string{"send", tTo("a")}, "error: mash send needs the message text\n"},
		{[]string{"send", tTo("a"), "a", "b"}, "error: unexpected argument \"b\" for mash send\n"},
		{[]string{"send", tTo("a"), "x", "--kind", "loud"}, "error: --kind must be one of blocker, question, task, result, fyi\n"},
		{[]string{"send", tTo("a"), "x", "--reply-to", "0"}, "error: --reply-to must be a whole number of 1 or more\n"},
		{[]string{"send", tTo("a"), "x", "--kind", "fyi", "--kind", "task"}, "error: --kind was given twice\n"},
		{[]string{"send", tTo("a"), "x", "--urgent=maybe"}, "error: --urgent must be true or false\n"},
		{[]string{"send", tTo("a"), "x", "--id", "has space"}, "error: send id \"has space\" must be printable ASCII with no spaces\n"},
		{[]string{"send", "", "x"}, "error: mash send needs <address>\n"},
	} {
		out, errb, code := runSplit(t, w, "b", tc.args...)
		if code != 2 || out != "" || errb != tc.want+"hint: mash send --help\n" {
			t.Errorf("%v: %d %q %q", tc.args, code, out, errb)
		}
	}
	// a failure is exit 1; --json puts the same on stdout and nothing on stderr
	out, errb, code = runSplit(t, w, "b", "send", "o.dev.nobody", "x")
	if code != 1 || out != "" || errb != "error: no endpoint has the address o.dev.nobody\nhint: mash who\n" {
		t.Fatalf("no such endpoint: %d %q %q", code, out, errb)
	}
	out, errb, code = runSplit(t, w, "b", "send", "o.dev.nobody", "x", "--json")
	if code != 1 || errb != "" || out != `{"error":"no endpoint has the address o.dev.nobody","hint":"mash who"}`+"\n" {
		t.Fatalf("no such endpoint --json: %d %q %q", code, out, errb)
	}
	if n := inboxCount(t, w, "a"); n != 4 {
		t.Fatalf("a has %d messages, want 4: a usage error sent something", n)
	}
}

// `mash version` is one line, and the same object as the MCP tool in --json.
func TestVersionCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, envFrom(nil), strings.NewReader(""), &out, &errb); code != 0 || errb.Len() != 0 ||
		!strings.HasPrefix(out.String(), "mash v") || strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("version: %d %q %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := run([]string{"version", "--json"}, envFrom(map[string]string{"MASH_PLUGIN_ROOT": pluginRoot(t, `{"version":"4.5.6"}`)}), strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("version --json: %d %q", code, errb.String())
	}
	var o map[string]any
	if err := json.Unmarshal(out.Bytes(), &o); err != nil || len(o) != 3 || o["plugin"] != "4.5.6" || o["version"] == "" || o["commit"] == "" {
		t.Fatalf("version --json: %q %v", out.String(), err)
	}
}

// The generated help is 80 columns at most, in the layout of rules.json, for both commands on the table.
func TestGeneratedHelpLayout(t *testing.T) {
	for _, verb := range []string{"send", "version"} {
		var out, errb bytes.Buffer
		if code := run([]string{verb, "--help"}, envFrom(nil), strings.NewReader(""), &out, &errb); code != 0 {
			t.Fatalf("%s --help: %d", verb, code)
		}
		at := -1
		for _, h := range []string{"\nUsage:\n  mash " + verb, "\n\nExamples:\n", "\n\nGlobal options:\n  --endpoint <address>"} {
			i := strings.Index(out.String(), h)
			if i <= at {
				t.Errorf("%s help: %q missing or out of order:\n%s", verb, h, out.String())
			}
			at = i
		}
		for _, l := range strings.Split(out.String(), "\n") {
			if utf8.RuneCountInString(l) > 80 {
				t.Errorf("%s help line over 80 columns: %q", verb, l)
			}
		}
	}
}

// The auto-tag is said: a reply marked question that asks nothing is sent as fyi and the text and --json say so; a reply
// that asks is not touched.
func TestSendRetaggedIsReported(t *testing.T) {
	w := newWorld(t, "a", "b")
	if _, _, code := runSplit(t, w, "b", "send", tTo("a"), "first?"); code != 0 {
		t.Fatal(code)
	}
	out, errb, code := runSplit(t, w, "a", "send", tTo("b"), "--reply-to", "1", "--kind", "question", "ok")
	if code != 0 || errb != "" || out != "Mail sent as fyi: a reply marked question that asks nothing.\n" {
		t.Fatalf("retagged: %d %q %q", code, out, errb)
	}
	out, _, code = runSplit(t, w, "a", "send", tTo("b"), "--reply-to", "1", "--kind", "question", "which one?", "--json")
	if code != 0 || out != `{"to":"o.dev.b","number":2,"kind":"question","urgent":false,"duplicate":false,"retagged":false}`+"\n" {
		t.Fatalf("asks: %d %q", code, out)
	}
}

// A flat or slashed address is refused with exit 2 before anything is opened or sent.
func TestSendRefusesOtherAddressForms(t *testing.T) {
	w := newWorld(t, "a", "b")
	flat := strings.ReplaceAll(tTo("a"), ".", "_")
	out, errb, code := runSplit(t, w, "b", "send", flat, "hi")
	if code != 2 || out != "" || errb != "error: "+flat+" is not an address; write it as "+tTo("a")+"\nhint: mash send --help\n" {
		t.Fatalf("flat: %d %q %q", code, out, errb)
	}
	slash := strings.ReplaceAll(tTo("a"), ".", "/")
	out, errb, code = runSplit(t, w, "b", "send", slash, "hi", "--json")
	if code != 2 || errb != "" || out != `{"error":"`+slash+` is not an address; write it as `+tTo("a")+`","hint":"mash send --help"}`+"\n" {
		t.Fatalf("slash --json: %d %q %q", code, out, errb)
	}
	if n := inboxCount(t, w, "a"); n != 0 {
		t.Fatalf("a has %d messages: a refused address sent something", n)
	}
	// the other verb that takes an address argument says the same
	out, errb, code = runSplit(t, w, "b", "prompt", flat, "hi")
	if code != 2 || out != "" || !strings.HasPrefix(errb, "error: "+flat+" is not an address; write it as "+tTo("a")+"\nhint: mash prompt --help\n") {
		t.Fatalf("prompt flat: %d %q %q", code, out, errb)
	}
}

// `mash help` prints what `mash --help` does, and `mash help VERB` what `mash VERB --help` does: on stdout, exit 0.
func TestHelpCommandMatchesHelpFlag(t *testing.T) {
	for _, tc := range []struct{ a, b []string }{
		{[]string{"help"}, []string{"--help"}},
		{[]string{"help", "send"}, []string{"send", "--help"}},
		{[]string{"help", "version"}, []string{"version", "--help"}},
		{[]string{"help", "who"}, []string{"who", "--help"}},
		{[]string{"help", "serve"}, []string{"serve", "--help"}},
	} {
		var o1, e1, o2, e2 bytes.Buffer
		c1 := run(tc.a, envFrom(nil), strings.NewReader(""), &o1, &e1)
		c2 := run(tc.b, envFrom(nil), strings.NewReader(""), &o2, &e2)
		if c1 != 0 || c2 != 0 || e1.Len() != 0 || e2.Len() != 0 || o1.Len() == 0 || o1.String() != o2.String() {
			t.Errorf("%v: %d %q %q; %v: %d %q", tc.a, c1, o1.String(), e1.String(), tc.b, c2, o2.String())
		}
	}
	var o, e bytes.Buffer
	if code := run([]string{"help", "nosuch"}, envFrom(nil), strings.NewReader(""), &o, &e); code != 2 || o.Len() != 0 ||
		e.String() != "error: mash has no command nosuch\nhint: mash help\n" {
		t.Errorf("help nosuch: %d %q %q", code, o.String(), e.String())
	}
	// the tool's own help has the table's commands generated from it
	o.Reset()
	run([]string{"help"}, envFrom(nil), strings.NewReader(""), &o, &e)
	for _, want := range []string{"  mash send <address> [<message>] [options]", "Send a message to an endpoint.", "  mash version  ", "Print the version."} {
		if !strings.Contains(o.String(), want) {
			t.Errorf("mash help lacks %q:\n%s", want, o.String())
		}
	}
}
