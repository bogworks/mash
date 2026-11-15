package activity

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// startHerdr starts a tracker on the fake with tiny delays and stops it with the test.
func startHerdr(t *testing.T, f *fakeHerdr, mod func(*Options)) *Tracker {
	t.Helper()
	o := Options{Env: mapEnv(herdrEnv(f)), HerdrPoll: time.Hour,
		Backoff: func(int) time.Duration { return time.Millisecond }}
	if mod != nil {
		mod(&o)
	}
	tr := New(o)
	ctx, cancel := context.WithCancel(context.Background())
	tr.Start(ctx)
	t.Cleanup(func() {
		cancel()
		waitGroup(t, tr)
	})
	return tr
}

// waitGroup waits for every goroutine the tracker started.
func waitGroup(t *testing.T, tr *Tracker) {
	t.Helper()
	done := make(chan struct{})
	go func() { tr.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tracker goroutines did not stop")
	}
}

func waitState(t *testing.T, tr *Tracker, want State) Activity {
	t.Helper()
	var got Activity
	waitFor(t, "state "+string(want), func() bool {
		a, ok := tr.Current()
		got = a
		return ok && a.State == want
	})
	return got
}

func waitAbsent(t *testing.T, tr *Tracker) {
	t.Helper()
	waitFor(t, "absent", func() bool { _, ok := tr.Current(); return !ok })
}

