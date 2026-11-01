package endpoint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// kindFixture is a hooked session "a" with a short idle wake, and a sender "b".
func kindFixture(t *testing.T) (*fixture, *events, func() []string, *Endpoint) {
	f, ev, sp, b := kindFixtureSpool(t)
	return f, ev, func() []string {
		var out []string
		for _, e := range waiting(t, sp) {
			out = append(out, e.Kind)
		}
		return out
	}, b
}

func kindFixtureSpool(t *testing.T) (*fixture, *events, spool.Spool, *Endpoint) {
	t.Helper()
	f := newFixture(t, "a", "b")
	f.idle = 300 * time.Millisecond
	_, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	return f, ev, sp, f.client(t, "b")
}

// A held blocker, question or task wakes an idle session once after the idle wake; a held result or fyi never does,
// over several idle-wake periods.
func TestKindDecidesTheIdleWake(t *testing.T) {
	for _, k := range kind.All {
		t.Run(string(k), func(t *testing.T) {
			f, ev, spooled, b := kindFixture(t)
			if _, err := b.SendWith(context.Background(), "a", "body", SendOptions{Kind: k}); err != nil {
				t.Fatal(err)
			}
			until(t, "spooled", func() bool { return len(spooled()) == 1 })
			if got := spooled()[0]; got != string(k) {
				t.Fatalf("spooled kind %q, want %q", got, k)
			}
			if k.Wakes() {
				ev.waitFor(t, 1, 5*time.Second)
				time.Sleep(f.idle) // the re-wake is 2x the base away
				if got := ev.all(); len(got) != 1 {
					t.Fatalf("%s: %d wakes, want 1: %q", k, len(got), got)
				}
				return
			}
			time.Sleep(6 * f.idle) // past the idle wake and the first re-wake
			if got := ev.all(); len(got) != 0 {
				t.Fatalf("%s woke the session: %q", k, got)
			}
		})
	}
}

// A fyi held, then a question arrives: the wake counts both, and the drain shows both.
func TestWakingKindArrivingAfterAFyiWakesWithBoth(t *testing.T) {
	f, ev, sp, b := kindFixtureSpool(t)
	spooled := func() []spool.Entry { return waiting(t, sp) }
	ctx := context.Background()
	if _, err := b.SendWith(ctx, "a", "fyi first", SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * f.idle)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("a fyi woke the session: %q", got)
	}
	if _, err := b.SendWith(ctx, "a", "then a question", SendOptions{Kind: kind.Question}); err != nil {
		t.Fatal(err)
	}
	until(t, "both spooled", func() bool { return len(spooled()) == 2 })
	got := ev.waitFor(t, 1, 5*time.Second)
	if got[0] != "2 messages from b (question, fyi)" && got[0] != "2 messages from b (fyi, question)" {
		t.Fatalf("the wake %q", got[0])
	}
	var ms []render.Mail
	for _, e := range waiting(t, sp) { // what the next hook drain shows
		ms = append(ms, MailOf(e))
	}
	out := render.Batch("a", f.e.Owner, ms, 10, 16384)
	if !strings.Contains(out, "\nkind: fyi\n> fyi first") || !strings.Contains(out, "\nkind: question\n> then a question") {
		t.Fatalf("the drain lacks one of them:\n%s", out)
	}
}

