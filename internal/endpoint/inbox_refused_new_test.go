package endpoint

import (
	"context"
	"testing"
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
