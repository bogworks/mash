package herdr

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/activity"
)

// recorder keeps the last report the source made.
type recorder struct {
	mu   sync.Mutex
	last activity.RuntimeReport
}

func (r *recorder) report(x activity.RuntimeReport) {
	r.mu.Lock()
	r.last = x
	r.mu.Unlock()
}

func (r *recorder) get() activity.RuntimeReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// running is a source started on the fake, with a way to stop it and wait for Run to return.
type running struct {
	*recorder
	src  activity.RuntimeSource
	done chan struct{}
}

// startHerdr starts a source on the fake with tiny delays and stops it with the test.
func startHerdr(t *testing.T, f *fakeHerdr, mod func(*Options)) *running {
	t.Helper()
	o := Options{Poll: time.Hour, Backoff: func(int) time.Duration { return time.Millisecond }}
	if mod != nil {
		mod(&o)
	}
	src, ok := FromEnv(mapEnv(herdrEnv(f)), o)
	if !ok {
		t.Fatal("not configured")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{recorder: &recorder{}, src: src, done: make(chan struct{})}
	go func() { src.Run(ctx, r.report); close(r.done) }()
	t.Cleanup(func() {
		cancel()
		waitDone(t, r)
	})
	return r
}

// waitDone waits for Run to return, which it does only after every goroutine it started has stopped.
func waitDone(t *testing.T, r *running) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("source goroutines did not stop")
	}
}

func waitState(t *testing.T, r *running, want activity.State) activity.RuntimeReport {
	t.Helper()
	var got activity.RuntimeReport
	waitFor(t, "state "+string(want), func() bool {
		got = r.get()
		return got.Available && got.State == want
	})
	return got
}

// waitAbsent waits for a report that gives no state: unavailable, or not an agent pane.
func waitAbsent(t *testing.T, r *running) {
	t.Helper()
	waitFor(t, "absent", func() bool { x := r.get(); return !x.Available || x.State == "" })
}

func absent(r *running) bool { x := r.get(); return !x.Available || x.State == "" }

