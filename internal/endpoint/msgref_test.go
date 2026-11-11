package endpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/spool"
)

func TestParseAndFormatMsgRef(t *testing.T) {
	for _, c := range []struct {
		in   string
		want MsgRef
		text string
	}{
		{"5", MsgRef{Seq: 5}, "5"},
		{"#5", MsgRef{Seq: 5}, "5"},
		{" 12 ", MsgRef{Seq: 12}, "12"},
		{"L5", MsgRef{Stream: LegacyStream, Seq: 5}, "L5"},
		{"#L5", MsgRef{Stream: LegacyStream, Seq: 5}, "L5"},
	} {
		got, err := ParseMsgRef(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseMsgRef(%q) = %+v, %v", c.in, got, err)
			continue
		}
		if f := FormatMsgRef(got); f != c.text {
			t.Errorf("FormatMsgRef(%+v) = %q, want %q", got, f, c.text)
		}
	}
	for _, bad := range []string{"", "#", "L", "0", "L0", "-1", "5x", "l5", "LL5", "1.5", "99999999999999999999999"} {
		if r, err := ParseMsgRef(bad); err == nil {
			t.Errorf("ParseMsgRef(%q) = %+v, want an error", bad, r)
		}
	}
	if f := FormatMsgRef(MsgRef{Stream: "other", Seq: 3}); f != "other:3" {
		t.Errorf("other stream: %q", f)
	}
}

// With no second stream configured (none is, yet) #Ln is an error the model can read; #n is as before.
func TestResolveRefNoLegacyStream(t *testing.T) {
	e := &Endpoint{}
	if r, err := e.ResolveRef("7"); err != nil || r != (MsgRef{Seq: 7}) {
		t.Fatalf("%+v %v", r, err)
	}
	_, err := e.ResolveRef("L7")
	if !errors.Is(err, ErrNoLegacyStream) || err.Error() != "mash: L7: no legacy stream on this endpoint" {
		t.Fatalf("%v", err)
	}
	if _, err := e.ResolveRef("x"); err == nil || errors.Is(err, ErrNoLegacyStream) {
		t.Fatalf("%v", err)
	}
}

func legacyMail(seq uint64) render.Mail {
	m := gMail(seq, "legacy")
	m.Stream = LegacyStream
	return m
}

// The seen set keeps (stream, sequence): the legacy stream's #5 is not a redelivery of the own stream's #5.
func TestSeenSetKeysByStream(t *testing.T) {
	s := newSeenSet(time.Minute)
	s.mark(pending{key: "u\x00a", mail: gMail(5, "")})
	if !s.dup(pending{key: "u\x00b", mail: gMail(5, "")}) {
		t.Fatal("same stream, same sequence: a redelivery")
	}
	if s.dup(pending{key: "u\x00c", mail: legacyMail(5)}) {
		t.Fatal("the legacy stream's #5 was taken for the own stream's")
	}
	s.mark(pending{key: "u\x00c", mail: legacyMail(5)})
	if !s.dup(pending{key: "u\x00d", mail: legacyMail(5)}) || gSeen(s) != 2 {
		t.Fatalf("legacy redelivery not recognised, set %d", gSeen(s))
	}
	// The same nonce re-seen at the same sequence of another stream is a replay inside the window, not a redelivery.
	if !s.dup(pending{key: "u\x00a", mail: legacyMail(5)}) {
		t.Fatal("a nonce shown in the own stream is a replay in the legacy stream inside the window")
	}
}

// The ack floor belongs to the primary consumer: it prunes primary refs and leaves another stream's.
func TestSeenSetPruneLeavesOtherStreams(t *testing.T) {
	s := newSeenSet(time.Minute)
	s.pruneAt, s.pruneEvery = 1, 0
	s.ackFloors = map[string]func() (uint64, bool){"": func() (uint64, bool) { return 10, true }}
	s.mark(pending{key: "u\x00a", mail: gMail(3, "")})
	s.mark(pending{key: "u\x00b", mail: legacyMail(3)})
	if _, ok := s.seqs[spool.Ref{Seq: 3}]; ok {
		t.Fatal("the own stream's #3 is below the floor and should be pruned")
	}
	if _, ok := s.seqs[spool.Ref{Stream: LegacyStream, Seq: 3}]; !ok {
		t.Fatal("the legacy stream's #3 was pruned by the primary floor")
	}
}

func TestWakingTrackerKeysByStream(t *testing.T) {
	w := newWakingTracker()
	w.start(0, 0, true, nil)
	own, old := spool.Ref{Seq: 5}, spool.Ref{Stream: LegacyStream, Seq: 5}
	w.add(own, true)
	w.add(old, true)
	if n, _ := w.snapshot(); n != 2 {
		t.Fatalf("two streams' #5 count %d, want 2", n)
	}
	w.listed(own)
	if n, _ := w.snapshot(); n != 1 {
		t.Fatalf("listed one: %d", n)
	}
	w.done(old)
	if n, _ := w.snapshot(); n != 0 || len(w.unread) != 1 {
		t.Fatalf("done the other: count %d, unread %d", n, len(w.unread))
	}
	w.done(own)
	if len(w.unread) != 0 {
		t.Fatal("unread not empty")
	}
}

