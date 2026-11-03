package endpoint

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// wakingFixture is a hooked lane "a" (mail is held in its spool until drained) with a sender "b" and a reader of a's
// lease. Lease refreshes are an hour apart, so every lease write a test sees is the count's own.
func wakingFixture(t *testing.T) (*fixture, spool.Spool, *Endpoint, jetstream.KeyValue) {
	t.Helper()
	return wakingFixtureWith(t, 50*time.Millisecond)
}

// wakingFixtureWith is wakingFixture with the coalesce window set before the endpoint starts (the package variable is
// read by its goroutines, so it must not change afterwards).
func wakingFixtureWith(t *testing.T, coalesce time.Duration) (*fixture, spool.Spool, *Endpoint, jetstream.KeyValue) {
	t.Helper()
	oldR, oldW, oldC := LeaseRefresh, WakingWriteMin, wakingCoalesce
	LeaseRefresh, WakingWriteMin, wakingCoalesce = time.Hour, 400*time.Millisecond, coalesce
	t.Cleanup(func() { LeaseRefresh, WakingWriteMin, wakingCoalesce = oldR, oldW, oldC })
	f := newFixture(t, "a", "b")
	_, _, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, err := js.KeyValue(context.Background(), store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	return f, sp, f.client(t, "b"), kv
}

// leaseUntil waits for a's lease to satisfy cond and returns it.
func leaseUntil(t *testing.T, kv jetstream.KeyValue, what string, cond func(leaseValue) bool) leaseValue {
	t.Helper()
	var lv leaseValue
	until(t, what, func() bool { lv = readLease(t, kv, "a"); return cond(lv) })
	return lv
}

func drainAll(t *testing.T, sp spool.Spool) {
	t.Helper()
	for _, e := range waiting(t, sp) {
		if err := sp.Drained(e.Seq); err != nil {
			t.Fatal(err)
		}
	}
}

// Held mail counts when its kind wakes (blocker, question, task; none or an unknown kind is task) or it was sent urgent;
// fyi and result do not. The count is stamped, and it goes to zero, still stamped, once the mail is shown.
func TestWakingPendingCountsByKind(t *testing.T) {
	f, sp, b, kv := wakingFixture(t)
	ctx := context.Background()
	lv := leaseUntil(t, kv, "a fresh zero", func(lv leaseValue) bool { return lv.WakingPendingAt != "" })
	if lv.WakingPending != 0 {
		t.Fatalf("an empty inbox counts %d", lv.WakingPending)
	}
	for _, k := range kind.All { // blocker, question, task, result, fyi
		if _, err := b.SendWith(ctx, "a", "body "+string(k), SendOptions{Kind: k}); err != nil {
			t.Fatal(err)
		}
	}
	// Old sender: no kind header, and an unknown one. Both are task.
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	id := identity(t, f.creds["b"])
	for _, v := range []string{"", "urgent-ish"} {
		h, err := id.Sign(sap.For(f.e.Owner, "a").Mail, []byte("raw"+v), "b", time.Now(), "")
		if err != nil {
			t.Fatal(err)
		}
		m := nats.NewMsg(sap.For(f.e.Owner, "a").Mail)
		m.Data = []byte("raw" + v)
		m.Header.Set(sap.HeaderName, h.Encode())
		if v != "" {
			m.Header.Set(KindHeader, v)
		}
		if _, err := nc.RequestMsg(m, 2*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "all spooled", func() bool { return len(waiting(t, sp)) == 7 })
	lv = leaseUntil(t, kv, "5 waking (blocker, question, task, 2 old)", func(lv leaseValue) bool { return lv.WakingPending == 5 })
	if _, err := time.Parse(time.RFC3339, lv.WakingPendingAt); err != nil || !strings.HasSuffix(lv.WakingPendingAt, "Z") {
		t.Fatalf("waking_pending_at %q is not RFC3339 UTC", lv.WakingPendingAt)
	}
	// Urgent counts whatever its kind.
	if _, err := b.SendWith(ctx, "a", "now", SendOptions{Kind: kind.FYI, Urgent: true}); err != nil {
		t.Fatal(err)
	}
	leaseUntil(t, kv, "6 with an urgent fyi", func(lv leaseValue) bool { return lv.WakingPending == 6 })
	// A fyi alone changes nothing; the count after reading goes to zero, with the stamp kept.
	drainAll(t, sp)
	lv = leaseUntil(t, kv, "0 after the mail is shown", func(lv leaseValue) bool { return lv.WakingPending == 0 && lv.WakingPendingAt != "" })
	raw, _ := kv.Get(ctx, "a")
	if strings.Contains(string(raw.Value()), "waking_pending\"") {
		t.Fatalf("a zero count is written, not omitted: %s", raw.Value())
	}
	if !strings.Contains(string(raw.Value()), "waking_pending_at") {
		t.Fatalf("a zero count has no stamp: %s", raw.Value())
	}
}

// The count follows the mail as it is shown, one message at a time.
func TestWakingPendingFallsAsMailIsRead(t *testing.T) {
	_, sp, b, kv := wakingFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := b.SendWith(ctx, "a", "task", SendOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	leaseUntil(t, kv, "3", func(lv leaseValue) bool { return lv.WakingPending == 3 })
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 3 })
	w := waiting(t, sp)
	if err := sp.Drained(w[0].Seq); err != nil {
		t.Fatal(err)
	}
	leaseUntil(t, kv, "2", func(lv leaseValue) bool { return lv.WakingPending == 2 })
	drainAll(t, sp)
	leaseUntil(t, kv, "0", func(lv leaseValue) bool { return lv.WakingPending == 0 && lv.WakingPendingAt != "" })
}

// Mail delivered on the channel is acked at once, so it is never pending for long.
func TestWakingPendingChannelMailIsNotPending(t *testing.T) {
	oldR := LeaseRefresh
	LeaseRefresh = time.Hour
	t.Cleanup(func() { LeaseRefresh = oldR })
	f := newFixture(t, "a", "b")
	a, ev := f.run(t, "a")
	if _, err := f.client(t, "b").Send(context.Background(), "a", "hi", 0); err != nil {
		t.Fatal(err)
	}
	ev.waitFor(t, 1, 5*time.Second)
	until(t, "shown", func() bool { n, _ := a.waking.snapshot(); return n == 0 })
}

// Several quick arrivals are one extra lease write, not one each.
func TestWakingPendingWritesAreThrottled(t *testing.T) {
	// The burst must fit inside one coalesce window however slow the sends are, or it is two writes by design.
	_, _, b, kv := wakingFixtureWith(t, 2*time.Second)
	ctx := context.Background()
	leaseUntil(t, kv, "the first stamp", func(lv leaseValue) bool { return lv.WakingPendingAt != "" })
	time.Sleep(2 * WakingWriteMin) // quiet: the next write is the burst's
	en, _ := kv.Get(ctx, "a")
	base := en.Revision()
	for i := 0; i < 6; i++ {
		if _, err := b.SendWith(ctx, "a", "task", SendOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	leaseUntil(t, kv, "6", func(lv leaseValue) bool { return lv.WakingPending == 6 })
	time.Sleep(WakingWriteMin / 2)
	en, _ = kv.Get(ctx, "a")
	if got := en.Revision() - base; got != 1 {
		t.Fatalf("%d lease writes for one burst, want 1", got)
	}
	// A change right after is held back until WakingWriteMin has passed since that write, then lands.
	if _, err := b.SendWith(ctx, "a", "task", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	leaseUntil(t, kv, "7", func(lv leaseValue) bool { return lv.WakingPending == 7 })
	en, _ = kv.Get(ctx, "a")
	if got := en.Revision() - base; got != 2 {
		t.Fatalf("%d lease writes, want 2", got)
	}
}

// The periodic refresh restamps the count even when nothing changed, so a reader can tell a fresh zero from an old one.
func TestWakingPendingRefreshStamps(t *testing.T) {
	// The fake clock is installed before Open: the lease goroutine reads wakingClock, so it must not change under it.
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC).UnixNano())
	old := wakingClock
	wakingClock = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	t.Cleanup(func() { wakingClock = old })
	f := newFixture(t, "a")
	ep, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	kv, _ := js.KeyValue(context.Background(), store.LeaseBucket)
	if lv := readLease(t, kv, "a"); lv.WakingPendingAt != "" || lv.WakingPending != 0 {
		t.Fatalf("a count is claimed before the inbox has been received: %+v", lv)
	}
	ep.waking.start(0, 0, true, nil)
	if err := ep.refreshLease(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lv := readLease(t, kv, "a"); lv.WakingPendingAt != "2026-10-31T12:00:00Z" || lv.WakingPending != 0 {
		t.Fatalf("lease %+v", lv)
	}
	clock.Add(int64(30 * time.Second))
	if err := ep.refreshLease(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lv := readLease(t, kv, "a"); lv.WakingPendingAt != "2026-10-31T12:00:30Z" {
		t.Fatalf("a refresh did not restamp: %+v", lv)
	}
}

// The count is unknown (no stamp) until the backlog the consumer held at start has arrived, then known.
func TestWakingPendingUnknownUntilBacklogArrives(t *testing.T) {
	w := newWakingTracker()
	if _, ok := w.snapshot(); ok {
		t.Fatal("known before the consumer started")
	}
	w.start(2, 10, true, nil)
	if _, ok := w.snapshot(); ok {
		t.Fatal("known with a backlog of 2 and none received")
	}
	w.add(1, true)
	w.received(1)
	w.add(2, false)
	w.received(2)
	if n, ok := w.snapshot(); !ok || n != 1 {
		t.Fatalf("got %d %v, want 1 true", n, ok)
	}
}

// Elapsed time never makes an unarrived backlog known; the server saying nothing is outstanding does.
func TestWakingPendingUnknownPastAnyDelayUntilAccounted(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC).UnixNano())
	old := wakingClock
	wakingClock = func() time.Time { return time.Unix(0, now.Load()).UTC() }
	t.Cleanup(func() { wakingClock = old })
	var pending uint64
	var ackPending int
	first := uint64(1)
	w := newWakingTracker()
	w.start(3, 10, true, func(context.Context) (uint64, int, uint64, error) { return pending, ackPending, first, nil })
	pending, ackPending = 0, 3 // three unacked, not redelivered
	now.Add(int64(time.Hour))
	w.reconcile(context.Background())
	if _, ok := w.snapshot(); ok {
		t.Fatal("known an hour on with 3 unacked and none held")
	}
	pending = 1 // not yet delivered
	w.add(1, true)
	ackPending = 1
	w.reconcile(context.Background())
	if _, ok := w.snapshot(); ok {
		t.Fatal("known with mail still pending on the server")
	}
	pending = 0
	w.reconcile(context.Background())
	if n, ok := w.snapshot(); !ok || n != 1 {
		t.Fatalf("got %d %v, want 1 true", n, ok)
	}
}

// Mail the stream evicted before it was acked leaves the count.
func TestWakingPendingEvictedMailLeavesTheCount(t *testing.T) {
	first := uint64(1)
	w := newWakingTracker()
	w.start(0, 10, true, func(context.Context) (uint64, int, uint64, error) { return 0, 3, first, nil })
	for i := uint64(1); i <= 3; i++ {
		w.add(i, i != 2)
		w.received(i)
	}
	if n, _ := w.snapshot(); n != 2 {
		t.Fatalf("count %d, want 2", n)
	}
	first = 3 // 1 and 2 evicted
	w.reconcile(context.Background())
	if n, ok := w.snapshot(); !ok || n != 1 {
		t.Fatalf("got %d %v after eviction, want 1 true", n, ok)
	}
	w.mu.Lock()
	left := len(w.unread)
	w.mu.Unlock()
	if left != 1 {
		t.Fatalf("%d tracked, want 1", left)
	}
	select {
	case <-w.notify:
	default:
		t.Fatal("a change in the count did not ask for a lease write")
	}
}

// Real server: a backlog left unacked by an earlier consumer is not redelivered for the 30 s ack wait, and the lease
// stays unknown however long (on the fake clock) until the server shows it accounted for. An empty inbox is known at once.
func TestWakingPendingBacklogNotRedeliveredStaysUnknown(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC).UnixNano())
	old := wakingClock
	wakingClock = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	t.Cleanup(func() { wakingClock = old })
	oldR := LeaseRefresh
	LeaseRefresh = time.Hour
	t.Cleanup(func() { LeaseRefresh = oldR })
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	b := f.client(t, "b")
	for i := 0; i < 2; i++ {
		if _, err := b.SendWith(ctx, "a", "task", SendOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	cons, err := js.Consumer(ctx, sap.InboxStream("a"), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := cons.FetchNoWait(10) // delivered, left unacked: the previous session's
	if err != nil {
		t.Fatal(err)
	}
	var held []jetstream.Msg
	for m := range batch.Messages() {
		held = append(held, m)
	}
	if len(held) != 2 {
		t.Fatalf("fetched %d, want 2", len(held))
	}
	ep, _ := f.run(t, "a")
	until(t, "the consumer started", func() bool { ep.waking.mu.Lock(); defer ep.waking.mu.Unlock(); return ep.waking.started })
	kv, err := js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // far beyond any old warmup
		clock.Add(int64(time.Hour))
		if err := ep.refreshLease(ctx); err != nil {
			t.Fatal(err)
		}
		if lv := readLease(t, kv, "a"); lv.WakingPendingAt != "" || lv.WakingPending != 0 {
			t.Fatalf("a count is claimed with an unredelivered backlog: %+v", lv)
		}
	}
	for _, m := range held {
		if err := m.DoubleAck(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := ep.refreshLease(ctx); err != nil {
		t.Fatal(err)
	}
	if lv := readLease(t, kv, "a"); lv.WakingPendingAt == "" || lv.WakingPending != 0 {
		t.Fatalf("not known once the server shows nothing outstanding: %+v", lv)
	}
}

// A clean empty inbox is known (0) as soon as the consumer starts, with no refresh needed.
func TestWakingPendingEmptyInboxKnownAtStart(t *testing.T) {
	f := newFixture(t, "a")
	ep, _ := f.run(t, "a")
	until(t, "known", func() bool { n, ok := ep.waking.snapshot(); return ok && n == 0 })
}

// Real server: mail the inbox stream evicts before it is acked leaves the count at the next lease refresh.
func TestWakingPendingEvictionOnRealStream(t *testing.T) {
	oldR := LeaseRefresh
	LeaseRefresh = time.Hour
	t.Cleanup(func() { LeaseRefresh = oldR })
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	ep, _, sp := f.runSpooled(t, "a")
	// A hook has run, so mail is held unacked in the spool; otherwise it is delivered and acked after the settle
	// window, leaves the count, and how many are counted at any moment depends on timing.
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	b := f.client(t, "b")
	for i := 0; i < 3; i++ {
		if _, err := b.SendWith(ctx, "a", "task", SendOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "3 counted", func() bool { n, ok := ep.waking.snapshot(); return ok && n == 3 })
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	st, err := js.Stream(ctx, sap.InboxStream("a"))
	if err != nil {
		t.Fatal(err)
	}
	// The stream denies delete and purge, so evict as its own limits do: cap it at one message and publish another.
	cfg := st.CachedInfo().Config
	cfg.MaxMsgs = 1
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendWith(ctx, "a", "task", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	// The count is not waited on at 4: the lease write the arrival triggers reconciles at once, so 4 can pass unseen.
	// Refresh until only the surviving message is counted.
	until(t, "only the survivor counted", func() bool {
		if err := ep.refreshLease(ctx); err != nil {
			t.Fatal(err)
		}
		n, ok := ep.waking.snapshot()
		return ok && n == 1
	})
	if n, ok := ep.waking.snapshot(); !ok || n != 1 {
		t.Fatalf("got %d %v after eviction, want 1 true", n, ok)
	}
}

// A lease written before these fields still parses; the new fields do not appear in a lease that has no count, and a
// lease with fields this version does not know still parses.
func TestWakingPendingOldAndFutureLeasesParse(t *testing.T) {
	var lv leaseValue
	if err := json.Unmarshal([]byte(`{"instance_id":"i","host":"h","pid":"1","since":"x","mcp_seen":"y"}`), &lv); err != nil ||
		lv.WakingPending != 0 || lv.WakingPendingAt != "" || lv.MCPSeen != "y" {
		t.Fatalf("old lease: %+v %v", lv, err)
	}
	if err := json.Unmarshal([]byte(`{"instance_id":"i","waking_pending":3,"waking_pending_at":"2026-10-31T12:00:00Z","future":{"x":1}}`), &lv); err != nil ||
		lv.WakingPending != 3 || lv.WakingPendingAt == "" {
		t.Fatalf("new lease: %+v %v", lv, err)
	}
	empty, _ := json.Marshal(leaseValue{Instance: "i"})
	if strings.Contains(string(empty), "waking") {
		t.Fatalf("an empty lease carries the fields: %s", empty)
	}
	if len(empty) > 1024 || store.LeaseMaxBytes < 1<<20 {
		t.Fatalf("lease value size %d vs cap %d", len(empty), store.LeaseMaxBytes)
	}
}

// A redelivery of one startup message counts once, and a message sent after start never counts toward the backlog.
func TestWakingPendingStartupBacklogCountsDistinctMessages(t *testing.T) {
	w := newWakingTracker()
	w.start(2, 10, true, nil)
	w.received(1)
	w.received(1)  // redelivered
	w.received(11) // sent after start
	w.received(12)
	if _, ok := w.snapshot(); ok {
		t.Fatal("known with one of two startup messages received (a redelivery and later arrivals do not count)")
	}
	w.received(2)
	if _, ok := w.snapshot(); !ok {
		t.Fatal("not known with both startup messages received")
	}
	// Without the stream's last sequence no delivery counts; only the server's rule can make it known.
	n := newWakingTracker()
	n.start(1, 0, false, func(context.Context) (uint64, int, uint64, error) { return 0, 0, 1, nil })
	n.received(1)
	if _, ok := n.snapshot(); ok {
		t.Fatal("known from a delivery with the start sequence unread")
	}
	n.reconcile(context.Background())
	if _, ok := n.snapshot(); !ok {
		t.Fatal("not known once the server shows nothing outstanding")
	}
}

// Real server: an old waking message left unacked at start is still outstanding when a new one arrives. The new
// delivery must not make the count known (it would omit the old one); accounting for the old one does.
func TestWakingPendingNewArrivalDoesNotStandInForOldBacklog(t *testing.T) {
	oldR := LeaseRefresh
	LeaseRefresh = time.Hour
	t.Cleanup(func() { LeaseRefresh = oldR })
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	b := f.client(t, "b")
	if _, err := b.SendWith(ctx, "a", "old task", SendOptions{Kind: kind.Task}); err != nil {
		t.Fatal(err)
	}
	js, _ := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	cons, err := js.Consumer(ctx, sap.InboxStream("a"), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := cons.FetchNoWait(10) // delivered, unacked: the previous session's
	if err != nil {
		t.Fatal(err)
	}
	var held []jetstream.Msg
	for m := range batch.Messages() {
		held = append(held, m)
	}
	if len(held) != 1 {
		t.Fatalf("fetched %d, want 1", len(held))
	}
	ep, _, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil { // hooked: held mail stays unread until drained
		t.Fatal(err)
	}
	until(t, "the consumer started", func() bool { ep.waking.mu.Lock(); defer ep.waking.mu.Unlock(); return ep.waking.started })
	if _, err := b.SendWith(ctx, "a", "new task", SendOptions{Kind: kind.Task}); err != nil {
		t.Fatal(err)
	}
	until(t, "the new message is tracked", func() bool { ep.waking.mu.Lock(); defer ep.waking.mu.Unlock(); return len(ep.waking.unread) == 1 })
	time.Sleep(300 * time.Millisecond) // two messages in the inbox, one delivered: the old one is still in its ack wait
	if _, ok := ep.waking.snapshot(); ok {
		t.Fatal("known after a new arrival while the old message is outstanding")
	}
	kv, err := js.KeyValue(ctx, store.LeaseBucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := ep.refreshLease(ctx); err != nil {
		t.Fatal(err)
	}
	if lv := readLease(t, kv, "a"); lv.WakingPendingAt != "" {
		t.Fatalf("a count is claimed with the old message outstanding: %+v", lv)
	}
	if err := held[0].DoubleAck(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ep.refreshLease(ctx); err != nil {
		t.Fatal(err)
	}
	if lv := readLease(t, kv, "a"); lv.WakingPendingAt == "" || lv.WakingPending != 1 {
		t.Fatalf("not known (1) once the old message is accounted for: %+v", lv)
	}
}
