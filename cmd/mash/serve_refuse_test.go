package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// publishUnsigned stores a message with no sender header in a's inbox, as b's connection.
func publishUnsigned(t *testing.T, w *world, to, from, body string) {
	t.Helper()
	nc := w.e.ConnAs(t, w.env[from]["MASH_CREDS"], tAddr(from).Flat())
	if _, err := nc.Request(tAddr(to).Subjects().Mail, []byte(body), 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

// Through serve: unsigned mail is not delivered, inbox and read (the shell door) do not show it, doctor counts it, the
// sender is told nothing. With --accept-unverified, and with MASH_ACCEPT_UNVERIFIED, it is delivered labelled.
func TestServeRefusesUnverifiedMailAndDoctorCountsIt(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_STATE_DIR"] = filepath.Join(t.TempDir(), "state")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "a", "who"); return whoShows(out, tTo("a"), "online") })
	publishUnsigned(t, w, "a", "b", "unsigned body")
	publishUnsigned(t, w, "a", "b", "unsigned again")
	eventually(t, 10*time.Second, "doctor counts them", func() bool {
		out, _ := w.cli(t, "a", "doctor")
		return strings.Contains(out, "refused   warn  2 unverified messages were refused\n")
	})
	if _, code := w.cli(t, "b", "send", tTo("a"), "signed body"); code != 0 {
		t.Fatal("send failed")
	}
	if got := content(s.await(t, 10*time.Second, isChannel)); !strings.Contains(got, "signed body") || strings.Contains(got, "unsigned") {
		t.Fatalf("channel event %q", got)
	}
	out, _ := w.cli(t, "a", "inbox")
	if !strings.Contains(out, "1 message for") || strings.Contains(out, "unsigned") {
		t.Fatalf("inbox:\n%s", out)
	}
	const refusedText = "error: message 1 was refused: it was not verified\nhint: mash doctor\n"
	if out, code := w.cli(t, "a", "read", "1"); code != 1 || out != refusedText {
		t.Fatalf("read of refused mail: %d %q", code, out)
	}
	if out, code := w.cli(t, "a", "read", "1", "--json"); code != 1 || out != "{\"error\":\"message 1 was refused: it was not verified\",\"hint\":\"mash doctor\"}\n" {
		t.Fatalf("read of refused mail, json: %d %q", code, out)
	}
	if r := s.call(t, 90, "read", `{"number":1}`); r["isError"] != true || resultText(r) != strings.TrimSuffix(refusedText, "\n") {
		t.Fatalf("read tool on refused mail: %v", r)
	}
	if out, code := w.cli(t, "a", "read", "99"); code != 1 || out != "error: o.dev.a has no message 99\nhint: mash inbox\n" {
		t.Fatalf("read of a number that never existed: %d %q", code, out)
	}
	for _, args := range [][]string{{"inbox", "--after", "0"}, {"inbox", "--new"}, {"inbox", "--limit", "10"}} {
		if out, _ := w.cli(t, "a", args...); !strings.Contains(out, "1 message for") || strings.Contains(out, "unsigned") {
			t.Fatalf("%v:\n%s", args, out)
		}
	}
	if out, _ := w.cli(t, "b", "inbox"); strings.Contains(out, "message for") && !strings.Contains(out, "0 messages") && !strings.Contains(out, "No messages") {
		t.Fatalf("the sender has mail:\n%s", out)
	}
}

func TestServeAcceptUnverifiedFlagDelivers(t *testing.T) {
	for _, via := range []string{"flag", "env"} {
		t.Run(via, func(t *testing.T) {
			w := newWorld(t, "a", "b")
			w.env["a"]["MASH_STATE_DIR"] = filepath.Join(t.TempDir(), "state")
			args := []string{"serve"}
			if via == "flag" {
				args = []string{"serve", "--accept-unverified"}
			} else {
				w.env["a"]["MASH_ACCEPT_UNVERIFIED"] = "1"
			}
			s := startServeArgs(t, w.env["a"], args...)
			s.initialize(t)
			s.tools(t)
			eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "a", "who"); return whoShows(out, tTo("a"), "online") })
			publishUnsigned(t, w, "a", "b", "unsigned body")
			if got := content(s.await(t, 10*time.Second, isChannel)); !strings.Contains(got, "unsigned body") || !strings.Contains(got, "[absent]") {
				t.Fatalf("channel event %q", got)
			}
		})
	}
}

// serveFlags takes the flag out of serve's arguments and shadows the file and the environment; other commands are untouched.
func TestServeFlagsShadowTheEnvironment(t *testing.T) {
	env := func(k string) string {
		if k == "MASH_ACCEPT_UNVERIFIED" {
			return "0"
		}
		return ""
	}
	lookup := func(k string) (string, bool) { v := env(k); return v, v != "" }
	args, env2, lookup2, _ := serveFlags([]string{"serve", "--accept-unverified"}, env, lookup)
	if len(args) != 1 || args[0] != "serve" || env2("MASH_ACCEPT_UNVERIFIED") != "1" {
		t.Fatalf("serve: %v %q", args, env2("MASH_ACCEPT_UNVERIFIED"))
	}
	if v, ok := lookup2("MASH_ACCEPT_UNVERIFIED"); !ok || v != "1" {
		t.Fatalf("lookup %q %v", v, ok)
	}
	if v, _ := lookup2("OTHER"); v != "" {
		t.Fatal(v)
	}
	args, env3, _, _ := serveFlags([]string{"serve"}, env, lookup)
	if len(args) != 1 || env3("MASH_ACCEPT_UNVERIFIED") != "0" {
		t.Fatal("no flag: nothing changes")
	}
	args, env4, _, _ := serveFlags([]string{"inbox", "--accept-unverified"}, env, lookup)
	if len(args) != 2 || env4("MASH_ACCEPT_UNVERIFIED") != "0" {
		t.Fatal("only serve takes the flag")
	}
}

// serve takes no argument but --accept-unverified (and the global options): a wrong command line is a usage error.
func TestServeRefusesUnknownArguments(t *testing.T) {
	for args, want := range map[string]string{"--x": "error: unknown option --x\n", "-q": "error: unknown option -q\n"} {
		var out, errb bytes.Buffer
		if code := run([]string{"serve", args}, func(string) string { return "" }, strings.NewReader(""), &out, &errb); code != 2 ||
			!strings.HasPrefix(errb.String(), want) || out.Len() != 0 {
			t.Errorf("serve %s: %d %q %q", args, code, out.String(), errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "stray"}, func(string) string { return "" }, strings.NewReader(""), &out, &errb); code != 2 ||
		!strings.HasPrefix(errb.String(), "error: serve takes no argument stray") {
		t.Errorf("serve stray: %d %q", code, errb.String())
	}
}
