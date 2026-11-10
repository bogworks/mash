package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/spool"
)

// Tests for the same-session takeover of the endpoint lease (ADR 0082).

// asSession makes every serve in this process look like a serve of one Claude Code session: the same parent, and the
// same session id. The other tests of this package run with no session identity (see TestMain).
func asSession(t *testing.T) {
	t.Helper()
	if os.Getppid() <= 1 {
		t.Skip("no usable parent pid")
	}
	old := endpoint.OurSession
	endpoint.OurSession = func() endpoint.SessionIdent {
		return endpoint.SessionIdent{PPID: fmt.Sprint(os.Getppid()), PStart: "777", Session: "sess-1"}
	}
	t.Cleanup(func() { endpoint.OurSession = old })
}

// Question 1: serve exits when its stdin reaches EOF, with code 0, and gives the lease back.
func TestServeExitsWhenStdinReachesEOF(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	w := newWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "a", "who"); return strings.Contains(out, "a\tonline") })
	s.in.Close() // EOF on serve's stdin
	s.exited(t, 5*time.Second)
	code := <-s.done
	s.done <- code
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if _, err := leaseKV(t, w).Get(context.Background(), "a"); err == nil {
		t.Fatal("the lease was not released at exit")
	}
}

// The reported defect: a new serve of the same session, while the old is alive and holds the lease, takes the lease at
// once (not after the retry delay); the old one steps down within a lease-refresh interval and says it was superseded.
func TestServeSameSessionTakesOverAndSupersedesOld(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second) // a retry would take an hour: only a takeover passes
	endpoint.LeaseRefresh = 100 * time.Millisecond
	asSession(t)
	w := newWorld(t, "a", "b")
	old := startServe(t, w.env["a"])
	old.initialize(t)
	old.tools(t)
	eventually(t, 5*time.Second, "old online", func() bool { out, _ := w.cli(t, "a", "who"); return strings.Contains(out, "a\tonline") })

	attempts := acquireAttempts.Load()
	fresh := startServe(t, w.env["a"])
	fresh.initialize(t)
	fresh.tools(t)
	if got := acquireAttempts.Load(); got != attempts {
		t.Fatalf("the new serve needed %d retries", got-attempts)
	}
	if txt := resultText(fresh.call(t, 5, "status", `{}`)); !strings.Contains(txt, "connection: OK") {
		t.Fatalf("the new serve's status: %s", txt)
	}

	// The old one steps down at its next refresh: tools say so and name the new holder.
	pid := fmt.Sprint(os.Getpid())
	eventually(t, 5*time.Second, "the old serve steps down", func() bool {
		return strings.Contains(resultText(old.call(t, 20, "status", `{}`)), "SUPERSEDED")
	})
	if txt := resultText(old.call(t, 21, "status", `{}`)); !strings.Contains(txt, "pid "+pid) || !strings.Contains(txt, "inactive") {
		t.Fatalf("the old serve's status: %s", txt)
	}
	r := old.call(t, 22, "send", `{"to":"b","text":"x"}`)
	if txt := resultText(r); r["isError"] != true || !strings.Contains(txt, "superseded by a newer mash serve of the same session") ||
		!strings.Contains(txt, "pid "+pid) || !strings.Contains(txt, "handled there") {
		t.Fatalf("the old serve's tool answer: %v", r)
	}
	if content(old.await(t, 5*time.Second, isChannel)) == "" { // told once, in the channel
		t.Fatal("empty notice")
	}

	// Mail goes to the new serve alone.
	if out, code := w.cli(t, "b", "send", "a", "after the takeover"); code != 0 {
		t.Fatalf("send: %d %s", code, out)
	}
	if ev := content(fresh.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "after the takeover") {
		t.Fatalf("the new serve got %s", ev)
	}
	old.none(t, 300*time.Millisecond, func(m map[string]any) bool {
		return isChannel(m) && strings.Contains(content(m), "after the takeover")
	})

	// The superseded serve is still up; it exits when its stdin closes, with code 0, and leaves the new holder's lease.
	old.in.Close()
	old.exited(t, 5*time.Second)
	if code := <-old.done; code != 0 {
		t.Fatalf("superseded serve exit code %d", code)
	}
	old.done <- 0
	if _, err := leaseKV(t, w).Get(context.Background(), "a"); err != nil {
		t.Fatalf("the new holder's lease is gone after the old serve exited: %v", err)
	}
}

