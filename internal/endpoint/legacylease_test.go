package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// Tests for security finding S1 on naming plan 1.4b: a new-form endpoint reads the legacy stream only while it holds
// the legacy identity's lease, so a legacy-form session and a new-form session of one identity never both pull the
// legacy durable consumer.

func leaseKey(t *testing.T, f *fixture, key string) (leaseValue, bool) {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	e, err := kv.Get(context.Background(), key)
	if err != nil {
		return leaseValue{}, false
	}
	var lv leaseValue
	if err := json.Unmarshal(e.Value(), &lv); err != nil {
		t.Fatal(err)
	}
	return lv, true
}

// (a) The legacy session holds its lease first: the new-form endpoint reads only its own stream, the legacy mail is
// left to the legacy session, and status says so.
func TestLegacyLeaseHeldByLegacySession(t *testing.T) {
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	_, legacyEv := f.run(t, "old")
	f.sendTo(t, snd, "old", "queued for legacy")
	waitText(t, legacyEv, "> queued for legacy")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	peer := f.addNewForm(t, "ag", "team", "peer", newFormOpts{})

	ep, ev, _ := f.runNF(t, id)
	if !ep.hasLegacyStream() || ep.legacyReading() {
		t.Fatalf("hasLegacyStream %v reading %v", ep.hasLegacyStream(), ep.legacyReading())
	}
	line, ok := ep.LegacyInboxLine()
	if want := "legacy inbox: not read (the lease of old is held by another session)"; !ok || line != want {
		t.Fatalf("status line %q, want %q", line, want)
	}
	// Mail for the legacy identity goes to the legacy session only, once.
	f.sendTo(t, snd, "old", "after the new-form open")
	waitText(t, legacyEv, "> after the new-form open")
	pe := f.clientNF(t, peer)
	f.sendTo(t, pe, id.Flat(), "own mail")
	waitText(t, ev, "> own mail")
	time.Sleep(300 * time.Millisecond)
	if got := joined(ev); strings.Contains(got, "legacy") || strings.Contains(got, "#L") {
		t.Fatalf("the new-form endpoint showed legacy mail it does not hold the lease for:\n%s", got)
	}
	if n := strings.Count(joined(legacyEv), "after the new-form open"); n != 1 {
		t.Fatalf("legacy session saw the message %d times", n)
	}
	// The legacy stream is still readable by number (read-only).
	if m, err := ep.ReadRef(context.Background(), MsgRef{Stream: LegacyStream, Seq: 1}); err != nil || !strings.Contains(string(m.Body), "queued for legacy") {
		t.Fatalf("ReadRef of the legacy stream: %v %q", err, m.Body)
	}
	// The own lease is untouched by the held legacy one; and the legacy lease still belongs to the legacy session.
	if _, ok := leaseKey(t, f, id.Flat()); !ok {
		t.Fatal("own lease missing")
	}
	if lv, _ := leaseKey(t, f, "old"); lv.Instance == ep.instance {
		t.Fatal("the legacy lease was taken from its holder")
	}
}

// A mail queued before the new-form open, left unconsumed when the legacy session holds the lease, is delivered to the
// legacy session that opens later.
func TestLegacyLeaseHeldQueuedMailStaysForLegacy(t *testing.T) {
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	legacyCtx, stopLegacy := context.WithCancel(context.Background())
	legacy, err := Open(legacyCtx, f.cfg("old")) // holds the lease, runs no consumer yet
	if err != nil {
		t.Fatal(err)
	}
	f.sendTo(t, snd, "old", "q1", "q2")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	_, ev, _ := f.runNF(t, id)
	time.Sleep(400 * time.Millisecond)
	if got := joined(ev); strings.Contains(got, "q1") || strings.Contains(got, "q2") {
		t.Fatalf("the new-form endpoint consumed legacy mail:\n%s", got)
	}
	if i := legacyConsumerInfo(t, f, "inbox-old"); i.NumAckPending != 0 || i.NumPending != 2 {
		t.Fatalf("legacy consumer touched: %+v", i)
	}
	lev := &events{}
	done := make(chan struct{})
	go func() { _ = legacy.Run(legacyCtx, lev.deliver); close(done) }()
	waitText(t, lev, "> q1", "> q2")
	stopLegacy()
	<-done
	legacy.Close()
}

