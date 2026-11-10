package endpoint

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

func consumerState(t *testing.T, ep *Endpoint, name string) (ackFloor uint64, pending, ackPending int) {
	t.Helper()
	c, err := ep.js.Consumer(context.Background(), sap.InboxStream(name), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	i, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return i.AckFloor.Stream, int(i.NumPending), i.NumAckPending
}

// C2: a long refresh interval, so only the immediate watch of the lease can tell the earlier serve.
func bigRefresh(t *testing.T) {
	old := LeaseRefresh
	LeaseRefresh = time.Hour
	t.Cleanup(func() { LeaseRefresh = old })
}

// The earlier serve learns of a takeover within about a second, not a refresh interval.
func TestEarlierServeLearnsOfTakeoverAtOnce(t *testing.T) {
	bigRefresh(t)
	setSession(t, ours(t))
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "a")
	first, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- first.Run(ctx, (&events{}).deliver) }()
	second, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	start := time.Now()
	select {
	case err := <-runErr:
		var lost *LeaseLostError
		if !errors.As(err, &lost) || !lost.SameSession || lost.PID == "" {
			t.Fatalf("Run returned %v, want a same-session LeaseLostError naming the new holder", err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Fatalf("learned after %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the earlier serve did not learn of the takeover (refresh interval is an hour)")
	}
	first.Close()
}

// Mail the earlier serve holds for a hook is neither acked nor left to wait out the ack wait: it is nak'd, its waiting
// spool entries are removed, and the new serve receives each message exactly once.
func TestEarlierServeAcksNothingAfterTakeover(t *testing.T) {
	bigRefresh(t)
	setSession(t, ours(t))
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "a", "snd")
	first, oldEv, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil { // a hook has run: mail is held, unacked
		t.Fatal(err)
	}
	snd := f.client(t, "snd")
	const n = 3
	for i := 0; i < n; i++ {
		if _, err := snd.Send(context.Background(), "a", fmt.Sprintf("held-%d", i), 0); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "held", func() bool { return len(waiting(t, sp)) == n })

	second, err := Open(context.Background(), f.cfg("a")) // takes the lease, does not consume yet
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	until(t, "the earlier serve drops its held mail", func() bool { return len(waiting(t, sp)) == 0 })
	time.Sleep(3 * SpoolCheck)
	floor, pend, ackp := consumerState(t, second, "a")
	if floor != 0 || pend+ackp != n {
		t.Fatalf("ack floor %d, pending %d, ack pending %d: the earlier serve acked mail it never delivered", floor, pend, ackp)
	}
	if got := oldEv.all(); len(got) != 0 {
		t.Fatalf("the earlier serve delivered %q", got)
	}
	_ = first

	ev2 := &events{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = second.Run(ctx, ev2.deliver) }()
	until(t, "the new serve is delivered all of it", func() bool { return strings.Count(strings.Join(ev2.all(), "\n"), "held-") >= n })
	until(t, "and acks it", func() bool { return unread(t, second) == 0 })
	all := strings.Join(ev2.all(), "\n")
	for i := 0; i < n; i++ {
		if c := strings.Count(all, fmt.Sprintf("held-%d", i)); c != 1 {
			t.Fatalf("held-%d delivered %d times to the new serve", i, c)
		}
	}
}

// The ack gate is the primary lease too: once it is lost nothing of any stream is acked.
func TestMayAckFalseOnceOwnLeaseLost(t *testing.T) {
	f := newFixture(t, "a")
	ep, _ := f.run(t, "a")
	p := pending{}
	p.mail.Stream = ""
	if !ep.mayAck(p) {
		t.Fatal("must ack while the lease is held")
	}
	ep.leaseGone.Store(true)
	if ep.mayAck(p) {
		t.Fatal("acked after the own lease was lost")
	}
}

// The legacy lease is watched too: a takeover of it by another session stops the legacy reading within about a second.
func TestLegacyLeaseLossLearnedAtOnce(t *testing.T) {
	bigRefresh(t)
	stubHolderGone(t, func(string, string) bool { return false })
	f := newFixture(t, "old")
	nf := f.addNewFormAlias(t, "ag", "team", "new", f.e.Owner, "old", aliasOpts{})
	ep, _, _ := f.runNF(t, nf)
	if !ep.legacyReading() {
		t.Fatal("not reading the legacy inbox")
	}
	putLease(t, f, "old", leaseValue{Instance: "someone-else", Host: "h", PID: "77", Since: "x", Machine: "elsewhere"})
	until(t, "the legacy loss", func() bool { return !ep.legacyReading() })
}

var _ = spool.Spool{}