// Startup accounting counts each (stream, sequence) once, and only for a stream whose last sequence is known.
func TestWakingBacklogByStream(t *testing.T) {
	w := newWakingTracker()
	w.start(2, 10, true, nil)
	w.received(spool.Ref{Seq: 4})
	w.received(spool.Ref{Stream: LegacyStream, Seq: 4}) // no last for this stream: not counted
	if _, ok := w.snapshot(); ok {
		t.Fatal("an unknown stream's delivery stood in for the backlog")
	}
	w.received(spool.Ref{Seq: 5})
	if _, ok := w.snapshot(); !ok {
		t.Fatal("two distinct primary messages account for the backlog")
	}
}

// reconcile drops only the primary stream's evicted sequences.
func TestWakingReconcileEvictsOnlyThePrimaryStream(t *testing.T) {
	w := newWakingTracker()
	w.start(0, 0, true, func(context.Context) (uint64, int, uint64, error) { return 0, 0, 10, nil })
	w.add(spool.Ref{Seq: 3}, true)
	w.add(spool.Ref{Stream: LegacyStream, Seq: 3}, true)
	w.reconcile(nil)
	if n, _ := w.snapshot(); n != 1 || len(w.unread) != 1 {
		t.Fatalf("count %d unread %d", n, len(w.unread))
	}
	if _, ok := w.unread[spool.Ref{Stream: LegacyStream, Seq: 3}]; !ok {
		t.Fatal("the legacy message was evicted by the primary stream's first sequence")
	}
}

// The batch map, the spool and the ack: both streams' #5 are held and spooled side by side, and draining one leaves
// the other.
func TestSpoolBatchKeepsStreamsApart(t *testing.T) {
	e, sp := gEndpoint(t)
	h := gHeld()
	seen := newSeenSet(0)
	own, old := &dblAck{}, &dblAck{}
	e.spoolBatch(nil, []pending{
		{mail: gMail(5, "own"), msg: own, key: "u\x00a"},
		{mail: legacyMail(5), msg: old, key: "u\x00b"},
	}, h, seen)
	if got := gFiles(t, sp.Dir); !reflect.DeepEqual(got, []string{"5.json", "legacy@5.json"}) {
		t.Fatalf("spool files %v", got)
	}
	if len(h.msgs) != 2 {
		t.Fatalf("held %d, want 2", len(h.msgs))
	}
	// A redelivery of the legacy #5 replaces only the legacy handle.
	fresh := &dblAck{}
	if got := e.dedupe([]pending{{mail: legacyMail(5), msg: fresh, key: "u\x00b"}}, seen, h); len(got) != 0 {
		t.Fatalf("shown %d", len(got))
	}
	if h.msgs[spool.Ref{Stream: LegacyStream, Seq: 5}].msg != any(fresh) || h.msgs[spool.Ref{Seq: 5}].msg != any(own) {
		t.Fatal("redelivery replaced the wrong handle")
	}
	// A hook drains the legacy #5: only it is acked and forgotten.
	if err := sp.Drained(spool.Ref{Stream: LegacyStream, Seq: 5}); err != nil {
		t.Fatal(err)
	}
	e.settleHeld(nil, h)
	if fresh.dacks != 1 || own.dacks != 0 || len(h.msgs) != 1 {
		t.Fatalf("dacks legacy=%d own=%d held=%d", fresh.dacks, own.dacks, len(h.msgs))
	}
	if _, ok := h.msgs[spool.Ref{Seq: 5}]; !ok {
		t.Fatal("the own stream's #5 was forgotten")
	}
	if got := gFiles(t, sp.Dir); !reflect.DeepEqual(got, []string{"5.json"}) {
		t.Fatalf("spool files after the ack %v", got)
	}
	// A tombstone for the own #5 does not ack a legacy #5 delivery: it is spooled.
	if err := sp.Drained(spool.Ref{Seq: 6}); err != nil {
		t.Fatal(err)
	}
	e.spoolBatch(nil, []pending{{mail: legacyMail(6), msg: &dblAck{}, key: "u\x00c"}}, h, seen)
	if _, ok := h.msgs[spool.Ref{Stream: LegacyStream, Seq: 6}]; !ok {
		t.Fatal("a tombstone of the own stream's #6 acked the legacy stream's #6")
	}
	if _, err := os.Stat(filepath.Join(sp.Dir, "legacy@6.json")); err != nil {
		t.Fatal(err)
	}
}

// Entries and mails keep the stream through the spool.
func TestEntryCarriesTheStream(t *testing.T) {
	m := legacyMail(4)
	en := EntryOf(m)
	if en.Stream != LegacyStream || en.Ref() != (spool.Ref{Stream: LegacyStream, Seq: 4}) {
		t.Fatalf("%+v", en)
	}
	if got := MailOf(en); got.Stream != LegacyStream || refOf(got) != en.Ref() {
		t.Fatalf("%+v", got)
	}
}
