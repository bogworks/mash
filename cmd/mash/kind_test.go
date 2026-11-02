package main

import (
	"strings"
	"testing"
)

// `mash send --kind K` sets the kind (case-insensitive, with --urgent in either order); an unknown kind is a usage
// error (exit 2) and sends nothing.
func TestSendKindFromTheCLI(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.cli(t, "b", "send", "a", "--kind", "Question", "which one"); code != 0 || !strings.Contains(out, "sent #1 from b to a") {
		t.Fatalf("send: %d %s", code, out)
	}
	if out, code := w.cli(t, "b", "send", "a", "--urgent", "--kind=blocker", "stuck"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if out, _ := w.cli(t, "a", "read", "1"); !strings.Contains(out, "\nkind: question\n> which one") {
		t.Fatalf("read #1: %s", out)
	}
	if out, _ := w.cli(t, "a", "read", "2"); !strings.Contains(out, "\nkind: blocker\n> stuck") {
		t.Fatalf("read #2: %s", out)
	}
	out, code := w.cli(t, "b", "send", "a", "--kind", "urgent", "x")
	if code != 2 || !strings.Contains(out, "blocker|question|task|result|fyi") {
		t.Fatalf("unknown kind: %d %s", code, out)
	}
	if out, code := w.cli(t, "b", "send", "a", "--kind"); code != 2 {
		t.Fatalf("--kind without a value: %d %s", code, out)
	}
	if out, _ := w.cli(t, "a", "inbox"); strings.Count(out, "\n") > 2 || strings.Contains(out, "\tx\t") {
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
	if txt, bad := send(10, `{"to":"a","text":"do it","kind":"task"}`); bad || txt != "sent #1 to a" {
		t.Fatalf("task: %v %s", bad, txt)
	}
	if txt, bad := send(11, `{"to":"a","text":"ok, done","in_reply_to":1,"kind":"question"}`); bad || txt != "sent #2 to a (tagged fyi: short reply with no question)" {
		t.Fatalf("tagged: %v %s", bad, txt)
	}
	if txt, bad := send(12, `{"to":"a","text":"ok?","in_reply_to":1,"kind":"question"}`); bad || txt != "sent #3 to a" {
		t.Fatalf("question mark: %v %s", bad, txt)
	}
	if txt, bad := send(13, `{"to":"a","text":"stuck","in_reply_to":1,"kind":"blocker"}`); bad || txt != "sent #4 to a" {
		t.Fatalf("blocker: %v %s", bad, txt)
	}
	if txt, bad := send(14, `{"to":"a","text":"hi","kind":"loud"}`); !bad || !strings.Contains(txt, "blocker|question|task|result|fyi") {
		t.Fatalf("unknown kind: %v %s", bad, txt)
	}
	for seq, want := range map[string]string{"1": "task", "2": "fyi", "3": "question", "4": "blocker"} {
		if out, _ := w.cli(t, "a", "read", seq); !strings.Contains(out, "\nkind: "+want+"\n") {
			t.Fatalf("#%s is not %s:\n%s", seq, want, out)
		}
	}
	if out, _ := w.cli(t, "a", "inbox"); strings.Contains(out, "B\thi\t") { // the text column; a bare "hi" matches inside a random instance id
		t.Fatalf("a refused send was delivered:\n%s", out)
	}
}

// A CLI send with no --kind is task; an explicit --kind fyi stays fyi.
func TestUnkindedCLISendIsTask(t *testing.T) {
	w := newWorld(t, "a", "b")
	for _, args := range [][]string{{"send", "a", "plain"}, {"send", "a", "--kind", "fyi", "quiet"}} {
		if out, code := w.cli(t, "b", args...); code != 0 {
			t.Fatalf("%v: %d %s", args, code, out)
		}
	}
	for seq, want := range map[string]string{"1": "task", "2": "fyi"} {
		if out, _ := w.cli(t, "a", "read", seq); !strings.Contains(out, "\nkind: "+want+"\n") {
			t.Fatalf("#%s is not %s:\n%s", seq, want, out)
		}
	}
}

// An MCP send with no kind is task, a task reply with no '?' is not downgraded, and the schema names the default.
func TestUnkindedMCPSendIsTask(t *testing.T) {
	w := newWorld(t, "a", "b")
	s := startServe(t, w.env["b"])
	s.initialize(t)
	for id, args := range []string{`{"to":"a","text":"plain"}`, `{"to":"a","text":"ok, done","in_reply_to":1}`} {
		if r := s.call(t, 20+id, "send", args); r["isError"] == true || strings.Contains(resultText(r), "tagged") {
			t.Fatalf("%s: %v", args, resultText(r))
		}
	}
	for _, seq := range []string{"1", "2"} {
		if out, _ := w.cli(t, "a", "read", seq); !strings.Contains(out, "\nkind: task\n") {
			t.Fatalf("#%s is not task:\n%s", seq, out)
		}
	}
	for _, tl := range s.tools(t) {
		if m := tl.(map[string]any); m["name"] == "send" {
			if d := m["inputSchema"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["description"].(string); !strings.Contains(d, "default task") {
				t.Fatalf("kind description %q", d)
			}
		}
	}
}
