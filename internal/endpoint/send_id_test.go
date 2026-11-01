package endpoint

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
	"github.com/nats-io/nats.go/jetstream"
)

// shortenWindow sets name's inbox stream duplicate window to f.dup, so a test does not wait out two minutes.
func (f *fixture) shortenWindow(t *testing.T, name string) {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.InboxConfig(f.e.Owner, name)
	cfg.Duplicates = f.dup
	if _, err := js.UpdateStream(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}

// A send --id repeated after the stream's duplicate window is a new message: the stream stores it, and the running
// session shows it. A re-run inside the window still lands once.
func TestSendIDRepeatedAfterTheWindowIsShownAgain(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.dup = time.Second
	f.shortenWindow(t, "a")
	b := f.client(t, "b")
	_, ev := f.run(t, "a")
	ctx := context.Background()

	first, err := b.SendDetailed(ctx, "a", "deploy now", SendOptions{ID: "K"})
	if err != nil || first.Duplicate {
		t.Fatalf("first send: %+v %v", first, err)
	}
	again, err := b.SendDetailed(ctx, "a", "deploy now", SendOptions{ID: "K"})
	if err != nil || !again.Duplicate || again.Seq != first.Seq {
		t.Fatalf("re-run inside the window: %+v %v, want a duplicate of #%d", again, err, first.Seq)
	}
	ev.waitFor(t, 1, 10*time.Second)

	time.Sleep(2500 * time.Millisecond) // past the 1 s window
	later, err := b.SendDetailed(ctx, "a", "deploy now", SendOptions{ID: "K"})
	if err != nil || later.Duplicate || later.Seq == first.Seq {
		t.Fatalf("send after the window: %+v %v, want a new message", later, err)
	}
	got := ev.waitFor(t, 2, 10*time.Second)
	time.Sleep(500 * time.Millisecond)
	if got = ev.all(); len(got) != 2 {
		t.Fatalf("session showed %d events, want 2 (one per stored message): %q", len(got), got)
	}
	if got := inboxBodies(t, b); len(got) != 0 {
		t.Fatalf("b's inbox: %q", got)
	}
}

func inboxBodies(t *testing.T, ep *Endpoint) []string {
	t.Helper()
	ms, err := ep.Inbox(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range ms {
		out = append(out, string(m.Body))
	}
	return out
}

// (a) A send re-run with the same id lands once; the second reports the original sequence and a duplicate.
func TestSendIDTwiceLandsOnce(t *testing.T) {
	f := newFixture(t, "a", "b")
	a, b := f.client(t, "a"), f.client(t, "b")
	ctx := context.Background()
	first, err := b.SendDetailed(ctx, "a", "once", SendOptions{ID: "deploy-42"})
	if err != nil || first.Duplicate {
		t.Fatalf("first send: %+v %v", first, err)
	}
	second, err := b.SendDetailed(ctx, "a", "once", SendOptions{ID: "deploy-42"})
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	if !second.Duplicate || second.Seq != first.Seq {
		t.Fatalf("second send: %+v, want duplicate of #%d", second, first.Seq)
	}
	if got := inboxBodies(t, a); len(got) != 1 {
		t.Fatalf("inbox: %q, want exactly one message", got)
	}
}

// (b) The same key from two senders, or to two recipients, is two messages.
func TestSendIDDoesNotCollideAcrossSendersOrRecipients(t *testing.T) {
	f := newFixture(t, "a", "b", "c", "d")
	a, b, c, d := f.client(t, "a"), f.client(t, "b"), f.client(t, "c"), f.client(t, "d")
	ctx := context.Background()
	for _, s := range []struct {
		from *Endpoint
		to   string
	}{{b, "a"}, {c, "a"}, {b, "d"}, {c, "d"}} {
		r, err := s.from.SendDetailed(ctx, s.to, "same key", SendOptions{ID: "k1"})
		if err != nil || r.Duplicate {
			t.Fatalf("send to %s: %+v %v", s.to, r, err)
		}
	}
	if got := inboxBodies(t, a); len(got) != 2 {
		t.Fatalf("a's inbox: %q, want two (one per sender)", got)
	}
	if got := inboxBodies(t, d); len(got) != 2 {
		t.Fatalf("d's inbox: %q, want two (one per sender)", got)
	}
	if SendNonce("b", "a", "k1") == SendNonce("b", "d", "k1") || SendNonce("b", "a", "k1") == SendNonce("c", "a", "k1") {
		t.Fatal("nonces collide across recipients or senders")
	}
	// Field boundaries are not ambiguous.
	if SendNonce("ab", "c", "k") == SendNonce("a", "bc", "k") || SendNonce("a", "b", "ck") == SendNonce("a", "bc", "k") {
		t.Fatal("nonce fields run together")
	}
	if n := SendNonce("b", "a", "k1"); n != SendNonce("b", "a", "k1") || len(n) == 0 || len(n) > 64 {
		t.Fatalf("nonce %q is not deterministic or not valid", n)
	}
}

// (c) The acknowledgement is lost after the server stored the message: the first send errors with a key, and a retry
// with that key leaves exactly one message.
func TestLostAckThenRetryWithTheKeyLandsOnce(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.client(t, "a")
	px := testenv.NewAckDropProxy(t, f.e.URL, sap.For(f.e.Owner, "a").Mail)
	cfg := f.cfg("b")
	cfg.URL = px.URL()
	lossy, err := OpenClient(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer lossy.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, err = lossy.Send(ctx, "a", "did it land?", 0)
	if err == nil {
		t.Fatal("send with a lost ack succeeded")
	}
	msg := err.Error()
	if !strings.Contains(msg, "may") || !strings.Contains(msg, "--id ") || !strings.Contains(msg, "at most once") ||
		!strings.Contains(msg, "within "+DuplicateWindowText()) || !strings.Contains(msg, "2 minutes") || !strings.Contains(msg, "second copy") {
		t.Fatalf("timeout error does not explain the retry: %q", msg)
	}
	var te *SendTimeoutError
	if !errors.As(err, &te) || te.Key == "" || !strings.Contains(msg, te.Key) {
		t.Fatalf("timeout error carries no key: %q", msg)
	}
	if px.Dropped() != 1 {
		t.Fatalf("proxy dropped %d acks, want 1", px.Dropped())
	}
	if got := inboxBodies(t, a); len(got) != 1 {
		t.Fatalf("after the lost ack the inbox has %q, want the one message", got)
	}
	retry, err := f.client(t, "b").SendDetailed(context.Background(), "a", "did it land?", SendOptions{ID: te.Key})
	if err != nil || !retry.Duplicate {
		t.Fatalf("retry with the key: %+v %v, want success as a duplicate", retry, err)
	}
	if got := inboxBodies(t, a); len(got) != 1 {
		t.Fatalf("after the retry the inbox has %q, want exactly one", got)
	}
}

// (d) Keys that are empty-looking, too long or unprintable are refused before anything is sent.
func TestSendIDValidation(t *testing.T) {
	f := newFixture(t, "a", "b")
	a, b := f.client(t, "a"), f.client(t, "b")
	for _, bad := range []string{strings.Repeat("k", 129), "has space", "tab\tkey", "new\nline", "nul\x00", "café", "\x7f"} {
		if _, err := b.SendDetailed(context.Background(), "a", "x", SendOptions{ID: bad}); err == nil || !strings.Contains(err.Error(), "id") {
			t.Errorf("id %q: want a clear error, got %v", bad, err)
		}
		if ValidateSendID(bad) == nil {
			t.Errorf("ValidateSendID(%q) accepted it", bad)
		}
	}
	if got := inboxBodies(t, a); len(got) != 0 {
		t.Fatalf("a refused id sent anyway: %q", got)
	}
	for _, ok := range []string{"k", "a-b_c.d:e/f", strings.Repeat("k", 128)} {
		if err := ValidateSendID(ok); err != nil {
			t.Errorf("ValidateSendID(%q): %v", ok, err)
		}
	}
}

// codex on #122: once more than 256 messages have been shown and the window has passed, the (user, nonce) entries are
// pruned; a redelivery of a pruned message (its ack was lost) has the same stream sequence and must still be dropped
// and acked, not shown twice. A repeat of a pruned nonce at a new sequence is a new send and is shown.
func TestRedeliveryOfAPrunedMessageIsDroppedAndAcked(t *testing.T) {
	e := &Endpoint{duplicateWindow: time.Second}
	h := &held{msgs: map[uint64]pending{}}
	seen := newSeenSet(e.dupWindow())
	base := time.Now()
	at := func(i int) time.Time { return base.Add(time.Duration(i) * 10 * time.Second) } // each past the 1 s window
	for i := 1; i <= 300; i++ {
		p := pending{mail: render.Mail{Seq: uint64(i), At: at(i)}, msg: &ackCounter{}, key: fmt.Sprintf("u/n%d", i)}
		if got := e.dedupe([]pending{p}, seen, h); len(got) != 1 {
			t.Fatalf("message %d: shown %d times", i, len(got))
		}
		seen.mark(p)
	}
	if _, ok := seen.m["u/n1"]; ok {
		t.Fatal("test setup: the nonce entry for #1 was not pruned")
	}
	redelivered := &ackCounter{}
	p := pending{mail: render.Mail{Seq: 1, At: at(1)}, msg: redelivered, key: "u/n1"}
	if got := e.dedupe([]pending{p}, seen, h); len(got) != 0 || redelivered.acks != 1 {
		t.Fatalf("redelivery of pruned #1: shown %d, acks %d, want dropped and acked once", len(got), redelivered.acks)
	}
	// The same nonce at a new sequence outside the window is a new send.
	fresh := pending{mail: render.Mail{Seq: 301, At: at(400)}, msg: &ackCounter{}, key: "u/n1"}
	if got := e.dedupe([]pending{fresh}, seen, h); len(got) != 1 {
		t.Fatalf("repeat nonce after the window: shown %d, want 1", len(got))
	}
}

// codex on #134: a shown sequence is pruned only once the consumer's ack floor has passed it. One old message stays
// unacked (the floor stays below it) while many later ones are shown and the pruning threshold is crossed repeatedly;
// its redelivery must still be dropped and acked.
func TestShownSequencePrunedOnlyBelowTheAckFloor(t *testing.T) {
	e := &Endpoint{duplicateWindow: time.Second}
	h := &held{msgs: map[uint64]pending{}}
	seen := newSeenSet(e.dupWindow())
	seen.pruneAt, seen.pruneEvery = 64, 0
	floor, floorOK := uint64(0), true
	seen.ackFloor = func() (uint64, bool) { return floor, floorOK }
	base := time.Now()
	at := func(i int) time.Time { return base.Add(time.Duration(i) * 10 * time.Second) }
	show := func(i int) {
		p := pending{mail: render.Mail{Seq: uint64(i), At: at(i)}, msg: &ackCounter{}, key: fmt.Sprintf("u/n%d", i)}
		if got := e.dedupe([]pending{p}, seen, h); len(got) != 1 {
			t.Fatalf("message %d: shown %d times", i, len(got))
		}
		seen.mark(p)
	}
	for i := 1; i <= 500; i++ {
		show(i)
		if i > 1 {
			floor = 0 // #1 is unacked: the floor cannot pass it, though #2.. are acked
		}
	}
	if len(seen.seqs) != 500 {
		t.Fatalf("with the floor below #1 the set holds %d sequences, want all 500", len(seen.seqs))
	}
	redelivered := &ackCounter{}
	p := pending{mail: render.Mail{Seq: 1, At: at(1)}, msg: redelivered, key: "u/n1"}
	if got := e.dedupe([]pending{p}, seen, h); len(got) != 0 || redelivered.acks != 1 {
		t.Fatalf("redelivery of unacked #1: shown %d, acks %d, want dropped and acked once", len(got), redelivered.acks)
	}
	// A failing floor prunes nothing.
	floor, floorOK = 400, false
	show(501)
	if len(seen.seqs) != 501 {
		t.Fatalf("failing ack floor: set holds %d, want 501", len(seen.seqs))
	}
	// Once the floor passes #1 (and the rest up to 400), those are pruned and the later ones are kept.
	floorOK = true
	show(502)
	if _, ok := seen.seqs[1]; ok {
		t.Fatal("#1 is below the ack floor and should be pruned")
	}
	if _, ok := seen.seqs[400]; ok {
		t.Fatal("#400 is at the ack floor and should be pruned")
	}
	if _, ok := seen.seqs[401]; !ok || len(seen.seqs) != 102 {
		t.Fatalf("sequences above the floor must be kept: holds %d, want 102", len(seen.seqs))
	}
}

// The ack floor is read at most once per interval, and not at all below the threshold.
func TestAckFloorReadIsRateLimited(t *testing.T) {
	seen := newSeenSet(time.Second)
	seen.pruneAt, seen.pruneEvery = 4, time.Hour
	calls := 0
	seen.ackFloor = func() (uint64, bool) { calls++; return 0, true }
	for i := 1; i <= 50; i++ {
		seen.mark(pending{key: fmt.Sprint("k", i), mail: render.Mail{Seq: uint64(i), At: time.Now()}})
	}
	if calls != 1 {
		t.Fatalf("ack floor read %d times, want 1", calls)
	}
}
