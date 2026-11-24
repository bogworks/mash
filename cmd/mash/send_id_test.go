package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func inboxCount(t *testing.T, w *world, as string) int {
	t.Helper()
	out, code := w.cli(t, as, "inbox", "--json") // the text says "about" without serve running (#958)
	if code != 0 {
		t.Fatalf("inbox: %d %s", code, out)
	}
	var o struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("inbox: no count in %q", out)
	}
	return o.Total
}

// A CLI send re-run with the same --id lands once and the second run succeeds, naming the original message.
func TestCLISendIDRerunLandsOnce(t *testing.T) {
	w := newWorld(t, "a", "b")
	out, code := w.cli(t, "b", "send", tTo("a"), "--id", "job-7", "ship it")
	if code != 0 || !isReceipt(out, "Mail sent") {
		t.Fatalf("first send: %d %q", code, out)
	}
	out, code = w.cli(t, "b", "send", tTo("a"), "--id=job-7", "ship it")
	if code != 0 || out != "Mail already sent; nothing new stored.\n" {
		t.Fatalf("second send: %d %q, want success naming #1 as a duplicate", code, out)
	}
	if n := inboxCount(t, w, "a"); n != 1 {
		t.Fatalf("inbox has %d messages, want 1", n)
	}
	// A different key is a different message; no key is a fresh one each time, printed exactly as before.
	if out, code = w.cli(t, "b", "send", tTo("a"), "--id", "job-8", "ship it"); code != 0 {
		t.Fatalf("send job-8: %d %q", code, out)
	}
	out, code = w.cli(t, "b", "send", tTo("a"), "ship it")
	if code != 0 || !isReceipt(out, "Mail sent") {
		t.Fatalf("plain send: %d %q", code, out)
	}
}

// --id composes with the other send flags and rejects bad keys with a usage-style error and no message sent.
func TestCLISendIDFlagsAndValidation(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.cli(t, "b", "send", tTo("a"), "--kind", "fyi", "--id", "k", "--urgent", "hello"); code != 0 {
		t.Fatalf("combined flags: %d %q", code, out)
	}
	for _, bad := range [][]string{
		{"send", tTo("a"), "--id", "has space", "x"},
		{"send", tTo("a"), "--id", strings.Repeat("k", 129), "x"},
		{"send", tTo("a"), "--id", "", "x"},
		{"send", tTo("a"), "--id"},
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
	if !ok || id["type"] != "string" || !strings.Contains(id["description"].(string), "same key") {
		t.Fatalf("send tool schema has no documented id: %v", props)
	}
	call := func(n float64, args string) (string, bool) {
		s.send(t, `{"jsonrpc":"2.0","id":`+strconv.Itoa(int(n))+`,"method":"tools/call","params":{"name":"send","arguments":`+args+`}}`)
		r := s.await(t, 5*time.Second, idIs(n))["result"].(map[string]any)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"] == true
	}
	first, bad := call(10, `{"address":"o.dev.b","message":"hi","id":"mcp-1"}`)
	if bad || !isReceipt(first, "Mail sent") {
		t.Fatalf("first: %q %v", first, bad)
	}
	second, bad := call(11, `{"address":"o.dev.b","message":"hi","id":"mcp-1"}`)
	if bad || second != "Mail already sent; nothing new stored." {
		t.Fatalf("second: %q %v", second, bad)
	}
	if bad, isErr := call(12, `{"address":"o.dev.b","message":"hi","id":"has space"}`); !isErr || !strings.Contains(bad, "id") {
		t.Fatalf("bad id: %q %v", bad, isErr)
	}
	if n := inboxCount(t, w, "b"); n != 1 {
		t.Fatalf("inbox has %d messages, want 1", n)
	}
}