// A superseded serve also exits when its parent changes.
func TestServeSupersededExitsWhenParentChanges(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	endpoint.LeaseRefresh = 100 * time.Millisecond
	parentWatchOld, ppidOld := parentWatchEvery, getppid
	var ppid atomic.Int64
	ppid.Store(int64(os.Getppid()))
	parentWatchEvery, getppid = 50*time.Millisecond, func() int { return int(ppid.Load()) }
	t.Cleanup(func() { parentWatchEvery, getppid = parentWatchOld, ppidOld })
	asSession(t)
	w := newWorld(t, "a")
	old := startServe(t, w.env["a"])
	old.initialize(t)
	old.tools(t)
	eventually(t, 5*time.Second, "old online", func() bool { out, _ := w.cli(t, "a", "who"); return strings.Contains(out, "a\tonline") })
	fresh := startServe(t, w.env["a"])
	fresh.initialize(t)
	fresh.tools(t)
	eventually(t, 5*time.Second, "the old serve steps down", func() bool {
		return strings.Contains(resultText(old.call(t, 20, "status", `{}`)), "SUPERSEDED")
	})
	old.none(t, 300*time.Millisecond, func(map[string]any) bool { return false })
	select {
	case <-old.done:
		t.Fatal("exited with its parent unchanged")
	default:
	}
	ppid.Store(1)
	old.exited(t, 5*time.Second)
}

// A same-session holder that is not taken over (the compare-and-set lost) is retried after a short delay, a few times;
// then the long one applies. Another session's holder gets the long delay at once.
func TestServeSameSessionHeldIsRetriedShort(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	oldR, oldT := sameSessionRetry, sameSessionTries
	sameSessionRetry, sameSessionTries = 20*time.Millisecond, 3
	t.Cleanup(func() { sameSessionRetry, sameSessionTries = oldR, oldT })
	var calls atomic.Int64
	openEndpoint = func(context.Context, config.Config) (*endpoint.Endpoint, error) {
		calls.Add(1)
		return nil, &endpoint.LeaseHeldError{Name: "a", Host: "h", PID: "1", Since: "x", SameSession: true}
	}
	w := newWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	eventually(t, 5*time.Second, "the short retries", func() bool { return calls.Load() >= 4 }) // the first Open and 3 retries
	time.Sleep(300 * time.Millisecond)
	if n := calls.Load(); n != 4 {
		t.Fatalf("%d attempts: the short retries are bounded, then leaseRetry (an hour) applies", n)
	}
}

func TestServeOtherSessionHeldIsRetriedLong(t *testing.T) {
	setTiming(t, time.Hour, 20*time.Second)
	oldR := sameSessionRetry
	sameSessionRetry = 20 * time.Millisecond
	t.Cleanup(func() { sameSessionRetry = oldR })
	var calls atomic.Int64
	openEndpoint = func(context.Context, config.Config) (*endpoint.Endpoint, error) {
		calls.Add(1)
		return nil, &endpoint.LeaseHeldError{Name: "a", Host: "h", PID: "1", Since: "x"}
	}
	w := newWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	time.Sleep(300 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d attempts, want 1", n)
	}
}

