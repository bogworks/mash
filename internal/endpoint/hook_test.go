package endpoint

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// runSpooled is run with a spool set, as serve does.
func (f *fixture) runSpooled(t *testing.T, name string) (*Endpoint, *events, spool.Spool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg(name))
	if err != nil {
		t.Fatal(err)
	}
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := ep.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	return ep, ev, sp
}

// unread is the inbox consumer's not-yet-acked count, what a launcher's delivery health reads.
func unread(t *testing.T, ep *Endpoint) uint64 {
	t.Helper()
	c, err := ep.js.Consumer(context.Background(), sap.InboxStream(ep.cfg.Name), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	i, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return i.NumPending + uint64(i.NumAckPending)
}

func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
	}
}

func waiting(t *testing.T, sp spool.Spool) []spool.Entry {
	t.Helper()
	w, err := sp.Waiting()
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// The switch-over (lead's condition 1): before any hook has run, mail is the channel's, in full, and nothing is
// spooled; once a hook runs, mail is spooled and held unacked, with no channel event while the session's hooks are
// running, and only what was spooled is shown by the hook. The ack waits for the hook (condition 3).
func TestHookSwitchOverShowsEachMessageOnce(t *testing.T) {
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooled(t, "a")
	b := f.client(t, "b")
	if _, err := b.Send(context.Background(), "a", "before the hook", 0); err != nil {
		t.Fatal(err)
	}
	if got := ev.waitFor(t, 1, 5*time.Second); !strings.Contains(got[0], "> before the hook") {
		t.Fatalf("fallback: %q", got)
	}
	until(t, "the fallback acks", func() bool { return unread(t, a) == 0 })
	if w := waiting(t, sp); len(w) != 0 {
		t.Fatalf("the fallback spooled %d message(s)", len(w))
	}

	if err := sp.Touch(); err != nil { // a hook runs
		t.Fatal(err)
	}
	if _, err := b.Send(context.Background(), "a", "after the hook", 0); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	w := waiting(t, sp)
	if len(w) != 1 || string(w[0].Body) != "after the hook" || w[0].From != "b" || w[0].Trust != string(sap.TrustVerified) {
		t.Fatalf("spool %+v", w)
	}
	time.Sleep(3 * SpoolCheck)
	if n := unread(t, a); n != 1 {
		t.Fatalf("unread %d before the hook drained, want 1 (spooled is not seen)", n)
	}
	if err := sp.Drained(w[0].Seq); err != nil { // the hook shows it
		t.Fatal(err)
	}
	until(t, "the drained message is acked", func() bool { return unread(t, a) == 0 })
	until(t, "its tombstone is gone", func() bool { return !sp.IsDrained(w[0].Seq) })
	if got := ev.all(); len(got) != 1 {
		t.Fatalf("a channel event besides the fallback's: %q", got)
	}
}

// An idle session runs no hook: spooled mail left unshown for IdleWake wakes it, once, with the summary line alone;
// a second only after the backoff (see TestHookReWakesWithBackoff), and the next idle spell wakes it again. Mail a hook shows within IdleWake
// costs no wake at all.
func TestHookIdleWake(t *testing.T) {
	old := IdleWake
	// Long enough that a slow runner (macOS CI) cannot let IdleWake pass between two sends or before a drain: the test
	// asserts "no wake" windows measured against it.
	IdleWake = time.Second
	t.Cleanup(func() { IdleWake = old })
	f := newFixture(t, "a", "b")
	_, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	b := f.client(t, "b")
	send := func(text string) {
		t.Helper()
		if _, err := b.SendWith(context.Background(), "a", text, SendOptions{Kind: kind.Question}); err != nil {
			t.Fatal(err)
		}
	}
	drain := func() {
		t.Helper()
		for _, e := range waiting(t, sp) {
			if err := sp.Drained(e.Seq); err != nil {
				t.Fatal(err)
			}
		}
	}

	send("shown in time") // a busy session: the hook shows it before IdleWake
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	drain()
	time.Sleep(2 * IdleWake)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("woke a session whose hook showed the mail: %q", got)
	}

	send("one")
	send("two")
	if got := ev.waitFor(t, 1, 5*time.Second); got[0] != "2 messages from b (question)" {
		t.Fatalf("the wake %q", got)
	}
	send("three") // the same idle spell: no second wake within the first backoff (2x IdleWake)
	time.Sleep(IdleWake / 4)
	if got := ev.all(); len(got) != 1 {
		t.Fatalf("woke again within the backoff: %q", got)
	}
	drain() // the woken session's hook shows the mail
	until(t, "drained and acked", func() bool { return len(waiting(t, sp)) == 0 })
	send("four") // the next idle spell
	if got := ev.waitFor(t, 2, 5*time.Second); got[1] != "1 message from b (question)" {
		t.Fatalf("the next wake %q", got)
	}
}