func TestHerdrInitialState(t *testing.T) {
	f := newFakeHerdr(t)
	f.set("working")
	var log bytes.Buffer
	var mu sync.Mutex
	tr := startHerdr(t, f, func(o *Options) { o.Log = lockedWriter{&mu, &log} })
	a := waitState(t, tr, StateWorking)
	if a.Source != "runtime" || a.Kind != "" || a.Since.IsZero() {
		t.Fatalf("activity = %+v", a)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(log.String(), "herdr 0.9.5 ok") {
		t.Fatalf("log = %q", log.String())
	}
	if d := tr.Describe(); !strings.Contains(d, "herdr 0.9.5 ok") || !strings.Contains(d, "working") {
		t.Fatalf("describe = %q", d)
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func TestHerdrEventUpdates(t *testing.T) {
	f := newFakeHerdr(t)
	tr := startHerdr(t, f, nil)
	waitState(t, tr, StateIdle)
	f.push("w1:p1", "working")
	waitState(t, tr, StateWorking)
	f.push("w1:p1", "blocked")
	waitState(t, tr, StateBlocked)
	f.push("w1:p1", "done")
	waitState(t, tr, StateDone)
}

func TestHerdrEventForOtherPaneIgnored(t *testing.T) {
	f := newFakeHerdr(t)
	tr := startHerdr(t, f, nil)
	waitState(t, tr, StateIdle)
	f.push("w9:p9", "working")
	f.push("w1:p1", "done") // after the first; once this lands the first was handled
	waitState(t, tr, StateDone)
}

func TestHerdrEventsLostResubscribesAndReconciles(t *testing.T) {
	f := newFakeHerdr(t)
	tr := startHerdr(t, f, nil)
	waitState(t, tr, StateIdle)
	before := f.count(func(f *fakeHerdr) int { return f.subscribe })
	// The change happens while events are lost: no event carries it, only the pane.get after resubscribing.
	f.set("blocked")
	f.eventsLost()
	waitState(t, tr, StateBlocked)
	waitFor(t, "resubscribe", func() bool { return f.count(func(f *fakeHerdr) int { return f.subscribe }) > before })
	// And the new subscription delivers.
	f.push("w1:p1", "working")
	waitState(t, tr, StateWorking)
}

func TestHerdrDroppedConnectionIsUnavailableAtOnceThenReconnects(t *testing.T) {
	f := newFakeHerdr(t)
	release := make(chan struct{})
	var mu sync.Mutex
	attempts := []int{}
	tr := startHerdr(t, f, func(o *Options) {
		o.Backoff = func(n int) time.Duration {
			mu.Lock()
			attempts = append(attempts, n)
			mu.Unlock()
			<-release // hold the source unavailable so the test can look at it
			return time.Millisecond
		}
	})
	waitState(t, tr, StateIdle)
	f.set("working")
	f.dropAll()
	waitAbsent(t, tr) // unavailable at once, before any retry
	close(release)
	waitState(t, tr, StateWorking)
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) == 0 || attempts[0] != 0 {
		t.Fatalf("backoff attempts = %v", attempts)
	}
}

func TestHerdrBackoffAttemptResetsAfterConnect(t *testing.T) {
	f := newFakeHerdr(t)
	var mu sync.Mutex
	var attempts []int
	tr := startHerdr(t, f, func(o *Options) {
		o.Backoff = func(n int) time.Duration {
			mu.Lock()
			attempts = append(attempts, n)
			mu.Unlock()
			return time.Millisecond
		}
	})
	waitState(t, tr, StateIdle)
	for i := 0; i < 2; i++ {
		mu.Lock()
		n := len(attempts)
		mu.Unlock()
		f.dropAll()
		waitFor(t, "backoff", func() bool { mu.Lock(); defer mu.Unlock(); return len(attempts) > n })
		waitState(t, tr, StateIdle)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, a := range attempts {
		if a != 0 {
			t.Fatalf("attempts = %v, every one followed a good connect so each is 0", attempts)
		}
	}
}

func TestHerdrRetriesWhileServerDown(t *testing.T) {
	f := newFakeHerdr(t)
	var mu sync.Mutex
	var attempts []int
	f.version = "" // every connect fails at ping
	tr := startHerdr(t, f, func(o *Options) {
		o.Backoff = func(n int) time.Duration {
			mu.Lock()
			attempts = append(attempts, n)
			mu.Unlock()
			return time.Millisecond
		}
	})
	waitFor(t, "three attempts", func() bool { mu.Lock(); defer mu.Unlock(); return len(attempts) >= 3 })
	mu.Lock()
	if attempts[0] != 0 || attempts[1] != 1 || attempts[2] != 2 {
		t.Fatalf("attempts = %v, want 0 1 2 while never connected", attempts)
	}
	mu.Unlock()
	if _, ok := tr.Current(); ok {
		t.Fatal("activity present while herdr is unavailable")
	}
}

func TestHerdrTerminalIDChangeIsUnavailable(t *testing.T) {
	f := newFakeHerdr(t)
	var mu sync.Mutex
	hold := true
	release := make(chan struct{})
	tr := startHerdr(t, f, func(o *Options) {
		o.HerdrPoll = 5 * time.Millisecond
		o.Backoff = func(int) time.Duration {
			mu.Lock()
			h := hold
			mu.Unlock()
			if h {
				<-release
			}
			return time.Millisecond
		}
	})
	waitState(t, tr, StateIdle)
	f.set("working")
	f.setTerm("term_2") // the pane id was reused by another terminal
	waitAbsent(t, tr)   // and the working of the new terminal is not reported as ours
	mu.Lock()
	hold = false
	mu.Unlock()
	close(release)
	// After reconnecting the source reads the new terminal id as its own.
	waitState(t, tr, StateWorking)
}

func TestHerdrUnknownStatusHasNoState(t *testing.T) {
	f := newFakeHerdr(t)
	f.set("unknown")
	tr := startHerdr(t, f, nil)
	waitFor(t, "connected", func() bool {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		return tr.rt.Available
	})
	if _, ok := tr.Current(); ok {
		t.Fatal("unknown status produced a state")
	}
	if d := tr.Describe(); !strings.Contains(d, "not an agent") {
		t.Fatalf("describe = %q", d)
	}
	f.push("w1:p1", "working")
	waitState(t, tr, StateWorking)
	f.push("w1:p1", "something-new") // a word we do not know is treated like unknown
	waitAbsent(t, tr)
}

func TestHerdrBackstopPollPicksUpChange(t *testing.T) {
	f := newFakeHerdr(t)
	tr := startHerdr(t, f, func(o *Options) { o.HerdrPoll = 5 * time.Millisecond })
	waitState(t, tr, StateIdle)
	f.set("working") // no event
	waitState(t, tr, StateWorking)
}

func TestHerdrPongWithoutVersionIsUnavailable(t *testing.T) {
	for name, mod := range map[string]func(*fakeHerdr){
		"no version":  func(f *fakeHerdr) { f.version = "" },
		"no protocol": func(f *fakeHerdr) { f.protocol = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeHerdr(t)
			mod(f)
			tr := startHerdr(t, f, nil)
			waitFor(t, "a ping", func() bool { return f.seenMethods()["ping"] >= 2 })
			if _, ok := tr.Current(); ok {
				t.Fatal("activity present")
			}
			if m := f.seenMethods(); m["pane.get"] != 0 {
				t.Fatalf("pane.get sent after a bad pong: %v", m)
			}
		})
	}
}

func TestHerdrPaneGetErrorIsUnavailable(t *testing.T) {
	f := newFakeHerdr(t)
	f.setPaneErr(true)
	tr := startHerdr(t, f, nil)
	waitFor(t, "two pane.get", func() bool { return f.count(func(f *fakeHerdr) int { return f.paneGets }) >= 2 })
	if _, ok := tr.Current(); ok {
		t.Fatal("activity present")
	}
	f.setPaneErr(false)
	waitState(t, tr, StateIdle)
}

func TestHerdrSendsOnlyReadMethods(t *testing.T) {
	f := newFakeHerdr(t)
	tr := startHerdr(t, f, func(o *Options) { o.HerdrPoll = 2 * time.Millisecond })
	waitState(t, tr, StateIdle)
	f.push("w1:p1", "working")
	f.eventsLost()
	f.dropAll()
	waitFor(t, "several pane.get", func() bool { return f.count(func(f *fakeHerdr) int { return f.paneGets }) >= 5 })
	for m := range f.seenMethods() {
		switch m {
		case "ping", "pane.get", "events.subscribe":
		default:
			t.Fatalf("source sent %q", m)
		}
	}
	_ = tr
}

func TestHerdrNotConfiguredWithoutBothVariables(t *testing.T) {
	for _, env := range []map[string]string{
		{"HERDR_SOCKET_PATH": "/nonexistent"},
		{"HERDR_PANE_ID": "w1:p1"},
	} {
		tr := New(Options{Env: mapEnv(env)})
		tr.Start(context.Background())
		if n := tr.spawned.Load(); n != 0 {
			t.Fatalf("env %v started %d goroutines", env, n)
		}
	}
}

func TestHerdrInjectedDial(t *testing.T) {
	f := newFakeHerdr(t)
	var mu sync.Mutex
	var paths []string
	tr := startHerdr(t, f, func(o *Options) {
		o.Dial = func(p string) (net.Conn, error) {
			mu.Lock()
			paths = append(paths, p)
			mu.Unlock()
			return net.Dial("unix", p)
		}
	})
	waitState(t, tr, StateIdle)
	mu.Lock()
	defer mu.Unlock()
	if len(paths) < 2 || paths[0] != f.path {
		t.Fatalf("dialled %v", paths)
	}
}
