package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/store"
)

// setTiming shortens the retry timers and registers their restoration. It must run before any session starts:
// cleanups run in reverse registration order, so the restoration then runs after every session has been
// cancelled and joined.
func setTiming(t *testing.T, retry, open time.Duration) {
	t.Helper()
	oldR, oldO, oldRefresh := leaseRetry, openTimeout, endpoint.LeaseRefresh
	oldB, oldA := beforeAttemptHook, afterOpenHook
	oldOpen, oldTM, oldTB := openEndpoint, transientMax, transientBudget
	oldPE, oldPW := mcpPingEvery, mcpPingWait
	leaseRetry, openTimeout = retry, open
	t.Cleanup(func() {
		leaseRetry, openTimeout, endpoint.LeaseRefresh = oldR, oldO, oldRefresh
		beforeAttemptHook, afterOpenHook = oldB, oldA
		openEndpoint, transientMax, transientBudget = oldOpen, oldTM, oldTB
		mcpPingEvery, mcpPingWait = oldPE, oldPW
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
	if err := kv.Delete(context.Background(), name); err != nil {
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
	if n := len(s.tools(t)); n != 7 {
		t.Fatalf("waiting session exposes %d tools, want 7", n)
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

// An XDG config.json makes the XDG directory the config directory, but an operator policy.md that only exists in the
// legacy ~/.config/mash must still be applied.
func TestServeLegacyPolicyWithXDGConfig(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	holdLease(t, leaseKV(t, w), "a")
	env := w.env["a"]
	xdg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdg, "mash"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "mash", "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	env["XDG_CONFIG_HOME"] = xdg
	dir := filepath.Join(env["HOME"], ".config", "mash")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "policy.md"), []byte("LEGACY-POLICY-SENTINEL"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := startServe(t, env)
	instr := s.initialize(t)["result"].(map[string]any)["instructions"].(string)
	if !strings.Contains(instr, "LEGACY-POLICY-SENTINEL") {
		t.Fatalf("legacy policy dropped: %q", instr)
	}
}

// An XDG policy.md that exists but cannot be read is a configuration failure: the operator's restrictions are
// neither dropped nor replaced by the legacy copy. A missing policy.md is still an empty policy.
func TestServeUnreadablePolicyIsConfigError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	env := w.env["a"]
	xdg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdg, "mash"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "mash", "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	pol := filepath.Join(xdg, "mash", "policy.md")
	if err := os.WriteFile(pol, []byte("XDG-POLICY"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(pol); err == nil {
		t.Skip("file modes are not enforced here")
	}
	env["XDG_CONFIG_HOME"] = xdg
	dir := filepath.Join(env["HOME"], ".config", "mash")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "policy.md"), []byte("LEGACY-POLICY-SENTINEL"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := startServe(t, env)
	instr := s.initialize(t)["result"].(map[string]any)["instructions"].(string)
	if strings.Contains(instr, "LEGACY-POLICY-SENTINEL") || !strings.Contains(instr, "reading "+pol) {
		t.Fatalf("unreadable policy not reported: %q", instr)
	}
	if st := resultText(s.call(t, 90, "status", "{}")); !strings.Contains(st, "config: FAILED") || !strings.Contains(st, "reading "+pol) {
		t.Fatalf("status does not report it: %s", st)
	}

	// A dangling symlink is present, not absent: ReadFile says ErrNotExist but it is still a configuration error.
	if err := os.Remove(pol); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(xdg, "nowhere.md"), pol); err != nil {
		t.Fatal(err)
	}
	sd := startServe(t, env)
	instr = sd.initialize(t)["result"].(map[string]any)["instructions"].(string)
	if strings.Contains(instr, "LEGACY-POLICY-SENTINEL") || !strings.Contains(instr, "reading "+pol) {
		t.Fatalf("dangling policy symlink not reported: %q", instr)
	}
	if st := resultText(sd.call(t, 90, "status", "{}")); !strings.Contains(st, "config: FAILED") || !strings.Contains(st, "reading "+pol) {
		t.Fatalf("status does not report dangling symlink: %s", st)
	}
	if err := os.Remove(pol); err != nil {
		t.Fatal(err)
	}

	// Missing policy.md: fine, empty policy.
	if err := os.Remove(filepath.Join(dir, "policy.md")); err != nil {
		t.Fatal(err)
	}
	holdLease(t, leaseKV(t, w), "a")
	s2 := startServe(t, env)
	instr = s2.initialize(t)["result"].(map[string]any)["instructions"].(string)
	if strings.Contains(instr, "reading ") {
		t.Fatalf("missing policy reported as an error: %q", instr)
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
	// No Open may run during the deprovision: one that read the directory record before the purge and created the
	// lease after it would come online (the flake this gate removes).
	var gate sync.RWMutex
	openEndpoint = func(ctx context.Context, cfg config.Config) (*endpoint.Endpoint, error) {
		gate.RLock()
		defer gate.RUnlock()
		return endpoint.Open(ctx, cfg)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	s.await(t, 5*time.Second, isChannel) // the held notice
	adm, err := admin.New(w.e.Conn(t, w.e.AdminCreds), w.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	gate.Lock()
	err = adm.Deprovision(context.Background(), "a")
	gate.Unlock()
	if err != nil {
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
// it is bounded by openTimeout, the connect included; this asserts that serve then notices the stdin EOF
// and exits (TestOpenTimeoutBoundsTheConnect asserts the bound).
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

// failingOpen makes the Opens numbered in fail (1-based, counting every Open serve makes) return err, and lets
// the rest through to endpoint.Open.
func failingOpen(fail func(n int64) bool, err error) func(context.Context, config.Config) (*endpoint.Endpoint, error) {
	var n atomic.Int64
	return func(ctx context.Context, cfg config.Config) (*endpoint.Endpoint, error) {
		if fail(n.Add(1)) {
			return nil, err
		}
		return endpoint.Open(ctx, cfg)
	}
}

// A transient error (a timeout, no responders, a closed connection) while waiting for a held name does not end the
// wait: serve backs off and keeps trying, and takes the name once it is free.
func TestServeWaitSurvivesTransientErrors(t *testing.T) {
	// ErrNoStreamResponse is what a KV Create returns when the lease stream has no responder (codex H1 on #8).
	for _, e := range []error{nats.ErrTimeout, jetstream.ErrNoStreamResponse} {
		t.Run(e.Error(), func(t *testing.T) {
			setTiming(t, 20*time.Millisecond, 20*time.Second)
			transientMax = 50 * time.Millisecond
			w := newWorld(t, "a")
			kv := leaseKV(t, w)
			holdLease(t, kv, "a")
			openEndpoint = failingOpen(func(n int64) bool { return n >= 2 && n <= 5 }, fmt.Errorf("mash: lease: %w", e))
			s := startServe(t, w.env["a"])
			s.initialize(t)
			s.tools(t)
			s.await(t, 5*time.Second, isChannel) // the held notice
			attempts := acquireAttempts.Load()
			eventually(t, 5*time.Second, "the transient failures are retried", func() bool { return acquireAttempts.Load() >= attempts+5 })
			before := acquiredCount.Load()
			freeLease(t, kv, "a")
			eventually(t, 5*time.Second, "the waiter takes the freed name", func() bool { return acquiredCount.Load() > before })
			if ev := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "online") {
				t.Fatalf("want the online notice, got %s", ev)
			}
		})
	}
}

// Transient errors in a row past transientBudget end the wait like a permanent one: a broker that never comes back
// is reported, not retried forever.
func TestServeTransientErrorsEndAfterTheBudget(t *testing.T) {
	setTiming(t, 20*time.Millisecond, 20*time.Second)
	transientMax, transientBudget = 30*time.Millisecond, 300*time.Millisecond
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	holdLease(t, kv, "a")
	openEndpoint = failingOpen(func(n int64) bool { return n >= 2 }, fmt.Errorf("mash: lease: %w", nats.ErrNoResponders))
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	// The budget may run out before Claude Code takes channel events, in which case the terminal notice is the
	// first event and no held notice is sent.
	isTerminal := func(m map[string]any) bool { return isChannel(m) && strings.Contains(content(m), "could not start") }
	if term := content(s.await(t, 5*time.Second, isTerminal)); !strings.Contains(term, "no responders") {
		t.Fatalf("terminal notice: %s", term)
	}
	n := acquireAttempts.Load()
	time.Sleep(200 * time.Millisecond)
	if acquireAttempts.Load() != n {
		t.Fatal("retries continued after the budget ran out")
	}
}

func TestTransientClassification(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("mash: lease: %w", nats.ErrTimeout), true},
		{fmt.Errorf("x: %w", nats.ErrNoResponders), true},
		{fmt.Errorf("mash: lease: %w", jetstream.ErrNoStreamResponse), true},
		{fmt.Errorf("x: %w", nats.ErrConnectionClosed), true},
		{fmt.Errorf("mash: connect u: %w", nats.ErrNoServers), true},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), true},
		{fmt.Errorf("x: %w", syscall.ECONNREFUSED), true},
		{fmt.Errorf("x: %w", syscall.ECONNRESET), true},
		{fmt.Errorf("x: %w", nats.ErrAuthorization), false},
		{fmt.Errorf("x: %w", nats.ErrAuthExpired), false},
		{errors.New("nats: permissions violation: Permissions Violation for Publish to \"x\""), false},
		{errors.New("mash: no endpoint named \"a\" in the directory"), false},
		{fmt.Errorf("mash: credentials: %w", os.ErrNotExist), false},
	} {
		if got := transient(c.err); got != c.want {
			t.Errorf("transient(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// A transient error on the very first Open (a restarted session meeting a NATS blip) does not start the session
// degraded: serve waits and retries like a held name, the tools say why mail is off meanwhile, and the session comes
// online once the broker answers.
func TestServeFirstOpenTransientIsRetried(t *testing.T) {
	setTiming(t, 20*time.Millisecond, 20*time.Second)
	transientMax = 50 * time.Millisecond
	w := newWorld(t, "a", "b")
	gate := make(chan struct{})
	var opens atomic.Int64
	openEndpoint = func(ctx context.Context, cfg config.Config) (*endpoint.Endpoint, error) {
		select {
		case <-gate:
			return endpoint.Open(ctx, cfg)
		default:
			opens.Add(1)
			return nil, fmt.Errorf("mash: connect nats://x: %w", nats.ErrNoServers)
		}
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	if ev := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "retrying") || !strings.Contains(ev, "no servers") {
		t.Fatalf("want the retrying notice, got %s", ev)
	}
	r := s.call(t, 40, "who", `{}`)
	if r["isError"] != true || !strings.Contains(resultText(r), "retrying") {
		t.Fatalf("tool while retrying: %v", r)
	}
	eventually(t, 5*time.Second, "the first Open's failure is retried", func() bool { return opens.Load() >= 3 })
	close(gate)
	if ev := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "online") {
		t.Fatalf("want the online notice, got %s", ev)
	}
	if out, code := w.cli(t, "b", "send", "a", "after the blip"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if ev := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "after the blip") {
		t.Fatalf("want the mail, got %s", ev)
	}
}

// A permanent error on the first Open still starts the session degraded at once, with no retries.
func TestServeFirstOpenPermanentIsNotRetried(t *testing.T) {
	setTiming(t, 20*time.Millisecond, 20*time.Second)
	w := newWorld(t, "a")
	var opens atomic.Int64
	openEndpoint = func(ctx context.Context, cfg config.Config) (*endpoint.Endpoint, error) {
		opens.Add(1)
		return nil, fmt.Errorf("mash: connect nats://x: %w", nats.ErrAuthorization)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	if ev := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "could not start") {
		t.Fatalf("want the error notice, got %s", ev)
	}
	time.Sleep(200 * time.Millisecond)
	if n := opens.Load(); n != 1 {
		t.Fatalf("a permanent first failure was retried: %d Opens", n)
	}
}

// serve pings its MCP client; an answered ping is recorded in the lease as mcp_seen, and channels says serve has
// begun pushing channel events. A supervisor reads both from the lease with no mail and no Claude turn.
func TestServeRecordsMCPSeenAndChannelsInTheLease(t *testing.T) {
	setTiming(t, 20*time.Millisecond, 20*time.Second)
	endpoint.LeaseRefresh = 50 * time.Millisecond
	mcpPingEvery, mcpPingWait = 50*time.Millisecond, time.Second
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	lease := func() map[string]any {
		en, err := kv.Get(context.Background(), "a")
		if err != nil {
			return nil
		}
		var v map[string]any
		_ = json.Unmarshal(en.Value(), &v)
		return v
	}
	ping := s.await(t, 5*time.Second, func(m map[string]any) bool { return m["method"] == "ping" })
	if lv := lease(); lv["mcp_seen"] != nil {
		t.Fatalf("mcp_seen before any ping was answered: %v", lv)
	}
	id, _ := json.Marshal(ping["id"])
	s.send(t, `{"jsonrpc":"2.0","id":`+string(id)+`,"result":{}}`)
	eventually(t, 5*time.Second, "mcp_seen in the lease", func() bool {
		lv := lease()
		seen, _ := lv["mcp_seen"].(string)
		at, err := time.Parse(time.RFC3339, seen)
		return err == nil && time.Since(at) < time.Minute
	})
	// No field claims channel delivery: a client's messages cannot show it (codex H1 on #14).
	if lv := lease(); lv["channels"] != nil {
		t.Fatalf("the lease claims channels: %v", lv)
	}
}

// A serve waiting for the lease records itself in the spool as .waiting.<ppid>, and the record is gone once it binds.
func TestServeRecordsItselfWhileWaiting(t *testing.T) {
	setTiming(t, 50*time.Millisecond, 20*time.Second)
	w := newWorld(t, "a", "b")
	kv := leaseKV(t, w)
	holdLease(t, kv, "a")
	sp := spoolOf(w.env["a"])
	rec := filepath.Join(sp.Dir, ".waiting."+strconv.Itoa(os.Getppid()))
	s := startServe(t, w.env["a"])
	s.initialize(t)
	eventually(t, 5*time.Second, "waiting record written", func() bool { _, err := os.Stat(rec); return err == nil })
	if got := sp.Waiters(); len(got) != 1 || got[0].PID != os.Getppid() {
		t.Fatalf("Waiters = %+v", got)
	}
	freeLease(t, kv, "a")
	s.await(t, 5*time.Second, isChannel) // the online notice
	eventually(t, 10*time.Second, "waiting record removed once bound", func() bool { _, err := os.Stat(rec); return err != nil })
}

// lease_retry (MASH_LEASE_RETRY) reaches the wait: with the test knob at 10 s, the configured 50 ms is what the held
// notice names and what retries the lease.
func TestServeLeaseRetryFromConfig(t *testing.T) {
	setTiming(t, 10*time.Second, 20*time.Second)
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	holdLease(t, kv, "a")
	env := w.env["a"]
	env["MASH_LEASE_RETRY"] = "50ms"
	s := startServe(t, env)
	s.initialize(t)
	s.tools(t)
	if held := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(held, "retrying every 50ms") {
		t.Fatalf("held notice: %s", held)
	}
	freeLease(t, kv, "a")
	if online := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(online, "online") {
		t.Fatalf("online notice: %s", online)
	}
}

func TestApplyRetryTiming(t *testing.T) {
	setTiming(t, time.Second, time.Second)
	transientMax, transientBudget = 3*time.Second, 4*time.Second
	applyRetryTiming(config.Config{})
	if leaseRetry != time.Second || transientMax != 3*time.Second || transientBudget != 4*time.Second {
		t.Fatalf("unset keys changed the knobs: %v %v %v", leaseRetry, transientMax, transientBudget)
	}
	applyRetryTiming(config.Config{LeaseRetry: 5 * time.Second, TransientMax: 6 * time.Second, TransientBudget: 7 * time.Second})
	if leaseRetry != 5*time.Second || transientMax != 6*time.Second || transientBudget != 7*time.Second {
		t.Fatalf("set keys: %v %v %v", leaseRetry, transientMax, transientBudget)
	}
}

func TestCLIDeadlines(t *testing.T) {
	if cliDeadline(config.Config{}) != 15*time.Minute || cliDeadline(config.Config{CLITimeout: time.Hour}) != time.Hour {
		t.Fatal("cliDeadline")
	}
}