// A message with no Mash-Kind header (an old sender) or an unknown kind is task: it wakes an idle session, and is shown as
// task. Only an explicit fyi does not wake.
func TestOldSenderWithoutAKindWakes(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.idle = 300 * time.Millisecond
	a, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client(t, "b").SendWith(context.Background(), "a", "from an old sender", SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	time.Sleep(3 * f.idle)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("an explicit fyi woke the session: %q", got)
	}
	// Strip the header as an old sender would: send raw, with an unknown kind too.
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	id := identity(t, f.creds["b"])
	for _, v := range []string{"", "urgent-ish"} {
		h, err := id.Sign(sap.For(f.e.Owner, "a").Mail, []byte("raw"+v), "b", time.Now(), "")
		if err != nil {
			t.Fatal(err)
		}
		m := nats.NewMsg(sap.For(f.e.Owner, "a").Mail)
		m.Data = []byte("raw" + v)
		m.Header.Set(sap.HeaderName, h.Encode())
		if v != "" {
			m.Header.Set(KindHeader, v)
		}
		if _, err := nc.RequestMsg(m, 2*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "all spooled", func() bool { return len(waiting(t, sp)) == 3 })
	if got := ev.waitFor(t, 1, 2*time.Second); !strings.Contains(got[0], "task") {
		t.Fatalf("the wake for mail with no or an unknown kind: %q", got[0])
	}
	for _, seq := range []uint64{2, 3} {
		got, err := a.Read(context.Background(), seq)
		if err != nil || got.Kind != kind.Task {
			t.Fatalf("read #%d: %+v %v", seq, got, err)
		}
	}
	for _, e := range waiting(t, sp) {
		k := MailOf(e).Kind
		if e.Seq == 1 && k != kind.FYI || e.Seq != 1 && (k != kind.Task || !k.Wakes()) {
			t.Fatalf("entry %d is %q", e.Seq, k)
		}
	}
}

// Urgent still wakes at once, whatever the kind.
func TestUrgentFyiStillWakesAtOnce(t *testing.T) {
	_, ev, spooled, b := kindFixture(t)
	if _, err := b.SendWith(context.Background(), "a", "now", SendOptions{Kind: kind.FYI, Urgent: true}); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(spooled()) == 1 })
	if got := ev.waitFor(t, 1, 2*time.Second); !strings.HasPrefix(got[0], "urgent, 1 message from b") {
		t.Fatalf("the wake %q", got[0])
	}
}

