package endpoint

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// A full table of failed lookups makes room by evicting its oldest entry: a flood of fake (name, key) pairs never
// stops the lookup of a real new sender, whose verified mail is then delivered.
func TestFailedLookupFloodDoesNotBlockARealNewSender(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	ep, ev, _ := serveCfg(t, cfg)
	subj := taddr("a").MailSubject()
	for i := 0; i < 2*maxFailedLookups; i++ {
		ep.reloadSender(sap.SenderHeader{V: 1, User: "UFAKE" + time.Duration(i).String(), Name: tkey("b")})
	}
	ep.mu.Lock()
	n := len(ep.failedLookups)
	bid := identity(t, f.creds["b"])
	// A real endpoint created now: its record is not in the cached directory.
	delete(ep.dir, tkey("b"))
	delete(ep.byUser, bid.User)
	ep.mu.Unlock()
	if n != maxFailedLookups {
		t.Fatalf("%d remembered lookups after the flood, want %d", n, maxFailedLookups)
	}
	hdr, _ := bid.Sign(subj, []byte("real body"), tkey("b"), time.Now(), "")
	publishRaw(t, f.e.ConnAs(t, f.creds["b"], tkey("b")), subj, hdr.Encode(), "real body")
	ev.waitFor(t, 1, 10*time.Second)
	if c := RefusedCount(cfg); c != 0 {
		t.Fatalf("refused count %d, want 0", c)
	}
}