// (b) The new-form endpoint is up first and holds both leases: a legacy-form session of the same identity is refused.
func TestLegacySessionRefusedWhileNewFormHoldsLegacyLease(t *testing.T) {
	f := newFixture(t, "old", "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, _ := f.runNF(t, id)
	if line, ok := ep.LegacyInboxLine(); !ok || line != "legacy inbox: read as old" {
		t.Fatalf("status line %q", line)
	}
	lv, ok := leaseKey(t, f, "old")
	if !ok || lv.Instance != ep.instance {
		t.Fatalf("legacy lease %+v (%v), want held by instance %s", lv, ok, ep.instance)
	}
	_, err := Open(context.Background(), f.cfg("old"))
	var held *LeaseHeldError
	if !errors.As(err, &held) {
		t.Fatalf("legacy-form open: %v, want a lease held error", err)
	}
}

// (c) Nothing is split or lost: N messages to the legacy subject reach the one holder exactly once; closing the
// new-form endpoint releases the legacy lease so a legacy session can open.
func TestLegacyMailNotSplitAndLeaseReleased(t *testing.T) {
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	_, ev, stop := f.runNF(t, id)
	const n = 8
	var bodies []string
	for i := 1; i <= n; i++ {
		bodies = append(bodies, fmt.Sprintf("msg-%d-end", i))
	}
	f.sendTo(t, snd, "old", bodies...)
	until(t, "all legacy mail shown", func() bool {
		got := joined(ev)
		for _, b := range bodies {
			if !strings.Contains(got, "> "+b) {
				return false
			}
		}
		return true
	})
	// A legacy-form open meanwhile is refused, so it cannot pull a share.
	if _, err := Open(context.Background(), f.cfg("old")); err == nil {
		t.Fatal("a legacy-form session opened while the new-form endpoint held the legacy lease")
	}
	time.Sleep(300 * time.Millisecond)
	got := joined(ev)
	for _, b := range bodies {
		if c := strings.Count(got, "> "+b); c != 1 {
			t.Fatalf("%s shown %d times", b, c)
		}
	}
	ackFloor(t, f, "inbox-old", n)
	stop()
	if _, ok := leaseKey(t, f, "old"); ok {
		t.Fatal("the legacy lease was not released on close")
	}
	_, lev := f.run(t, "old") // opens: the lease is free
	time.Sleep(300 * time.Millisecond)
	if got := joined(lev); strings.Contains(got, "msg-") {
		t.Fatalf("acknowledged legacy mail was shown again:\n%s", got)
	}
}

// (d) A legacy lease lost mid-session stops the legacy consumer only: the own stream is still read and the own lease
// is still held.
func TestLegacyLeaseLostStopsLegacyConsumptionOnly(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = old })
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	peer := f.addNewForm(t, "ag", "team", "peer", newFormOpts{})
	ep, ev, _ := f.runNF(t, id)
	f.sendTo(t, snd, "old", "before the loss")
	waitText(t, ev, "> before the loss")

	// Another session takes the legacy name over (as the lease expiring and a legacy session starting would).
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := json.Marshal(leaseValue{Instance: "someone-else", Host: "h", PID: "1", Since: time.Now().UTC().Format(time.RFC3339)})
	if _, err := kv.Put(context.Background(), "old", other); err != nil {
		t.Fatal(err)
	}
	until(t, "legacy lease loss noticed", func() bool { return !ep.legacyReading() })
	if line, _ := ep.LegacyInboxLine(); !strings.HasPrefix(line, "legacy inbox: not read (") {
		t.Fatalf("status line %q", line)
	}
	f.sendTo(t, snd, "old", "after the loss")
	pe := f.clientNF(t, peer)
	f.sendTo(t, pe, id.Flat(), "own after the loss")
	waitText(t, ev, "> own after the loss")
	time.Sleep(400 * time.Millisecond)
	if strings.Contains(joined(ev), "> after the loss") {
		t.Fatalf("legacy mail was consumed without the lease:\n%s", joined(ev))
	}
	if i := legacyConsumerInfo(t, f, "inbox-old"); i.NumAckPending != 0 || i.NumPending != 1 {
		t.Fatalf("legacy consumer after the loss: %+v", i)
	}
	// Still in the own lease: the endpoint was not stopped.
	if lv, ok := leaseKey(t, f, id.Flat()); !ok || lv.Instance != ep.instance {
		t.Fatalf("own lease %+v (%v)", lv, ok)
	}
	// Close leaves the other session's legacy lease alone.
	ep.Close()
	if lv, ok := leaseKey(t, f, "old"); !ok || lv.Instance != "someone-else" {
		t.Fatalf("close touched another session's legacy lease: %+v (%v)", lv, ok)
	}
}