// SendWith sets Mash-Kind; an invalid kind is refused.
func TestSendWithSetsTheKindHeader(t *testing.T) {
	f := newFixture(t, "a", "b")
	a := f.client(t, "a")
	b := f.client(t, "b")
	ctx := context.Background()
	if _, err := b.SendWith(ctx, "a", "q", SendOptions{Kind: kind.Question}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendWith(ctx, "a", "default", SendOptions{}); err != nil { // no kind is task
		t.Fatal(err)
	}
	if _, err := b.SendWith(ctx, "a", "bad", SendOptions{Kind: "nope"}); err == nil {
		t.Fatal("an unknown kind was sent")
	}
	m1, err := a.Read(ctx, 1)
	if err != nil || m1.Kind != kind.Question {
		t.Fatalf("#1 %+v %v", m1, err)
	}
	m2, err := a.Read(ctx, 2)
	if err != nil || m2.Kind != kind.Task {
		t.Fatalf("#2 %+v %v", m2, err)
	}
}

// Mail that never wakes still wakes the session once it is a backlog near the consumer's limit, so waking mail behind it
// can arrive (codex H1 on #75): 4 held fyi do not wake with SpoolWakeCount 5, 5 do.
func TestFyiBacklogAtTheCountWakes(t *testing.T) {
	old := SpoolWakeCount
	SpoolWakeCount = 5
	t.Cleanup(func() { SpoolWakeCount = old })
	f, ev, sp, b := kindFixtureSpool(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if _, err := b.SendWith(ctx, "a", "fyi", SendOptions{Kind: kind.FYI}); err != nil {
			t.Fatal(err)
		}
	}
	until(t, "4 spooled", func() bool { return len(waiting(t, sp)) == 4 })
	time.Sleep(4 * f.idle)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("4 fyi woke the session: %q", got)
	}
	if _, err := b.SendWith(ctx, "a", "fyi", SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
	until(t, "5 spooled", func() bool { return len(waiting(t, sp)) == 5 })
	ev.waitFor(t, 1, 5*time.Second)
	time.Sleep(f.idle) // the re-wake is 2x the base away
	if got := ev.all(); len(got) != 1 {
		t.Fatalf("%d wakes, want 1: %q", len(got), got)
	}
}

// The same by held bytes: SpoolWakeBytes wakes.
func TestFyiBacklogAtTheWakeBytesWakes(t *testing.T) {
	old := SpoolWakeBytes
	SpoolWakeBytes = 30000
	t.Cleanup(func() { SpoolWakeBytes = old })
	f, ev, sp, b := kindFixtureSpool(t)
	ctx := context.Background()
	if _, err := b.SendWith(ctx, "a", strings.Repeat("x", 20000), SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
	until(t, "1 spooled", func() bool { return len(waiting(t, sp)) == 1 })
	time.Sleep(4 * f.idle)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("20000 bytes of fyi woke the session: %q", got)
	}
	if _, err := b.SendWith(ctx, "a", strings.Repeat("x", 20000), SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
	ev.waitFor(t, 1, 5*time.Second)
}

// A waking message the spool's byte cap turned away still wakes the session to drain what is held.
func TestSpoolFullWakes(t *testing.T) {
	old := SpoolMaxBytes
	SpoolMaxBytes = 20
	t.Cleanup(func() { SpoolMaxBytes = old })
	f, ev, sp, b := kindFixtureSpool(t)
	ctx := context.Background()
	if _, err := b.SendWith(ctx, "a", "123456", SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
	until(t, "1 spooled", func() bool { return len(waiting(t, sp)) == 1 })
	if _, err := b.SendWith(ctx, "a", strings.Repeat("x", 30), SendOptions{Kind: kind.Task}); err != nil {
		t.Fatal(err)
	}
	ev.waitFor(t, 1, 5*time.Second)
	_ = f
}

// The byte wake comes well before the inbox stream discards old (unacked) messages: its limit counts headers too.
func TestSpoolWakeBytesIsWellBelowTheInboxLimit(t *testing.T) {
	if SpoolWakeBytes > store.InboxMaxBytes/4 {
		t.Fatalf("SpoolWakeBytes %d is above a quarter of the inbox stream's %d", SpoolWakeBytes, store.InboxMaxBytes)
	}
}

// Large headers on a tiny body count toward the byte backlog wake, as they count toward the inbox stream's limit.
func TestFyiBacklogCountsHeaders(t *testing.T) {
	old := SpoolWakeBytes
	SpoolWakeBytes = 2000
	t.Cleanup(func() { SpoolWakeBytes = old })
	f, ev, sp, _ := kindFixtureSpool(t)
	nc := f.e.ConnAs(t, f.creds["b"], "b")
	id := identity(t, f.creds["b"])
	h, err := id.Sign(sap.For(f.e.Owner, "a").Mail, []byte("hi"), "b", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	m := nats.NewMsg(sap.For(f.e.Owner, "a").Mail)
	m.Data = []byte("hi")
	m.Header.Set(sap.HeaderName, h.Encode())
	m.Header.Set(KindHeader, "fyi")
	m.Header.Set("X-Padding", strings.Repeat("x", 3000))
	if _, err := nc.RequestMsg(m, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	ev.waitFor(t, 1, 5*time.Second)
}

// A send with no kind is task and wakes an idle session; an explicit fyi does not.
func TestUnkindedSendWakesButFyiDoesNot(t *testing.T) {
	f := newFixture(t, "a", "b")
	f.idle = 300 * time.Millisecond
	_, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	b := f.client(t, "b")
	if _, err := b.SendWith(context.Background(), "a", "quiet", SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
	until(t, "spooled", func() bool { return len(waiting(t, sp)) == 1 })
	time.Sleep(3 * f.idle)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("an explicit fyi woke the session: %q", got)
	}
	if _, err := b.Send(context.Background(), "a", "plain", 0); err != nil {
		t.Fatal(err)
	}
	if got := ev.waitFor(t, 1, 3*time.Second); !strings.Contains(got[0], "task") {
		t.Fatalf("the wake for an unkinded send: %q", got[0])
	}
}
