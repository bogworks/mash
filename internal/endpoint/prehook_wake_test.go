package endpoint

import (
	"context"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
)

// v0.2.7: a session that has run no hook yet has nothing to idle on, so under SpoolFromStart mail that wakes wakes at
// once, each message once; fyi and result still wait. After the first hook the idle wake rules apply as before.
func TestSpoolFromStartWakesAtOnceBeforeTheFirstHook(t *testing.T) {
	oi := IdleWake
	IdleWake = time.Hour // any wake inside the test is not the idle wake
	t.Cleanup(func() { IdleWake = oi })
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledClaudeFromStart(t, "a") // sp.Touch is never called
	b := f.client(t, "b")
	if _, err := b.Send(context.Background(), "a", "first, no kind", 0); err != nil {
		t.Fatal(err)
	}
	got := ev.waitFor(t, 1, 3*time.Second)
	if got[0] != "1 message from b (task)" {
		t.Fatalf("the wake %q, want the summary line alone", got[0])
	}
	if n := unread(t, a); n != 1 {
		t.Fatalf("unread %d, want 1 (a wake is not a receipt)", n)
	}
	if _, err := b.SendWith(context.Background(), "a", "second", SendOptions{Kind: kind.Task}); err != nil {
		t.Fatal(err)
	}
	got = ev.waitFor(t, 2, 3*time.Second) // still no hook: the second message rings too
	if got[1] != "2 messages from b (task)" {
		t.Fatalf("the second wake %q", got[1])
	}
	time.Sleep(4 * SpoolCheck)
	if got := ev.all(); len(got) != 2 {
		t.Fatalf("%d wakes, want 2 (each message rings once): %q", len(got), got)
	}
	if w := waiting(t, sp); len(w) != 2 {
		t.Fatalf("waiting %+v", w)
	}
}

func TestSpoolFromStartFyiAndResultDoNotWakeBeforeTheFirstHook(t *testing.T) {
	oi := IdleWake
	IdleWake = time.Hour
	t.Cleanup(func() { IdleWake = oi })
	f := newFixture(t, "a", "b")
	_, ev, sp := f.runSpooledClaudeFromStart(t, "a")
	b := f.client(t, "b")
	for _, k := range []kind.Kind{kind.FYI, kind.Result} {
		if _, err := b.SendWith(context.Background(), "a", "quiet "+string(k), SendOptions{Kind: k}); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "both spooled", func() bool { return len(waiting(t, sp)) == 2 })
	time.Sleep(8 * SpoolCheck)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("fyi or result woke the session before any hook: %q", got)
	}
}

func TestSpoolFromStartWaitsForTheIdleWakeAfterAHook(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.idle = 1500 * time.Millisecond
	_, ev, sp := f.runSpooledClaudeFromStart(t, "a")
	if err := sp.Touch(); err != nil { // a hook has run
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := f.client(t, "b").Send(context.Background(), "a", "after a hook", 0); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	time.Sleep(f.idle / 2)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("woke before the idle wake: %q", got)
	}
	ev.waitFor(t, 1, 5*time.Second)
	if d := time.Since(start); d < f.idle {
		t.Fatalf("woke after %v, before the idle wake %v", d, f.idle)
	}
}

// A pre-hook ring may be dropped by Claude Code (a channel event before the first prompt), so it must not start the
// backoff: the idle wake still follows the oldest waking message, not twice the idle wake after the ring.
func TestSpoolFromStartPreHookRingDoesNotStartTheBackoff(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.idle = 1500 * time.Millisecond
	_, ev, _ := f.runSpooledClaudeFromStart(t, "a") // no hook ever runs
	start := time.Now()
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", "ring once", SendOptions{Kind: kind.Task}); err != nil {
		t.Fatal(err)
	}
	ev.waitFor(t, 1, 3*time.Second) // the pre-hook ring
	if d := time.Since(start); d >= f.idle {
		t.Fatalf("the ring came after %v, want at once", d)
	}
	ev.waitFor(t, 2, 5*time.Second) // the idle wake
	d := time.Since(start)
	if d < f.idle-100*time.Millisecond {
		t.Fatalf("second wake after %v, before the idle wake %v", d, f.idle)
	}
	if d >= 2*f.idle {
		t.Fatalf("second wake after %v, want about the idle wake %v, not the backoff 2x", d, f.idle)
	}
}