// A fenced (deprovisioned) legacy key counts as held.
func TestLegacyLeaseFencedIsHeld(t *testing.T) {
	f := newFixture(t, "old", "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	if _, err := kv.Put(context.Background(), "old", store.LeaseTombstone()); err != nil {
		t.Fatal(err)
	}
	ep, _, _ := f.runNF(t, id)
	if ep.legacyReading() {
		t.Fatal("a fenced legacy key was taken")
	}
	if line, _ := ep.LegacyInboxLine(); !strings.Contains(line, "not read") {
		t.Fatalf("status line %q", line)
	}
}

// An endpoint with no alias has no legacy line and takes no extra lease.
func TestNoAliasNoLegacyLease(t *testing.T) {
	f := newFixture(t, "old")
	id := f.addNewForm(t, "ag", "team", "plain", newFormOpts{})
	ep, _, _ := f.runNF(t, id)
	if line, ok := ep.LegacyInboxLine(); ok {
		t.Fatalf("unexpected line %q", line)
	}
	if _, ok := leaseKey(t, f, "old"); ok {
		t.Fatal("a lease for the legacy name exists")
	}
}

// Legacy mail held for a hook when the lease is lost is dropped unacked, never shown or acknowledged afterwards, its
// waiting spool files go, the own stream's held mail is unaffected, and the next holder (a legacy-form session) is
// delivered each legacy message exactly once.
func TestLegacyLeaseLostDropsHeldLegacyMailUnacked(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = old })
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	f.sendTo(t, snd, "old", "legacy-1", "legacy-2", "legacy-3")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	peer := f.addNewForm(t, "ag", "team", "peer", newFormOpts{})
	pe := f.clientNF(t, peer)

	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfgNF(id))
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
	f.sendTo(t, pe, id.Flat(), "own-held")
	until(t, "all held", func() bool { return len(waiting(t, sp)) == 4 })
	if i := legacyConsumerInfo(t, f, "inbox-old"); i.AckFloor.Stream != 0 || i.NumAckPending != 3 {
		t.Fatalf("legacy consumer before the loss: %+v", i)
	}

	// The lease is lost (taken over by another session).
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := json.Marshal(leaseValue{Instance: "someone-else", Host: "h", PID: "1", Since: time.Now().UTC().Format(time.RFC3339)})
	if _, err := kv.Put(context.Background(), "old", other); err != nil {
		t.Fatal(err)
	}
	until(t, "legacy lease loss noticed", func() bool { return !ep.legacyReading() })
	until(t, "held legacy mail dropped from the spool", func() bool {
		w := waiting(t, sp)
		return len(w) == 1 && string(w[0].Body) == "own-held"
	})
	// A hook drain of a dropped legacy ref, or any later pass, acks nothing of the legacy stream.
	_ = sp.Drained(spool.Ref{Stream: LegacyStream, Seq: 1})
	time.Sleep(3 * SpoolCheck)
	if i := legacyConsumerInfo(t, f, "inbox-old"); i.AckFloor.Stream != 0 || uint64(i.NumAckPending)+i.NumPending != 3 {
		t.Fatalf("legacy consumer after the loss: %+v (want nothing acked: all 3 pending or awaiting redelivery)", i)
	}
	// The own stream's held mail is unaffected: it is still held, and acked once drained.
	if i := legacyConsumerInfo(t, f, "inbox-ag_team_new"); i.NumAckPending != 1 {
		t.Fatalf("own consumer: %+v", i)
	}
	if err := sp.Drained(spool.Ref{Seq: 1}); err != nil {
		t.Fatal(err)
	}
	until(t, "own mail acked once drained", func() bool {
		i := legacyConsumerInfo(t, f, "inbox-ag_team_new")
		return i.AckFloor.Stream == 1 && i.NumAckPending == 0
	})

	// The next holder: the lease is free, a legacy-form session of the identity opens and gets each message once.
	if err := kv.Delete(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	_, lev := f.run(t, "old")
	until(t, "next holder shown all three", func() bool {
		got := joined(lev)
		return strings.Contains(got, "> legacy-1") && strings.Contains(got, "> legacy-2") && strings.Contains(got, "> legacy-3")
	})
	time.Sleep(300 * time.Millisecond)
	for _, b := range []string{"legacy-1", "legacy-2", "legacy-3"} {
		if n := strings.Count(joined(lev), "> "+b); n != 1 {
			t.Fatalf("%s shown %d times by the next holder", b, n)
		}
	}
	if strings.Contains(joined(ev), "legacy-") {
		t.Fatalf("the old holder showed legacy mail:\n%s", joined(ev))
	}
}

