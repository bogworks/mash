package endpoint

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

// Issue #1399: with #1 unacknowledged and urgent #2 acknowledged out of order, the ack floor stays at 0, so the read
// still counts #2 as new urgent mail; the server cannot say it is acknowledged, but the read says some message above
// the floor is (AckedOutOfOrder), not Partial, and the waiting count stays the server's own. The control acknowledges
// #1 in order: the floor moves, nothing is out of order.
func TestInboxUrgentNotCountedWhenAckedOutOfOrder(t *testing.T) {
	t.Run("out of order", func(t *testing.T) { inboxAckCase(t, 2, 1, true) })
	t.Run("in order", func(t *testing.T) { inboxAckCase(t, 1, 1, false) })
}

func inboxAckCase(t *testing.T, ackSeq uint64, wantWaiting int, wantOOO bool) {
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	b := f.client(t, "b")
	if _, err := b.SendWith(ctx, tdot("a"), "one", SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendWith(ctx, tdot("a"), "two, urgent", SendOptions{Urgent: true}); err != nil {
		t.Fatal(err)
	}
	ajs := adminJS(t, f)
	cons, err := ajs.CreateOrUpdateConsumer(ctx, taddr("a").InboxStream(), store.ConsumerConfig())
	if err != nil {
		t.Fatal(err)
	}
	batch, err := cons.Fetch(2, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for m := range batch.Messages() {
		md, err := m.Metadata()
		if err != nil {
			t.Fatal(err)
		}
		n++
		if md.Sequence.Stream == ackSeq {
			if err := m.DoubleAck(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := batch.Error(); err != nil || n != 2 {
		t.Fatalf("fetched %d, err %v", n, err)
	}
	r, err := strict(t, f, "a").ReadInbox(ctx, InboxQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("read: State.Waiting=%d NewUrgent=%d Partial=%v AckedOutOfOrder=%v State.AckFloor=%d", r.State.Waiting, r.NewUrgent, r.Partial, r.AckedOutOfOrder, r.State.AckFloor)
	if r.State.Waiting != wantWaiting {
		t.Errorf("State.Waiting = %d, want %d", r.State.Waiting, wantWaiting)
	}
	if r.AckedOutOfOrder != wantOOO {
		t.Errorf("AckedOutOfOrder = %v, want %v", r.AckedOutOfOrder, wantOOO)
	}
	if r.Partial {
		t.Errorf("Partial is set: nothing was cut short")
	}
}
