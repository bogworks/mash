package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"
)

func runStdin(w *world, as, stdin string, args ...string) (out, errb string, code int) {
	var o, e bytes.Buffer
	code = run(args, envFrom(w.env[as]), strings.NewReader(stdin), &o, &e)
	return o.String(), withoutPlainWarning(e.String()), code
}

// A bare "-" as the message reads standard input; text that only starts with "-" stays literal.
func TestSendDashReadsStdin(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, errb, code := runStdin(w, "b", "from a pipe\n", "send", tTo("a"), "-"); code != 0 {
		t.Fatalf("send -: %d %q %q", code, out, errb)
	}
	if rd, _ := w.cli(t, "a", "read", "1"); !strings.Contains(rd, "> from a pipe\n") || strings.Contains(rd, "> -\n") {
		t.Fatalf("read #1: %q", rd)
	}
	_, errb, code := runStdin(w, "b", "x\n", "send", tTo("a"), "-", "--file", "-")
	if code != 2 || !strings.HasPrefix(errb, "error: mash send takes the message text or --file, not both\n") {
		t.Fatalf("- with --file: %d %q", code, errb)
	}
	if _, _, code := runStdin(w, "b", "", "send", tTo("a"), "--", "-x"); code != 0 {
		t.Fatalf("-x: %d", code)
	}
	if _, _, code := runStdin(w, "b", "stdin ignored\n", "send", tTo("a"), "--", "--dashes"); code != 0 {
		t.Fatalf("--dashes: %d", code)
	}
	rd2, _ := w.cli(t, "a", "read", "2")
	rd3, _ := w.cli(t, "a", "read", "3")
	if !strings.Contains(rd2, "> -x\n") || !strings.Contains(rd3, "> --dashes\n") {
		t.Fatalf("literal: %q %q", rd2, rd3)
	}
	if n := inboxCount(t, w, "a"); n != 3 {
		t.Fatalf("a has %d messages, want 3", n)
	}
}

// A blank message is a usage error on the command line and on the MCP tool; nothing is sent.
func TestSendBlankMessageRefused(t *testing.T) {
	w := newWorld(t, "a", "b")
	for _, m := range []string{"   ", "\t \n"} {
		out, errb, code := runStdin(w, "b", "", "send", tTo("a"), m)
		if code != 2 || out != "" || errb != "error: mash send needs the message text\nhint: mash send --help\n" {
			t.Errorf("%q: %d %q %q", m, code, out, errb)
		}
	}
	s := startServe(t, w.env["b"])
	s.initialize(t)
	s.send(t, `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"send","arguments":{"address":"o.dev.a","message":"   "}}}`)
	r := s.await(t, 5*time.Second, idIs(20))["result"].(map[string]any)
	txt := r["content"].([]any)[0].(map[string]any)["text"].(string)
	if r["isError"] != true || !strings.Contains(txt, "message is required") {
		t.Fatalf("mcp blank: %q %v", txt, r["isError"])
	}
	// the old argument names are reported as unknown, with the valid ones listed
	s.send(t, `{"jsonrpc":"2.0","id":`+strconv.Itoa(21)+`,"method":"tools/call","params":{"name":"send","arguments":{"to":"o.dev.a","text":"hi"}}}`)
	r = s.await(t, 5*time.Second, idIs(21))["result"].(map[string]any)
	txt = r["content"].([]any)[0].(map[string]any)["text"].(string)
	if r["isError"] != true || !strings.Contains(txt, "unknown argument text") && !strings.Contains(txt, "unknown argument to") ||
		!strings.Contains(txt, "the arguments are address, message, kind, urgent, reply_to, relay, id") {
		t.Fatalf("mcp old names: %q", txt)
	}
	if n := inboxCount(t, w, "a"); n != 0 {
		t.Fatalf("a has %d messages, want 0", n)
	}
}

// A bad --id is a usage error (exit 2) even when no endpoint is configured.
func TestSendBadIDWithNoEndpoint(t *testing.T) {
	var o, e bytes.Buffer
	env := envFrom(map[string]string{"HOME": t.TempDir()})
	code := run([]string{"send", "o.dev.a", "x", "--id", "has space"}, env, strings.NewReader(""), &o, &e)
	if code != 2 || o.String() != "" || !strings.Contains(e.String(), "send id \"has space\" must be printable ASCII") {
		t.Fatalf("%d %q %q", code, o.String(), e.String())
	}
	o.Reset()
	e.Reset()
	if code := run([]string{"send", "o.dev.a", "x"}, env, strings.NewReader(""), &o, quiet(&e)); code != 2 || e.String() != "error: no endpoint is installed here\nhint: mash join <file>\n" {
		t.Fatalf("no endpoint: %d %q", code, e.String())
	}
}
