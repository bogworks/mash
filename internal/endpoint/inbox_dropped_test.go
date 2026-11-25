package endpoint

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

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
		by          string // PartialBy
		at          int    // PartialAt
	}{
		{"message cap", 10, 1 << 30, true, 2, "messages", 10},
		{"byte cap", 1000, 1, true, 1, "bytes", 1},
		{"exactly the backlog", 27, 1 << 30, false, 3, "", 0}, // 30 less the 3 listed
		{"above", 1000, 1 << 30, false, 3, "", 0},
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
		if r.PartialBy != c.by || r.PartialAt != c.at {
			t.Fatalf("%s: stopped by %q at %d, want %q at %d (#958: the count says why it is not exact)", c.name, r.PartialBy, r.PartialAt, c.by, c.at)
		}
		if !r.State.OldestWaiting.Equal(first.At) {
			t.Fatalf("%s: oldest %v, want #1's %v", c.name, r.State.OldestWaiting, first.At)
		}
	}
}

// The wake's decision rests on the inbox count, not the held set: WakeCount+1 messages are held, one from a sender since
// deleted, so the inbox counts WakeCount and no wake is sent. A count that said no stands for wakeRecount (more mail
// that would change the answer is not seen); after it the count is read again, the wake is sent, its line and meta
// carry the filtered held mail's count (the reading only decides), and the dropped message is not among the held ones shown.
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
	check(wakeCooldown) // starts the read in the background: this tick is skipped
	if line != "" || !ep.wakeNoAt.IsZero() {
		t.Fatalf("decided before the reading was in: %q", line)
	}
	waitWakeRead(t, ep)
	check(0) // four held, the inbox counts three: not a backlog
	if line != "" || ep.wakeNoAt.IsZero() {
		t.Fatalf("woke on a count of three, or did not record the no: %q", line)
	}
	for n := uint64(5); n <= 6; n++ { // two more arrive, held as serve holds them: the count would now say five
		if _, err := cc.Send(ctx, tdot("a"), "from c", 0); err != nil {
			t.Fatal(err)
		}
		r.h.msgs[spool.Ref{Seq: n}] = pending{msg: &dblAck{}, mail: render.Mail{Seq: n, From: "c", Kind: kind.Task, At: r.now, Body: []byte("x")}}
	}
	check(10 * time.Second)
	if line != "" {
		t.Fatalf("read again inside wakeRecount: %q", line)
	}
	check(wakeRecount) // the reading is stale: a new read starts
	waitWakeRead(t, ep)
	check(0)
	if line == "" {
		t.Fatal("no wake after wakeRecount with five waiting")
	}
	if !strings.HasPrefix(line, "5 messages") || meta["count"] != "5" {
		t.Fatalf("line %q, count %q: want the filtered held five", line, meta["count"])
	}
	if meta["seqs"] != "2,3,4,5,6" {
		t.Fatalf("seqs %q: want 2,3,4 without the dropped #1", meta["seqs"])
	}
}

// waitWakeRead waits until the wake's background read has finished.
func waitWakeRead(t *testing.T, e *Endpoint) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		e.wakeMu.Lock()
		busy := e.wakeInFlight
		e.wakeMu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the wake's read did not finish")
}

// stubWakeRead replaces the wake's read on e, before its first tick; the stub counts its calls.
func stubWakeRead(e *Endpoint, fn func(n int) (waitingReading, bool)) *atomic.Int32 {
	var calls atomic.Int32
	e.wakeRead = func(*Endpoint, time.Time) (waitingReading, bool) { return fn(int(calls.Add(1))) }
	return &calls
}

// connectedRig is a backlog rig whose endpoint has a stream handle (a read is attempted); count messages held.
func connectedRig(t *testing.T, count, held int) (*backlogRig, func(deliver Deliverer, d time.Duration)) {
	t.Helper()
	f := newFixture(t, "a")
	r := newBacklogRig(t, count, 0)
	cfg := f.cfgState("a", t.TempDir(), false)
	cfg.WakeCount, cfg.BatchMsgs, cfg.BatchBytes = count, 10, 16384
	ep, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	ep.spool, ep.wakeStart = &r.sp, r.now
	for n := uint64(1); n <= uint64(held); n++ {
		r.h.msgs[spool.Ref{Seq: n}] = pending{msg: &dblAck{}, mail: render.Mail{Seq: n, From: "c", Kind: kind.Task, At: r.now, Body: []byte("x")}}
	}
	r.e = ep
	return r, func(deliver Deliverer, d time.Duration) {
		r.now = r.now.Add(d)
		ep.wakeBacklog(deliver, r.h, HooklessNone)
	}
}

