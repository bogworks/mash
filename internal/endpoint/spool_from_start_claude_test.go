package endpoint

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/spool"
)

// runSpooledClaudeFromStart is a Claude-like setup (MASH_SPOOL_FROM_START): SpoolFromStart after a successful SetSpool,
// without NoChannel, so the channel is still there to fall back on.
func (f *fixture) runSpooledClaudeFromStart(t *testing.T, name string) (*Endpoint, *events, spool.Spool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg(name))
	if err != nil {
		t.Fatal(err)
	}
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := ep.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	ep.SpoolFromStart()
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	return ep, ev, sp
}

// #16: Claude Code drops channel events that arrive before its first user message, so mail that comes before any hook
// has run is spooled and held unacked: no channel event carries its body, the wake (at once, before any hook) is the summary line alone, and
// the ack waits for the drain.
func TestSpoolFromStartClaudeHoldsMailSentBeforeAnyHook(t *testing.T) {
	oi := IdleWake
	IdleWake = 2 * time.Second
	t.Cleanup(func() { IdleWake = oi })
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledClaudeFromStart(t, "a") // sp.Touch is never called
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", "startup body", SendOptions{Kind: kind.Question}); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	for i := 0; i < 3; i++ {
		time.Sleep(SpoolCheck)
		if n := unread(t, a); n != 1 {
			t.Fatalf("unread %d, want 1: nothing is acked before a hook shows it", n)
		}
	}
	got := ev.waitFor(t, 1, 5*time.Second) // no hook has run: the wake is at once (v0.2.7), not after the idle wake
	if got[0] != "1 message from b (question)" || strings.Contains(got[0], "startup body") {
		t.Fatalf("the wake %q, want the summary line alone", got[0])
	}
	time.Sleep(3 * SpoolCheck)
	if n := unread(t, a); n != 1 {
		t.Fatalf("unread %d after the wake, want 1 (a wake is not a receipt)", n)
	}
	w := waiting(t, sp)
	if len(w) != 1 || string(w[0].Body) != "startup body" {
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

// Under SpoolFromStart, a spool that will not take a write holds the mail before any hook has run: Claude drops
// channel events then, and the channel's ack would lose the mail.
func TestSpoolFromStartClaudeUnwritableSpoolHoldsBeforeAnyHook(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a 0500 directory")
	}
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledClaudeFromStart(t, "a")
	if err := os.Chmod(sp.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sp.Dir, 0o700) })
	if _, err := f.client(t, "b").Send(context.Background(), "a", "must be held", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second) // past the three spool tries
	for _, g := range ev.all() {
		if strings.Contains(g, "must be held") {
			t.Fatalf("channel event %q carries the body", g)
		}
	}
	if n := unread(t, a); n != 1 {
		t.Fatalf("unread %d, want 1: the mail stays unacked", n)
	}
}

// After a hook has run, Claude takes channel events: the same unwritable spool falls back to the channel, as today.
func TestSpoolFromStartClaudeUnwritableSpoolFallsBackToTheChannelAfterAHook(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a 0500 directory")
	}
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledClaudeFromStart(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sp.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sp.Dir, 0o700) })
	if _, err := f.client(t, "b").Send(context.Background(), "a", "falls back", 0); err != nil {
		t.Fatal(err)
	}
	got := ev.waitFor(t, 1, 10*time.Second)
	if !strings.Contains(got[0], "> falls back") {
		t.Fatalf("fallback %q, want the body on the channel", got[0])
	}
	until(t, "the fallback acks", func() bool { return unread(t, a) == 0 })
}