// openSpooled opens a new-form endpoint with an alias, spooling from the start with no channel, over a fresh spool.
func openSpooled(t *testing.T, f *fixture, id sap.EndpointIdentity) (*Endpoint, *events, spool.Spool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfgNF(id))
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

// takeLegacyOver makes another session the legacy lease's holder, as a takeover would.
func takeLegacyOver(t *testing.T, f *fixture) jetstream.KeyValue {
	t.Helper()
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := json.Marshal(leaseValue{Instance: "someone-else", Host: "h", PID: "1", Since: time.Now().UTC().Format(time.RFC3339)})
	if _, err := kv.Put(context.Background(), "old", other); err != nil {
		t.Fatal(err)
	}
	return kv
}

func hasLegacy(w []spool.Entry) bool {
	for _, e := range w {
		if e.Stream == LegacyStream {
			return true
		}
	}
	return false
}

// loseAt installs seam so that, the first time it runs, the legacy lease is taken over and the call returns once this
// endpoint has noticed the loss. It returns the lease bucket and a channel closed when the seam has fired.
func loseAt(t *testing.T, f *fixture, ep *Endpoint, seam *func()) (*jetstream.KeyValue, chan struct{}) {
	t.Helper()
	var kv jetstream.KeyValue
	var once sync.Once
	fired := make(chan struct{})
	*seam = func() {
		once.Do(func() {
			kv = takeLegacyOver(t, f)
			for deadline := time.Now().Add(5 * time.Second); ep.legacyReading() && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			}
			close(fired)
		})
	}
	t.Cleanup(func() { *seam = nil })
	return &kv, fired
}

// The lease epoch is recorded when the lease is taken and as "none" the moment the loss is learned; a legacy entry is
// stamped with the epoch of its write.
func TestLegacyLeaseEpochRecordedAndStamped(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = old })
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, sp := openSpooled(t, f, id)
	if n, ok := sp.Epoch(); !ok || n == 0 || n != ep.legacy.epoch.Load() {
		t.Fatalf("epoch after SetSpool: %d %v (hold %d)", n, ok, ep.legacy.epoch.Load())
	}
	f.sendTo(t, snd, "old", "stamped")
	until(t, "legacy mail spooled", func() bool { return hasLegacy(waiting(t, sp)) })
	if w := waiting(t, sp); w[0].Epoch != ep.legacy.epoch.Load() {
		t.Fatalf("entry epoch %d, hold %d", w[0].Epoch, ep.legacy.epoch.Load())
	}
	takeLegacyOver(t, f)
	until(t, "loss noticed", func() bool { return !ep.legacyReading() })
	until(t, "epoch none", func() bool { n, ok := sp.Epoch(); return ok && n == 0 })
}