// A refusal recorded with accept_unverified off stays refused when serve restarts with it on: a redelivery (its ack
// was lost) is acknowledged, not delivered to the session.
func TestRefusedRedeliveryNotDeliveredAfterAcceptOn(t *testing.T) {
	f := newFixture(t, "a", "b")
	state := t.TempDir()
	off := f.cfgState("a", state, false)
	subj := taddr("a").MailSubject()
	publishRaw(t, f.e.ConnAs(t, f.creds["b"], tkey("b")), subj, "", "absent body")

	ctx := context.Background()
	ep1, err := Open(ctx, off)
	if err != nil {
		t.Fatal(err)
	}
	if err := ep1.SetSpool(spool.Spool{Dir: t.TempDir() + "/spool"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ep1.recordRefusal(1); err != nil { // refused, the ack lost
		t.Fatal(err)
	}
	// Delivered once and not acked: what comes next is a redelivery.
	cons, err := ep1.js.Consumer(ctx, ep1.ident.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for m := range b.Messages() {
		_ = m.Nak()
	}
	ep1.Close()

	on := f.cfgState("a", state, true)
	ep2, err := Open(ctx, on)
	if err != nil {
		t.Fatal(err)
	}
	if err := ep2.SetSpool(spool.Spool{Dir: t.TempDir() + "/spool"}, ""); err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = ep2.Run(rctx, ev.deliver); close(done) }()
	defer func() { cancel(); <-done; ep2.Close() }()
	cons2, err := ep2.js.Consumer(ctx, ep2.ident.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	until(t, "the redelivery is acknowledged", func() bool {
		ci, err := cons2.Info(ctx)
		return err == nil && ci.AckFloor.Stream == 1
	})
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("a recorded-refused message reached the session: %v", got)
	}
	if n := RefusedCount(on); n != 1 {
		t.Fatalf("count %d, want 1", n)
	}
}

// The refusal state is bound by the consumer's start, not by SetSpool: with no spool set (SetSpool failed or was never
// called) a restarted serve with accept_unverified on still acknowledges a refused redelivery and delivers nothing.
func TestRefusedRedeliveryNotDeliveredWithoutSetSpool(t *testing.T) {
	f := newFixture(t, "a", "b")
	state := t.TempDir()
	off := f.cfgState("a", state, false)
	subj := taddr("a").MailSubject()
	publishRaw(t, f.e.ConnAs(t, f.creds["b"], tkey("b")), subj, "", "absent body")

	ctx := context.Background()
	ep1, err := Open(ctx, off)
	if err != nil {
		t.Fatal(err)
	}
	if err := ep1.SetSpool(spool.Spool{Dir: t.TempDir() + "/spool"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ep1.recordRefusal(1); err != nil { // refused, the ack lost
		t.Fatal(err)
	}
	// Delivered once and not acked: what comes next is a redelivery.
	cons, err := ep1.js.Consumer(ctx, ep1.ident.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for m := range b.Messages() {
		_ = m.Nak()
	}
	ep1.Close()

	on := f.cfgState("a", state, true)
	ep2, err := Open(ctx, on)
	if err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	if ep2.refusedStream.Load() != nil {
		t.Fatal("bound before serve started")
	}
	go func() { _ = ep2.Run(rctx, ev.deliver); close(done) }()
	defer func() { cancel(); <-done; ep2.Close() }()
	cons2, err := ep2.js.Consumer(ctx, ep2.ident.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	until(t, "the redelivery is acknowledged", func() bool {
		ci, err := cons2.Info(ctx)
		return err == nil && ci.AckFloor.Stream == 1
	})
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("a recorded-refused message reached the session: %v", got)
	}
	if n := RefusedCount(on); n != 1 {
		t.Fatalf("count %d, want 1", n)
	}
}

// When recording failed, losing the lease leaves the count alone (and nothing is taken back, ever).
func TestUnrecordedRefusalLeavesCountAlone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root")
	}
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	subj := taddr("a").MailSubject()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	dir, _ := SpoolDir(cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RefusedFile), []byte("5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ep.bindRefusals(ctx); err != nil { // serve binds before consuming; binding needs the directory writable
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RefusedFile), []byte("5\n"), 0o600); err != nil { // binding reset the count
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { // recording the refusal now fails
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	var hooked atomic.Bool
	ep.refusedHook = func() { hooked.Store(true); ep.leaseGone.Store(true) }
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, (&events{}).deliver); close(done) }()
	defer func() { cancel(); <-done; ep.Close() }()
	publishRaw(t, f.e.ConnAs(t, f.creds["b"], tkey("b")), subj, "", "absent body")
	until(t, "the refusal is attempted", hooked.Load)
	time.Sleep(500 * time.Millisecond)
	if n := RefusedCount(cfg); n != 5 {
		t.Fatalf("count %d after a failed recording and a lost lease, want 5", n)
	}
}

// #1069 through the consumer: a kill between the sequence write and the count write leaves the sequence recorded and
// the count low. The redelivery, acknowledged by the handler's refused-redelivery branch, repairs the count before the
// ack, and a further redelivery does not count it again.
func TestRefusedRedeliveryRepairsCountThroughConsumer(t *testing.T) {
	f := newFixture(t, "a", "b")
	state := t.TempDir()
	off := f.cfgState("a", state, false)
	subj := taddr("a").MailSubject()
	publishRaw(t, f.e.ConnAs(t, f.creds["b"], tkey("b")), subj, "", "absent body")

	ctx := context.Background()
	ep1, err := Open(ctx, off)
	if err != nil {
		t.Fatal(err)
	}
	if err := ep1.SetSpool(spool.Spool{Dir: t.TempDir() + "/spool"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ep1.recordRefusal(1); err != nil {
		t.Fatal(err)
	}
	// The kill: the sequence is recorded, the count write never happened.
	dir, err := SpoolDir(off)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RefusedFile), []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := RefusedCount(off); n != 0 {
		t.Fatalf("setup: count %d, want 0", n)
	}
	cons, err := ep1.js.Consumer(ctx, ep1.ident.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for m := range b.Messages() {
		_ = m.Nak()
	}
	ep1.Close()

	on := f.cfgState("a", state, true)
	ep2, err := Open(ctx, on)
	if err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = ep2.Run(rctx, ev.deliver); close(done) }()
	defer func() { cancel(); <-done; ep2.Close() }()
	cons2, err := ep2.js.Consumer(ctx, ep2.ident.InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	until(t, "the redelivery is acknowledged", func() bool {
		ci, err := cons2.Info(ctx)
		return err == nil && ci.AckFloor.Stream == 1
	})
	if n := RefusedCount(on); n != 1 {
		t.Fatalf("count after the redelivery %d, want 1", n)
	}
	// Another redelivery of the same sequence (a repeated ack loss) does not count again.
	if n, added, err := ep2.recordRefusalNew(1); err != nil || added || n != 1 {
		t.Fatalf("further redelivery: n=%d added=%v err=%v", n, added, err)
	}
	if err := ep2.reconcileRefusedCount(*ep2.refusedStream.Load()); err != nil {
		t.Fatal(err)
	}
	if n := RefusedCount(on); n != 1 {
		t.Fatalf("count after a further redelivery %d, want 1", n)
	}
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("a recorded-refused message reached the session: %v", got)
	}
}
