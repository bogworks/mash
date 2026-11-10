package endpoint

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/spool"
)

// The one place numbering_golden_test.go touches the key type of the held map, the spool, the seen set and the
// waking tracker: with (stream, sequence) keys it builds primary-stream refs.

func gHeld() *held {
	return &held{msgs: map[spool.Ref]pending{}, at: map[spool.Ref]time.Time{}, rung: map[spool.Ref]bool{}, fails: map[spool.Ref]int{}}
}
func gHeldHas(h *held, seq uint64) bool { _, ok := h.msgs[spool.Ref{Seq: seq}]; return ok }
func gHeldMsg(h *held, seq uint64) jetstream.Msg {
	return h.msgs[spool.Ref{Seq: seq}].msg
}
func gDrain(t *testing.T, s spool.Spool, seqs ...uint64) {
	t.Helper()
	for _, q := range seqs {
		if err := s.Drained(spool.Ref{Seq: q}); err != nil {
			t.Fatal(err)
		}
	}
}
func gSpoolHas(s spool.Spool, seq uint64) bool { return s.Has(spool.Ref{Seq: seq}) }
func gSpoolDrained(s spool.Spool, seq uint64) bool {
	return s.IsDrained(spool.Ref{Seq: seq})
}
func gSeen(s *seenSet) int                          { return len(s.seqs) }
func gAdd(w *wakingTracker, seq uint64, wakes bool) { w.add(spool.Ref{Seq: seq}, wakes) }
func gDone(w *wakingTracker, seq uint64)            { w.done(spool.Ref{Seq: seq}) }
func gListedW(w *wakingTracker, seq uint64)         { w.listed(spool.Ref{Seq: seq}) }
func gReceived(w *wakingTracker, seq uint64)        { w.received(spool.Ref{Seq: seq}) }