// The lease is lost after dropUnleased passed the message and before the spool write: the live check inside the write's
// critical section sees the loss, so nothing is written, the message is nak'd (not acked, not marked seen), and the new
// holder is redelivered it and shows it once; the old endpoint shows nothing.
func TestLegacyLeaseEpochLostBeforeSpoolWriteWritesNothing(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = old })
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, ev, sp := openSpooled(t, f, id)
	kv, fired := loseAt(t, f, ep, &testLegacyCheckSeam)
	f.sendTo(t, snd, "old", "raced")
	<-fired
	if ep.legacyReading() {
		t.Fatal("the lease loss was not noticed")
	}
	time.Sleep(3 * SpoolCheck)
	if hasLegacy(waiting(t, sp)) || sp.Has(spool.Ref{Stream: LegacyStream, Seq: 1}) {
		t.Fatal("a legacy entry was written after the lease was lost")
	}
	if i := legacyConsumerInfo(t, f, "inbox-old"); i.AckFloor.Stream != 0 {
		t.Fatalf("legacy consumer acked: %+v", i)
	}
	if err := (*kv).Delete(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	_, lev := f.run(t, "old")
	until(t, "next holder shown it", func() bool { return strings.Contains(joined(lev), "> raced") })
	time.Sleep(300 * time.Millisecond)
	if n := strings.Count(joined(lev), "> raced"); n != 1 {
		t.Fatalf("the next holder showed it %d times", n)
	}
	if strings.Contains(joined(ev), "raced") {
		t.Fatalf("the old holder showed it:\n%s", joined(ev))
	}
}

// The lease is lost between the live check and the write itself, inside the critical section: the entry may reach the
// disk, but the loss records epoch "none" under the same lock right after, so no hook read shows it, it is dropped and
// nak'd, and the new holder shows the message once.
func TestLegacyLeaseEpochLostBetweenCheckAndWriteIsHidden(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = old })
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, ev, sp := openSpooled(t, f, id)
	kv, fired := loseAt(t, f, ep, &testLegacyPutSeam)
	f.sendTo(t, snd, "old", "raced-late")
	<-fired
	// A hook read, made under the spool lock as a hook does, never shows the entry: the entry's write and the loss's epoch
	// write are each one critical section, the write first.
	for end := time.Now().Add(3 * SpoolCheck); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		unlock, err := sp.Lock() // a hook reads under the spool lock, as the loss's epoch write and the entry's write do
		if err != nil {
			t.Fatal(err)
		}
		shown := hasLegacy(waiting(t, sp))
		unlock()
		if shown {
			t.Fatal("a hook read showed a legacy entry written under a lost lease")
		}
	}
	if n, ok := sp.Epoch(); !ok || n != 0 {
		t.Fatalf("epoch %d %v, want none", n, ok)
	}
	until(t, "the entry dropped", func() bool { return !sp.Has(spool.Ref{Stream: LegacyStream, Seq: 1}) })
	if err := (*kv).Delete(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	_, lev := f.run(t, "old")
	until(t, "next holder shown it", func() bool { return strings.Contains(joined(lev), "> raced-late") })
	time.Sleep(300 * time.Millisecond)
	if n := strings.Count(joined(lev), "> raced-late"); n != 1 {
		t.Fatalf("the next holder showed it %d times", n)
	}
	if strings.Contains(joined(ev), "raced-late") {
		t.Fatalf("the old holder showed it:\n%s", joined(ev))
	}
}

