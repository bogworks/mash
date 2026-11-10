package endpoint

import (
	"context"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/spool"
)

// runSpooledFromStart is runSpooled for a host that takes no channel delivery: SpoolFromStart before SetSpool, and no
// hook is ever run unless a test touches the spool.
func (f *fixture) runSpooledFromStart(t *testing.T, name string) (*Endpoint, *events, spool.Spool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg(name))
	if err != nil {
		t.Fatal(err)
	}
	ep.NoChannel()
	ep.SpoolFromStart()
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := ep.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	return ep, ev, sp
}

// With no hook ever run, mail is spooled from the first message and held unacked; the idle wake is the summary line
// alone; nothing is acked until a hook drains it.
func TestSpoolFromStartHoldsUntilDrained(t *testing.T) {
	oi := IdleWake
	IdleWake = 2 * time.Second
	t.Cleanup(func() { IdleWake = oi })
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledFromStart(t, "a") // sp.Touch is never called
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", "the body", SendOptions{Kind: kind.Question}); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	for i := 0; i < 3; i++ { // several SpoolCheck periods, still before the idle wake
		time.Sleep(SpoolCheck)
		if n := unread(t, a); n != 1 {
			t.Fatalf("unread %d, want 1 (spooled is not seen)", n)
		}
	}
	got := ev.waitFor(t, 1, 5*time.Second) // no hook has run: the wake is at once (v0.2.7), not after the idle wake
	if got[0] != "1 message from b (question)" {
		t.Fatalf("the wake %q, want the summary line alone", got[0])
	}
	time.Sleep(3 * SpoolCheck)
	if n := unread(t, a); n != 1 {
		t.Fatalf("unread %d after the wake, want 1 (a wake is not a receipt)", n)
	}
	w := waiting(t, sp)
	if len(w) != 1 {
		t.Fatalf("waiting %+v", w)
	}
	if err := sp.Drained(spool.Ref{Seq: w[0].Seq}); err != nil {
		t.Fatal(err)
	}
	until(t, "acked once drained", func() bool { return unread(t, a) == 0 })
	if got := ev.all(); len(got) != 1 {
		t.Fatalf("channel events %q, want the wake only", got)
	}
}

// A wake is a hint, not a receipt: with no drain the mail stays unacked and spooled, and the session is woken again
// by the idle wake, until HooklessWakes wakes (the ring included) have gone out with no hook run (ADR 0041).
func TestSpoolFromStartReWakesWithoutDrain(t *testing.T) {
	oi, oc := IdleWake, SpoolCheck
	IdleWake, SpoolCheck = 200*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { IdleWake, SpoolCheck = oi, oc })
	f := newFixture(t, "a", "b")
	ep, ev, sp := f.runSpooledFromStart(t, "a")
	sent := time.Now()
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", "held", SendOptions{Kind: kind.Question}); err != nil {
		t.Fatal(err)
	}
	ev.waitFor(t, 1, 5*time.Second) // the pre-hook wake, at once; it starts no backoff
	if n := unread(t, ep); n != 1 || len(waiting(t, sp)) != 1 {
		t.Fatalf("after the wake: unread %d, waiting %d, want 1 and 1", n, len(waiting(t, sp)))
	}
	// Woken again by the idle wake, IdleWake after the mail was held: timed from the send, since the first wake is
	// observed a little after it fired (a gap from that observation flaked at 190 ms on macOS CI).
	ev.waitFor(t, 2, 5*time.Second)
	if gap := time.Since(sent); gap < IdleWake {
		t.Fatalf("second wake %s after the send, want the idle wake", gap)
	}
	if n := unread(t, ep); n != 1 || len(waiting(t, sp)) != 1 {
		t.Fatalf("after the second wake: unread %d, waiting %d, want 1 and 1", n, len(waiting(t, sp)))
	}
	// The third wake is the last: no hook has run, so re-waking is stopped (suspected hookless).
	ev.waitFor(t, HooklessWakes, 5*time.Second)
	time.Sleep(8 * IdleWake)
	if got := ev.all(); len(got) != HooklessWakes {
		t.Fatalf("%d wakes, want %d and then none: %q", len(got), HooklessWakes, got)
	}
	if n := unread(t, ep); n != 1 || len(waiting(t, sp)) != 1 {
		t.Fatalf("after the last wake: unread %d, waiting %d, want 1 and 1", n, len(waiting(t, sp)))
	}
}

// If the spool cannot be set (serve's SetSpool failed) under NoChannel, the channel must not deliver: nothing is
// delivered or acked, and the mail stays in the inbox.
func TestSpoolFromStartWithoutSpoolHoldsMail(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	a, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	a.NoChannel() // no SetSpool
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = a.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; a.Close() })
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", "stays", SendOptions{Kind: kind.Question, Urgent: true}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("channel events %q, want none", got)
	}
	if n := unread(t, a); n != 1 {
		t.Fatalf("unread %d, want 1: the mail must stay unacked", n)
	}
}
