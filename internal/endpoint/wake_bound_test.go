package endpoint

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
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

// noWakeBefore fails if the session was woken again (beyond the already wakes seen) before d has passed since start; it
// sleeps up to that point first.
func noWakeBefore(t *testing.T, ev *events, start time.Time, d time.Duration, already int) {
	t.Helper()
	if wait := time.Until(start.Add(d)); wait > 0 {
		time.Sleep(wait)
	}
	if got := ev.all(); len(got) != already {
		t.Fatalf("woken %v after the drain, before the idle wake (%v) ran from it: %q", time.Since(start), boundIdle, got)
	}
}

// drainAt drains seq as a hook would and returns a time not after the drain, a safe lower bound for when the idle spell
// restarted.
func drainAt(t *testing.T, sp interface{ Drained(spool.Ref) error }, seq uint64) time.Time {
	t.Helper()
	at := time.Now()
	if err := sp.Drained(spool.Ref{Seq: seq}); err != nil {
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
	noWakeBefore(t, ev, at, boundIdle*6/10, 0) // past where the oldest mail alone would wake it
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

// A crowded backlog behind a hook drain waits for the idle wake after the drain while it is shrinking: no wake before
// it, then a wake.
func TestCrowdedBacklogAfterADrainWaitsForTheIdleSpell(t *testing.T) {
	old := SpoolWakeCount
	SpoolWakeCount = 5
	t.Cleanup(func() { SpoolWakeCount = old })
	ev, sp, b := boundFixture(t, boundIdle)
	for i := 0; i < 6; i++ {
		sendFYI(t, b, "fyi")
	}
	until(t, "6 spooled", func() bool { return len(waiting(t, sp)) == 6 })
	// The six crowd the backlog and wake it once, however late a slow runner delivers that wake; only then drain, so
	// that wake cannot be taken for one that the drain should have held back.
	ev.waitFor(t, 1, 10*time.Second)
	at := drainAt(t, sp, 1) // 5 held: still crowded, and not growing
	until(t, "5 held", func() bool { return len(waiting(t, sp)) == 5 })
	noWakeBefore(t, ev, at, boundIdle*6/10, 1)
	ev.waitFor(t, 2, boundIdle+5*time.Second)
	if since := time.Since(at); since < boundIdle {
		t.Fatalf("woken %v after the drain, want at least the idle wake %v", since, boundIdle)
	}
}

func sendFYI(t *testing.T, b *Endpoint, body string) {
	t.Helper()
	if _, err := b.SendWith(context.Background(), "a", body, SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
}

// A drain does not hold back the wake once new mail makes the crowded backlog grow: the wake comes long before the
// idle wake.
func TestCrowdedBacklogGrowingAfterADrainWakesAtOnce(t *testing.T) {
	old := SpoolWakeCount
	SpoolWakeCount = 5
	t.Cleanup(func() { SpoolWakeCount = old })
	const idle = 20 * time.Second
	ev, sp, b := boundFixture(t, idle)
	for i := 0; i < 6; i++ {
		sendFYI(t, b, "fyi")
	}
	until(t, "6 spooled", func() bool { return len(waiting(t, sp)) == 6 })
	// The first six crowd the backlog and wake it once; the drain then restarts the wake state.
	ev.waitFor(t, 1, 10*time.Second)
	at := drainAt(t, sp, 1) // 5 held
	until(t, "5 held", func() bool { return len(waiting(t, sp)) == 5 })
	time.Sleep(time.Second) // let the endpoint see the drain before the new mail, or both land in one pass
	sendFYI(t, b, "new")    // 6 held: above the post-drain count
	ev.waitFor(t, 2, 10*time.Second)
	if since := time.Since(at); since >= idle/2 {
		t.Fatalf("a growing crowded backlog woke %v after the drain, want far less than the idle wake %v", since, idle)
	}
}

// A held count past 3/2 of SpoolWakeCount wakes at once right after a drain even though the backlog shrinks: growth
// detection is best effort, so the count has a hard line of its own.
func TestCrowdedCountPastTheHardLineWakesAfterADrain(t *testing.T) {
	old := SpoolWakeCount
	SpoolWakeCount = 5 // hard line 7
	t.Cleanup(func() { SpoolWakeCount = old })
	const idle = 20 * time.Second
	ev, sp, b := boundFixture(t, idle)
	for i := 0; i < 12; i++ {
		sendFYI(t, b, "fyi")
	}
	until(t, "12 spooled", func() bool { return len(waiting(t, sp)) == 12 })
	ev.waitFor(t, 1, 10*time.Second)
	at := drainAt(t, sp, 1) // 11 held: shrinking, bytes low, but past the count's hard line
	until(t, "11 held", func() bool { return len(waiting(t, sp)) == 11 })
	ev.waitFor(t, 2, 10*time.Second)
	if since := time.Since(at); since >= idle/2 {
		t.Fatalf("a held count past the hard line woke %v after the drain, want far less than the idle wake %v", since, idle)
	}
}

// Held bytes past half the inbox limit (twice SpoolWakeBytes) wake at once even right after a drain and a shrinking
// backlog.
func TestCrowdedBytesPastTheHardLineWakeAfterADrain(t *testing.T) {
	oldB, oldC := SpoolWakeBytes, SpoolWakeCount
	SpoolWakeBytes = 2 << 10
	t.Cleanup(func() { SpoolWakeBytes, SpoolWakeCount = oldB, oldC })
	const idle = 20 * time.Second
	ev, sp, b := boundFixture(t, idle)
	body := strings.Repeat("x", 1<<10)
	for i := 0; i < 7; i++ { // 7 KiB and more held: over 2 * SpoolWakeBytes after one drain
		sendFYI(t, b, body)
	}
	until(t, "7 spooled", func() bool { return len(waiting(t, sp)) == 7 })
	at := drainAt(t, sp, 1)
	until(t, "6 held", func() bool { return len(waiting(t, sp)) == 6 })
	ev.waitFor(t, 1, 10*time.Second)
	if since := time.Since(at); since >= idle/2 {
		t.Fatalf("held bytes past the hard line woke %v after the drain, want far less than the idle wake %v", since, idle)
	}
}

// fakeStreamBytes sets the stream-headroom seam before the endpoints start and restores it after; it returns the call count.
func fakeStreamBytes(t *testing.T, bytes, max uint64, err error) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	old := streamBytes
	streamBytes = func(context.Context, *Endpoint) (uint64, uint64, error) {
		calls.Add(1)
		return bytes, max, err
	}
	t.Cleanup(func() { streamBytes = old })
	return &calls
}

// crowdedAfterADrain crowds the backlog, lets it wake once, then drains one: a shrinking crowded backlog right after a
// drain. It returns the events and the drain time.
func crowdedAfterADrain(t *testing.T) (ev *events, at time.Time) {
	t.Helper()
	old := SpoolWakeCount
	SpoolWakeCount = 5
	t.Cleanup(func() { SpoolWakeCount = old })
	ev, sp, b := boundFixture(t, 20*time.Second)
	for i := 0; i < 6; i++ {
		sendFYI(t, b, "fyi")
	}
	until(t, "6 spooled", func() bool { return len(waiting(t, sp)) == 6 })
	ev.waitFor(t, 1, 10*time.Second)
	at = drainAt(t, sp, 1)
	until(t, "5 held", func() bool { return len(waiting(t, sp)) == 5 })
	return ev, at
}

// A stream already near its byte limit (retained history) wakes a shrinking crowded backlog at once after a drain.
func TestCrowdedBacklogOnAFullStreamWakesAtOnceAfterADrain(t *testing.T) {
	calls := fakeStreamBytes(t, 90, 100, nil)
	ev, at := crowdedAfterADrain(t)
	ev.waitFor(t, 2, 10*time.Second)
	if since := time.Since(at); since >= 10*time.Second {
		t.Fatalf("woke %v after the drain, want far less than the idle wake", since)
	}
	if calls.Load() == 0 {
		t.Fatal("the stream was never read")
	}
}

// An unreadable stream is no headroom: the wake is at once.
func TestCrowdedBacklogWithAnUnreadableStreamWakesAtOnce(t *testing.T) {
	fakeStreamBytes(t, 0, 0, errors.New("no info"))
	ev, _ := crowdedAfterADrain(t)
	ev.waitFor(t, 2, 10*time.Second)
}

// The reading is cached: many settles in one window read the stream once.
func TestStreamHeadroomIsCached(t *testing.T) {
	calls := fakeStreamBytes(t, 1, 100, nil)
	e := &Endpoint{}
	for i := 0; i < 50; i++ {
		if !e.streamHasHeadroom(context.Background()) {
			t.Fatal("no headroom at 1% of the limit")
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("stream read %d times in one window, want 1", n)
	}
}
