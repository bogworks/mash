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
