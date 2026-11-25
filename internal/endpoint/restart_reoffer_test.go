package endpoint

import (
	"context"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
	"github.com/nats-io/nats.go/jetstream"
)

// restartRig is a serve over one fixed spool that a test can stop and start again over the same endpoint, stream,
// durable consumer and spool directory, as a restart of the Claude session does.
type restartRig struct {
	t  *testing.T
	f  *fixture
	sp spool.Spool
}

func newRestartRig(t *testing.T) *restartRig {
	t.Helper()
	listedKnobs(t)
	return &restartRig{t: t, f: newFixture(t, "a", "b"), sp: spool.Spool{Dir: t.TempDir() + "/spool"}}
}

// start opens a serve and returns it with its stop. The first call also shortens the consumer's AckWait, so mail the
// stopped serve left unacked is redelivered to the next one at once.
func (r *restartRig) start(first bool) (*Endpoint, *events, func()) {
	r.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, r.f.cfg("a"))
	if err != nil {
		r.t.Fatal(err)
	}
	ep.NoChannel()
	ep.SpoolFromStart()
	if err := ep.SetSpool(r.sp, ""); err != nil {
		r.t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	if first {
		cc := store.ConsumerConfig()
		cc.AckWait = time.Second
		ajs, err := jetstream.New(r.f.e.Conn(r.t, r.f.e.AdminCreds))
		if err != nil {
			r.t.Fatal(err)
		}
		if _, err := ajs.UpdateConsumer(context.Background(), taddr("a").InboxStream(), cc); err != nil {
			r.t.Fatal(err)
		}
	}
	return ep, ev, func() { cancel(); <-done; ep.Close() }
}

// #816: mail that was shown and then read is not offered again after a restart. One message is shown in full by a hook
// (drained), one was listed in a digest and then read with the read tool (MarkRead, as `read` does after its result is
// written). Both are acked, so a new serve over the same endpoint and spool respools nothing and wakes nothing.
func TestMailShownOrReadIsNotOfferedAgainAfterARestart(t *testing.T) {
	r := newRestartRig(t)
	a1, _, stop1 := r.start(true)
	if err := r.sp.Touch(); err != nil {
		t.Fatal(err)
	}
	sendQuestions(t, r.f, 2)
	until(t, "spooled", func() bool { return len(waiting(t, r.sp)) == 2 })
	w := waiting(t, r.sp)
	shown, listed := w[0].Seq, w[1].Seq
	if err := r.sp.Drained(spool.Ref{Seq: shown}); err != nil { // the hook showed it in full
		t.Fatal(err)
	}
	if err := r.sp.Listed(spool.Ref{Seq: listed}); err != nil { // a digest listed the other
		t.Fatal(err)
	}
	until(t, "the shown message is acked", func() bool { return unread(t, a1) == 1 })
	if !a1.MarkRead(MsgRef{Seq: listed}) { // the read tool
		t.Fatal("MarkRead did not drain a listed message")
	}
	until(t, "the read message is acked", func() bool { return unread(t, a1) == 0 })
	stop1()

	a2, ev2, stop2 := r.start(false)
	t.Cleanup(stop2)
	time.Sleep(2*time.Second + 3*IdleWake) // past AckWait and several idle wakes: a redelivery would have come
	if got := waiting(t, r.sp); len(got) != 0 {
		t.Fatalf("a restart offered mail again that was shown or read: %+v", got)
	}
	if got := ev2.all(); len(got) != 0 {
		t.Fatalf("a restart woke the session for mail that was shown or read: %q", got)
	}
	if n := unread(t, a2); n != 0 {
		t.Fatalf("unread %d after the restart, want 0", n)
	}
}

// #816: mail a digest only listed and the model never read stays unacked, so after a restart it is offered again,
// once and as new mail (its listed mark is cleared so one digest lists it), while mail that was read is not. This is
// the documented behaviour: docs/operations.md ("After a restart of serve, unacked listed mail is redelivered and
// listed again once, in one digest"), docs/troubleshooting.md and ADR 0046. The field report's "same three messages
// at every restart" is this, as long as the model never calls read for them; once it has read them, a further
// restart offers nothing.
func TestListedUnreadMailIsOfferedOnceAgainAfterARestartAndReadMailIsNot(t *testing.T) {
	r := newRestartRig(t)
	a1, _, stop1 := r.start(true)
	if err := r.sp.Touch(); err != nil {
		t.Fatal(err)
	}
	sendQuestions(t, r.f, 3)
	until(t, "spooled", func() bool { return len(waiting(t, r.sp)) == 3 })
	seqs := listAll(t, r.sp) // one digest lists all three
	read, unreadSeqs := seqs[0], seqs[1:]
	if !a1.MarkRead(MsgRef{Seq: read}) {
		t.Fatal("MarkRead did not drain a listed message")
	}
	until(t, "one acked, two unread", func() bool { return unread(t, a1) == 2 })
	stop1()

	// First restart: the two unread are offered again, as new (unlisted) mail, and the read one is not.
	a2, _, stop2 := r.start(false)
	until(t, "the unread two are respooled", func() bool { return len(waiting(t, r.sp)) == 2 })
	for _, e := range waiting(t, r.sp) {
		if e.Seq == read {
			t.Fatalf("#%d was read and is offered again after the restart", e.Seq)
		}
		if e.Listed || r.sp.IsListed(spool.Ref{Seq: e.Seq}) {
			t.Fatalf("#%d is still listed after the restart: it would never be shown again", e.Seq)
		}
	}
	if n := unread(t, a2); n != 2 {
		t.Fatalf("unread %d, want 2", n)
	}
	// The new session's hook lists them again; the model reads one.
	listAll(t, r.sp)
	if !a2.MarkRead(MsgRef{Seq: unreadSeqs[0]}) {
		t.Fatal("MarkRead did not drain a re-listed message")
	}
	until(t, "one unread left", func() bool { return unread(t, a2) == 1 })
	stop2()

	// Second restart: only the one still unread comes back.
	_, _, stop3 := r.start(false)
	t.Cleanup(stop3)
	until(t, "the one unread is respooled", func() bool { return len(waiting(t, r.sp)) == 1 })
	time.Sleep(2*time.Second + IdleWake)
	got := waiting(t, r.sp)
	if len(got) != 1 || got[0].Seq != unreadSeqs[1] {
		t.Fatalf("after the second restart waiting %+v, want only #%d", got, unreadSeqs[1])
	}
}
