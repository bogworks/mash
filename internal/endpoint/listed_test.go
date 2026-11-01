package endpoint

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
	"github.com/nats-io/nats.go/jetstream"
)

// listedKnobs shortens the idle wake and the spool check for a test, and puts them back.
func listedKnobs(t *testing.T) {
	t.Helper()
	oi, oc := IdleWake, SpoolCheck
	IdleWake, SpoolCheck = 300*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { IdleWake, SpoolCheck = oi, oc })
}

func sendQuestions(t *testing.T, f *fixture, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := f.client(t, "b").SendWith(context.Background(), "a", "q"+strconv.Itoa(i), SendOptions{Kind: kind.Question}); err != nil {
			t.Fatal(err)
		}
	}
}

// listAll is the hook's digest: every waiting message is marked listed, none drained.
func listAll(t *testing.T, sp spool.Spool) []uint64 {
	t.Helper()
	var seqs []uint64
	for _, e := range waiting(t, sp) {
		if err := sp.Listed(e.Seq); err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, e.Seq)
	}
	return seqs
}

// A message a digest only listed stays unacked: the consumer's unread count is unchanged until the model reads it,
// in any mode (here the normal one, not hookless), and then it is acked. Reading another seq acks nothing.
func TestListedStaysUnackedUntilRead(t *testing.T) {
	listedKnobs(t)
	f := newFixture(t, "a", "b")
	a, _, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	sendQuestions(t, f, 2)
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 2 })
	seqs := listAll(t, sp)
	time.Sleep(5 * SpoolCheck)
	if n := unread(t, a); n != 2 {
		t.Fatalf("unread %d after a digest, want 2 (listed is not seen)", n)
	}
	if a.MarkRead(seqs[1] + 100) {
		t.Fatal("MarkRead drained a message that is not held")
	}
	if !a.MarkRead(seqs[0]) {
		t.Fatal("MarkRead did not drain a listed message")
	}
	until(t, "the read message is acked", func() bool { return unread(t, a) == 1 })
	time.Sleep(5 * SpoolCheck)
	if n := unread(t, a); n != 1 {
		t.Fatalf("unread %d, want 1: the other listed message is still unread", n)
	}
	if sp.IsListed(seqs[0]) {
		t.Fatal("the read message is still marked listed")
	}
	if !sp.IsListed(seqs[1]) || !sp.Has(seqs[1]) {
		t.Fatal("the unread message lost its listed mark or its file")
	}
}

// A message that is waiting and was never listed is not drained by a read in the normal state (unchanged, ADR 0041).
func TestReadOfAnUnlistedMessageStillDoesNotAckInTheNormalState(t *testing.T) {
	listedKnobs(t)
	f := newFixture(t, "a", "b")
	a, _, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	sendQuestions(t, f, 1)
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	if a.MarkRead(waiting(t, sp)[0].Seq) {
		t.Fatal("MarkRead drained an unlisted message in the normal state")
	}
}

// After the first ring a digest-only message causes no wake and no waking_pending, however long it is held.
func TestListedNeverWakes(t *testing.T) {
	listedKnobs(t)
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooledFromStart(t, "a")
	sendQuestions(t, f, 3)
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 3 })
	ev.waitFor(t, 1, 5*time.Second) // the pre-hook ring
	until(t, "the count is known", func() bool { _, ok := a.waking.snapshot(); return ok })
	if n, _ := a.waking.snapshot(); n != 3 {
		t.Fatalf("waking_pending %d before the digest, want 3", n)
	}
	if err := sp.Touch(); err != nil { // the hook runs and lists them
		t.Fatal(err)
	}
	listAll(t, sp)
	until(t, "waking_pending drops", func() bool { n, _ := a.waking.snapshot(); return n == 0 })
	rung := len(ev.all())
	time.Sleep(6 * IdleWake) // several idle wakes and backoffs
	if got := ev.all(); len(got) != rung {
		t.Fatalf("woke for listed mail: %q", got)
	}
	if n, _ := a.waking.snapshot(); n != 0 {
		t.Fatalf("waking_pending %d, want 0", n)
	}
	if n := unread(t, a); n != 3 {
		t.Fatalf("unread %d, want 3: listed mail stays unacked", n)
	}
}

// New mail wakes as usual, and the wake's summary does not announce the listed mail again.
func TestListedMailDoesNotHideOrRepeatInANewWake(t *testing.T) {
	listedKnobs(t)
	f := newFixture(t, "a", "b")
	_, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	sendQuestions(t, f, 2)
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 2 })
	listAll(t, sp)
	time.Sleep(3 * IdleWake)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("woke for listed mail: %q", got)
	}
	sendQuestions(t, f, 1)
	if got := ev.waitFor(t, 1, 5*time.Second); got[0] != "1 message from b (question)" {
		t.Fatalf("the wake %q, want the new message alone", got[0])
	}
}

// A later full showing drains a listed message: the hook's drained mark acks it, and its listed mark goes.
func TestListedIsAckedWhenALaterHookShowsItInFull(t *testing.T) {
	listedKnobs(t)
	f := newFixture(t, "a", "b")
	a, _, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	sendQuestions(t, f, 1)
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	seq := listAll(t, sp)[0]
	time.Sleep(3 * SpoolCheck)
	if n := unread(t, a); n != 1 {
		t.Fatalf("unread %d, want 1", n)
	}
	if err := sp.Drained(seq); err != nil {
		t.Fatal(err)
	}
	until(t, "acked", func() bool { return unread(t, a) == 0 })
	until(t, "forgotten", func() bool { return !sp.IsListed(seq) && !sp.IsDrained(seq) && !sp.Has(seq) })
}

// A restart (a rebind) spools the unacked listed mail again as new mail: one digest of it, then no wake loop.
func TestListedMailIsListedOnceAgainAfterARebind(t *testing.T) {
	listedKnobs(t)
	f := newFixture(t, "a", "b")
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	open := func() (*Endpoint, *events, func()) {
		ctx, cancel := context.WithCancel(context.Background())
		ep, err := Open(ctx, f.cfg("a"))
		if err != nil {
			t.Fatal(err)
		}
		ep.NoChannel()
		ep.SpoolFromStart()
		if err := ep.SetSpool(sp, ""); err != nil {
			t.Fatal(err)
		}
		ev := &events{}
		done := make(chan struct{})
		go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
		return ep, ev, func() { cancel(); <-done; ep.Close() }
	}
	a1, _, stop1 := open()
	cc := store.ConsumerConfig()
	cc.AckWait = time.Second // an unacked message is redelivered to the next session at once
	ajs, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ajs.UpdateConsumer(context.Background(), sap.InboxStream("a"), cc); err != nil {
		t.Fatal(err)
	}
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	sendQuestions(t, f, 3)
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 3 })
	listAll(t, sp)
	stop1()
	_ = a1

	a2, ev2, stop2 := open()
	t.Cleanup(stop2)
	until(t, "respooled", func() bool { return len(waiting(t, sp)) == 3 })
	for _, e := range waiting(t, sp) {
		if e.Listed || sp.IsListed(e.Seq) {
			t.Fatalf("#%d is still listed after the rebind: it would never be shown again", e.Seq)
		}
	}
	if err := sp.Touch(); err != nil { // the new session's hook runs and lists them: the one digest
		t.Fatal(err)
	}
	listAll(t, sp)
	rung := len(ev2.all())
	time.Sleep(6 * IdleWake)
	if got := ev2.all(); len(got) != rung {
		t.Fatalf("a wake loop after the rebind: %q", got)
	}
	if n := unread(t, a2); n != 3 {
		t.Fatalf("unread %d, want 3", n)
	}
}
