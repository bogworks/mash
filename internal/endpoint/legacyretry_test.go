package endpoint

import (
	"context"
	"errors"
	"strings"
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
	if n, ok := sp.Epoch(); !ok || n == 0 || n != ep.legacy.epoch.Load() {
		t.Fatalf("epoch %d %v (hold %d)", n, ok, ep.legacy.epoch.Load())
	}
}
