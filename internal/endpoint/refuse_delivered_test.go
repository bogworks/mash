package endpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// unknownSignerMsg stores one unknown-signer message in to's inbox.
func (f *fixture) unknownSignerMsg(t *testing.T, to string) {
	t.Helper()
	subj := taddr(to).Subjects().Mail
	xcreds, _ := f.e.Mint(t, taddr("x"))
	xh, _ := identity(t, xcreds).Sign(subj, []byte("unknown body"), tkey("x"), time.Now(), "")
	publishRaw(t, f.e.ConnAs(t, xcreds, tkey("x")), subj, xh.Encode(), "unknown body")
}

// What serve delivered under accept_unverified a shell shows with its label, whatever the shell's setting; what serve
// refused stays refused at a shell that accepts.
func TestShellShowsWhatServeDelivered(t *testing.T) {
	f := newFixture(t, "a", "b")
	state := t.TempDir()
	serveOn := f.cfgState("a", state, true)
	shellOff := f.cfgState("a", state, false)
	f.unknownSignerMsg(t, "a")
	_, ev, stop := serveCfg(t, serveOn)
	ev.waitFor(t, 1, 5*time.Second)
	dir, _ := SpoolDir(serveOn)
	if _, seqs := readRefusedSeqs(filepath.Join(dir, deliveredSeqsFile)); len(seqs) != 1 || seqs[0] != 1 {
		t.Fatalf("delivered record: %v", seqs)
	}
	stop()

	c, err := OpenClient(context.Background(), shellOff)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m, err := c.Read(context.Background(), 1)
	if err != nil || m.Trust != sap.TrustUnknownSigner || string(m.Body) != "unknown body" {
		t.Fatalf("read: %+v %v", m, err)
	}
	ms, err := c.Inbox(context.Background(), 20)
	if err != nil || len(ms) != 1 || ms[0].Trust != sap.TrustUnknownSigner {
		t.Fatalf("inbox: %+v %v", ms, err)
	}
	r, err := c.ReadInbox(context.Background(), InboxQuery{Limit: 20})
	if err != nil || len(r.Mail) != 1 || r.State.Total != 1 {
		t.Fatalf("ReadInbox: %d messages, total %d, %v", len(r.Mail), r.State.Total, err)
	}
}

func TestShellKeepsRefusedRefusedAndJudgesUnjudged(t *testing.T) {
	f := newFixture(t, "a", "b")
	state := t.TempDir()
	serveOff := f.cfgState("a", state, false)
	shellOn := f.cfgState("a", state, true)
	shellOff := f.cfgState("a", state, false)
	f.unknownSignerMsg(t, "a") // #1: serve refuses it
	_, _, stop := serveCfg(t, serveOff)
	waitRefused(t, serveOff, 1)
	stop()
	f.unknownSignerMsg(t, "a") // #2: no serve has judged it

	for _, tc := range []struct {
		cfg          config.Config
		show1, show2 bool
	}{{shellOn, false, true}, {shellOff, false, false}} {
		c, err := OpenClient(context.Background(), tc.cfg)
		if err != nil {
			t.Fatal(err)
		}
		for n, show := range map[uint64]bool{1: tc.show1, 2: tc.show2} {
			m, err := c.Read(context.Background(), n)
			var rf *RefusedMessageError
			if show && (err != nil || m.Trust != sap.TrustUnknownSigner) {
				t.Fatalf("accept=%v read #%d: %+v %v", tc.cfg.AcceptUnverified, n, m, err)
			}
			if !show && !errors.As(err, &rf) {
				t.Fatalf("accept=%v read #%d should be refused: %+v %v", tc.cfg.AcceptUnverified, n, m, err)
			}
		}
		c.Close()
	}
}

// A delivered record of another stream identity is ignored, and replaced when serve records.
func TestDeliveredSeqsOfAnotherStreamIgnored(t *testing.T) {
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	e := &Endpoint{cfg: cfg}
	dir, _ := SpoolDir(cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, deliveredSeqsFile)
	if err := writeRefusedSeqs(p, "inbox@old", []uint64{1, 2}); err != nil {
		t.Fatal(err)
	}
	if e.deliveredSeq(1, "inbox@new") || !e.deliveredSeq(1, "inbox@old") {
		t.Fatal("the record applies to its own stream only")
	}
	e.refusedStream.Store(new(string))
	id := "inbox@new"
	e.refusedStream.Store(&id)
	if err := e.recordDelivered(5); err != nil {
		t.Fatal(err)
	}
	if e.deliveredSeq(1, id) || e.deliveredSeq(1, "inbox@old") || !e.deliveredSeq(5, id) {
		t.Fatal("the other stream's sequences were not replaced")
	}
	// A damaged file means nothing is recorded.
	if err := os.WriteFile(p, []byte("garbage\n\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if e.deliveredSeq(5, id) {
		t.Fatal("damaged file applied")
	}
}

func TestDeliveredSeqsKeptWhileStreamHoldsThem(t *testing.T) {
	old := refusedSeqsKeep
	refusedSeqsKeep = 10
	t.Cleanup(func() { refusedSeqsKeep = old })
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	e := &Endpoint{cfg: cfg}
	id := "inbox@now"
	e.refusedStream.Store(&id)
	first := uint64(1)
	e.firstSeqFn = func(context.Context) (uint64, error) { return first, nil }
	const n = 30
	for i := uint64(1); i <= n; i++ {
		if err := e.recordDelivered(i); err != nil {
			t.Fatal(err)
		}
	}
	for i := uint64(1); i <= n; i++ {
		if !e.deliveredSeq(i, id) {
			t.Fatalf("sequence %d lost", i)
		}
	}
	first = 25
	for i := uint64(n + 1); i <= n+2; i++ {
		if err := e.recordDelivered(i); err != nil {
			t.Fatal(err)
		}
	}
	for i := uint64(1); i <= n+2; i++ {
		if want := i >= 25; e.deliveredSeq(i, id) != want {
			t.Fatalf("sequence %d delivered=%v, want %v", i, !want, want)
		}
	}
}

// codex H1: a message serve delivered under accept_unverified (recorded, ack lost) is redelivered to a serve restarted
// with accept_unverified off: it is delivered again, not refused and not recorded as refused.
func TestDeliveredRedeliveryNotRefusedAfterAcceptOff(t *testing.T) {
	f := newFixture(t, "a", "b")
	state := t.TempDir()
	on := f.cfgState("a", state, true)
	off := f.cfgState("a", state, false)
	f.unknownSignerMsg(t, "a")

	ctx := context.Background()
	ep1, err := Open(ctx, on)
	if err != nil {
		t.Fatal(err)
	}
	if err := ep1.SetSpool(spool.Spool{Dir: t.TempDir() + "/spool"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ep1.bindRefusals(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ep1.recordDelivered(1); err != nil { // delivered, the ack lost
		t.Fatal(err)
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

	_, ev, stop := serveCfg(t, off)
	defer stop()
	ev.waitFor(t, 1, 5*time.Second)
	if n := RefusedCount(off); n != 0 {
		t.Fatalf("refused count %d, want 0", n)
	}
	dir, _ := SpoolDir(off)
	if _, seqs := readRefusedSeqs(filepath.Join(dir, refusedSeqsFile)); len(seqs) != 0 {
		t.Fatalf("refused record: %v", seqs)
	}
}
