package main

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func inboxCount(t *testing.T, w *world, as string) int {
	t.Helper()
	out, code := w.cli(t, as, "inbox")
	if code != 0 {
		t.Fatalf("inbox: %d %s", code, out)
	}
	if strings.TrimSpace(out) == "inbox empty" {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(out), "\n"))
}

// A CLI send re-run with the same --id lands once and the second run succeeds, naming the original message.
func TestCLISendIDRerunLandsOnce(t *testing.T) {
	w := newWorld(t, "a", "b")
	out, code := w.cli(t, "b", "send", "a", "--id", "job-7", "ship it")
	if code != 0 || !strings.Contains(out, "sent #1 from b to a") || strings.Contains(out, "duplicate") {
		t.Fatalf("first send: %d %q", code, out)
	}
	out, code = w.cli(t, "b", "send", "a", "--id=job-7", "ship it")
	if code != 0 || !strings.Contains(out, "#1") || !strings.Contains(out, "duplicate") {
		t.Fatalf("second send: %d %q, want success naming #1 as a duplicate", code, out)
	}
	if n := inboxCount(t, w, "a"); n != 1 {
		t.Fatalf("inbox has %d messages, want 1", n)
	}
	// A different key is a different message; no key is a fresh one each time, printed exactly as before.
	if out, code = w.cli(t, "b", "send", "a", "--id", "job-8", "ship it"); code != 0 {
		t.Fatalf("send job-8: %d %q", code, out)
	}
	out, code = w.cli(t, "b", "send", "a", "ship it")
	if code != 0 || out != "sent #3 from b to a\n" {
		t.Fatalf("plain send: %d %q", code, out)
	}
}

// --id composes with the other send flags and rejects bad keys with a usage-style error and no message sent.
func TestCLISendIDFlagsAndValidation(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.cli(t, "b", "send", "a", "--kind", "fyi", "--id", "k", "--urgent", "hello"); code != 0 {
		t.Fatalf("combined flags: %d %q", code, out)
	}
	for _, bad := range [][]string{
		{"send", "a", "--id", "has space", "x"},
		{"send", "a", "--id", strings.Repeat("k", 129), "x"},
		{"send", "a", "--id", "", "x"},
		{"send", "a", "--id"},
	} {
		out, code := w.cli(t, "b", bad...)
		if code == 0 || !strings.Contains(out, "id") {
			t.Fatalf("%q: want a clear error, got %d %q", bad, code, out)
		}
	}
	if n := inboxCount(t, w, "a"); n != 1 {
		t.Fatalf("inbox has %d messages, want only the valid one", n)
	}
}

// The MCP send tool takes an optional `id`, documents it, and de-duplicates on it.
func TestServeSendToolID(t *testing.T) {
	w := newWorld(t, "a", "b")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	var props map[string]any
	for _, tl := range s.tools(t) {
		m := tl.(map[string]any)
		if m["name"] == "send" {
			props = m["inputSchema"].(map[string]any)["properties"].(map[string]any)
		}
	}
	id, ok := props["id"].(map[string]any)
	if !ok || id["type"] != "string" || !strings.Contains(id["description"].(string), "once") {
		t.Fatalf("send tool schema has no documented id: %v", props)
	}
	call := func(n float64, args string) (string, bool) {
		s.send(t, `{"jsonrpc":"2.0","id":`+strconv.Itoa(int(n))+`,"method":"tools/call","params":{"name":"send","arguments":`+args+`}}`)
		r := s.await(t, 5*time.Second, idIs(n))["result"].(map[string]any)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"] == true
	}
	first, bad := call(10, `{"to":"b","text":"hi","id":"mcp-1"}`)
	if bad || !strings.HasPrefix(first, "sent #1 to b") {
		t.Fatalf("first: %q %v", first, bad)
	}
	second, bad := call(11, `{"to":"b","text":"hi","id":"mcp-1"}`)
	if bad || !strings.Contains(second, "#1") || !strings.Contains(second, "duplicate") {
		t.Fatalf("second: %q %v", second, bad)
	}
	if bad, isErr := call(12, `{"to":"b","text":"hi","id":"has space"}`); !isErr || !strings.Contains(bad, "id") {
		t.Fatalf("bad id: %q %v", bad, isErr)
	}
	if n := inboxCount(t, w, "b"); n != 1 {
		t.Fatalf("inbox has %d messages, want 1", n)
	}
}
