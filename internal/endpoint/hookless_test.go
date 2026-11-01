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

// hooklessKnobs shortens the idle wake and the grace for a test, and puts them back.
func hooklessKnobs(t *testing.T) {
	t.Helper()
	oi, oc, og, ow := IdleWake, SpoolCheck, HookGrace, HooklessWakes
	IdleWake, SpoolCheck, HookGrace = 200*time.Millisecond, 20*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { IdleWake, SpoolCheck, HookGrace, HooklessWakes = oi, oc, og, ow })
}

func sendTask(t *testing.T, f *fixture, body string) {
	t.Helper()
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", body, SendOptions{Kind: kind.Task}); err != nil {
		t.Fatal(err)
	}
}

// twoToolCalls is two ended tool calls HookGrace apart, as the serve's tool-call hook records them.
func twoToolCalls(ep *Endpoint) {
	ep.ToolCalled(time.Now())
	time.Sleep(HookGrace + 50*time.Millisecond)
	ep.ToolCalled(time.Now())
}

// With no hook ever run, a waking message rings (at once), then the idle wake and one backoff wake: three wakes, then
// nothing for as long as the session lives. The message stays held and unacked.
func TestNoHookStopsReWakingAfterThreeWakes(t *testing.T) {
	hooklessKnobs(t)
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledClaudeFromStart(t, "a")
	sendTask(t, f, "stuck")
	ev.waitFor(t, 3, 5*time.Second)
	time.Sleep(8 * IdleWake)
	if got := ev.all(); len(got) != 3 {
		t.Fatalf("%d wakes, want exactly 3: %q", len(got), got)
	}
	if st, n := a.Hookless(); st != HooklessSuspected || n != 3 {
		t.Fatalf("state %v after %d wakes, want suspected after 3", st, n)
	}
	if n := unread(t, a); n != 1 || len(waiting(t, sp)) != 1 {
		t.Fatalf("unread %d, waiting %d, want 1 and 1: nothing is acked", n, len(waiting(t, sp)))
	}
}

// A message that arrives once the session is suspected still rings, once, and nothing repeats.
func TestNewMailRingsOnceWhileSuspected(t *testing.T) {
	hooklessKnobs(t)
	f := newFixture(t, "a", "b")
	a, ev, _ := f.runSpooledClaudeFromStart(t, "a")
	sendTask(t, f, "first")
	ev.waitFor(t, 3, 5*time.Second)
	time.Sleep(4 * IdleWake)
	if st, _ := a.Hookless(); st != HooklessSuspected {
		t.Fatalf("state %v, want suspected", st)
	}
	sendTask(t, f, "second")
	ev.waitFor(t, 4, 5*time.Second)
	time.Sleep(8 * IdleWake)
	got := ev.all()
	if len(got) != 4 || got[3] != "2 messages from b (task)" {
		t.Fatalf("wakes %q, want 4 and the last for both messages", got)
	}
}

// Two mash tool calls HookGrace apart with no hook between them prove the hooks do not run: one notice with the read
// hint goes out, and no more.
func TestToolCallsWithoutHooksProveHookless(t *testing.T) {
	hooklessKnobs(t)
	IdleWake = time.Hour // the only wakes are the ring and the notice
	f := newFixture(t, "a", "b")
	a, ev, _ := f.runSpooledClaudeFromStart(t, "a")
	sendTask(t, f, "stuck")
	ev.waitFor(t, 1, 5*time.Second) // the ring
	a.ToolCalled(time.Now())
	if st, _ := a.Hookless(); st == HooklessProven {
		t.Fatal("one tool call proved it")
	}
	time.Sleep(HookGrace + 50*time.Millisecond)
	a.ToolCalled(time.Now())
	got := ev.waitFor(t, 2, 5*time.Second)
	if !strings.HasSuffix(got[1], HooklessReadHint) || !strings.HasPrefix(got[1], "1 message from b (task)") {
		t.Fatalf("the notice %q, want the summary line and the read hint", got[1])
	}
	if strings.Contains(got[0], "hooks are not running") {
		t.Fatalf("the first wake %q carries the hint before it was proven", got[0])
	}
	time.Sleep(2 * time.Second)
	if got := ev.all(); len(got) != 2 {
		t.Fatalf("%d wakes, want 2 (the ring and one notice): %q", len(got), got)
	}
	if st, _ := a.Hookless(); st != HooklessProven {
		t.Fatalf("state %v, want proven", st)
	}
}