// Every held message is from a dropped sender: the reading says over, nothing is shown, and the count is not read again
// on every tick but after wakeRecount.
func TestBacklogAllHeldDroppedDoesNotReadEveryTick(t *testing.T) {
	r, check := connectedRig(t, 3, 4)
	calls := stubWakeRead(r.e, func(int) (waitingReading, bool) {
		return waitingReading{waiting: 9, oldest: time.Now(), dropped: map[uint64]bool{1: true, 2: true, 3: true, 4: true}}, true
	})
	woke := false
	deliver := func(string, map[string]string) error { woke = true; return nil }
	check(deliver, wakeCooldown)
	waitWakeRead(t, r.e)
	check(deliver, 0)
	if woke || r.e.wakeNoAt.IsZero() {
		t.Fatalf("woke %v, wakeNoAt %v: want no wake and the no recorded", woke, r.e.wakeNoAt)
	}
	for range 20 {
		check(deliver, 250*time.Millisecond)
		waitWakeRead(t, r.e)
	}
	if n := calls.Load(); n != 1 || woke {
		t.Fatalf("reads %d, woke %v: want 1 and no wake", n, woke)
	}
	check(deliver, wakeRecount)
	waitWakeRead(t, r.e)
	if n := calls.Load(); n != 1 {
		t.Fatalf("reads %d after wakeRecount with the held set unchanged: want 1", n)
	}
	r.h.msgs[spool.Ref{Seq: 5}] = pending{msg: &dblAck{}, mail: render.Mail{Seq: 5, From: "c", Kind: kind.Task, At: r.now, Body: []byte("x")}}
	check(deliver, wakeRecount) // a new arrival: the held set changed, counted again
	waitWakeRead(t, r.e)
	if n := calls.Load(); n != 2 {
		t.Fatalf("reads %d after the held set changed: want 2", n)
	}
}

// A failed deliver records the no too, so the retry waits for wakeRecount and is not repeated every tick.
func TestBacklogDeliverFailureSetsWakeNoAt(t *testing.T) {
	r, check := connectedRig(t, 3, 4)
	stubWakeRead(r.e, func(int) (waitingReading, bool) { return waitingReading{waiting: 9, oldest: time.Now()}, true })
	fails := 0
	deliver := func(string, map[string]string) error { fails++; return errors.New("down") }
	check(deliver, wakeCooldown)
	waitWakeRead(t, r.e)
	check(deliver, 0)
	if fails != 1 || r.e.wakeNoAt.IsZero() {
		t.Fatalf("deliveries %d, wakeNoAt %v: want 1 and set", fails, r.e.wakeNoAt)
	}
	check(deliver, 250*time.Millisecond)
	if fails != 1 {
		t.Fatalf("deliver retried inside wakeRecount: %d", fails)
	}
}

// A partial reading (count not usable) still gives its dropped set: four held, one dropped, WakeCount 3, is three
// shown, not over, no wake.
func TestBacklogPartialReadingFiltersHeldByItsDroppedSet(t *testing.T) {
	r, check := connectedRig(t, 3, 4)
	stubWakeRead(r.e, func(int) (waitingReading, bool) {
		return waitingReading{dropped: map[uint64]bool{1: true}}, false
	})
	woke := false
	deliver := func(string, map[string]string) error { woke = true; return nil }
	check(deliver, wakeCooldown)
	waitWakeRead(t, r.e)
	check(deliver, 0)
	if woke {
		t.Fatal("woke on four held with one from a dropped sender")
	}
}

