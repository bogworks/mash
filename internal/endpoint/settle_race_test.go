package endpoint

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/spool"
)

// A settle pass reads each held entry's drained state at its own instant. A hook that drains an urgent message first
// and then older mail can land between those reads: the pass saw the urgent entry held, then saw an older entry
// drained, and took the drain for one that had left the urgent message unshown. It must not ring that message again:
// the hook has by now shown it. The seam stands in for the hook's drain landing mid-pass, so the interleaving is exact.
func TestSettleDoesNotRingAnUrgentMessageTheHookJustDrained(t *testing.T) {
	var stage atomic.Int32 // 0 idle, 1 pre-drain the fyi in this pass, 2 drain the urgent in the pass after, 3 done
	var sp spool.Spool
	var fyiSeq, urgentSeq uint64
	testSettleSeam = func() {
		switch stage.Load() {
		case 1:
			if err := sp.Drained(spool.Ref{Seq: fyiSeq}); err != nil {
				t.Error(err)
			}
			stage.Store(2)
		case 2: // this pass's loop saw the fyi drained and the urgent entry held
			if err := sp.Drained(spool.Ref{Seq: urgentSeq}); err != nil {
				t.Error(err)
			}
			stage.Store(3)
		}
	}
	t.Cleanup(func() { testSettleSeam = nil }) // registered before the endpoint's, so it runs after the endpoint stops

	fakeStreamBytes(t, 0, 1<<40, nil)
	f := newFixture(t, "a", "b")
	f.idle = 20 * time.Second
	_, ev, sp2 := f.runSpooled(t, "a")
	sp = sp2
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	b := f.client(t, "b")
	ctx := context.Background()
	const fyi = 5
	for i := 1; i <= fyi; i++ {
		if _, err := b.SendWith(ctx, "a", "fyi "+strconv.Itoa(i), SendOptions{Kind: kind.FYI}); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "fyi spooled", func() bool { return len(waiting(t, sp)) == fyi })
	time.Sleep(time.Second)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("%d wakes for fyi mail alone, want none: %q", len(got), got)
	}
	seq, err := b.SendWith(ctx, "a", "the build is red", SendOptions{Kind: kind.Task, Urgent: true})
	if err != nil {
		t.Fatal(err)
	}
	urgentSeq, fyiSeq = seq, 1
	until(t, "urgent spooled", func() bool { return len(waiting(t, sp)) == fyi+1 })
	got := ev.waitFor(t, 1, 5*time.Second)
	time.Sleep(time.Second)
	if got = ev.all(); len(got) != 1 || !strings.HasPrefix(got[0], "urgent") {
		t.Fatalf("want exactly one urgent ring, got %d: %q", len(got), got)
	}

	stage.Store(1)
	until(t, "the seam drained both", func() bool { return stage.Load() == 3 })
	until(t, "settled", func() bool { return len(waiting(t, sp)) == fyi-1 })
	time.Sleep(time.Second)
	if got = ev.all(); len(got) != 1 {
		t.Fatalf("%d wakes after the urgent message was drained, want still 1: %q", len(got), got)
	}
}
