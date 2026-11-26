package endpoint

import (
	"context"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/store"
	"github.com/nats-io/nats.go/jetstream"
)

// A refused message is never new: the inbox lists it without OnlyNew, and with OnlyNew leaves it out of the listed
// mails while the read's refused count still counts it.
func TestInboxOnlyNewDoesNotListARefusedMessage(t *testing.T) {
	f := newFixture(t, "a", "c")
	ctx := context.Background()
	cc := f.client(t, "c")
	for _, body := range []string{"refused one", "normal two"} {
		if _, err := cc.Send(ctx, tdot("a"), body, 0); err != nil {
			t.Fatal(err)
		}
	}
	ep, err := Open(ctx, f.cfgState("a", t.TempDir(), false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	if _, err := ep.recordRefusal(1); err != nil {
		t.Fatal(err)
	}
	listed := func(r InboxRead) map[uint64]bool {
		out := map[uint64]bool{}
		for _, m := range r.Mail {
			out[m.Seq] = true
		}
		return out
	}
	all, err := ep.readInbox(ctx, InboxQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if l := listed(all); !l[1] || !l[2] || all.Refused != 1 {
		t.Fatalf("OnlyNew false: listed %v, refused %d: want seqs 1 and 2 listed, 1 refused", l, all.Refused)
	}
	fresh, err := ep.readInbox(ctx, InboxQuery{OnlyNew: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if l := listed(fresh); l[1] || !l[2] || fresh.Refused != 1 {
		t.Fatalf("OnlyNew true: listed %v, refused %d: want only seq 2 listed, 1 refused", l, fresh.Refused)
	}
}

// The top-up read, reached when the first batch held gone messages, passes over a refused message with OnlyNew and
// lists it without.
func TestInboxOnlyNewTopUpDoesNotListARefusedMessage(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	ctx := context.Background()
	for _, from := range []string{"c", "b", "b", "c"} { // #1 stays, #2 and #3 go, #4 is refused
		if _, err := f.client(t, from).Send(ctx, tdot("a"), "from "+from, 0); err != nil {
			t.Fatal(err)
		}
	}
	cc := f.client(t, "c")
	for i := 0; i < fillerCount; i++ { // newer mail, so the first batch does not reach #4
		if _, err := cc.Send(ctx, tdot("a"), "filler", 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.adm.Deprovision(ctx, taddr("b")); err != nil {
		t.Fatal(err)
	}
	ep := strict(t, f, "a")
	if _, err := ep.recordRefusal(4); err != nil {
		t.Fatal(err)
	}
	all, err := ep.readInbox(ctx, InboxQuery{After: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Mail) != 1 || all.Mail[0].Seq != 4 {
		t.Fatalf("OnlyNew false: listed %+v: want the refused #4", all.Mail)
	}
	fresh, err := ep.readInbox(ctx, InboxQuery{After: 1, Limit: 1, OnlyNew: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Mail) != 1 || fresh.Mail[0].Seq != 5 {
		t.Fatalf("OnlyNew true: listed %+v: want #5, the refused #4 passed over", fresh.Mail)
	}
}

// The waiting count leaves out a refused message the consumer still holds as pending: it is stored, never delivered,
// and OnlyNew lists no row for it.
func TestInboxWaitingLeavesOutARefusedPendingMessage(t *testing.T) {
	f := newFixture(t, "a", "c")
	ctx := context.Background()
	cc := f.client(t, "c")
	for _, body := range []string{"refused one", "normal two"} {
		if _, err := cc.Send(ctx, tdot("a"), body, 0); err != nil {
			t.Fatal(err)
		}
	}
	ep, err := Open(ctx, f.cfgState("a", t.TempDir(), false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	if _, err := ep.recordRefusal(1); err != nil {
		t.Fatal(err)
	}
	cons, err := ep.js.Consumer(ctx, ep.ident.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatalf("the consumer must exist for this test: %v", err)
	}
	if n := cons.CachedInfo().NumPending; n != 2 {
		t.Fatalf("consumer NumPending = %d: want 2 (the refused message is counted by the server)", n)
	}
	fresh, err := ep.readInbox(ctx, InboxQuery{OnlyNew: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.State.Waiting != 1 || len(fresh.Mail) != 1 || fresh.Mail[0].Seq != 2 {
		t.Fatalf("waiting %d, listed %+v: want 1 waiting and only seq 2 listed", fresh.State.Waiting, fresh.Mail)
	}
}

// A refused message already delivered to serve is acknowledged after it is recorded, possibly out of order above the
// ack floor; the server then no longer counts it, so the waiting count must not subtract it a second time.
func TestInboxWaitingCountsAMessageBehindARefusedOneAckedOutOfOrder(t *testing.T) {
	f := newFixture(t, "a", "c")
	ctx := context.Background()
	cc := f.client(t, "c")
	for _, body := range []string{"normal one", "refused two"} {
		if _, err := cc.Send(ctx, tdot("a"), body, 0); err != nil {
			t.Fatal(err)
		}
	}
	ep, err := Open(ctx, f.cfgState("a", t.TempDir(), false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	if _, err := ep.recordRefusal(2); err != nil {
		t.Fatal(err)
	}
	cons, err := ep.js.Consumer(ctx, ep.ident.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatalf("the consumer must exist for this test: %v", err)
	}
	batch, err := cons.Fetch(2, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for m := range batch.Messages() {
		md, err := m.Metadata()
		if err != nil {
			t.Fatal(err)
		}
		if md.Sequence.Stream == 2 { // ack only the refused one: #1 stays pending, above no floor
			if err := m.DoubleAck(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := ep.readInbox(ctx, InboxQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if r.State.Waiting != 1 {
		t.Fatalf("waiting %d: want 1 (seq 1 still waits behind the acked refused #2)", r.State.Waiting)
	}
}