// A dropped sequence of another stream is not the endpoint's own: only own-stream sequences are filtered.
func TestBacklogDroppedFiltersOnlyTheOwnStream(t *testing.T) {
	r, check := connectedRig(t, 3, 0)
	stubWakeRead(r.e, func(int) (waitingReading, bool) {
		return waitingReading{waiting: 9, oldest: time.Now(), dropped: map[uint64]bool{1: true}}, true
	})
	for _, ref := range []spool.Ref{{Seq: 1}, {Stream: "other", Seq: 1}, {Seq: 2}, {Seq: 3}} {
		r.h.msgs[ref] = pending{msg: &dblAck{}, mail: render.Mail{Seq: ref.Seq, From: "c", Kind: kind.Task, At: r.now, Body: []byte("x")}}
	}
	var meta map[string]string
	deliver := func(_ string, m map[string]string) error { meta = m; return nil }
	check(deliver, wakeCooldown)
	waitWakeRead(t, r.e)
	check(deliver, 0)
	if meta == nil || meta["count"] != "3" {
		t.Fatalf("meta %v: want the other stream's #1 kept and the own #1 left out (count 3)", meta)
	}
}

// The tick never waits for the read: a read stuck in the stub leaves the tick returning at once, with no second read
// started, and the result is used once it lands.
func TestBacklogTickDoesNotBlockOnTheRead(t *testing.T) {
	release := make(chan struct{})
	r, check := connectedRig(t, 3, 4)
	calls := stubWakeRead(r.e, func(int) (waitingReading, bool) {
		<-release
		return waitingReading{waiting: 9, oldest: time.Now()}, true
	})
	woke := 0
	deliver := func(string, map[string]string) error { woke++; return nil }
	done := make(chan struct{})
	go func() {
		defer close(done)
		check(deliver, wakeCooldown)
		check(deliver, 250*time.Millisecond)
		check(deliver, 250*time.Millisecond)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		close(release)
		t.Fatal("the tick blocked on the read")
	}
	if woke != 0 || r.e.wakeNoAt.IsZero() == false {
		t.Fatalf("woke %d before the reading landed", woke)
	}
	close(release)
	waitWakeRead(t, r.e)
	check(deliver, 0)
	if woke != 1 || calls.Load() != 1 {
		t.Fatalf("woke %d, reads %d: want 1 and 1", woke, calls.Load())
	}
}

// all is the whole range as a slice (a test's view; the code never builds one).
func (r seqRange) all() []uint64 { return r.take(int(min(r.remaining(), 1<<20))) }

// A sequence range over a span of 1<<40 costs nothing in proportion to it, and yields the right values up and down.
func TestSeqRangeIsLazy(t *testing.T) {
	const span = uint64(1) << 40
	var sink uint64
	allocs := testing.AllocsPerRun(100, func() {
		u, d := seqsUp(1, span), seqsDown(span, 1)
		sink += u.remaining() + d.remaining()
	})
	if allocs > 0 {
		t.Fatalf("building a range over %d sequences allocated %v times", span, allocs)
	}
	if sink == 0 {
		t.Fatal("range empty")
	}
	up, down := seqsUp(5, span), seqsDown(span, 1)
	if got := up.take(3); len(got) != 3 || got[0] != 5 || got[2] != 7 {
		t.Fatalf("up: %v", got)
	}
	if got := up.take(2); got[0] != 8 || got[1] != 9 || up.remaining() != span-9 {
		t.Fatalf("up, next: %v, %d left", got, up.remaining())
	}
	if got := down.take(3); got[0] != span || got[2] != span-2 || down.remaining() != span-3 {
		t.Fatalf("down: %v", got)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		r := seqsUp(1, span)
		r.take(10)
	}); allocs > 1 {
		t.Fatalf("taking 10 of %d sequences allocated %v times, want only the result slice", span, allocs)
	}
	short := seqsUp(1, 2)
	if got := short.take(10); len(got) != 2 || short.remaining() != 0 || len(short.take(1)) != 0 {
		t.Fatalf("short range: %v", got)
	}
}

