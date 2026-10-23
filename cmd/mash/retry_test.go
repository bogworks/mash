package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/randybias/mash/internal/admin"
	"github.com/randybias/mash/internal/endpoint"
	"github.com/randybias/mash/internal/store"
)

// setTiming shortens the retry timers and registers their restoration. It must run before any session starts:
// cleanups run in reverse registration order, so the restoration then runs after every session has been
// cancelled and joined.
func setTiming(t *testing.T, retry, open time.Duration) {
	t.Helper()
	oldR, oldO, oldRefresh := leaseRetry, openTimeout, endpoint.LeaseRefresh
	oldB, oldA := beforeAttemptHook, afterOpenHook
	leaseRetry, openTimeout = retry, open
	t.Cleanup(func() {
		leaseRetry, openTimeout, endpoint.LeaseRefresh = oldR, oldO, oldRefresh
		beforeAttemptHook, afterOpenHook = oldB, oldA
	})
}

func leaseKV(t *testing.T, w *world) jetstream.KeyValue {
	t.Helper()
	js, err := jetstream.New(w.e.Conn(t, w.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	return kv
}

// holdLease plants a lease for a holder that never refreshes it (a crashed session).
func holdLease(t *testing.T, kv jetstream.KeyValue, name string) {
	t.Helper()
	v := `{"instance_id":"dead-holder","host":"deadhost","pid":"4242","since":"2026-10-23T00:00:00Z"}`
	if _, err := kv.Create(context.Background(), name, []byte(v)); err != nil {
		t.Fatal(err)
	}
}

// freeLease removes it, which is what the bucket TTL does 90 s after the last refresh.
func freeLease(t *testing.T, kv jetstream.KeyValue, name string) {
	t.Helper()
	if err := kv.Purge(context.Background(), name); err != nil {
		t.Fatal(err)
	}
}

// none fails the test if a line matching pred arrives within d.
func (s *session) none(t *testing.T, d time.Duration, pred func(map[string]any) bool) {
	t.Helper()
	for _, m := range s.backlog {
		if pred(m) {
			t.Fatalf("unexpected line %v", m)
		}
	}
	deadline := time.After(d)
	for {
		select {
		case m, ok := <-s.lines:
			if !ok {
				return
			}
			if pred(m) {
				t.Fatalf("unexpected line %v", m)
			}
			s.backlog = append(s.backlog, m)
		case <-deadline:
			return
		}
	}
}

// exited waits for serve to return and puts the code back for the cleanup that also waits.
func (s *session) exited(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case code := <-s.done:
		s.done <- code
	case <-time.After(d):
		t.Fatal("serve did not exit")
	}
}

func content(m map[string]any) string { return m["params"].(map[string]any)["content"].(string) }

func eventually(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *session) call(t *testing.T, id int, tool, args string) map[string]any {
	t.Helper()
	s.send(t, `{"jsonrpc":"2.0","id":`+itoa(id)+`,"method":"tools/call","params":{"name":"`+tool+`","arguments":`+args+`}}`)
	return s.await(t, 5*time.Second, idIs(float64(id)))["result"].(map[string]any)
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func resultText(r map[string]any) string {
	return r["content"].([]any)[0].(map[string]any)["text"].(string)
}

func TestServeRetriesHeldLease(t *testing.T) {
	setTiming(t, 50*time.Millisecond, 20*time.Second)
	w := newWorld(t, "a", "b")
	kv := leaseKV(t, w)
	holdLease(t, kv, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	if n := len(s.tools(t)); n != 5 {
		t.Fatalf("waiting session exposes %d tools, want 5", n)
	}
	held := content(s.await(t, 5*time.Second, isChannel))
	for _, want := range []string{"already in use", "deadhost", "4242", "retrying"} {
		if !strings.Contains(held, want) {
			t.Fatalf("held notice lacks %q: %s", want, held)
		}
	}
	// Mail sent while the name is held waits in the inbox and arrives after the online notice.
	if out, code := w.cli(t, "b", "send", "a", "sent while held"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	freeLease(t, kv, "a")
	if online := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(online, "online") {
		t.Fatalf("online notice: %s", online)
	}
	if ev := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "sent while held") {
		t.Fatalf("expected the queued mail after the online notice, got %s", ev)
	}
	if out, code := w.cli(t, "b", "send", "a", "hello after recovery"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if ev := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "hello after recovery") {
		t.Fatalf("expected the mail, got %s", ev)
	}
	// The registered tools now work on the recovered endpoint, as endpoint a.
	r := s.call(t, 10, "send", `{"to":"b","text":"thanks"}`)
	if txt := resultText(r); !strings.HasPrefix(txt, "sent #") || r["isError"] == true {
		t.Fatalf("send tool after recovery: %v", r)
	}
	if txt := resultText(s.call(t, 11, "inbox", `{}`)); !strings.Contains(txt, "sent while held") || !strings.Contains(txt, "hello after recovery") {
		t.Fatalf("inbox after recovery: %s", txt)
	}
	if txt := resultText(s.call(t, 12, "read", `{"seq":1}`)); !strings.Contains(txt, "sent while held") {
		t.Fatalf("read after recovery: %s", txt)
	}
	if txt := resultText(s.call(t, 13, "who", `{}`)); !strings.Contains(txt, "a\tonline") {
		t.Fatalf("who after recovery: %s", txt)
	}
	if out, _ := w.cli(t, "b", "inbox"); !strings.Contains(out, "from a [verified]") || !strings.Contains(out, "thanks") {
		t.Fatalf("b's inbox: %s", out)
	}
	if out, _ := w.cli(t, "b", "who"); !strings.Contains(out, "a\tonline") {
		t.Fatalf("who: %s", out)
	}
}

func TestServeAcquireBeforeInitialized(t *testing.T) {
	setTiming(t, 50*time.Millisecond, 20*time.Second)
	w := newWorld(t, "a", "b")
	kv := leaseKV(t, w)
	holdLease(t, kv, "a")
	s := startServe(t, w.env["a"])
	attempts := acquireAttempts.Load()
	eventually(t, 5*time.Second, "first Open found the lease held and a retry started", func() bool { return acquireAttempts.Load() > attempts })
	before := acquiredCount.Load()
	freeLease(t, kv, "a")
	eventually(t, 5*time.Second, "waiter takes the lease before initialize", func() bool { return acquiredCount.Load() > before })
	// Claude has not initialized: nothing may be pushed yet.
	s.none(t, 400*time.Millisecond, isChannel)
	s.initialize(t)
	s.tools(t)
	online := content(s.await(t, 5*time.Second, isChannel))
	if !strings.Contains(online, "online") || strings.Contains(online, "retrying") {
		t.Fatalf("expected exactly the online notice, got %s", online)
	}
	if out, code := w.cli(t, "b", "send", "a", "queued mail"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if ev := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "queued mail") {
		t.Fatalf("expected the mail, got %s", ev)
	}
}

func TestServeDoesNotDisplaceRenewingHolder(t *testing.T) {
	setTiming(t, 20*time.Millisecond, 20*time.Second)
	endpoint.LeaseRefresh = 50 * time.Millisecond
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	holder := startServe(t, w.env["a"])
	holder.initialize(t)
	holder.tools(t)
	eventually(t, 5*time.Second, "holder online", func() bool { out, _ := w.cli(t, "a", "who"); return strings.Contains(out, "a\tonline") })
	first, err := kv.Get(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	attempts0, acquired0 := acquireAttempts.Load(), acquiredCount.Load()
	waiter := startServe(t, w.env["a"])
	waiter.initialize(t)
	waiter.tools(t)
	eventually(t, 10*time.Second, "10 retries while the holder renews", func() bool { return acquireAttempts.Load()-attempts0 >= 10 })
	eventually(t, 10*time.Second, "5 holder refreshes", func() bool {
		cur, err := kv.Get(context.Background(), "a")
		return err == nil && cur.Revision() >= first.Revision()+5
	})
	// The holder still gets its mail while a waiter is retrying against it.
	if out, code := w.cli(t, "a", "send", "a", "to the holder"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if ev := content(holder.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "to the holder") {
		t.Fatalf("holder event %s", ev)
	}
	if acquiredCount.Load() != acquired0 {
		t.Fatal("the waiter took a continuously renewed lease")
	}
	// The holder goes away cleanly (releasing the lease): the waiter takes over, with no restart.
	holder.in.Close()
	eventually(t, 10*time.Second, "waiter takes the released lease", func() bool { return acquiredCount.Load() > acquired0 })
}

func TestServeWaitingToolsAreSafe(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	holdLease(t, leaseKV(t, w), "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	calls := map[string]string{
		"send": `{"to":"x","text":"y"}`, "read": `{"seq":1}`, "inbox": `{}`, "who": `{}`,
		"reply": `{"request_id":"r","text":"t"}`,
	}
	id := 20
	for tool, args := range calls {
		id++
		r := s.call(t, id, tool, args)
		if r["isError"] != true || !strings.Contains(resultText(r), "waiting for endpoint a") {
			t.Fatalf("%s while waiting: %v", tool, r)
		}
	}
}

func TestServeWaitingInstructionsKeepPolicy(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	holdLease(t, leaseKV(t, w), "a")
	dir := filepath.Join(w.env["a"]["HOME"], ".config", "mash")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "policy.md"), []byte("POLICY-SENTINEL"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := startServe(t, w.env["a"])
	instr := s.initialize(t)["result"].(map[string]any)["instructions"].(string)
	if !strings.Contains(instr, "POLICY-SENTINEL") || !strings.Contains(instr, "waiting") {
		t.Fatalf("waiting instructions %q", instr)
	}
}

// stdin closing at any point of the wait must end serve and leave no connection or lease behind.
func waitGone(t *testing.T, w *world, base int, name string, kv jetstream.KeyValue, planted bool) {
	t.Helper()
	eventually(t, 5*time.Second, "endpoint connections closed", func() bool { return w.e.Connections() <= base })
	if !planted {
		return
	}
	if _, err := kv.Get(context.Background(), name); err == nil {
		t.Fatal("a lease is still held")
	}
}

func TestServeCancelDuringWait(t *testing.T) {
	setTiming(t, 20*time.Millisecond, 20*time.Second)
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	base := w.e.Connections()
	holdLease(t, kv, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	attempts := acquireAttempts.Load()
	eventually(t, 5*time.Second, "waiting", func() bool { return acquireAttempts.Load() > attempts+2 })
	s.in.Close()
	s.exited(t, 5*time.Second)
	waitGone(t, w, base, "a", kv, false)
}

// Cancellation (here the parent context, which is observable) is already in effect when a retry's Open
// succeeds: the worker must still close that endpoint and release its lease.
func TestServeCancelRightAfterOpen(t *testing.T) {
	setTiming(t, 20*time.Millisecond, 20*time.Second)
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	base := w.e.Connections()
	holdLease(t, kv, "a")
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	t.Cleanup(letGo) // registered before the session: a failed test cannot leave the attempt blocked
	afterOpenHook = func() { close(reached); <-release }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done, closeIO := startServeCtx(t, ctx, w.env["a"])
	t.Cleanup(closeIO)
	freeLease(t, kv, "a")
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("no successful Open")
	}
	cancel() // cancellation is in effect before the successful Open is published
	letGo()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit")
	}
	waitGone(t, w, base, "a", kv, false)
	if _, err := kv.Get(context.Background(), "a"); err == nil {
		t.Fatal("a successful Open raced with shutdown and left its lease held")
	}
}

func TestServeCancelBeforeAttemptAndParentCancel(t *testing.T) {
	setTiming(t, 20*time.Millisecond, 20*time.Second)
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	base := w.e.Connections()
	holdLease(t, kv, "a")
	reached, release := make(chan struct{}, 100), make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	t.Cleanup(letGo)
	beforeAttemptHook = func() { reached <- struct{}{}; <-release }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done, closeIO := startServeCtx(t, ctx, w.env["a"])
	t.Cleanup(closeIO)
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("no attempt started")
	}
	cancel()
	letGo()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not exit on parent cancel")
	}
	waitGone(t, w, base, "a", kv, false)
}

// startServeCtx runs serve under a caller-controlled context, for cancellation that stdin EOF cannot order.
func startServeCtx(t *testing.T, ctx context.Context, env map[string]string) (<-chan int, func()) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan int, 1)
	go func() { done <- runCtx(ctx, []string{"serve"}, envFrom(env), inR, outW, io.Discard); outW.Close() }()
	go io.Copy(io.Discard, outR)
	io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n")
	return done, func() { inW.Close(); outR.Close() }
}