func TestHerdrInitialState(t *testing.T) {
	f := newFakeHerdr(t)
	f.set("working")
	var log bytes.Buffer
	var mu sync.Mutex
	tr := startHerdr(t, f, func(o *Options) { o.Log = lockedWriter{&mu, &log} })
	a := waitState(t, tr, activity.StateWorking)
	if !a.Available || a.Version != "0.9.5" {
		t.Fatalf("report = %+v", a)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(log.String(), "herdr 0.9.5 ok") {
		t.Fatalf("log = %q", log.String())
	}
	if d := tr.src.Describe(a); d != "herdr 0.9.5 ok" {
		t.Fatalf("describe = %q", d)
	}
	if d := tr.src.Describe(activity.RuntimeReport{Available: true}); d != "herdr ok" {
		t.Fatalf("describe without a version = %q", d)
	}
	if n := tr.src.Name(); n != "herdr" {
		t.Fatalf("name = %q", n)
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
	waitState(t, tr, activity.StateIdle)
	f.push("w1:p1", "working")
	waitState(t, tr, activity.StateWorking)
	f.push("w1:p1", "blocked")
	waitState(t, tr, activity.StateBlocked)
	f.push("w1:p1", "done")
	waitState(t, tr, activity.StateDone)
}

func TestHerdrEventForOtherPaneIgnored(t *testing.T) {
	f := newFakeHerdr(t)
	tr := startHerdr(t, f, nil)
	waitState(t, tr, activity.StateIdle)
	f.push("w9:p9", "working")
	f.push("w1:p1", "done") // after the first; once this lands the first was handled
	waitState(t, tr, activity.StateDone)
}

func TestHerdrEventsLostResubscribesAndReconciles(t *testing.T) {
	f := newFakeHerdr(t)
	tr := startHerdr(t, f, nil)
	waitState(t, tr, activity.StateIdle)
	before := f.count(func(f *fakeHerdr) int { return f.subscribe })
	// The change happens while events are lost: no event carries it, only the pane.get after resubscribing.
	f.set("blocked")
	f.eventsLost()
	waitState(t, tr, activity.StateBlocked)
	waitFor(t, "resubscribe", func() bool { return f.count(func(f *fakeHerdr) int { return f.subscribe }) > before })
	// And the new subscription delivers.
	f.push("w1:p1", "working")
	waitState(t, tr, activity.StateWorking)
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
	waitState(t, tr, activity.StateIdle)
	f.set("working")
	f.dropAll()
	waitAbsent(t, tr) // unavailable at once, before any retry
	close(release)
	waitState(t, tr, activity.StateWorking)
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
	waitState(t, tr, activity.StateIdle)
	for i := 0; i < 2; i++ {
		mu.Lock()
		n := len(attempts)
		mu.Unlock()
		f.dropAll()
		waitFor(t, "backoff", func() bool { mu.Lock(); defer mu.Unlock(); return len(attempts) > n })
		waitState(t, tr, activity.StateIdle)
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
	if !absent(tr) {
		t.Fatal("state reported while herdr is unavailable")
	}
}

func TestHerdrTerminalIDChangeIsUnavailable(t *testing.T) {
	f := newFakeHerdr(t)
	var mu sync.Mutex
	hold := true
	release := make(chan struct{})
	tr := startHerdr(t, f, func(o *Options) {
		o.Poll = 5 * time.Millisecond
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
	waitState(t, tr, activity.StateIdle)
	f.set("working")
	f.setTerm("term_2") // the pane id was reused by another terminal
	waitAbsent(t, tr)   // and the working of the new terminal is not reported as ours
	mu.Lock()
	hold = false
	mu.Unlock()
	close(release)
	// After reconnecting the source reads the new terminal id as its own.
	waitState(t, tr, activity.StateWorking)
}

func TestHerdrUnknownStatusHasNoState(t *testing.T) {
	f := newFakeHerdr(t)
	f.set("unknown")
	tr := startHerdr(t, f, nil)
	waitFor(t, "connected", func() bool { return tr.get().Available })
	if !absent(tr) {
		t.Fatal("unknown status produced a state")
	}
	f.push("w1:p1", "working")
	waitState(t, tr, activity.StateWorking)
	f.push("w1:p1", "something-new") // a word we do not know is treated like unknown
	waitAbsent(t, tr)
}

func TestHerdrBackstopPollPicksUpChange(t *testing.T) {
	f := newFakeHerdr(t)
	tr := startHerdr(t, f, func(o *Options) { o.Poll = 5 * time.Millisecond })
	waitState(t, tr, activity.StateIdle)
	f.set("working") // no event
	waitState(t, tr, activity.StateWorking)
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
			if !absent(tr) {
				t.Fatal("state reported")
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
	if !absent(tr) {
		t.Fatal("state reported")
	}
	f.setPaneErr(false)
	waitState(t, tr, activity.StateIdle)
}

func TestHerdrSendsOnlyReadMethods(t *testing.T) {
	f := newFakeHerdr(t)
	tr := startHerdr(t, f, func(o *Options) { o.Poll = 2 * time.Millisecond })
	waitState(t, tr, activity.StateIdle)
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
		{},
		{"HERDR_SOCKET_PATH": "/nonexistent"},
		{"HERDR_PANE_ID": "w1:p1"},
		{"HERDR_SOCKET_PATH": "", "HERDR_PANE_ID": "w1:p1"},
	} {
		if src, ok := FromEnv(mapEnv(env), Options{}); ok || src != nil {
			t.Fatalf("env %v gave a source", env)
		}
	}
}

// TestHerdrThroughTracker runs the source under a real tracker: the combination, the status text and the stop.
func TestHerdrThroughTracker(t *testing.T) {
	f := newFakeHerdr(t)
	f.set("working")
	var log bytes.Buffer
	var mu sync.Mutex
	src, _ := FromEnv(mapEnv(herdrEnv(f)), Options{Poll: time.Hour, Log: lockedWriter{&mu, &log},
		Backoff: func(int) time.Duration { return time.Millisecond }})
	tr := activity.New(activity.Options{Env: mapEnv(nil), Runtime: src})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.Start(ctx)
	waitFor(t, "working", func() bool { a, ok := tr.Current(); return ok && a.State == activity.StateWorking })
	if a, _ := tr.Current(); a.Source != "runtime" || a.Kind != "" || a.Since.IsZero() {
		t.Fatalf("activity = %+v", a)
	}
	want := "activity: herdr 0.9.5 ok: working (source runtime)"
	if d := tr.Describe(); d != want {
		t.Fatalf("describe = %q, want %q", d, want)
	}
	f.set("unknown")
	f.push("w1:p1", "unknown")
	waitFor(t, "absent", func() bool { _, ok := tr.Current(); return !ok })
	if d := tr.Describe(); d != "activity: absent (herdr: not an agent pane, no hook record)" {
		t.Fatalf("describe = %q", d)
	}
	f.dropAll()
	f.set("working")
	waitFor(t, "back", func() bool { a, ok := tr.Current(); return ok && a.State == activity.StateWorking })
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
	waitState(t, tr, activity.StateIdle)
	mu.Lock()
	defer mu.Unlock()
	if len(paths) < 2 || paths[0] != f.path {
		t.Fatalf("dialled %v", paths)
	}
}

func TestHerdrCancelDuringBackoffStops(t *testing.T) {
	f := newFakeHerdr(t)
	f.version = ""
	r := startHerdr(t, f, func(o *Options) { o.Backoff = func(int) time.Duration { return time.Hour } })
	waitFor(t, "first ping", func() bool { return f.seenMethods()["ping"] >= 1 })
	_ = r // cleanup cancels and waits for Run
}

func TestHerdrCancelledBeforeRunStops(t *testing.T) {
	f := newFakeHerdr(t)
	src, _ := FromEnv(mapEnv(herdrEnv(f)), Options{Backoff: func(int) time.Duration { return time.Millisecond }})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &running{recorder: &recorder{}, src: src, done: make(chan struct{})}
	go func() { src.Run(ctx, r.report); close(r.done) }()
	waitDone(t, r)
}