// Tool calls before the spool was bound are not evidence, and a call that has not ended HookGrace after the first
// is not either.
func TestToolCallsBeforeTheBindOrWithinTheGraceDoNotProve(t *testing.T) {
	hooklessKnobs(t)
	f := newFixture(t, "a", "b")
	a, _, _ := f.runSpooledClaudeFromStart(t, "a")
	a.ToolCalled(time.Now().Add(-time.Hour))
	a.ToolCalled(time.Now())
	if st, _ := a.Hookless(); st != HooklessNone {
		t.Fatalf("state %v, want none", st)
	}
	a.ToolCalled(time.Now().Add(10 * time.Millisecond))
	if st, _ := a.Hookless(); st != HooklessNone {
		t.Fatalf("state %v, want none: calls inside the grace", st)
	}
}

// The read tool's MarkRead drains a held message only in the proven state. In the normal and suspected states, and for
// a message that is not held, it does nothing.
func TestReadAcksOnlyWhenProvenHookless(t *testing.T) {
	hooklessKnobs(t)
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledClaudeFromStart(t, "a")
	sendTask(t, f, "the body")
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	seq := waiting(t, sp)[0].Seq
	held := func(what string) {
		t.Helper()
		if n := unread(t, a); n != 1 || len(waiting(t, sp)) != 1 || sp.IsDrained(seq) {
			t.Fatalf("%s: unread %d, waiting %d, drained %v, want a held, undrained message", what, n, len(waiting(t, sp)), sp.IsDrained(seq))
		}
	}
	if a.MarkRead(seq) {
		t.Fatal("MarkRead drained in the normal state")
	}
	held("normal")
	ev.waitFor(t, 3, 5*time.Second)
	time.Sleep(2 * SpoolCheck)
	if st, _ := a.Hookless(); st != HooklessSuspected {
		t.Fatalf("state %v, want suspected", st)
	}
	if a.MarkRead(seq) {
		t.Fatal("MarkRead drained in the suspected state")
	}
	held("suspected")
	twoToolCalls(a)
	if a.MarkRead(seq + 100) {
		t.Fatal("MarkRead drained a message that is not held")
	}
	held("proven, other seq")
	if !a.MarkRead(seq) {
		t.Fatal("MarkRead did not drain in the proven state")
	}
	until(t, "acked", func() bool { return unread(t, a) == 0 })
	if len(waiting(t, sp)) != 0 {
		t.Fatalf("still waiting: %+v", waiting(t, sp))
	}
	if a.MarkRead(seq) {
		t.Fatal("MarkRead drained a message already acked")
	}
}

// A hook run ends the hookless states: delivery is normal again, the backoff resumes, and the read tool no longer
// acks.
func TestAHookRunEndsHooklessMode(t *testing.T) {
	hooklessKnobs(t)
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledClaudeFromStart(t, "a")
	sendTask(t, f, "stuck")
	ev.waitFor(t, 1, 5*time.Second)
	twoToolCalls(a)
	ev.waitFor(t, 2, 5*time.Second) // the notice
	if st, _ := a.Hookless(); st != HooklessProven {
		t.Fatalf("state %v, want proven", st)
	}
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Hookless(); st != HooklessNone {
		t.Fatalf("state %v after a hook ran, want none", st)
	}
	seq := waiting(t, sp)[0].Seq
	if a.MarkRead(seq) {
		t.Fatal("MarkRead drained after a hook ran")
	}
	ev.waitFor(t, 3, 5*time.Second) // the backoff wake is back
	w := waiting(t, sp)
	if len(w) != 1 {
		t.Fatalf("waiting %+v", w)
	}
	if err := sp.Drained(w[0].Seq); err != nil { // a hook shows it
		t.Fatal(err)
	}
	until(t, "acked once a hook drained it", func() bool { return unread(t, a) == 0 })
}

// A subagent's hook (.alive) shows that hooks run: the session is never suspected or proven, and the backoff goes on.
func TestSubagentHookIsNotHookless(t *testing.T) {
	hooklessKnobs(t)
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledClaudeFromStart(t, "a")
	if err := sp.Alive(); err != nil {
		t.Fatal(err)
	}
	sendTask(t, f, "busy in a subagent")
	ev.waitFor(t, 4, 8*time.Second) // more than the three wakes of a hookless session
	twoToolCalls(a)
	if st, _ := a.Hookless(); st != HooklessNone {
		t.Fatalf("state %v, want none", st)
	}
	if a.MarkRead(waiting(t, sp)[0].Seq) {
		t.Fatal("MarkRead drained while hooks run")
	}
	for _, w := range ev.all() {
		if strings.Contains(w, "hooks are not running") {
			t.Fatalf("a wake %q says the hooks are not running", w)
		}
	}
}