// A message a hook showed before serve could ack it (serve restarted) is acked on redelivery, not spooled again.
func TestHookRedeliveryOfADrainedMessageIsAcked(t *testing.T) {
	f := newFixture(t, "a", "b")
	a, _, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	seq, err := f.client(t, "b").Send(context.Background(), "a", "once", 0)
	if err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	// What a crash between the hook's tombstone and serve's ack leaves: the tombstone, and the message unacked.
	if err := os.WriteFile(sp.Dir+"/drained/"+strconv.FormatUint(seq, 10), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	until(t, "the redelivered, already shown message is acked", func() bool { return unread(t, a) == 0 })
	// The server's ack is visible before serve has forgotten the message in the spool (ack, then spool.Acked), so
	// "nothing waiting" is polled, not read once: a message spooled again would stay, a forgotten one goes (#153).
	until(t, "the acked message is forgotten by the spool", func() bool { return len(waiting(t, sp)) == 0 })
}

// A full spool takes no more: the mail stays in NATS, unread (condition 4). Never dropped.
func TestHookFullSpoolLeavesMailInTheInbox(t *testing.T) {
	old := SpoolMaxBytes
	SpoolMaxBytes = 4
	t.Cleanup(func() { SpoolMaxBytes = old })
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client(t, "b").Send(context.Background(), "a", "too big for the spool", 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if w := waiting(t, sp); len(w) != 0 {
		t.Fatalf("spooled past the bound: %+v", w)
	}
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("events %q", got)
	}
	if n := unread(t, a); n != 1 {
		t.Fatalf("unread %d, want 1: the mail waits in NATS", n)
	}
}

// The hook's frame names the batch and, word for word with the instructions, what an unproven sender's mail is.
func TestHookFrameCarriesTheTrustNotes(t *testing.T) {
	instr := Instructions("a", "o", "")
	plain := HookFrame("a", []sap.Trust{sap.TrustVerified})
	if !strings.Contains(plain, "mash mail for a") || strings.Contains(plain, "not proven") || strings.Contains(plain, "replayed") {
		t.Fatalf("verified-only frame %q", plain)
	}
	for _, c := range []struct {
		trust sap.Trust
		note  string
	}{{sap.TrustClaimed, trustUnproven}, {sap.TrustUnknownSigner, trustUnproven}, {sap.TrustAbsent, trustUnproven}, {sap.TrustStale, trustStale}} {
		got := HookFrame("a", []sap.Trust{sap.TrustVerified, c.trust})
		if !strings.Contains(got, c.note) || !strings.Contains(instr, c.note) {
			t.Errorf("%s: the frame or the instructions lack %q:\n%s", c.trust, c.note, got)
		}
	}
}

// ackCounter is a jetstream.Msg that counts what is done to it.
type ackCounter struct {
	jetstream.Msg
	acks, progress, naks, terms int
}

func (a *ackCounter) Ack() error                       { a.acks++; return nil }
func (a *ackCounter) InProgress() error                { a.progress++; return nil }
func (a *ackCounter) Nak() error                       { a.naks++; return nil }
func (a *ackCounter) NakWithDelay(time.Duration) error { a.naks++; return nil }
func (a *ackCounter) Term() error                      { a.terms++; return nil }

// codex H1 on #21: a redelivery of a still-held message is never acked (no hook has shown it); it replaces the held
// handle. A duplicate of a message not held is acked as before.
func TestDedupeNeverAcksAHeldMessage(t *testing.T) {
	e := &Endpoint{}
	old := &ackCounter{}
	h := &held{msgs: map[uint64]pending{5: {mail: render.Mail{Seq: 5, Body: []byte("abc")}, msg: old, key: "k1"}}, bytes: 3}
	seen := newSeenSet(0)
	seen.mark(pending{mail: render.Mail{Seq: 5}, key: "k1"})
	seen.mark(pending{mail: render.Mail{Seq: 6}, key: "k2"})

	fresh := &ackCounter{}
	got := e.dedupe([]pending{{mail: render.Mail{Seq: 5}, msg: fresh, key: "k1"}}, seen, h)
	if len(got) != 0 || fresh.acks != 0 || old.acks != 0 {
		t.Fatalf("held redelivery: got %d shown, acks new=%d old=%d", len(got), fresh.acks, old.acks)
	}
	if h.msgs[5].msg != jetstream.Msg(fresh) || h.bytes != 3 || string(h.msgs[5].mail.Body) != "abc" {
		t.Fatalf("held entry not replaced in place: %+v bytes=%d", h.msgs[5], h.bytes)
	}

	dup := &ackCounter{}
	if got := e.dedupe([]pending{{mail: render.Mail{Seq: 6}, msg: dup, key: "k2"}}, seen, h); len(got) != 0 || dup.acks != 1 {
		t.Fatalf("shown duplicate: got %d shown, acks=%d", len(got), dup.acks)
	}

	nu := &ackCounter{}
	if got := e.dedupe([]pending{{mail: render.Mail{Seq: 7}, msg: nu, key: "k3"}}, seen, h); len(got) != 1 || nu.acks != 0 {
		t.Fatalf("new mail: got %d shown, acks=%d", len(got), nu.acks)
	}
}

// A nonce seen before is a new send when its stream sequence differs and it was stored more than a window after the
// one shown; inside the window it is a replay; with the same sequence it is always a redelivery.
func TestSeenSetWindowAndSequence(t *testing.T) {
	t0 := time.Unix(1000, 0)
	w := 2 * time.Minute
	s := newSeenSet(w)
	first := pending{key: "u\x00n", mail: render.Mail{Seq: 5, At: t0}}
	if s.dup(first) {
		t.Fatal("unseen is a dup")
	}
	s.mark(first)
	at := func(seq uint64, d time.Duration) pending {
		return pending{key: "u\x00n", mail: render.Mail{Seq: seq, At: t0.Add(d)}}
	}
	if !s.dup(at(5, time.Hour)) {
		t.Fatal("same sequence is a redelivery, however late")
	}
	if !s.dup(at(9, w-time.Second)) {
		t.Fatal("new sequence inside the window is a replay")
	}
	if s.dup(at(9, w)) {
		t.Fatal("new sequence after the window is a new send")
	}
	if s.dup(pending{mail: render.Mail{Seq: 9, At: t0}}) {
		t.Fatal("unsigned mail has no key and is never a dup")
	}
	for i := range 300 { // pruning keeps the set bounded
		s.mark(pending{key: fmt.Sprint("k", i), mail: render.Mail{Seq: uint64(100 + i), At: t0.Add(time.Duration(i) * time.Minute)}})
	}
	if len(s.m) > 100 {
		t.Fatalf("set holds %d entries, want old ones pruned", len(s.m))
	}
}

// A copy of signed mail (same nonce, same signed timestamp) is dropped for as long as its signature could be shown as
// verified, not only for the duplicate window; a "send --id" re-send, signed afresh, is shown.
func TestSeenSetDropsReplayedCopyUntilSignatureIsNotFresh(t *testing.T) {
	t0 := time.Unix(1000, 0)
	signed := t0.Format(time.RFC3339Nano)
	resigned := t0.Add(3 * time.Minute).Format(time.RFC3339Nano)
	s := newSeenSet(2 * time.Minute)
	s.mark(pending{key: "u\x00n", ts: signed, mail: render.Mail{Seq: 5, At: t0}})
	at := func(seq uint64, ts string, d time.Duration) pending {
		return pending{key: "u\x00n", ts: ts, mail: render.Mail{Seq: seq, At: t0.Add(d)}}
	}
	if !s.dup(at(9, signed, time.Minute)) {
		t.Fatal("copy at +1m is a replay")
	}
	if !s.dup(at(9, signed, 3*time.Minute)) {
		t.Fatal("exact copy at +3m, past the window but still fresh, must be dropped")
	}
	if s.dup(at(9, resigned, 3*time.Minute)) {
		t.Fatal("re-send with a new signed timestamp at +3m is a new send")
	}
	// At +6m the copy classifies as stale; it must still not be shown (and never as verified).
	if !s.dup(at(9, signed, 6*time.Minute)) {
		t.Fatal("exact copy at +6m must not be shown")
	}
}

// codex H1 on #185: a re-send replaces nothing; a copy of the original after it is still a replay while fresh.
func TestSeenSetKeepsEveryShownTimestampUntilNotFresh(t *testing.T) {
	t0 := time.Unix(1000, 0)
	a := t0.Format(time.RFC3339Nano)
	b := t0.Add(150 * time.Second).Format(time.RFC3339Nano)
	s := newSeenSet(2 * time.Minute)
	at := func(seq uint64, ts string, d time.Duration) pending {
		return pending{key: "u\x00n", ts: ts, mail: render.Mail{Seq: seq, At: t0.Add(d)}}
	}
	s.mark(at(5, a, 0))
	if s.dup(at(7, b, 150*time.Second)) {
		t.Fatal("re-send with a new timestamp is shown")
	}
	s.mark(at(7, b, 150*time.Second))
	if !s.dup(at(9, a, 285*time.Second)) { // past the re-send's window, within the original's signature age
		t.Fatal("copy of the original after a re-send, still fresh, must be dropped")
	}
	if !s.dup(at(9, b, 7*time.Minute)) {
		t.Fatal("copy of the re-send must be dropped")
	}
}

// codex M1 on #21: SetSpool clears the previous session's waiting files but keeps the drained tombstones.
func TestSetSpoolClearsStaleWaiting(t *testing.T) {
	f := newFixture(t, "a")
	e, err := Open(context.Background(), f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	sp := spool.Spool{Dir: t.TempDir()}
	if err := e.SetSpool(sp, ""); err != nil { // binds the spool to this stream
		t.Fatal(err)
	}
	if err := sp.Put(spool.Entry{Seq: 1, Body: []byte("old")}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Drained(2); err != nil {
		t.Fatal(err)
	}
	if err := e.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	if w, _ := sp.Waiting(); len(w) != 0 {
		t.Fatalf("stale waiting: %+v", w)
	}
	if !sp.IsDrained(2) {
		t.Fatal("tombstone lost")
	}
}

// codex H1 on #21 (round 2): a tombstone left by a previous inbox stream (deprovision, then provision: sequences restart
// at 1) must not ack new mail no hook has shown.
func TestHookReprovisionDoesNotAckNewMailByAnOldTombstone(t *testing.T) {
	f := newFixture(t, "a", "b")
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Bind("old-stream"); err != nil {
		t.Fatal(err)
	}
	if err := sp.Drained(1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = a.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; a.Close() })
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if seq, err := f.client(t, "b").Send(context.Background(), "a", "new mail", 0); err != nil || seq != 1 {
		t.Fatalf("send: seq %d, %v", seq, err)
	}
	until(t, "the message is spooled", func() bool { return len(waiting(t, sp)) == 1 })
	time.Sleep(3 * SpoolCheck)
	if n := unread(t, a); n != 1 {
		t.Fatalf("unread %d, want 1: an old stream's tombstone acked unseen mail", n)
	}
}

// A tombstone that survived a serve restart marks mail a hook already showed: before any hook has run in the new
// session it is acked, not shown on the channel again (codex H1 on #21, round 5).
func TestHookTombstoneSuppressesChannelDeliveryBeforeAHookRuns(t *testing.T) {
	f := newFixture(t, "a", "b")
	a, ev, sp := f.runSpooled(t, "a")
	if err := os.WriteFile(sp.Dir+"/drained/1", nil, 0o600); err != nil { // the first message of a new inbox is #1
		t.Fatal(err)
	}
	if seq, err := f.client(t, "b").Send(context.Background(), "a", "already shown", 0); err != nil || seq != 1 {
		t.Fatalf("send: seq %d, %v", seq, err)
	}
	until(t, "the shown message is acked", func() bool { return unread(t, a) == 0 })
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("shown again on the channel: %q", got)
	}
}

// Urgent mail wakes a hooked session at once (not after IdleWake), each urgent message once; an urgent mark on mail
// that is not from a verified sender counts for nothing.
func TestHookUrgentWakesAtOnce(t *testing.T) {
	f := newFixture(t, "a", "b")
	_, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	b := f.client(t, "b")
	if _, err := b.SendWith(context.Background(), "a", "can wait", SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	time.Sleep(3 * SpoolCheck)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("normal mail woke the session before IdleWake: %q", got)
	}
	if _, err := b.SendWith(context.Background(), "a", "cannot wait", SendOptions{Urgent: true}); err != nil {
		t.Fatal(err)
	}
	if got := ev.waitFor(t, 1, 5*time.Second); got[0] != "urgent, 2 messages from b (fyi, task)" {
		t.Fatalf("the urgent wake %q", got)
	}
	if w := waiting(t, sp); len(w) != 2 || !w[1].Urgent || w[0].Urgent {
		t.Fatalf("spool %+v", w)
	}
	time.Sleep(3 * SpoolCheck)
	if got := ev.all(); len(got) != 1 {
		t.Fatalf("the urgent message woke the session twice: %q", got)
	}

	// A claimed sender (an unverified header) with an urgent mark: spooled as normal mail, no wake.
	subj := sap.For(f.e.Owner, "a").Mail
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	id := identity(t, f.creds["b"])
	// A claimed header (no signature over the message), with an urgent mark that b's key did sign for it.
	claimed := sap.SenderHeader{V: 1, Account: id.Account, User: id.User, Name: "b"}
	u, err := id.SignUrgent(claimed)
	if err != nil || !sap.VerifyUrgent(u, claimed) {
		t.Fatalf("mark: %v", err)
	}
	m := nats.NewMsg(subj)
	m.Data = []byte("claimed urgent")
	m.Header.Set(sap.HeaderName, claimed.Encode())
	m.Header.Set(UrgentHeader, u)
	if _, err := nc.RequestMsg(m, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 3 })
	if w := waiting(t, sp); w[2].Urgent || w[2].Trust == string(sap.TrustVerified) {
		t.Fatalf("an unverified urgent mark counted: %+v", w[2])
	}
	time.Sleep(3 * SpoolCheck)
	if got := ev.all(); len(got) != 1 {
		t.Fatalf("an unverified urgent mark woke the session: %q", got)
	}
}

