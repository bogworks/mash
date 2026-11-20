package main

import (
	"strings"
	"testing"
)

// `mash send --kind K` sets the kind (case-insensitive, with --urgent in either order); an unknown kind is a usage
// error (exit 2) and sends nothing.
func TestSendKindFromTheCLI(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.cli(t, "b", "send", tTo("a"), "--kind", "Question", "which one"); code != 0 || !strings.Contains(out, "Question mail sent (number ") {
		t.Fatalf("send: %d %s", code, out)
	}
	if out, code := w.cli(t, "b", "send", tTo("a"), "--urgent", "--kind=blocker", "stuck"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if out, _ := w.cli(t, "a", "read", "1"); !strings.Contains(out, "\nkind:     question\n") {
		t.Fatalf("read #1: %s", out)
	}
	if out, _ := w.cli(t, "a", "read", "2"); !strings.Contains(out, "\nkind:     blocker\n") || !strings.Contains(out, "\nurgent:   yes\n") || !strings.HasSuffix(out, "\n\nstuck\n") {
		t.Fatalf("read #2: %s", out)
	}
	out, code := w.cli(t, "b", "send", tTo("a"), "--kind", "urgent", "x")
	if code != 2 || !strings.Contains(out, "blocker, question, task, result, fyi") {
		t.Fatalf("unknown kind: %d %s", code, out)
	}
	if out, code := w.cli(t, "b", "send", tTo("a"), "--kind"); code != 2 {
		t.Fatalf("--kind without a value: %d %s", code, out)
	}
	if out, _ := w.cli(t, "a", "inbox"); !strings.HasPrefix(out, "2 messages for ") || strings.Contains(out, " x\n") {
		t.Fatalf("a refused send was delivered:\n%s", out)
	}
}

// The MCP send tool takes `kind` (an enum in its schema); an unknown kind is an error naming the allowed ones; a short
// reply with no question is tagged fyi and says so; a blocker is never downgraded.
func TestSendKindFromTheMCPTool(t *testing.T) {
	w := newWorld(t, "a", "b")
	s := startServe(t, w.env["b"])
	s.initialize(t)
	var enum []any
	for _, tl := range s.tools(t) {
		if m := tl.(map[string]any); m["name"] == "send" {
			p := m["inputSchema"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)
			enum = p["enum"].([]any)
			if d := p["description"].(string); !strings.Contains(d, "wake an idle recipient") {
				t.Fatalf("kind description %q", d)
			}
		}
	}
	if len(enum) != 5 {
		t.Fatalf("kind enum %v", enum)
	}
	send := func(id int, args string) (string, bool) {
		r := s.call(t, id, "send", args)
		return resultText(r), r["isError"] == true
	}
	if txt, bad := send(10, `{"address":"o.dev.a","message":"do it","kind":"task"}`); bad || !isReceipt(txt, "Mail sent") {
		t.Fatalf("task: %v %s", bad, txt)
	}
	if txt, bad := send(11, `{"address":"o.dev.a","message":"ok, done","reply_to":1,"kind":"question"}`); bad || !isReceipt(txt, "Mail sent as fyi", ": a reply marked question that asks nothing.") {
		t.Fatalf("tagged: %v %s", bad, txt)
	}
	if txt, bad := send(12, `{"address":"o.dev.a","message":"ok?","reply_to":1,"kind":"question"}`); bad || !isReceipt(txt, "Question mail sent") {
		t.Fatalf("question mark: %v %s", bad, txt)
	}
	if txt, bad := send(13, `{"address":"o.dev.a","message":"stuck","reply_to":1,"kind":"blocker"}`); bad || !isReceipt(txt, "Blocker mail sent") {
		t.Fatalf("blocker: %v %s", bad, txt)
	}
	if txt, bad := send(14, `{"address":"o.dev.a","message":"hi","kind":"loud"}`); !bad || !strings.Contains(txt, "blocker, question, task, result, fyi") {
		t.Fatalf("unknown kind: %v %s", bad, txt)
	}
	for seq, want := range map[string]string{"1": "task", "2": "fyi", "3": "question", "4": "blocker"} {
		if out, _ := w.cli(t, "a", "read", seq); !strings.Contains(out, "\nkind:     "+want+"\n") {
			t.Fatalf("#%s is not %s:\n%s", seq, want, out)
		}
	}
	if out, _ := w.cli(t, "a", "inbox"); !strings.HasPrefix(out, "4 messages for ") || strings.HasSuffix(strings.TrimRight(out, "\n"), " hi") { // the text column
		t.Fatalf("a refused send was delivered:\n%s", out)
	}
}

// A CLI send with no --kind is task; an explicit --kind fyi stays fyi.
func TestUnkindedCLISendIsTask(t *testing.T) {
	w := newWorld(t, "a", "b")
	for _, args := range [][]string{{"send", tTo("a"), "plain"}, {"send", tTo("a"), "--kind", "fyi", "quiet"}} {
		if out, code := w.cli(t, "b", args...); code != 0 {
			t.Fatalf("%v: %d %s", args, code, out)
		}
	}
	for seq, want := range map[string]string{"1": "task", "2": "fyi"} {
		if out, _ := w.cli(t, "a", "read", seq); !strings.Contains(out, "\nkind:     "+want+"\n") {
			t.Fatalf("#%s is not %s:\n%s", seq, want, out)
		}
	}
}

// An MCP send with no kind is task, a task reply with no '?' is not downgraded, and the schema names the default.
func TestUnkindedMCPSendIsTask(t *testing.T) {
	w := newWorld(t, "a", "b")
	s := startServe(t, w.env["b"])
	s.initialize(t)
	for id, args := range []string{`{"address":"o.dev.a","message":"plain"}`, `{"address":"o.dev.a","message":"ok, done","reply_to":1}`} {
		if r := s.call(t, 20+id, "send", args); r["isError"] == true || !isReceipt(resultText(r), "Mail sent") {
			t.Fatalf("%s: %v", args, resultText(r))
		}
	}
	for _, seq := range []string{"1", "2"} {
		if out, _ := w.cli(t, "a", "read", seq); !strings.Contains(out, "\nkind:     task\n") {
			t.Fatalf("#%s is not task:\n%s", seq, out)
		}
	}
	for _, tl := range s.tools(t) {
		if m := tl.(map[string]any); m["name"] == "send" {
			if d := m["inputSchema"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["description"].(string); !strings.Contains(d, "Default: task") {
				t.Fatalf("kind description %q", d)
			}
		}
	}
}

// The MCP send tool takes an optional integer `relay` (documented in the schema), signs it with reply_to, and a
// recipient sees "relayed from <origin> #N" and "answers #N" only because both verify. A relay with no such message in the
// sender's inbox, or a value that is not a positive integer, is a tool error and sends nothing.
func TestSendViaFromTheMCPTool(t *testing.T) {
	w := newWorld(t, "a", "b", "c")
	if out, code := w.cli(t, "c", "send", tTo("b"), "the original"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	s := startServe(t, w.env["b"])
	s.initialize(t)
	var via map[string]any
	for _, tl := range s.tools(t) {
		if m := tl.(map[string]any); m["name"] == "send" {
			via, _ = m["inputSchema"].(map[string]any)["properties"].(map[string]any)["relay"].(map[string]any)
		}
	}
	if via == nil || via["type"] != "integer" || !strings.Contains(via["description"].(string), "passes on") {
		t.Fatalf("send schema via: %v", via)
	}
	if r := s.call(t, 30, "send", `{"address":"o.dev.a","message":"fwd","relay":1,"reply_to":9}`); r["isError"] == true || !isReceipt(resultText(r), "Mail sent") {
		t.Fatalf("send: %v", resultText(r))
	}
	out, _ := w.cli(t, "a", "read", "1")
	if !strings.Contains(out, "\ntrust:    verified\n") || !strings.Contains(out, "\nreply to: 9\nrelay:    o.dev.c #1\n") || !strings.HasSuffix(out, "\n\nfwd\n") {
		t.Fatalf("read #1:\n%s", out)
	}
	for i, args := range []string{`{"address":"o.dev.a","message":"x","relay":7}`, `{"address":"o.dev.a","message":"x","relay":-1}`, `{"address":"o.dev.a","message":"x","relay":"L1"}`, `{"address":"o.dev.a","message":"x","relay":1.5}`} {
		if r := s.call(t, 31+i, "send", args); r["isError"] != true {
			t.Fatalf("%s: not an error: %v", args, resultText(r))
		}
	}
	if out, _ := w.cli(t, "a", "read", "2"); strings.Contains(out, "\n\nx") {
		t.Fatalf("a refused send was delivered:\n%s", out)
	}
}