// collect over a span of 1<<40 reads only the page: limit reads when nothing is gone, and one more read for each gone
// sequence in the way.
func TestCollectCostsThePage(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	a, b := f.client(t, "a"), f.client(t, "b")
	if _, err := b.Send(ctx, tdot("a"), "m", 0); err != nil {
		t.Fatal(err)
	}
	s, err := a.inbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const span = uint64(1) << 40
	for _, gone := range []int{0, 5} {
		var reads atomic.Int64
		get := func(ctx context.Context, seq uint64, opts ...jetstream.GetMsgOpt) (*jetstream.RawStreamMsg, error) {
			reads.Add(1)
			if seq > span-uint64(gone) {
				return nil, jetstream.ErrMsgNotFound
			}
			return s.GetMsg(ctx, 1, opts...)
		}
		ms, err := a.collect(ctx, get, seqsDown(span, 1), 3)
		if err != nil || len(ms) != 3 || reads.Load() != int64(3+gone) {
			t.Fatalf("%d gone: %d mails, %d reads, %v: want 3 mails and %d reads", gone, len(ms), reads.Load(), err, 3+gone)
		}
	}
}

// An inbox whose first sequence is far above 1 (the stream evicted the rest) lists the right page, up and down. The
// stream refuses deletes, so a gap inside the span cannot be made here; this shows the output is unchanged, and the
// cost bound is TestSeqRangeIsLazy's and TestCollectCostsThePage's.
func TestReadInboxAfterEviction(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	b := f.client(t, "b")
	for i := 1; i <= 40; i++ {
		if _, err := b.Send(ctx, tdot("a"), "m", 0); err != nil {
			t.Fatal(err)
		}
	}
	js := adminJS(t, f)
	s, err := js.Stream(ctx, taddr("a").InboxStream())
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg := info.Config
	cfg.MaxMsgs = 3
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	a := strict(t, f, "a")
	for _, c := range []struct {
		q    InboxQuery
		want []uint64
	}{
		{InboxQuery{Limit: 2}, []uint64{39, 40}},
		{InboxQuery{Limit: 5}, []uint64{38, 39, 40}},
		{InboxQuery{Limit: 2, After: 1}, []uint64{38, 39}},
		{InboxQuery{Limit: 2, After: 39}, []uint64{40}},
	} {
		r, err := a.ReadInbox(ctx, c.q)
		if err != nil {
			t.Fatal(err)
		}
		var got []uint64
		for _, m := range r.Mail {
			got = append(got, m.Seq)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%+v: listed %v, want %v", c.q, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%+v: listed %v, want %v", c.q, got, c.want)
			}
		}
	}
}

// Held mail from senders no longer verified keeps the held set over WakeCount while the count says no. The no stands
// while the held set is the same, however many wakeRecount intervals pass: the inbox is not read again every minute.
func TestBacklogNoStandsWhileTheHeldSetIsUnchanged(t *testing.T) {
	r, check := connectedRig(t, 3, 4)
	calls := stubWakeRead(r.e, func(int) (waitingReading, bool) {
		return waitingReading{waiting: 3, oldest: r.now, dropped: map[uint64]bool{1: true}}, true
	})
	woke := false
	deliver := func(string, map[string]string) error { woke = true; return nil }
	check(deliver, wakeCooldown)
	waitWakeRead(t, r.e)
	check(deliver, 0)
	for range 10 {
		check(deliver, wakeRecount)
		waitWakeRead(t, r.e)
	}
	if n := calls.Load(); n != 1 || woke {
		t.Fatalf("reads %d, woke %v: want 1 read over ten minutes and no wake", n, woke)
	}
}

// The same holds when every held message is from a dropped sender.
func TestBacklogAllDroppedNoStandsWhileTheHeldSetIsUnchanged(t *testing.T) {
	r, check := connectedRig(t, 3, 4)
	calls := stubWakeRead(r.e, func(int) (waitingReading, bool) {
		return waitingReading{waiting: 9, oldest: r.now, dropped: map[uint64]bool{1: true, 2: true, 3: true, 4: true}}, true
	})
	deliver := func(string, map[string]string) error { return nil }
	check(deliver, wakeCooldown)
	waitWakeRead(t, r.e)
	check(deliver, 0)
	for range 10 {
		check(deliver, wakeRecount)
		waitWakeRead(t, r.e)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("reads %d: want 1", n)
	}
}

