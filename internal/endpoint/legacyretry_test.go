package endpoint

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// tinyRetry makes the legacy lease retry fast for a test.
func tinyRetry(t *testing.T) {
	t.Helper()
	oMin, oMax, oRef := LegacyRetryMin, LegacyRetryMax, LeaseRefresh
	LegacyRetryMin, LegacyRetryMax, LeaseRefresh = 20*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { LegacyRetryMin, LegacyRetryMax, LeaseRefresh = oMin, oMax, oRef })
}

// Held by another session at Open: the endpoint takes the lease once the holder exits, and the mail queued meanwhile is
// delivered once.
func TestLegacyRetryTakesOverWhenHolderExits(t *testing.T) {
	tinyRetry(t)
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	holder, err := Open(context.Background(), f.cfg("old")) // holds the legacy lease, runs no consumer
	if err != nil {
		t.Fatal(err)
	}
	f.sendTo(t, snd, "old", "q1", "q2", "q3")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, ev, _ := f.runNF(t, id)
	if ep.legacyReading() {
		t.Fatal("took a held lease")
	}
	if line, _ := ep.LegacyInboxLine(); !strings.Contains(line, "held by another session") {
		t.Fatalf("status line %q", line)
	}
	time.Sleep(300 * time.Millisecond) // several retries fail
	if got := joined(ev); strings.Contains(got, "q1") {
		t.Fatalf("legacy mail consumed while held:\n%s", got)
	}
	holder.Close()
	waitText(t, ev, "> q1", "> q2", "> q3")
	if line, _ := ep.LegacyInboxLine(); line != "legacy inbox: read as old" {
		t.Fatalf("status line after the takeover %q", line)
	}
	f.sendTo(t, snd, "old", "q4")
	waitText(t, ev, "> q4")
	time.Sleep(300 * time.Millisecond)
	got := joined(ev)
	for _, b := range []string{"> q1", "> q2", "> q3", "> q4"} {
		if c := strings.Count(got, b); c != 1 {
			t.Fatalf("%s shown %d times:\n%s", b, c, got)
		}
	}
	ackFloor(t, f, "inbox-old", 4)
}

// A lease lost during the session is retried: when the other session releases it, the endpoint reads again.
func TestLegacyRetryAfterLossResumesReading(t *testing.T) {
	tinyRetry(t)
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, ev, _ := f.runNF(t, id)
	f.sendTo(t, snd, "old", "before")
	waitText(t, ev, "> before")
	kv := takeLegacyOver(t, f)
	until(t, "loss noticed", func() bool { return !ep.legacyReading() })
	f.sendTo(t, snd, "old", "during")
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(joined(ev), "> during") {
		t.Fatal("legacy mail consumed without the lease")
	}
	if err := kv.Delete(context.Background(), "old"); err != nil { // the other session exits
		t.Fatal(err)
	}
	waitText(t, ev, "> during")
	until(t, "reading again", func() bool { return ep.legacyReading() })
	f.sendTo(t, snd, "old", "after")
	waitText(t, ev, "> after")
	time.Sleep(200 * time.Millisecond)
	got := joined(ev)
	for _, b := range []string{"> before", "> during", "> after"} {
		if c := strings.Count(got, b); c != 1 {
			t.Fatalf("%s shown %d times:\n%s", b, c, got)
		}
	}
}

// The epoch of a hold that cannot be recorded gives the hold up: the lease is released, the state is failed with a
// reason, and the retry loop takes it again once the write works.
func TestLegacyEpochWriteFailureGivesUpTheHold(t *testing.T) {
	tinyRetry(t)
	var fail atomic.Bool
	var tries atomic.Int32
	fail.Store(true)
	old := legacyNoteEpoch
	legacyNoteEpoch = func(sp *spool.Spool, epoch uint64) error {
		if fail.Load() && epoch != 0 {
			tries.Add(1)
			return errors.New("injected write failure")
		}
		return sp.NoteEpoch(epoch)
	}
	t.Cleanup(func() { legacyNoteEpoch = old })
	f := newFixture(t, "old", "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, err := Open(context.Background(), f.cfgNF(id))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := ep.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	if n := tries.Load(); n < int32(legacyEpochTries) {
		t.Fatalf("epoch written %d times, want at least %d", n, legacyEpochTries)
	}
	until(t, "hold given up", func() bool { return !ep.legacyReading() })
	if line, _ := ep.LegacyInboxLine(); !strings.Contains(line, "could not be recorded") {
		t.Fatalf("status line %q", line)
	}
	fail.Store(false)
	until(t, "hold taken again", func() bool { return ep.legacyReading() })
	// The state turns to read just before the epoch file is written (a hook sees "none" until then, which fails closed).
	until(t, "the epoch of the new hold recorded", func() bool {
		n, ok := sp.Epoch()
		return ok && n != 0 && n == ep.legacy.epoch.Load()
	})
}

// heldLegacyBodies returns the waiting legacy entries by body text.
func heldLegacyBodies(t *testing.T, sp spool.Spool) map[string][]uint64 {
	t.Helper()
	out := map[string][]uint64{}
	for _, en := range waiting(t, sp) {
		if en.Stream == LegacyStream {
			out[string(en.Body)] = append(out[string(en.Body)], en.Epoch)
		}
	}
	return out
}