// A wake announces every urgent message, but a hook drains a bounded batch: an urgent message it leaves held rings
// again at once (codex H1 on #23).
func TestHookUrgentLeftHeldRingsAgain(t *testing.T) {
	f := newFixture(t, "a", "b")
	_, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	b := f.client(t, "b")
	for _, body := range []string{"one", "two"} {
		if _, err := b.SendWith(context.Background(), "a", body, SendOptions{Urgent: true}); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "both spooled", func() bool { return len(waiting(t, sp)) == 2 })
	ev.waitFor(t, 1, 5*time.Second)
	if err := sp.Drained(1); err != nil {
		t.Fatal(err)
	}
	if got := ev.waitFor(t, 2, 5*time.Second); len(got) < 2 {
		t.Fatalf("no second wake for the urgent message left held: %q", got)
	}
}

// The test-only before-ack failpoint (MASH_SERVE_FAILPOINT): a drained held message is not acked, and the failpoint
// fires once for it.
func TestFailpointBeforeAckHoldsTheAck(t *testing.T) {
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
	hits := make(chan uint64, 4)
	release := make(chan struct{})
	ep.failBeforeAck = func(seq uint64) { hits <- seq; <-release }
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { close(release); cancel(); <-done; ep.Close() })

	b, err := Open(ctx, f.cfg("b"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	// A hook has run: mail is spooled and held from now on.
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Send(ctx, "a", "held", 0); err != nil {
		t.Fatal(err)
	}
	until(t, "the message is spooled", func() bool { return len(waiting(t, sp)) == 1 })
	seq := waiting(t, sp)[0].Seq
	if err := sp.Drained(seq); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-hits:
		if got != seq {
			t.Fatalf("failpoint for #%d, want #%d", got, seq)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the failpoint did not fire")
	}
	time.Sleep(time.Second)
	if n := unread(t, ep); n != 1 {
		t.Fatalf("unread %d: a failpointed message must stay unacked", n)
	}
	if !sp.IsDrained(seq) {
		t.Fatal("tombstone gone: the message was acked")
	}
	if len(hits) != 0 {
		t.Fatal("the failpoint fired twice")
	}
}

// codex H1 on #121: a stream recreated after Open (a session that waited long for the lease) must not leave the spool
// bound to the old stream's identity: SetSpool binds to the stream as it is now.
func TestSetSpoolBindsToTheRecreatedStream(t *testing.T) {
	f := newFixture(t, "a")
	ctx := context.Background()
	ep, err := Open(ctx, f.cfg("a"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ep.Close)
	ajs, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	old, err := ajs.Stream(ctx, sap.InboxStream("a"))
	if err != nil {
		t.Fatal(err)
	}
	oldCreated := old.CachedInfo().Created
	// What e.inbox would hand back: the stream as an earlier read saw it.
	ep.pre.inbox, ep.pre.inboxSet = old, true
	time.Sleep(10 * time.Millisecond)
	if err := ajs.DeleteStream(ctx, sap.InboxStream("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := ajs.CreateStream(ctx, store.InboxConfig(f.e.Owner, "a")); err != nil {
		t.Fatal(err)
	}
	fresh, err := ajs.Stream(ctx, sap.InboxStream("a"))
	if err != nil {
		t.Fatal(err)
	}
	want := sap.InboxStream("a") + "@" + fresh.CachedInfo().Created.UTC().Format(time.RFC3339Nano)
	if !fresh.CachedInfo().Created.After(oldCreated) {
		t.Fatal("the stream was not recreated")
	}
	sp := spool.Spool{Dir: t.TempDir() + "/spool"}
	if err := ep.SetSpool(sp, ""); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(sp.Dir + "/.stream")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("spool bound to %q, want the recreated stream %q", got, want)
	}
}
