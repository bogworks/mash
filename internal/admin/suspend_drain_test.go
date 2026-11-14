package admin_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// TestSuspendDrainLosesNothing publishes concurrently with Suspend, then drains the inbox: every acked publish is
// stored, none is acked after Suspend returned, and the state's LastSeq is exactly the last acked sequence.
func TestSuspendDrainLosesNothing(t *testing.T) {
	e, a, js := setup(t)
	ctx := context.Background()
	provisionFor(t, e, a, "alpha")
	const subj = "agents.mail.cc.o.alpha"

	var (
		acked, refused atomic.Int64
		maxSeq         atomic.Uint64
		stop           = make(chan struct{})
		done           sync.WaitGroup
	)
	done.Add(1)
	go func() {
		defer done.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			pctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			ack, err := js.Publish(pctx, subj, []byte("m"))
			cancel()
			if err != nil {
				refused.Add(1)
				continue
			}
			acked.Add(1)
			maxSeq.Store(ack.Sequence)
		}
	}()

	for acked.Load() < 50 {
		time.Sleep(time.Millisecond)
	}
	s, err := a.Suspend(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	refusedAtSuspend := refused.Load()
	got, err := a.Suspended(ctx, "alpha")
	if err != nil || !got.Suspended || got.LastSeq != s.LastSeq {
		t.Fatalf("Suspended: %+v %v, Suspend gave %+v", got, err, s)
	}
	time.Sleep(300 * time.Millisecond)
	close(stop)
	done.Wait()

	// No publish may be acked past the sequence Suspend reported. (Counting acks seen after Suspend returned is racy: a
	// publish acked just before the park can be counted by the sender goroutine just after it.)
	if m := maxSeq.Load(); m > s.LastSeq {
		t.Fatalf("a publish was acked at seq %d, past Suspend's last seq %d", m, s.LastSeq)
	}
	if refused.Load() == refusedAtSuspend {
		t.Fatal("sender saw no refused publish after Suspend")
	}
	if s.LastSeq != maxSeq.Load() || s.LastSeq != uint64(acked.Load()) {
		t.Fatalf("LastSeq %d, highest acked seq %d, acked %d", s.LastSeq, maxSeq.Load(), acked.Load())
	}

	// Drain through the durable consumer.
	stream, err := js.Stream(ctx, sap.InboxStream("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	cons, err := stream.Consumer(ctx, store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	var read uint64
	deadline := time.Now().Add(20 * time.Second)
	for read < s.LastSeq && time.Now().Before(deadline) {
		b, err := cons.Fetch(100, jetstream.FetchMaxWait(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		for m := range b.Messages() {
			md, err := m.Metadata()
			if err != nil {
				t.Fatal(err)
			}
			if md.Sequence.Stream != read+1 {
				t.Fatalf("stream seq %d after %d reads: gap or duplicate", md.Sequence.Stream, read)
			}
			if err := m.DoubleAck(ctx); err != nil {
				t.Fatal(err)
			}
			read++
		}
	}
	if read != s.LastSeq {
		t.Fatalf("read %d messages, want %d", read, s.LastSeq)
	}
	// Nothing lies beyond LastSeq.
	b, err := cons.Fetch(1, jetstream.FetchMaxWait(500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	for m := range b.Messages() {
		md, _ := m.Metadata()
		t.Fatalf("message beyond LastSeq: stream seq %d", md.Sequence.Stream)
	}
	if end, err := a.Suspended(ctx, "alpha"); err != nil || end.LastSeq != s.LastSeq {
		t.Fatalf("final state %+v %v", end, err)
	}
	if err := a.Deprovision(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
}