// A changed held set is counted again after wakeRecount: a new arrival (the highest sequence rises) and an
// acknowledged message (the count falls) each re-check, and the wake fires once the count says over.
func TestBacklogChangedHeldSetRechecks(t *testing.T) {
	r, check := connectedRig(t, 3, 4)
	waiting := 3
	calls := stubWakeRead(r.e, func(int) (waitingReading, bool) {
		return waitingReading{waiting: waiting, oldest: r.now}, true
	})
	woke := 0
	deliver := func(string, map[string]string) error { woke++; return nil }
	step := func(d time.Duration) {
		check(deliver, d)
		waitWakeRead(t, r.e)
		check(deliver, 0)
	}
	step(wakeCooldown)
	step(wakeRecount)
	if n := calls.Load(); n != 1 || woke != 0 {
		t.Fatalf("reads %d, woke %d: want 1 and 0 with the held set unchanged", n, woke)
	}
	r.h.msgs[spool.Ref{Seq: 5}] = pending{msg: &dblAck{}, mail: render.Mail{Seq: 5, From: "c", Kind: kind.Task, At: r.now, Body: []byte("x")}}
	step(wakeRecount) // a new arrival: counted again, still no
	if n := calls.Load(); n != 2 || woke != 0 {
		t.Fatalf("reads %d, woke %d after an arrival: want 2 and 0", n, woke)
	}
	delete(r.h.msgs, spool.Ref{Seq: 2}) // one gone: the count of the set falls, same highest
	step(wakeRecount)
	if n := calls.Load(); n != 3 {
		t.Fatalf("reads %d after a removal: want 3", n)
	}
	waiting = 9
	r.h.msgs[spool.Ref{Seq: 6}] = pending{msg: &dblAck{}, mail: render.Mail{Seq: 6, From: "c", Kind: kind.Task, At: r.now, Body: []byte("x")}}
	step(wakeRecount)
	if woke != 1 {
		t.Fatalf("woke %d: want the wake once the count says over", woke)
	}
}

// WakeAge is time-based: a no given while the oldest waiting message was under the age stands only until it could pass
// it. The held set is unchanged, and the count is read again then and the wake fires.
func TestBacklogNoRechecksWhenTheAgeCouldHaveFlipped(t *testing.T) {
	r, check := connectedRig(t, 3, 2)
	r.e.cfg.WakeAge = 5 * time.Minute
	for ref, p := range r.h.msgs {
		p.mail.At = r.now.Add(-10 * time.Minute) // the held set looks old; the count's oldest is younger
		r.h.msgs[ref] = p
	}
	start := r.now
	calls := stubWakeRead(r.e, func(int) (waitingReading, bool) {
		return waitingReading{waiting: 2, oldest: start.Add(wakeCooldown - 4*time.Minute)}, true
	})
	woke := false
	deliver := func(string, map[string]string) error { woke = true; return nil }
	check(deliver, wakeCooldown)
	waitWakeRead(t, r.e)
	check(deliver, 0) // oldest is 4 minutes old: under the age, a no
	if woke || calls.Load() != 1 {
		t.Fatalf("woke %v, reads %d: want a no after one read", woke, calls.Load())
	}
	check(deliver, 30*time.Second)
	check(deliver, wakeRecount) // 1.5 minutes on: 5.5 minutes old, past the age: read again
	waitWakeRead(t, r.e)
	check(deliver, 0)
	if calls.Load() != 2 || !woke {
		t.Fatalf("reads %d, woke %v: want a second read and the wake once the oldest passed WakeAge", calls.Load(), woke)
	}
}

// The refused-sequences file is read once per inbox read, not once per message: a read of twenty messages opens it as
// often as a read of one.
func TestInboxReadParsesTheRefusedSeqsOncePerRead(t *testing.T) {
	f := newFixture(t, "a", "c")
	ctx := context.Background()
	cc := f.client(t, "c")
	send := func(n int) {
		for range n {
			if _, err := cc.Send(ctx, tdot("a"), "from c", 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	ep, err := Open(ctx, f.cfgState("a", t.TempDir(), false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	reads := func() int64 {
		before := refusedFileReads.Load()
		r, err := ep.readInbox(ctx, InboxQuery{OnlyNew: true})
		if err != nil || r.State.Waiting == 0 {
			t.Fatalf("read: %v, %+v", err, r.State)
		}
		return refusedFileReads.Load() - before
	}
	send(1)
	one := reads()
	send(19)
	if twenty := reads(); twenty != one {
		t.Fatalf("sequences file read %d times for twenty messages, %d for one: want the same", twenty, one)
	}
}
