package endpoint

import (
	"context"
	"testing"
)

// strict is a client of name that refuses mail from a sender it cannot verify (accept_unverified off).
func strict(t *testing.T, f *fixture, name string) *Endpoint {
	t.Helper()
	ep, err := OpenClient(context.Background(), f.cfgState(name, t.TempDir(), false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	return ep
}

// A message whose sender was verified when it arrived but is no longer known when the inbox is read is not listed, so
// it is not counted: the total, the new count, the urgent count and the oldest-waiting time are of the messages shown.
func TestReadInboxCountsOnlyWhatTheListShows(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	ctx := context.Background()
	if _, err := f.client(t, "b").Send(ctx, tdot("a"), "from b, soon gone", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client(t, "c").Send(ctx, tdot("a"), "from c, stays", 0); err != nil {
		t.Fatal(err)
	}
	if err := f.adm.Deprovision(ctx, taddr("b")); err != nil {
		t.Fatal(err)
	}
	r, err := strict(t, f, "a").ReadInbox(ctx, InboxQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Mail) != 1 || r.Mail[0].Seq != 2 {
		t.Fatalf("listed: %+v", r.Mail)
	}
	if r.State.Total != 1 || r.State.Waiting != 1 {
		t.Fatalf("total %d, waiting %d: want 1 and 1", r.State.Total, r.State.Waiting)
	}
	if want := r.Mail[0].At; !r.State.OldestWaiting.Equal(want) && r.State.OldestWaiting.Before(want) {
		t.Fatalf("oldest waiting %v is from the dropped message, not the listed one (%v)", r.State.OldestWaiting, want)
	}
}

// With the only message dropped the inbox is empty: nothing total, nothing new, nothing urgent, no oldest.
func TestReadInboxAllDroppedIsEmpty(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	if _, err := f.client(t, "b").Send(ctx, tdot("a"), "from b, soon gone", 0); err != nil {
		t.Fatal(err)
	}
	if err := f.adm.Deprovision(ctx, taddr("b")); err != nil {
		t.Fatal(err)
	}
	r, err := strict(t, f, "a").ReadInbox(ctx, InboxQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Mail) != 0 || r.State.Total != 0 || r.State.Waiting != 0 || r.NewUrgent != 0 || !r.State.OldestWaiting.IsZero() {
		t.Fatalf("want an empty inbox, got %+v", r)
	}
}

// Dropped messages found while topping up the list (the first batch had dropped mail, so the listing reads further
// on) are not counted either.
func TestReadInboxCountsDroppedFoundWhileToppingUp(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	ctx := context.Background()
	for _, from := range []string{"c", "b", "b"} { // #1 stays, #2 and #3 go
		if _, err := f.client(t, from).Send(ctx, tdot("a"), "from "+from, 0); err != nil {
			t.Fatal(err)
		}
	}
	// enough newer mail that #2 and #3 are outside what the read fetches up front (past the first batch the read takes)
	cc := f.client(t, "c")
	for i := 0; i < fillerCount; i++ {
		if _, err := cc.Send(ctx, tdot("a"), "filler", 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.adm.Deprovision(ctx, taddr("b")); err != nil {
		t.Fatal(err)
	}
	// oldest first after #1, one wanted: #2 is dropped in the first batch, the top-up drops #3 and lists #4
	r, err := strict(t, f, "a").ReadInbox(ctx, InboxQuery{After: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Mail) != 1 || r.Mail[0].Seq != 4 {
		t.Fatalf("listed: %+v", r.Mail)
	}
	if want := fillerCount + 1; r.State.Total != want || r.State.Waiting != want {
		t.Fatalf("total %d, waiting %d: want %d and %d", r.State.Total, r.State.Waiting, want, want)
	}
}

// The oldest-waiting scan looks past dropped messages, including one it has to fetch itself: the time is of the first
// message that is shown.
func TestReadInboxOldestWaitingSkipsADroppedMessageItFetches(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	ctx := context.Background()
	for _, from := range []string{"b", "b", "c"} { // #1 and #2 go, #3 stays
		if _, err := f.client(t, from).Send(ctx, tdot("a"), "from "+from, 0); err != nil {
			t.Fatal(err)
		}
	}
	// enough newer mail that #2 is outside what the read fetches for the urgent count (past the first batch the read takes)
	cc := f.client(t, "c")
	for i := 0; i < fillerCount; i++ {
		if _, err := cc.Send(ctx, tdot("a"), "filler", 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.adm.Deprovision(ctx, taddr("b")); err != nil {
		t.Fatal(err)
	}
	a := strict(t, f, "a")
	third, err := a.Read(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.ReadInbox(ctx, InboxQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !r.State.OldestWaiting.Equal(third.At) {
		t.Fatalf("oldest waiting %v: want #3's time %v", r.State.OldestWaiting, third.At)
	}
}

// A dropped message that both the up-front wave and the top-up reach (the newest ones, in the counting pass) is
// counted out once, not twice.
func TestReadInboxCountsADroppedMessageOnceWhenToppingUp(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	ctx := context.Background()
	for _, from := range []string{"c", "b", "b"} { // #1 stays; the newest two go
		if _, err := f.client(t, from).Send(ctx, tdot("a"), "from "+from, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.adm.Deprovision(ctx, taddr("b")); err != nil {
		t.Fatal(err)
	}
	r, err := strict(t, f, "a").ReadInbox(ctx, InboxQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Mail) != 1 || r.Mail[0].Seq != 1 {
		t.Fatalf("listed: %+v", r.Mail)
	}
	if r.State.Total != 1 || r.State.Waiting != 1 {
		t.Fatalf("total %d, waiting %d: want 1 and 1", r.State.Total, r.State.Waiting)
	}
}

// fillerCount is how many newer messages the tests below pile on.
const fillerCount = 200

// Urgent mail is counted over the whole waiting backlog, not the newest few hundred: 250 waiting, urgent at #3 and #7.
func TestReadInboxUrgentCountCoversTheWholeBacklog(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	b := f.client(t, "b")
	for i := 1; i <= 250; i++ {
		if _, err := b.SendWith(ctx, tdot("a"), "m", SendOptions{Urgent: i == 3 || i == 7}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := strict(t, f, "a").ReadInbox(ctx, InboxQuery{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if r.NewUrgent != 2 || r.State.Total != 250 || r.State.Waiting != 250 {
		t.Fatalf("urgent %d, total %d, waiting %d: want 2, 250, 250", r.NewUrgent, r.State.Total, r.State.Waiting)
	}
}

// An older waiting message from a deleted sender, far outside the listed window, is not counted in the total or the
// new count, and the count the wake decides on is the inbox's own count. The dropped message is counted out once.
func TestReadInboxAndWakeCountOlderDroppedMailOnce(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	ctx := context.Background()
	if _, err := f.client(t, "b").Send(ctx, tdot("a"), "from b, soon gone", 0); err != nil {
		t.Fatal(err)
	}
	cc := f.client(t, "c")
	for i := 0; i < 250; i++ {
		if _, err := cc.SendWith(ctx, tdot("a"), "m", SendOptions{Urgent: i == 100}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.adm.Deprovision(ctx, taddr("b")); err != nil {
		t.Fatal(err)
	}
	a := strict(t, f, "a")
	r, err := a.ReadInbox(ctx, InboxQuery{Limit: 3}) // the newest 3: #1 is far outside
	if err != nil {
		t.Fatal(err)
	}
	if r.State.Total != 250 || r.State.Waiting != 250 || r.NewUrgent != 1 {
		t.Fatalf("total %d, waiting %d, urgent %d: want 250, 250, 1", r.State.Total, r.State.Waiting, r.NewUrgent)
	}
	n, oldest, ok := a.waitingNow()
	if !ok || n != r.State.Waiting {
		t.Fatalf("wake count %d (ok %v), inbox count %d", n, ok, r.State.Waiting)
	}
	if oldest.IsZero() || oldest.Before(r.State.OldestWaiting) || oldest.After(r.State.OldestWaiting) {
		t.Fatalf("wake oldest %v, inbox oldest %v", oldest, r.State.OldestWaiting)
	}
}