// A hook refused as another session's writes no .hooked: for delivery that session is hookless.
func TestRefusedHooksStillProveHookless(t *testing.T) {
	hooklessKnobs(t)
	f := newFixture(t, "a", "b")
	a, _, sp := f.runSpooledClaudeFromStart(t, "a")
	if err := sp.NoteRefused(spool.Refusal{Time: time.Now(), Why: "session id differs"}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := sp.Refused(); !ok {
		t.Fatal("the refusal was not recorded")
	}
	twoToolCalls(a)
	if st, _ := a.Hookless(); st != HooklessProven {
		t.Fatalf("state %v, want proven", st)
	}
}

// A spool that holds only result or fyi mail (kinds that never wake) still gets the one notice once the session is
// proven hookless, since no hook will drain it. Before that, such mail sends no wake.
func TestProvenHooklessNoticeCoversResultAndFyiMail(t *testing.T) {
	hooklessKnobs(t)
	IdleWake = 100 * time.Millisecond
	f := newFixture(t, "a", "b")
	a, ev, _ := f.runSpooledClaudeFromStart(t, "a")
	for _, k := range []kind.Kind{kind.Result, kind.FYI} {
		if _, err := f.client(t, "b").SendWith(context.Background(), "a", "held "+string(k), SendOptions{Kind: k}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(10 * IdleWake)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("wakes %q before proven, want none", got)
	}
	a.ToolCalled(time.Now())
	if st, _ := a.Hookless(); st == HooklessProven {
		t.Fatal("one tool call proved it")
	}
	time.Sleep(HookGrace + 50*time.Millisecond)
	a.ToolCalled(time.Now())
	got := ev.waitFor(t, 1, 5*time.Second)
	if !strings.HasSuffix(got[0], HooklessReadHint) || !strings.HasPrefix(got[0], "2 messages from b") {
		t.Fatalf("the notice %q, want the summary line and the read hint", got[0])
	}
	time.Sleep(10 * IdleWake)
	if got := ev.all(); len(got) != 1 {
		t.Fatalf("%d wakes, want exactly 1: %q", len(got), got)
	}
}

// Issue #40: a channel write is not delivery when wakes are not landing. Once HooklessWakes wakes have started no turn,
// a message the spool will not take is held unacked, not written to the channel; after a hook runs it falls back to
// the channel and acks again. (Before any hook, under SpoolFromStart, it is held too: see
// TestSpoolFromStartClaudeUnwritableSpoolHoldsBeforeAnyHook; the channel's ack path is
// TestSpoolFromStartClaudeUnwritableSpoolFallsBackToTheChannelAfterAHook.)
func TestSuspectedHooklessHoldsSpoolFallbackUntilAHookRuns(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a 0500 directory")
	}
	hooklessKnobs(t)
	od := spoolFullDelay
	spoolFullDelay = time.Second
	t.Cleanup(func() { spoolFullDelay = od })
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledClaudeFromStart(t, "a")
	sendTask(t, f, "stuck")
	ev.waitFor(t, 3, 5*time.Second)
	until(t, "suspected", func() bool { st, _ := a.Hookless(); return st == HooklessSuspected })
	wakes := len(ev.all())

	if err := os.Chmod(sp.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sp.Dir, 0o700) })
	sendTask(t, f, "fallback body")
	time.Sleep(4 * time.Second) // past the three spool tries and a redelivery
	for _, g := range ev.all() {
		if strings.Contains(g, "fallback body") {
			t.Fatalf("channel event %q shows a message while wakes are not landing", g)
		}
	}
	if n := unread(t, a); n != 2 {
		t.Fatalf("unread %d, want 2: the fallback message must stay unacked", n)
	}
	if got := len(ev.all()); got != wakes {
		t.Fatalf("%d channel events, want the %d wakes only", got, wakes)
	}

	// A hook runs: the state clears and the fallback acks on its channel write.
	if err := os.Chmod(sp.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sp.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.Hookless(); st != HooklessNone {
		t.Fatalf("state %v after a hook ran, want none", st)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		found := false
		for _, g := range ev.all() {
			found = found || strings.Contains(g, "> fallback body")
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fallback never reached the channel after a hook ran: %q", ev.all())
		}
		time.Sleep(50 * time.Millisecond)
	}
	until(t, "the fallback acks", func() bool { return unread(t, a) == 1 }) // the spooled "stuck" is still held
}
