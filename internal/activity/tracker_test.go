package activity

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func TestCombine(t *testing.T) {
	unavailable := runtimeView{}
	notAgent := runtimeView{Available: true}
	rtIdle := runtimeView{Available: true, State: StateIdle}
	rtWorking := runtimeView{Available: true, State: StateWorking}
	rtDone := runtimeView{Available: true, State: StateDone}
	rtBlocked := runtimeView{Available: true, State: StateBlocked}
	idleAfterDialog := runtimeView{Available: true, State: StateIdle, BlockedSeen: t0.Add(time.Second)}
	idleBeforeDialog := runtimeView{Available: true, State: StateIdle, BlockedSeen: t0.Add(-time.Second)}
	none := hooksView{}
	hWorking := hooksView{Present: true, State: StateWorking, At: t0}
	hDone := hooksView{Present: true, State: StateDone, At: t0}
	hPerm := hooksView{Present: true, State: StateBlocked, Kind: KindPermission, At: t0}
	hQuestion := hooksView{Present: true, State: StateBlocked, Kind: KindQuestion, At: t0}

	cases := []struct {
		name string
		rt   runtimeView
		hk   hooksView
		want Activity
		ok   bool
	}{
		{"runtime only", rtWorking, none, Activity{State: StateWorking, Source: "runtime"}, true},
		{"runtime idle only", rtIdle, none, Activity{State: StateIdle, Source: "runtime"}, true},
		{"same state both", rtWorking, hWorking, Activity{State: StateWorking, Source: "both"}, true},
		{"done both", rtDone, hDone, Activity{State: StateDone, Source: "both"}, true},
		{"blocked both takes the hooks kind", rtBlocked, hQuestion, Activity{State: StateBlocked, Kind: KindQuestion, Source: "both"}, true},
		{"blocked runtime only has no kind", rtBlocked, none, Activity{State: StateBlocked, Source: "runtime"}, true},
		{"different states: runtime, no kind", rtBlocked, hWorking, Activity{State: StateBlocked, Source: "runtime"}, true},
		{"runtime working beats hooks blocked", rtWorking, hPerm, Activity{State: StateWorking, Source: "runtime"}, true},
		{"runtime done beats hooks blocked", rtDone, hQuestion, Activity{State: StateDone, Source: "runtime"}, true},
		{"idle with hooks working: runtime", rtIdle, hWorking, Activity{State: StateIdle, Source: "runtime"}, true},
		{"idle with hooks done: runtime", rtIdle, hDone, Activity{State: StateIdle, Source: "runtime"}, true},
		{"idle, hooks blocked, never saw blocked: blocked", rtIdle, hPerm, Activity{State: StateBlocked, Kind: KindPermission, Source: "hooks"}, true},
		{"idle, hooks blocked, saw it before the record: blocked", idleBeforeDialog, hQuestion, Activity{State: StateBlocked, Kind: KindQuestion, Source: "hooks"}, true},
		{"idle, hooks blocked, saw it after the record: idle", idleAfterDialog, hPerm, Activity{State: StateIdle, Source: "runtime"}, true},
		{"runtime unavailable, hooks state", unavailable, hWorking, Activity{State: StateWorking, Source: "hooks"}, true},
		{"runtime unavailable, hooks blocked", unavailable, hPerm, Activity{State: StateBlocked, Kind: KindPermission, Source: "hooks"}, true},
		{"not an agent, hooks state", notAgent, hDone, Activity{State: StateDone, Source: "hooks"}, true},
		{"unavailable, nothing", unavailable, none, Activity{}, false},
		{"not an agent, nothing", notAgent, none, Activity{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := combine(tc.rt, tc.hk)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("got %+v %v, want %+v %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestRule2BothWaysThroughTracker(t *testing.T) {
	h := newHooksTracker(t, envLaunch)
	perm := h.rec("PermissionRequest")
	perm.Tool = "Bash"
	h.write(t, perm)

	// herdr does not recognise the dialog and says idle: stay blocked.
	h.clk.Advance(time.Second)
	h.setRuntime(RuntimeReport{Available: true, State: StateIdle})
	if a, _ := h.Current(); a.State != StateBlocked || a.Kind != KindPermission || a.Source != "hooks" {
		t.Fatalf("idle without a sighting: %+v", a)
	}

	// herdr sees the dialog: both agree.
	h.clk.Advance(time.Second)
	h.setRuntime(RuntimeReport{Available: true, State: StateBlocked})
	if a, _ := h.Current(); a.State != StateBlocked || a.Kind != KindPermission || a.Source != "both" {
		t.Fatalf("both blocked: %+v", a)
	}

	// The person refused: no hook fires, herdr says idle, and herdr had seen the dialog: idle.
	h.clk.Advance(time.Second)
	h.setRuntime(RuntimeReport{Available: true, State: StateIdle})
	if a, _ := h.Current(); a.State != StateIdle || a.Source != "runtime" || a.Kind != "" {
		t.Fatalf("idle after a sighting: %+v", a)
	}

	// A new dialog (a newer record) that herdr has not shown is blocked again.
	h.clk.Advance(time.Second)
	q := h.rec("PermissionRequest")
	q.Tool = "AskUserQuestion"
	h.write(t, q)
	if a, _ := h.Current(); a.State != StateBlocked || a.Kind != KindQuestion || a.Source != "hooks" {
		t.Fatalf("new record: %+v", a)
	}

	// Runtime working beats hooks blocked.
	h.clk.Advance(time.Second)
	h.setRuntime(RuntimeReport{Available: true, State: StateWorking})
	if a, _ := h.Current(); a.State != StateWorking || a.Source != "runtime" {
		t.Fatalf("working: %+v", a)
	}
}

func TestRuntimeUnavailableFallsBackToHooks(t *testing.T) {
	h := newHooksTracker(t, envLaunch)
	h.write(t, h.rec("UserPromptSubmit"))
	h.setRuntime(RuntimeReport{Available: true, State: StateDone})
	if a, _ := h.Current(); a.State != StateDone || a.Source != "runtime" {
		t.Fatalf("%+v", a)
	}
	h.setRuntime(RuntimeReport{})
	if a, _ := h.Current(); a.State != StateWorking || a.Source != "hooks" {
		t.Fatalf("%+v", a)
	}
}

func TestSinceStableAcrossPollsAndMovesWithStateOrKind(t *testing.T) {
	h := newHooksTracker(t, envLaunch)
	start := h.clk.Now()
	h.write(t, h.rec("UserPromptSubmit"))
	a, _ := h.Current()
	if !a.Since.Equal(start) {
		t.Fatalf("since %v, want %v", a.Since, start)
	}
	for i := 0; i < 3; i++ {
		h.clk.Advance(time.Minute)
		h.pollHooks()
		if b, _ := h.Current(); !b.Since.Equal(start) {
			t.Fatalf("since moved to %v", b.Since)
		}
	}
	// Another event with the same state does not move it.
	h.write(t, h.rec("PreToolUse"))
	if b, _ := h.Current(); !b.Since.Equal(start) {
		t.Fatalf("same state moved since to %v", b.Since)
	}
	// A new state does.
	h.clk.Advance(time.Minute)
	stop := h.rec("Stop")
	h.write(t, stop)
	if b, _ := h.Current(); b.State != StateDone || !b.Since.Equal(stop.At) {
		t.Fatalf("%+v", b)
	}
	// So does a new kind of the same state.
	h.clk.Advance(time.Minute)
	p := h.rec("PermissionRequest")
	p.Tool = "Bash"
	h.write(t, p)
	b, _ := h.Current()
	if b.Kind != KindPermission || !b.Since.Equal(p.At) {
		t.Fatalf("%+v", b)
	}
	h.clk.Advance(time.Minute)
	q := h.rec("PermissionRequest")
	q.Tool = "AskUserQuestion"
	h.write(t, q)
	if c, _ := h.Current(); c.Kind != KindQuestion || !c.Since.Equal(q.At) {
		t.Fatalf("%+v", c)
	}
}

func TestSinceNotResetBySourceChangeAlone(t *testing.T) {
	h := newHooksTracker(t, envLaunch)
	start := h.clk.Now()
	h.write(t, h.rec("PreToolUse"))
	h.clk.Advance(time.Minute)
	h.setRuntime(RuntimeReport{Available: true, State: StateWorking}) // hooks-only becomes both
	a, _ := h.Current()
	if a.Source != "both" || !a.Since.Equal(start) {
		t.Fatalf("%+v, want both since %v", a, start)
	}
	h.clk.Advance(time.Minute)
	h.setRuntime(RuntimeReport{}) // and back to hooks-only
	a, _ = h.Current()
	if a.Source != "hooks" || !a.Since.Equal(start) {
		t.Fatalf("%+v, want hooks since %v", a, start)
	}
}

func TestSinceAfterAbsenceIsNew(t *testing.T) {
	clk := newClock()
	tr := New(Options{Env: mapEnv(nil), Now: clk.Now})
	tr.setRuntime(RuntimeReport{Available: true, State: StateIdle})
	first, _ := tr.Current()
	tr.setRuntime(RuntimeReport{})
	if _, ok := tr.Current(); ok {
		t.Fatal("want absent")
	}
	clk.Advance(time.Hour)
	tr.setRuntime(RuntimeReport{Available: true, State: StateIdle})
	again, _ := tr.Current()
	if !again.Since.After(first.Since) {
		t.Fatalf("since %v did not move past %v after an absence", again.Since, first.Since)
	}
}

func TestSinceFromRuntimeIsWhenSeen(t *testing.T) {
	clk := newClock()
	tr := New(Options{Env: mapEnv(nil), Now: clk.Now})
	tr.setRuntime(RuntimeReport{Available: true, State: StateWorking})
	if a, _ := tr.Current(); !a.Since.Equal(clk.Now()) || a.Since.Location() != time.UTC {
		t.Fatalf("%+v", a)
	}
}

// fakeRuntime is a RuntimeSource for the tests: Run counts its calls and blocks until ctx is done, reporting
// whatever the test pushes.
type fakeRuntime struct {
	runs    atomic.Int32
	in      chan RuntimeReport
	stopped atomic.Bool
}

func newFakeRuntime() *fakeRuntime { return &fakeRuntime{in: make(chan RuntimeReport, 16)} }

func (f *fakeRuntime) Run(ctx context.Context, report func(RuntimeReport)) {
	f.runs.Add(1)
	defer f.stopped.Store(true)
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-f.in:
			report(r)
		}
	}
}

func (f *fakeRuntime) Name() string { return "fakert" }

func (f *fakeRuntime) Describe(r RuntimeReport) string {
	if r.Version != "" {
		return "fakert " + r.Version + " fine"
	}
	return "fakert fine"
}

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

// waitFor polls cond until it holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func mapEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestOffRunsNothing(t *testing.T) {
	f := newFakeRuntime()
	tr := New(Options{Off: true, Env: mapEnv(nil), Dir: t.TempDir(), Runtime: f})
	tr.Start(context.Background())
	if n := tr.spawned.Load(); n != 0 {
		t.Fatalf("%d goroutines started", n)
	}
	if _, ok := tr.Current(); ok {
		t.Fatal("off but present")
	}
	if d := tr.Describe(); d != "activity: off" {
		t.Fatalf("describe = %q", d)
	}
	if n := f.runs.Load(); n != 0 {
		t.Fatalf("the runtime source was run %d times", n)
	}
}

func TestNotConfiguredRunsNothing(t *testing.T) {
	tr := New(Options{Env: mapEnv(nil)})
	tr.Start(context.Background())
	if n := tr.spawned.Load(); n != 0 {
		t.Fatalf("%d goroutines started", n)
	}
	if _, ok := tr.Current(); ok {
		t.Fatal("present")
	}
	if d := tr.Describe(); d != "activity: absent (no runtime source, no hook record)" {
		t.Fatalf("describe = %q", d)
	}
}

func TestStartTwiceStartsOnce(t *testing.T) {
	dir := t.TempDir()
	f := newFakeRuntime()
	tr := New(Options{Env: mapEnv(nil), Dir: dir, HooksPoll: time.Hour, Runtime: f})
	ctx, cancel := context.WithCancel(context.Background())
	tr.Start(ctx)
	tr.Start(ctx)
	if n := tr.spawned.Load(); n != 2 {
		t.Fatalf("%d goroutines, want the source and the hooks poll", n)
	}
	waitFor(t, "source run", func() bool { return f.runs.Load() == 1 })
	cancel()
	waitGroup(t, tr)
	if n := f.runs.Load(); n != 1 {
		t.Fatalf("source run %d times", n)
	}
}

func TestRuntimeReportsReachCurrent(t *testing.T) {
	f := newFakeRuntime()
	tr := New(Options{Env: mapEnv(nil), Runtime: f})
	ctx, cancel := context.WithCancel(context.Background())
	tr.Start(ctx)
	f.in <- RuntimeReport{Available: true, State: StateWorking, Version: "1.2"}
	if a := waitState(t, tr, StateWorking); a.Source != "runtime" {
		t.Fatalf("%+v", a)
	}
	f.in <- RuntimeReport{Available: true, State: StateBlocked, Version: "1.2"}
	waitState(t, tr, StateBlocked)
	f.in <- RuntimeReport{Available: true}
	waitFor(t, "not an agent", func() bool { _, ok := tr.Current(); return !ok })
	f.in <- RuntimeReport{}
	waitFor(t, "unavailable", func() bool { return strings.Contains(tr.Describe(), "fakert unavailable") })
	cancel()
	waitGroup(t, tr)
}

func TestCancelStopsEverything(t *testing.T) {
	f := newFakeRuntime()
	tr := New(Options{Env: mapEnv(nil), Dir: t.TempDir(), HooksPoll: time.Millisecond, Runtime: f})
	ctx, cancel := context.WithCancel(context.Background())
	tr.Start(ctx)
	f.in <- RuntimeReport{Available: true, State: StateIdle}
	waitState(t, tr, StateIdle)
	if tr.spawned.Load() < 2 {
		t.Fatalf("spawned %d", tr.spawned.Load())
	}
	cancel()
	waitGroup(t, tr)
	if !f.stopped.Load() {
		t.Fatal("the source did not stop")
	}
}

func TestCancelledBeforeStartStopsEverything(t *testing.T) {
	f := newFakeRuntime()
	tr := New(Options{Env: mapEnv(nil), Dir: t.TempDir(), HooksPoll: time.Millisecond, Runtime: f})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr.Start(ctx)
	waitGroup(t, tr)
}

// TestActivityHasOnlyFixedFields guards against free text leaving the package: a field added to Activity breaks
// this test and forces a review of whether it may be sent to a peer.
func TestActivityHasOnlyFixedFields(t *testing.T) {
	rt := reflect.TypeOf(Activity{})
	var got []string
	for i := 0; i < rt.NumField(); i++ {
		got = append(got, rt.Field(i).Name+" "+rt.Field(i).Type.String())
	}
	want := []string{"State activity.State", "Kind activity.Kind", "Since time.Time", "Source string"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Activity fields = %v, want %v", got, want)
	}
}

func TestDescribeForms(t *testing.T) {
	f := newFakeRuntime()
	dir := t.TempDir()
	env := map[string]string{"MASH_LAUNCH_TOKEN": "tok-1"}
	tr := New(Options{Env: mapEnv(env), Dir: dir, HooksPoll: 2 * time.Millisecond, Runtime: f})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.Start(ctx)
	f.in <- RuntimeReport{Available: true, State: StateWorking, Version: "1.2"}
	waitState(t, tr, StateWorking)
	if err := WriteRecord(dir, Record{Event: "UserPromptSubmit", At: time.Now(), Launch: "tok-1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "source both", func() bool { a, _ := tr.Current(); return a.Source == "both" })
	// The source's own text is used for an available report.
	want := "activity: fakert 1.2 fine, hooks ok: working (source both)"
	if d := tr.Describe(); d != want {
		t.Fatalf("describe = %q, want %q", d, want)
	}

	// Blocked says its kind.
	h := newHooksTracker(t, envLaunch)
	p := h.rec("PermissionRequest")
	p.Tool = "Bash"
	h.write(t, p)
	if d := h.Describe(); !strings.Contains(d, "blocked (permission)") || !strings.Contains(d, "source hooks") {
		t.Fatalf("describe = %q", d)
	}
}
