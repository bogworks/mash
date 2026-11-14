package endpoint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/ledger"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
)

func readLedger(t *testing.T, path string) ledger.State {
	t.Helper()
	st, err := ledger.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func ledgerLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n")
}

// A hook display then serve's ack: one ledger line, written and synced before the ack. With the ack held at the
// before-ack failpoint the line is already there and the message is still unacked; when the ack goes through, the
// ledger is unchanged, and writing the same sequence again changes nothing in the fold.
func TestLedgerLineIsWrittenBeforeTheAck(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := ep.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sp.Dir, ledger.File)
	hits := make(chan uint64, 4)
	release := make(chan struct{})
	ep.failBeforeAck = func(seq uint64) { hits <- seq; <-release }
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	var once bool
	releaseOnce := func() {
		if !once {
			once = true
			close(release)
		}
	}
	t.Cleanup(func() { releaseOnce(); cancel(); <-done; ep.Close() })

	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client(t, "b").Send(ctx, "a", "held", 0); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	seq := waiting(t, sp)[0].Seq
	if err := sp.Drained(spool.Ref{Seq: seq}); err != nil { // the hook showed it
		t.Fatal(err)
	}
	select {
	case <-hits:
	case <-time.After(5 * time.Second):
		t.Fatal("the ack was not reached")
	}
	// The ack is held: the ledger already has the line, the message is unacked.
	if n := unread(t, ep); n != 1 {
		t.Fatalf("unread %d: the ack must not have happened", n)
	}
	if n := ledgerLines(t, path); n != 1 {
		t.Fatalf("%d ledger lines before the ack, want 1", n)
	}
	st := readLedger(t, path)
	var gen string
	for g := range st.Gens {
		gen = g
	}
	if !strings.HasPrefix(gen, sap.InboxStream("a")+"@") {
		t.Fatalf("generation %q is not the stream identity the spool binds", gen)
	}
	if !st.Gens[gen].P.Has(seq) {
		t.Fatalf("#%d not presented in the ledger: %+v", seq, st.Gens[gen])
	}
	releaseOnce()
	until(t, "acked", func() bool { return unread(t, ep) == 0 })
	if n := ledgerLines(t, path); n != 1 {
		t.Fatalf("%d ledger lines after the ack, want still 1", n)
	}
	// A repeated pass (the ack had failed and the message came round again) writes the same sequence once more.
	ep.ledgerNote(ledger.Presented, spool.Ref{Seq: seq})
	if p, _, _, _ := readLedger(t, path).Total(); p != 1 {
		t.Fatalf("presented %d after a repeated write, want 1", p)
	}
}

// A ledger that cannot be written never holds an ack: the message is acked, the failure is logged once, and status says so.
func TestLedgerWriteFailureStillAcksAndIsReported(t *testing.T) {
	f := newFixture(t, "a", "b")
	ep, _, sp := f.runSpooled(t, "a")
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ep.useLedger(filepath.Join(blocker, ledger.File)) // a directory under a regular file: unwritable
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client(t, "b").Send(context.Background(), "a", "held", 0); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	if err := sp.Drained(spool.Ref{Seq: waiting(t, sp)[0].Seq}); err != nil {
		t.Fatal(err)
	}
	until(t, "the message is acked although the ledger failed", func() bool { return unread(t, ep) == 0 })
	line, ok := ep.LedgerLine(context.Background())
	if !ok || !strings.Contains(line, "LEDGER WRITE FAILED") || !strings.Contains(line, "mail is still acked") {
		t.Fatalf("status line does not report the failure: %q", line)
	}
}

// Dedupe records a duplicate, with the original when it is known; a duplicate's original that was never presented
// leaves it unaccounted.
func TestLedgerRecordsDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), ledger.File)
	e := &Endpoint{}
	e.useLedger(path)
	e.noteGen("", "g")
	seen := newSeenSet(time.Hour)
	h := &held{msgs: map[spool.Ref]pending{}}
	orig := pending{mail: render.Mail{Seq: 5}, key: "u\x00n"}
	seen.mark(orig)
	// the same signed message under another sequence, and a redelivery of its own sequence
	got := e.dedupe([]pending{{mail: render.Mail{Seq: 9}, msg: &ackCounter{}, key: "u\x00n"}, {mail: render.Mail{Seq: 5}, msg: &ackCounter{}, key: "u\x00n"}}, seen, h)
	if len(got) != 0 {
		t.Fatalf("shown %d", len(got))
	}
	g := readLedger(t, path).Gens["g"]
	if _, ok := g.Dups[9]; !ok || len(g.Dups[9]) != 1 || g.Dups[9][0] != 5 {
		t.Fatalf("duplicate of #5 not recorded for #9: %+v", g.Dups)
	}
	if _, ok := g.Dups[5]; !ok || len(g.Dups[5]) != 0 {
		t.Fatalf("redelivery of #5 should be recorded with no original: %+v", g.Dups)
	}
	if g.Accounted().Has(9) {
		t.Fatal("#9 is a duplicate of #5, which the ledger does not hold as presented")
	}
	e.ledgerNote(ledger.Presented, spool.Ref{Seq: 5})
	if !readLedger(t, path).Gens["g"].Accounted().Has(9) {
		t.Fatal("#9 should be accounted once #5 is presented")
	}
}

// A forged-sender drop is recorded refused, a channel hand-off as channel only, and neither counts as presented.
func TestLedgerRecordsRefusedAndChannel(t *testing.T) {
	f := newFixture(t, "a", "b")
	subj := sap.For(f.e.Owner, "a").Mail
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	id := identity(t, f.creds["b"])
	h, _ := id.Sign(subj, []byte("forged body"), "b", time.Now(), "")
	h.Sig = strings.Repeat("A", len(h.Sig))
	publishRaw(t, nc, subj, h.Encode(), "forged body") // #1
	publishRaw(t, nc, subj, "", "absent body")         // #2: shown by the channel

	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ledger.File)
	ep.useLedger(path)
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	ev.waitFor(t, 1, 5*time.Second)
	until(t, "both recorded", func() bool {
		var n uint64
		for _, g := range readLedger(t, path).Gens {
			n += g.R.Count() + g.C.Count()
		}
		return n == 2
	})
	for _, g := range readLedger(t, path).Gens {
		if g.P.Count() != 0 {
			t.Fatalf("a message was recorded as presented with no hook and no read: %+v", g.P)
		}
		if !g.R.Has(1) || !g.C.Has(2) {
			t.Fatalf("refused %v channel %v, want #1 refused and #2 channel", g.R, g.C)
		}
	}
	line, _ := ep.LedgerLine(context.Background())
	if !strings.Contains(line, "0 presented, 1 refused, 0 duplicate, 1 channel-acked (not proven)") {
		t.Fatalf("status line: %q", line)
	}
}