// A legacy entry written under an old epoch is skipped by the hook read once the epoch has changed.
func TestLegacyLeaseEpochOldEntrySkippedAfterChange(t *testing.T) {
	f := newFixture(t, "old", "snd")
	snd := f.client(t, "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, sp := openSpooled(t, f, id)
	f.sendTo(t, snd, "old", "under the old epoch")
	until(t, "legacy mail spooled", func() bool { return hasLegacy(waiting(t, sp)) })
	unlock, err := sp.Lock()
	if err != nil {
		t.Fatal(err)
	}
	err = sp.NoteEpoch(ep.legacy.epoch.Load() + 1000) // another hold took the epoch
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	if hasLegacy(waiting(t, sp)) {
		t.Fatal("a legacy entry of an earlier epoch is still listed")
	}
}

// healHeld rewrites a legacy entry only through the lease-checked, epoch-stamped path: stamped while the lease is held,
// not written once it is lost (a rewrite with no epoch would stay visible after the loss).
func TestLegacyLeaseHealHeldStampsAndSkipsAfterLoss(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = old })
	f := newFixture(t, "old", "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, sp := openSpooled(t, f, id)
	ref := spool.Ref{Stream: LegacyStream, Seq: 5}

	h := gHeld()
	h.msgs[ref] = pending{mail: legacyMail(5), msg: &dblAck{}}
	ep.healHeld(h)
	if !sp.Has(ref) {
		t.Fatal("a held legacy message was not rewritten while the lease is held")
	}
	if w := waiting(t, sp); len(w) != 1 || w[0].Epoch == 0 || w[0].Epoch != ep.legacy.epoch.Load() {
		t.Fatalf("rewritten entry not stamped with the epoch of the hold: %+v", w)
	}

	// Lost: the entry is gone, and a heal writes nothing.
	takeLegacyOver(t, f)
	until(t, "loss noticed", func() bool { return !ep.legacyReading() })
	if err := os.Remove(filepath.Join(sp.Dir, "legacy@5.json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	ep.healHeld(h)
	if sp.Has(ref) || hasLegacy(waiting(t, sp)) {
		t.Fatal("a legacy entry was rewritten after the lease was lost")
	}
}

// A duplicate legacy delivery is acked only while the lease is held; after the loss it is nak'd, never acked.
func TestLegacyLeaseDedupeAckGatedByLease(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = old })
	f := newFixture(t, "old", "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, _ := openSpooled(t, f, id)

	dup := func() *ackCounter {
		a, b := &ackCounter{}, &ackCounter{}
		got := ep.dedupe([]pending{{mail: legacyMail(7), msg: a, key: "k"}, {mail: legacyMail(7), msg: b, key: "k"}}, newSeenSet(0), gHeld())
		if len(got) != 1 {
			t.Fatalf("shown %d", len(got))
		}
		return b
	}
	if b := dup(); b.acks != 1 || b.naks != 0 {
		t.Fatalf("held: acks=%d naks=%d", b.acks, b.naks)
	}
	takeLegacyOver(t, f)
	until(t, "loss noticed", func() bool { return !ep.legacyReading() })
	if b := dup(); b.acks != 0 || b.naks != 1 {
		t.Fatalf("lost: acks=%d naks=%d", b.acks, b.naks)
	}
}

// The lease lost between dropUnleased and deliverBatch: the channel path fences the legacy mail at the write, so the
// old session shows none of it, acks none of it, and nak's it for the new holder; own-stream mail is still delivered.
func TestLegacyLeaseLostBeforeChannelDeliveryFencesLegacyMail(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = 100 * time.Millisecond
	t.Cleanup(func() { LeaseRefresh = old })
	f := newFixture(t, "old", "snd")
	id := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, _ := openSpooled(t, f, id)

	var calls int
	var meta map[string]string
	deliver := func(_ string, m map[string]string) error { calls++; meta = m; return nil }

	// Held: delivered and acked as before.
	held := &ackCounter{}
	ep.deliverBatch(deliver, []pending{{mail: legacyMail(7), msg: held}}, newSeenSet(0))
	if calls != 1 || held.acks != 1 || held.naks != 0 {
		t.Fatalf("held: calls=%d acks=%d naks=%d", calls, held.acks, held.naks)
	}

	// A new holder takes the lease over; the batch was already past dropUnleased.
	takeLegacyOver(t, f)
	until(t, "loss noticed", func() bool { return !ep.legacyReading() })
	calls = 0
	lost := &ackCounter{}
	ep.deliverBatch(deliver, []pending{{mail: legacyMail(8), msg: lost}}, newSeenSet(0))
	if calls != 0 || lost.acks != 0 || lost.naks != 1 {
		t.Fatalf("lost: calls=%d acks=%d naks=%d", calls, lost.acks, lost.naks)
	}

	// A mixed batch: only the own-stream message is shown.
	own, lost2 := &ackCounter{}, &ackCounter{}
	ep.deliverBatch(deliver, []pending{{mail: gMail(3, "own"), msg: own}, {mail: legacyMail(9), msg: lost2}}, newSeenSet(0))
	if calls != 1 || meta["count"] != "1" || meta["seqs"] != "3" {
		t.Fatalf("mixed: calls=%d meta=%v", calls, meta)
	}
	if own.acks != 1 || own.naks != 0 || lost2.acks != 0 || lost2.naks != 1 {
		t.Fatalf("mixed: own acks=%d naks=%d, legacy acks=%d naks=%d", own.acks, own.naks, lost2.acks, lost2.naks)
	}
}
