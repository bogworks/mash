package main

import (
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
)

// statusPeer answers status requests for endpoint name with body; a nil body subscribes and never answers.
func (w *world) statusPeer(t *testing.T, name string, body []byte) {
	t.Helper()
	nc := w.e.Conn(t, w.e.AdminCreds)
	if _, err := nc.Subscribe(sap.For(w.e.Owner, name).Status, func(m *nats.Msg) {
		if body != nil {
			_ = m.Respond(body)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
}

// The MCP send tool's receipt adds the recipient's self-reported state for kinds that wake (and urgent sends), and
// never for fyi or result; a status request nobody answers costs the receipt about a second, not a failure.
func TestServeSendReceiptRecipientState(t *testing.T) {
	w := newWorld(t, "a", "b", "c", "d")
	w.statusPeer(t, "b", statusJSON(`,"program_status":{"state":"blocked","kind":"question"}`))
	w.statusPeer(t, "c", statusJSON(""))
	w.statusPeer(t, "d", nil)
	s := startServe(t, w.env["a"])
	s.initialize(t)
	n := 10
	call := func(args string) (string, bool, time.Duration) {
		n++
		start := time.Now()
		s.send(t, `{"jsonrpc":"2.0","id":`+strconv.Itoa(n)+`,"method":"tools/call","params":{"name":"send","arguments":`+args+`}}`)
		r := s.await(t, 5*time.Second, idIs(float64(n)))["result"].(map[string]any)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"] == true, time.Since(start)
	}
	out, bad, _ := call(`{"to":"b","text":"why?","kind":"question"}`)
	if bad || !strings.HasPrefix(out, "sent #1 to b\nrecipient: blocked (question); your ask will wait until it is answered there") {
		t.Fatalf("question: %q %v", out, bad)
	}
	out, bad, _ = call(`{"to":"c","text":"do it"}`)
	if bad || !strings.HasPrefix(out, "sent #1 to c\nrecipient: online, not reporting what it is doing") {
		t.Fatalf("task: %q %v", out, bad)
	}
	for _, args := range []string{`{"to":"b","text":"note","kind":"fyi"}`, `{"to":"b","text":"done","kind":"result"}`} {
		if out, bad, _ = call(args); bad || strings.Contains(out, "recipient:") || !strings.HasPrefix(out, "sent #") {
			t.Fatalf("%s: %q %v", args, out, bad)
		}
	}
	if out, bad, _ = call(`{"to":"b","text":"now","kind":"fyi","urgent":true}`); bad || !strings.Contains(out, "\nrecipient: blocked (question)") {
		t.Fatalf("urgent fyi: %q %v", out, bad)
	}
	out, bad, took := call(`{"to":"d","text":"anyone?"}`)
	if bad || !strings.HasPrefix(out, "sent #1 to d\nrecipient: ") ||
		!(strings.Contains(out, "state unknown") || strings.Contains(out, "no answer")) || took > 1500*time.Millisecond {
		t.Fatalf("silent peer: %q %v after %v", out, bad, took)
	}
	// A failed send reports the failure and no state line.
	w.statusPeer(t, "ghost", statusJSON(`,"program_status":{"state":"working"}`))
	if out, bad, _ = call(`{"to":"ghost","text":"hi"}`); !bad || strings.Contains(out, "recipient:") {
		t.Fatalf("failed send: %q %v", out, bad)
	}
}

// The CLI send output is a compatibility contract (ADR 0034): no state line.
func TestCLISendOutputHasNoRecipientState(t *testing.T) {
	w := newWorld(t, "a", "b")
	w.statusPeer(t, "a", statusJSON(`,"program_status":{"state":"working"}`))
	if out, code := w.cli(t, "b", "send", "a", "hi"); code != 0 || out != "sent #1 from b to a\n" {
		t.Fatalf("%d %q", code, out)
	}
}

// firstLine is the receipt text proper, without the advisory recipient-state line that may follow it.
func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }

// A send the name rule refuses, or to a name that is no endpoint name, is refused before anything reaches the
// recipient: the status request that rides alongside a send is never made.
func TestServeSendRefusedMakesNoStatusRequest(t *testing.T) {
	w := newWorld(t, "a", "blocked")
	rule := configARuleFile(t, t.TempDir(), "name-rule.json",
		`{"version":1,"rules":[{"id":"no-blocked","match":{"name":{"is":"blocked"}},"message":"blocked is retired"}]}`)
	env := map[string]string{"MASH_NAME_RULE_FILE": rule}
	for k, v := range w.env["a"] {
		env[k] = v
	}
	var asked atomic.Int32
	nc := w.e.Conn(t, w.e.AdminCreds)
	for _, name := range []string{"blocked", "UPPER"} {
		if _, err := nc.Subscribe(sap.For(w.e.Owner, name).Status, func(m *nats.Msg) {
			asked.Add(1)
			_ = m.Respond(statusJSON(""))
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	s := startServe(t, env)
	s.initialize(t)
	for i, c := range []struct{ args, want string }{
		{`{"to":"blocked","text":"hi"}`, "blocked is retired"},
		{`{"to":"UPPER","text":"hi"}`, ""},
	} {
		id := 100 + i
		s.send(t, `{"jsonrpc":"2.0","id":`+strconv.Itoa(id)+`,"method":"tools/call","params":{"name":"send","arguments":`+c.args+`}}`)
		r := s.await(t, 5*time.Second, idIs(float64(id)))["result"].(map[string]any)
		out := r["content"].([]any)[0].(map[string]any)["text"].(string)
		if r["isError"] != true || strings.Contains(out, "\nrecipient:") || !strings.Contains(out, c.want) {
			t.Fatalf("%s: %q", c.args, out)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if n := asked.Load(); n != 0 {
		t.Fatalf("a refused send made %d status requests", n)
	}
}
