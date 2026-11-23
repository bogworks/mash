package endpoint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/spool"
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

// An older waiting message from a deleted sender, far outside the listed window and outside the newest 200 waiting (the
// old urgent look), is not counted in the total or the new count, and the count the wake decides on is the inbox's own
// count. #1 is valid and #2 is the deleted sender's, with 250 messages after them: the old code read only the newest
// UrgentLook waiting and the oldest (#1), so it never saw #2 and counted 252, not 251.
func TestReadInboxAndWakeCountOlderDroppedMailOnce(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	ctx := context.Background()
	cc := f.client(t, "c")
	if _, err := cc.Send(ctx, tdot("a"), "valid, oldest", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client(t, "b").Send(ctx, tdot("a"), "from b, soon gone", 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250; i++ {
		if _, err := cc.SendWith(ctx, tdot("a"), "m", SendOptions{Urgent: i == 100}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.adm.Deprovision(ctx, taddr("b")); err != nil {
		t.Fatal(err)
	}
	a := strict(t, f, "a")
	r, err := a.ReadInbox(ctx, InboxQuery{Limit: 3}) // the newest 3: #2 is far outside
	if err != nil {
		t.Fatal(err)
	}
	if r.State.Total != 251 || r.State.Waiting != 251 || r.NewUrgent != 1 || r.Partial {
		t.Fatalf("total %d, waiting %d, urgent %d, partial %v: want 251, 251, 1, false", r.State.Total, r.State.Waiting, r.NewUrgent, r.Partial)
	}
	rd, ok := a.waitingNow(time.Now())
	if !ok || rd.waiting != r.State.Waiting || !rd.dropped[2] {
		t.Fatalf("wake reading %+v (ok %v), inbox count %d", rd, ok, r.State.Waiting)
	}
	if !rd.oldest.Equal(r.State.OldestWaiting) {
		t.Fatalf("wake oldest %v, inbox oldest %v", rd.oldest, r.State.OldestWaiting)
	}
}

// setCaps lowers the cap of the counting pass for one test.
func setCaps(t *testing.T, msgs, bytes int) {
	t.Helper()
	om, ob := countMaxMsgs, countMaxBytes
	countMaxMsgs, countMaxBytes = msgs, bytes
	t.Cleanup(func() { countMaxMsgs, countMaxBytes = om, ob })
}

// The counting pass stops at its cap: 30 waiting, urgent at #2, #15 and #29, the newest 3 listed (outside the cap) and
// a cap of 10 (a byte cap of 1 reads only #1: urgent 1). The read is Partial, the server's counts stand, the urgent count is of what was read (the oldest 10 and
// the listed 3: #2 and #29, not #15), and the oldest is from the scan. At or above 30 it is not Partial and counts all.
func TestReadInboxCountingPassStopsAtTheCap(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	b := f.client(t, "b")
	for i := 1; i <= 30; i++ {
		if _, err := b.SendWith(ctx, tdot("a"), "m", SendOptions{Urgent: i == 2 || i == 15 || i == 29}); err != nil {
			t.Fatal(err)
		}
	}
	a := strict(t, f, "a")
	first, err := a.Read(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name        string
		msgs, bytes int
		partial     bool
		urgent      int
	}{
		{"message cap", 10, 1 << 30, true, 2},
		{"byte cap", 1000, 1, true, 1},
		{"exactly the backlog", 27, 1 << 30, false, 3}, // 30 less the 3 listed
		{"above", 1000, 1 << 30, false, 3},
	} {
		setCaps(t, c.msgs, c.bytes)
		r, err := a.ReadInbox(ctx, InboxQuery{Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		if r.Partial != c.partial || r.NewUrgent != c.urgent || r.State.Total != 30 || r.State.Waiting != 30 || len(r.Mail) != 3 {
			t.Fatalf("%s: partial %v urgent %d total %d waiting %d listed %d: want %v, %d, 30, 30, 3",
				c.name, r.Partial, r.NewUrgent, r.State.Total, r.State.Waiting, len(r.Mail), c.partial, c.urgent)
		}
		if !r.State.OldestWaiting.Equal(first.At) {
			t.Fatalf("%s: oldest %v, want #1's %v", c.name, r.State.OldestWaiting, first.At)
		}
	}
}

// The wake's decision rests on the inbox count, not the held set: WakeCount+1 messages are held, one from a sender since
// deleted, so the inbox counts WakeCount and no wake is sent. A count that said no stands for wakeRecount (more mail
// that would change the answer is not seen); after it the count is read again, the wake is sent, its line and meta
// carry the reading's count, and the dropped message is not among the held ones shown.
func TestBacklogWakeDecidesOnTheInboxCountAndRecountsAfterAMinute(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	ctx := context.Background()
	if _, err := f.client(t, "b").Send(ctx, tdot("a"), "from b, soon gone", 0); err != nil {
		t.Fatal(err)
	}
	cc := f.client(t, "c")
	for range 3 {
		if _, err := cc.Send(ctx, tdot("a"), "from c", 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.adm.Deprovision(ctx, taddr("b")); err != nil {
		t.Fatal(err)
	}
	r := newBacklogRig(t, 3, 0)
	cfg := f.cfgState("a", t.TempDir(), false)
	cfg.WakeCount, cfg.BatchMsgs, cfg.BatchBytes = 3, 10, 16384
	ep, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	ep.spool, ep.wakeStart = &r.sp, r.now
	for n := uint64(1); n <= 4; n++ { // all four held, as serve holds them
		r.h.msgs[spool.Ref{Seq: n}] = pending{msg: &dblAck{}, mail: render.Mail{Seq: n, From: "c", Kind: kind.Task, At: r.now, Body: []byte("x")}}
	}
	r.e = ep
	var line string
	var meta map[string]string
	check := func(d time.Duration) {
		r.now = r.now.Add(d)
		ep.wakeBacklog(func(text string, m map[string]string) error { line, meta = text, m; return nil }, r.h, HooklessNone)
	}
	check(wakeCooldown) // four held, the inbox counts three: not a backlog
	if line != "" || ep.wakeNoAt.IsZero() {
		t.Fatalf("woke on a count of three, or did not record the no: %q", line)
	}
	for range 2 { // two more waiting: the count would now say five
		if _, err := cc.Send(ctx, tdot("a"), "from c", 0); err != nil {
			t.Fatal(err)
		}
	}
	check(10 * time.Second)
	if line != "" {
		t.Fatalf("read again inside wakeRecount: %q", line)
	}
	check(wakeRecount)
	if line == "" {
		t.Fatal("no wake after wakeRecount with five waiting")
	}
	if !strings.HasPrefix(line, "5 messages") || meta["count"] != "5" {
		t.Fatalf("line %q, count %q: want the reading's five", line, meta["count"])
	}
	if strings.Contains(meta["seqs"], "1") || !strings.Contains(meta["seqs"], "2") {
		t.Fatalf("seqs %q: want 2,3,4 without the dropped #1", meta["seqs"])
	}
}