// Question 4: the holder shown is the one found by the latest retry, in the tool, in status, in the channel, and in the
// spool's lease-wait state (which the hook turns into its notice).
func TestServeHeldHolderRefreshedOnEveryRetry(t *testing.T) {
	setTiming(t, 30*time.Millisecond, 20*time.Second)
	var mu sync.Mutex
	pid := "1111"
	openEndpoint = func(context.Context, config.Config) (*endpoint.Endpoint, error) {
		mu.Lock()
		defer mu.Unlock()
		return nil, &endpoint.LeaseHeldError{Name: "a", Host: "host-" + pid, PID: pid, Since: "since-" + pid}
	}
	w := newWorld(t, "a")
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	if txt := resultText(s.call(t, 5, "status", `{}`)); !strings.Contains(txt, "pid 1111") {
		t.Fatalf("status: %s", txt)
	}
	if first := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(first, "pid 1111") {
		t.Fatalf("first notice: %s", first)
	}
	sp := spoolOf(w.env["a"])
	if ws, ok := sp.LiveWaitState(); !ok || ws.HolderPID != "1111" || ws.ServePID != os.Getpid() {
		t.Fatalf("wait state %+v %v", ws, ok)
	}
	mu.Lock()
	pid = "2222" // the holder changes between retries
	mu.Unlock()
	eventually(t, 5*time.Second, "status shows the new holder", func() bool {
		txt := resultText(s.call(t, 6, "status", `{}`))
		return strings.Contains(txt, "pid 2222") && strings.Contains(txt, "host-2222") && !strings.Contains(txt, "1111")
	})
	r := s.call(t, 7, "inbox", `{}`)
	if txt := resultText(r); r["isError"] != true || !strings.Contains(txt, "host-2222 pid 2222 since since-2222") || strings.Contains(txt, "1111") {
		t.Fatalf("tool answer: %s", txt)
	}
	if ev := content(s.await(t, 5*time.Second, isChannel)); !strings.Contains(ev, "pid 2222") {
		t.Fatalf("the change was not told: %s", ev)
	}
	eventually(t, 5*time.Second, "wait state shows the new holder", func() bool {
		ws, ok := sp.LiveWaitState()
		return ok && ws.HolderPID == "2222" && ws.HolderHost == "host-2222"
	})
}

// The lease-wait state is recorded while the lease is held by another session, and removed when serve takes the lease
// and when it exits.
func TestServeWaitStateRecordedAndCleared(t *testing.T) {
	setTiming(t, 50*time.Millisecond, 20*time.Second)
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	holdLease(t, kv, "a")
	s := startServe(t, w.env["a"])
	sp := spoolOf(w.env["a"])
	eventually(t, 5*time.Second, "the wait state is recorded", func() bool {
		ws, ok := sp.LiveWaitState()
		return ok && ws.HolderPID == "4242" && ws.HolderHost == "deadhost"
	})
	freeLease(t, kv, "a")
	eventually(t, 5*time.Second, "the wait state is removed once the lease is taken", func() bool {
		_, ok := sp.LiveWaitState()
		return !ok
	})
	s.initialize(t)
	s.tools(t)
	// A second wait, ended by exit: held again by another session through a fresh serve whose first Open fails.
	s.in.Close()
	s.exited(t, 5*time.Second)

	holdLease(t, kv, "a")
	s2 := startServe(t, w.env["a"])
	eventually(t, 5*time.Second, "the wait state is recorded again", func() bool { _, ok := sp.LiveWaitState(); return ok })
	s2.in.Close()
	s2.exited(t, 5*time.Second)
	if _, ok := sp.LiveWaitState(); ok {
		t.Fatal("the wait state survived the serve's exit")
	}
}

// Two serves of one endpoint wait at once (C3): the one that exits must not clear the state the other wrote after it.
func TestServeWaitStateOfTwoWaitingServes(t *testing.T) {
	setTiming(t, time.Minute, 20*time.Second) // no retry rewrites the state during the test
	w := newWorld(t, "a")
	kv := leaseKV(t, w)
	holdLease(t, kv, "a")
	sp := spoolOf(w.env["a"])
	s1 := startServe(t, w.env["a"])
	var first spool.WaitState
	eventually(t, 5*time.Second, "the first serve's state", func() bool {
		ws, ok := sp.LiveWaitState()
		first = ws
		return ok && ws.Instance != ""
	})
	s2 := startServe(t, w.env["a"])
	eventually(t, 5*time.Second, "the second serve's state replaces it", func() bool {
		ws, ok := sp.LiveWaitState()
		return ok && ws.Instance != first.Instance
	})
	second, _ := sp.LiveWaitState()
	s1.in.Close()
	s1.exited(t, 5*time.Second)
	if ws, ok := sp.LiveWaitState(); !ok || ws.Instance != second.Instance {
		t.Fatalf("the earlier serve's exit cleared or changed the later one's state: %+v %v", ws, ok)
	}
	s2.in.Close()
	s2.exited(t, 5*time.Second)
	if _, ok := sp.LiveWaitState(); ok {
		t.Fatal("the owner's exit did not clear its state")
	}
}