func TestServeHeldThenNonHeldErrorIsTerminal(t *testing.T) {
	setTiming(t, 30*time.Millisecond, 20*time.Second)
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	holdLease(t, kv, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	s.await(t, 5*time.Second, isChannel) // the held notice
	adm, err := admin.New(w.e.Conn(t, w.e.AdminCreds), w.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Deprovision(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if term := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(term, "could not start") {
		t.Fatalf("terminal notice: %s", term)
	}
	n := acquireAttempts.Load()
	time.Sleep(300 * time.Millisecond)
	if acquireAttempts.Load() != n {
		t.Fatal("retries continued after a non-held error")
	}
	r := s.call(t, 30, "who", `{}`)
	if r["isError"] != true || !strings.Contains(resultText(r), "could not start") {
		t.Fatalf("tool after terminal failure: %v", r)
	}
}

func TestServeOrdinaryStartupHasNoNotice(t *testing.T) {
	setTiming(t, 50*time.Millisecond, 20*time.Second)
	w := newWorld(t, "a", "b")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	s.none(t, 1200*time.Millisecond, isChannel)
	if out, code := w.cli(t, "b", "send", "a", "plain mail"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if ev := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "plain mail") {
		t.Fatalf("first event should be the mail, got %s", ev)
	}
}

// The first Open happens before the MCP server exists. Against a server that accepts but never speaks NATS
// it is bounded by nats' own handshake timeout (openTimeout bounds only context-aware steps); this asserts
// that serve then notices the stdin EOF and exits, not that openTimeout is effective.
func TestServeFirstOpenUnresponsiveThenEOF(t *testing.T) {
	setTiming(t, 50*time.Millisecond, 300*time.Millisecond)
	ln, err := net.Listen("tcp", "127.0.0.1:0") // accepts, never speaks NATS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	w := newWorld(t, "a")
	env := map[string]string{}
	for k, v := range w.env["a"] {
		env[k] = v
	}
	env["MASH_URL"] = "nats://" + ln.Addr().String()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan int, 1)
	go func() { done <- run([]string{"serve"}, envFrom(env), inR, outW, io.Discard); outW.Close() }()
	go io.Copy(io.Discard, outR)
	t.Cleanup(func() { outR.Close() })
	inW.Close() // EOF while the first Open is in flight
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not exit after the first Open returned and stdin was closed")
	}
}