// The lease is lost and taken again before the delivery worker runs: the legacy mail held under the old hold is dropped
// and nak'd the moment the worker next runs, and its redelivery to the current hold is shown (held) exactly once.
func TestLegacyRetakenBeforeWorkerDropsOldEpochHeldMail(t *testing.T) {
	tinyRetry(t)
	oldCheck := SpoolCheck
	SpoolCheck = time.Hour // the worker runs only when a batch arrives
	t.Cleanup(func() { SpoolCheck = oldCheck })
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	f.sendTo(t, snd, "old", "legacy-1", "legacy-2", "legacy-3")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, sp := openSpooled(t, f, id)
	until(t, "three held", func() bool { return len(heldLegacyBodies(t, sp)) == 3 })
	e1 := ep.legacy.epoch.Load()
	kv := takeLegacyOver(t, f)
	until(t, "loss noticed", func() bool { return !ep.legacyReading() })
	if err := kv.Delete(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	until(t, "taken again", func() bool { return ep.legacyReading() && ep.legacy.epoch.Load() != e1 })
	e2 := ep.legacy.epoch.Load()
	until(t, "new epoch recorded", func() bool { n, ok := sp.Epoch(); return ok && n == e2 })
	for body, eps := range heldLegacyBodies(t, sp) {
		if len(eps) != 1 || eps[0] != e1 {
			t.Fatalf("%s: epochs %v, want only the old %d before the worker ran", body, eps, e1)
		}
	}
	f.sendTo(t, snd, "old", "legacy-4") // a batch: the worker drops what was held under the old hold, then spools anew
	until(t, "all four held under the new epoch", func() bool {
		m := heldLegacyBodies(t, sp)
		if len(m) != 4 {
			return false
		}
		for _, eps := range m {
			if len(eps) != 1 || eps[0] != e2 {
				return false
			}
		}
		return true
	})
	time.Sleep(300 * time.Millisecond)
	if m := heldLegacyBodies(t, sp); len(m) != 4 {
		t.Fatalf("held after settling: %v", m)
	}
}

// The epoch of a hold cannot be recorded because the spool lock cannot be taken: the hold is given up (not kept while
// the epoch file names the old hold), and it is taken again once the lock works.
func TestLegacyEpochLockFailureGivesUpTheHold(t *testing.T) {
	tinyRetry(t)
	var fail atomic.Bool
	var tries atomic.Int32
	old := legacyLockSpool
	legacyLockSpool = func(sp *spool.Spool) (func(), error) {
		if fail.Load() {
			tries.Add(1)
			return nil, errors.New("injected lock failure")
		}
		return sp.Lock()
	}
	t.Cleanup(func() { legacyLockSpool = old })
	f := newFixture(t, "old", "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, sp := openSpooled(t, f, id)
	e1 := ep.legacy.epoch.Load()
	kv := takeLegacyOver(t, f)
	until(t, "loss noticed", func() bool { return !ep.legacyReading() })
	fail.Store(true)
	tries.Store(0)
	if err := kv.Delete(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	until(t, "hold attempted and given up", func() bool { return tries.Load() >= int32(legacyEpochTries) })
	until(t, "failed state", func() bool {
		line, _ := ep.LegacyInboxLine()
		return !ep.legacyReading() && strings.Contains(line, "could not be recorded")
	})
	fail.Store(false)
	until(t, "hold taken again", func() bool { return ep.legacyReading() && ep.legacy.epoch.Load() != e1 })
	until(t, "epoch of the new hold recorded", func() bool {
		n, ok := sp.Epoch()
		return ok && n == ep.legacy.epoch.Load()
	})
}

// A legacy callback blocked while the lease is lost and taken again: no second consume loop starts until it returns,
// and when it does it queues nothing (its epoch is stale); the message is redelivered to the new loop and shown once.
func TestLegacyOneConsumeLoopWhileOldCallbackBlocked(t *testing.T) {
	tinyRetry(t)
	oWarn := legacyStopWarn
	legacyStopWarn = 50 * time.Millisecond
	t.Cleanup(func() { legacyStopWarn = oWarn })
	var once, release sync.Once
	block, entered := make(chan struct{}), make(chan struct{})
	unblock := func() { release.Do(func() { close(block) }) }
	testLegacyHandlerGate = func() {
		first := false
		once.Do(func() { first = true })
		if first {
			close(entered)
			<-block
		}
	}
	t.Cleanup(func() { unblock(); testLegacyHandlerGate = nil })
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, sp := openSpooled(t, f, id)
	e1 := ep.legacy.epoch.Load()
	f.sendTo(t, snd, "old", "blocked-A")
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the legacy callback never ran")
	}
	kv := takeLegacyOver(t, f)
	until(t, "loss noticed", func() bool { return !ep.legacyReading() })
	if err := kv.Delete(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	until(t, "taken again", func() bool { return ep.legacyReading() && ep.legacy.epoch.Load() != e1 })
	for end := time.Now().Add(600 * time.Millisecond); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if n := ep.legacy.loops.Load(); n != 1 {
			t.Fatalf("%d legacy consume loops while the old callback is blocked", n)
		}
		if len(heldLegacyBodies(t, sp)) != 0 {
			t.Fatal("mail queued while the old callback is blocked")
		}
	}
	unblock()
	until(t, "redelivered to the new loop and held", func() bool { return len(heldLegacyBodies(t, sp)) == 1 })
	f.sendTo(t, snd, "old", "after-B")
	until(t, "the new loop reads", func() bool { return len(heldLegacyBodies(t, sp)) == 2 })
	time.Sleep(300 * time.Millisecond)
	m := heldLegacyBodies(t, sp)
	if len(m) != 2 || len(m["blocked-A"]) != 1 || ep.legacy.loops.Load() != 1 {
		t.Fatalf("held %v, loops %d", m, ep.legacy.loops.Load())
	}
}
