package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/spool"
)

// hooklessWorld starts a session spooled from the start (as the plugin starts it) with no hook ever run, and the
// detector's knobs shortened.
func hooklessWorld(t *testing.T, grace time.Duration) (*world, *session) {
	t.Helper()
	og, ow := endpoint.HookGrace, endpoint.HooklessWakes
	endpoint.HookGrace, endpoint.HooklessWakes = grace, 1
	t.Cleanup(func() { endpoint.HookGrace, endpoint.HooklessWakes = og, ow })
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_SPOOL_FROM_START"] = "1"
	s := startServe(t, w.env["a"])
	s.initialize(t)
	return w, s
}

func sendAndWaitSpooled(t *testing.T, w *world, body string) spool.Entry {
	t.Helper()
	sp := spoolFor(w, "a")
	if out, code := w.cli(t, "b", "send", tTo("a"), body); code != 0 {
		t.Fatalf("send: %s", out)
	}
	var e []spool.Entry
	eventually(t, 10*time.Second, "serve spooled "+body, func() bool { e, _ = sp.Waiting(); return len(e) == 1 })
	return e[0]
}

// The read tool acks a held message only once the session is proven hookless; the inbox tool never does.
func TestReadToolAcksOnlyWhenProvenHookless(t *testing.T) {
	w, s := hooklessWorld(t, 150*time.Millisecond)
	sp := spoolFor(w, "a")
	e := sendAndWaitSpooled(t, w, "the body")
	seqArg := `{"number":` + itoa(int(e.Seq)) + `}`
	held := func(what string) {
		t.Helper()
		if ws, _ := sp.Waiting(); len(ws) != 1 || unreadOf(t, w, "a") != 1 {
			t.Fatalf("%s: waiting %d, unread %d, want the message held and unacked", what, len(ws), unreadOf(t, w, "a"))
		}
	}
	// The first tool call of the session: nothing yet shows that no hook runs.
	if out := resultText(s.call(t, 20, "read", seqArg)); !strings.Contains(out, "the body") {
		t.Fatalf("read: %s", out)
	}
	held("first read")
	time.Sleep(endpoint.HookGrace + 100*time.Millisecond)
	// Proven by now, but inbox never acks.
	if out := resultText(s.call(t, 21, "inbox", `{}`)); !strings.Contains(out, "\n"+itoa(int(e.Seq))+" ") || !strings.Contains(out, "the body") {
		t.Fatalf("inbox: %s", out)
	}
	held("inbox")
	if out := resultText(s.call(t, 22, "read", seqArg)); !strings.Contains(out, "the body") {
		t.Fatalf("read: %s", out)
	}
	eventually(t, 10*time.Second, "read acked the message", func() bool {
		ws, _ := sp.Waiting()
		return len(ws) == 0 && unreadOf(t, w, "a") == 0
	})
}

// gatedWriter fails every write while down is set: a client that stopped reading.
type gatedWriter struct {
	w    io.Writer
	down atomic.Bool
}

func (g *gatedWriter) Write(p []byte) (int, error) {
	if g.down.Load() {
		return 0, io.ErrClosedPipe
	}
	return g.w.Write(p)
}

// A read whose result cannot be written to the client leaves the message held: the model never got the body, so
// serve must not ack it. A read that is written drains it.
func TestReadToolDoesNotDrainWhenResultWriteFails(t *testing.T) {
	og, ow := endpoint.HookGrace, endpoint.HooklessWakes
	endpoint.HookGrace, endpoint.HooklessWakes = 150*time.Millisecond, 1
	t.Cleanup(func() { endpoint.HookGrace, endpoint.HooklessWakes = og, ow })
	w := newWorld(t, "a", "b")
	w.env["a"]["MASH_SPOOL_FROM_START"] = "1"
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	gw := &gatedWriter{w: outW}
	s := &session{in: inW, lines: make(chan map[string]any, 100), done: make(chan int, 1)}
	go func() { s.done <- run([]string{"serve"}, envFrom(w.env["a"]), inR, gw, &s.errs); outW.Close() }()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				s.lines <- m
			}
		}
		close(s.lines)
	}()
	t.Cleanup(func() {
		inW.Close()
		select {
		case <-s.done:
		case <-time.After(5 * time.Second):
			t.Error("serve did not exit when stdin closed")
		}
	})
	s.initialize(t)
	sp := spoolFor(w, "a")
	e := sendAndWaitSpooled(t, w, "the body")
	seqArg := `{"number":` + itoa(int(e.Seq)) + `}`
	s.call(t, 30, "inbox", `{}`)
	time.Sleep(endpoint.HookGrace + 100*time.Millisecond)
	s.call(t, 31, "inbox", `{}`) // the second call, far enough after the first: proven hookless
	gw.down.Store(true)
	s.send(t, `{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"read","arguments":`+seqArg+`}}`)
	time.Sleep(500 * time.Millisecond) // the call ran and its write failed
	gw.down.Store(false)
	if ws, _ := sp.Waiting(); len(ws) != 1 || unreadOf(t, w, "a") != 1 {
		t.Fatalf("after a failed write: waiting %d, unread %d, want the message still held and unacked", len(ws), unreadOf(t, w, "a"))
	}
	if out := resultText(s.call(t, 33, "read", seqArg)); !strings.Contains(out, "the body") {
		t.Fatalf("read: %s", out)
	}
	eventually(t, 10*time.Second, "a written read acked the message", func() bool {
		ws, _ := sp.Waiting()
		return len(ws) == 0 && unreadOf(t, w, "a") == 0
	})
}
