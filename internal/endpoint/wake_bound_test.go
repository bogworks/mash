package endpoint

import (
	"context"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/spool"
)

// These tests pin the bound of ADR 0069: the idle spell is measured from the later of the oldest held mail and the last
// hook drain, and a crowded backlog wakes at once only when no hook drained within the idle wake. Every check that no
// wake has happened yet has a wide margin, and every wait for a wake is generous, so a slow runner cannot flake them.

const boundIdle = 3 * time.Second

// boundFixture is kindFixtureSpool with the idle wake set before the endpoint starts (it is read while running).
func boundFixture(t *testing.T, idle time.Duration) (*events, spool.Spool, *Endpoint) {
	t.Helper()
	f := newFixture(t, "a", "b")
	f.idle = idle
	_, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	return ev, sp, f.client(t, "b")
}

// noWakeBefore fails if the session was woken before d has passed since start; it sleeps up to that point first.
func noWakeBefore(t *testing.T, ev *events, start time.Time, d time.Duration) {
	t.Helper()
	if wait := time.Until(start.Add(d)); wait > 0 {
		time.Sleep(wait)
	}
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("woken %v after the drain, before the idle wake (%v) ran from it: %q", time.Since(start), boundIdle, got)
	}
}

// drainAt drains seq as a hook would and returns a time not after the drain, a safe lower bound for when the idle spell
// restarted.
func drainAt(t *testing.T, sp interface{ Drained(uint64) error }, seq uint64) time.Time {
	t.Helper()
	at := time.Now()
	if err := sp.Drained(seq); err != nil {
		t.Fatal(err)
	}
	return at
}

// A held task that has not yet woken is not woken at the end of its own idle spell once a hook drained other mail in the
// meantime: the spell restarts at the drain, so the wake comes an idle wake after it and not before. (Held fyi never
// wakes by the idle spell, so a task stands for the held mail here.)
func TestIdleSpellRunsFromTheLastDrain(t *testing.T) {
	ev, sp, b := boundFixture(t, boundIdle)
	ctx := context.Background()
	for _, s := range []string{"held", "drained"} {
		if _, err := b.SendWith(ctx, "a", s, SendOptions{Kind: kind.Task}); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "2 spooled", func() bool { return len(waiting(t, sp)) == 2 })
	time.Sleep(boundIdle / 2) // the held task is half an idle wake old: without the rule it wakes half an idle wake from now
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("woken before the idle wake: %q", got)
	}
	at := drainAt(t, sp, 2)
	noWakeBefore(t, ev, at, boundIdle*6/10) // past where the oldest mail alone would wake it
	ev.waitFor(t, 1, boundIdle+5*time.Second)
	if since := time.Since(at); since < boundIdle {
		t.Fatalf("woken %v after the drain, want at least the idle wake %v", since, boundIdle)
	}
}

// A crowded backlog that nobody drains wakes at once, long before the idle wake.
func TestCrowdedBacklogWithoutDrainWakesAtOnce(t *testing.T) {
	old := SpoolWakeCount
	SpoolWakeCount = 5
	t.Cleanup(func() { SpoolWakeCount = old })
	const idle = 20 * time.Second
	ev, _, b := boundFixture(t, idle)
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 5; i++ {
		if _, err := b.SendWith(ctx, "a", "fyi", SendOptions{Kind: kind.FYI}); err != nil {
			t.Fatal(err)
		}
	}
	ev.waitFor(t, 1, 10*time.Second)
	if since := time.Since(start); since >= idle/2 {
		t.Fatalf("a crowded backlog woke after %v, want far less than the idle wake %v", since, idle)
	}
}

// A crowded backlog behind a hook drain waits for the idle wake after the drain: no wake before it, then a wake.
func TestCrowdedBacklogAfterADrainWaitsForTheIdleSpell(t *testing.T) {
	old := SpoolWakeCount
	SpoolWakeCount = 5
	t.Cleanup(func() { SpoolWakeCount = old })
	ev, sp, b := boundFixture(t, boundIdle)
	ctx := context.Background()
	send := func() {
		t.Helper()
		if _, err := b.SendWith(ctx, "a", "fyi", SendOptions{Kind: kind.FYI}); err != nil {
			t.Fatal(err)
		}
	}
	send()
	until(t, "1 spooled", func() bool { return len(waiting(t, sp)) == 1 })
	at := drainAt(t, sp, 1)
	for i := 0; i < 5; i++ { // crowded now, and the drain was moments ago
		send()
	}
	until(t, "5 spooled", func() bool { return len(waiting(t, sp)) == 5 })
	noWakeBefore(t, ev, at, boundIdle*6/10)
	ev.waitFor(t, 1, boundIdle+5*time.Second)
	if since := time.Since(at); since < boundIdle {
		t.Fatalf("woken %v after the drain, want at least the idle wake %v", since, boundIdle)
	}
}
