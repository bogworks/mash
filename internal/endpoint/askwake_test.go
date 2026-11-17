package endpoint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
)

func TestParseAnswerWithin(t *testing.T) {
	for _, c := range []struct {
		answer, within string
		d              time.Duration
		ok             bool
		err            string
	}{
		{"", "", 0, false, ""},
		{"now", "", DefaultAnswerWithin, true, ""},
		{"now", "30m", 30 * time.Minute, true, ""},
		{"now", "24h", 24 * time.Hour, true, ""},
		{"", "5m", 0, false, "needs answer=now"},
		{"now", "25h", 0, false, "out of range"},
		{"now", "0s", 0, false, "out of range"},
		{"now", "-1m", 0, false, "out of range"},
		{"now", "soon", 0, false, "not a duration"},
		{"later", "", 0, false, "must be"},
	} {
		d, ok, err := ParseAnswerWithin(c.answer, c.within)
		if (err == nil) != (c.err == "") || (err != nil && !strings.Contains(err.Error(), c.err)) || d != c.d || ok != c.ok {
			t.Errorf("%+v: got %v %v %v", c, d, ok, err)
		}
	}
}

// The table is capped, persists across a restart (a reopen of the same file), closes an ask on its first match only,
// and drops entries older than a day.
func TestAskTablePersistsCapsAndExpires(t *testing.T) {
	dir := t.TempDir()
	path := AskTablePath(dir, "o", "a")
	tb := OpenAskTable(path)
	for i := 0; i < MaxOpenAsks; i++ {
		if err := tb.Check(); err != nil {
			t.Fatalf("ask %d refused: %v", i, err)
		}
		if _, err := tb.Add("b", "UB", uint64(i+1), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if err := tb.Check(); err == nil || !strings.Contains(err.Error(), "20 answer=now asks are already open") {
		t.Fatalf("21st: %v", err)
	}
	if _, err := os.Stat(path + askTableTemp); !os.IsNotExist(err) {
		t.Fatalf("the temp file was left behind: %v", err)
	}
	re := OpenAskTable(path)
	if re.Len() != MaxOpenAsks {
		t.Fatalf("reloaded %d asks", re.Len())
	}
	a := re.Asks()[2]
	if a.Recipient != "b" || a.RecipientKey != "UB" || a.ReceiptSeq != 3 || a.ID == "" || !a.Deadline.Equal(a.SentAt.Add(time.Minute)) {
		t.Fatalf("%+v", a)
	}
	if re.AnswerMatches("UX", 3) || re.AnswerMatches("UB", 99) || re.CloseAnswered("UX", 3) || re.CloseAnswered("UB", 99) {
		t.Fatal("matched the wrong sender or sequence")
	}
	if !re.AnswerMatches("UB", 3) || !re.AnswerMatches("UB", 3) || re.Len() != MaxOpenAsks {
		t.Fatal("a match must not close the ask")
	}
	if !re.CloseAnswered("UB", 3) || re.CloseAnswered("UB", 3) || re.AnswerMatches("UB", 3) {
		t.Fatal("an ask closes on its first match only")
	}
	if OpenAskTable(path).Len() != MaxOpenAsks-1 {
		t.Fatal("the close was not persisted")
	}
	old := OpenAskTable(path)
	old.now = func() time.Time { return time.Now().Add(AskTTL + time.Minute) }
	if old.Len() != 0 {
		t.Fatalf("%d asks survived 24h", old.Len())
	}
	if filepath.Dir(path) != filepath.Join(dir, "asks", "o") {
		t.Fatal(path)
	}
}

// A verified reply that answers an open ask wakes the asker whatever its kind, and closes the ask; nothing else does.
func TestAnswerNowReplyWakesWhateverTheKind(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	f.idle = 300 * time.Millisecond
	a, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	b := f.client(t, "b")
	ctx := context.Background()
	rec, err := a.lookup(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	tb := OpenAskTable("")
	a.SetReplyWaker(tb)
	if _, err := tb.Add("b", rec.User, 7, time.Minute); err != nil {
		t.Fatal(err)
	}
	quiet := func(what string, n int) {
		t.Helper()
		until(t, what+" spooled", func() bool { return len(waiting(t, sp)) == n })
		time.Sleep(4 * f.idle)
		if got := ev.all(); len(got) != 0 {
			t.Fatalf("%s woke the session: %q", what, got)
		}
		if tb.Len() != 1 {
			t.Fatalf("%s closed the ask", what)
		}
	}
	// no in-reply-to
	if _, err := b.SendWith(ctx, "a", "no mark", SendOptions{Kind: kind.FYI}); err != nil {
		t.Fatal(err)
	}
	quiet("no in-reply-to", 1)
	// the right number from another verified sender
	if _, err := f.client(t, "c").SendWith(ctx, "a", "not yours", SendOptions{Kind: kind.FYI, InReplyTo: 7}); err != nil {
		t.Fatal(err)
	}
	quiet("a reply from another sender", 2)
	// the right sender, a different number
	if _, err := b.SendWith(ctx, "a", "other", SendOptions{Kind: kind.FYI, InReplyTo: 8}); err != nil {
		t.Fatal(err)
	}
	quiet("a different number", 3)
	// a forged in-reply-to: the signature is over another number
	rawMark(t, f, "b", "a", "forged", func(id sap.Identity, h *sap.SenderHeader, m *nats.Msg) {
		r, _ := id.SignInReplyTo(*h, 8)
		m.Header.Set(InReplyToHeader, strings.Replace(r, "8.", "7.", 1))
		m.Header.Set(KindHeader, string(kind.FYI))
	})
	quiet("a forged in-reply-to", 4)
	// the reply: fyi, which normally waits
	if _, err := b.SendWith(ctx, "a", "the answer", SendOptions{Kind: kind.FYI, InReplyTo: 7}); err != nil {
		t.Fatal(err)
	}
	if w := ev.waitFor(t, 1, 3*time.Second); len(w) != 1 {
		t.Fatalf("wake %q", w)
	}
	// Spooled is not settled: the ask stays open until a hook shows the reply and serve acks it.
	if tb.Len() != 1 {
		t.Fatal("the ask closed when the reply was only spooled")
	}
	var got *struct{ wake, urgent bool }
	var answer spool.Entry
	for _, e := range waiting(t, sp) {
		if string(e.Body) == "the answer" {
			answer = e
			got = &struct{ wake, urgent bool }{e.AnswerWake, e.Urgent}
			m := MailOf(e)
			if m.Trust != sap.TrustVerified || m.Kind != kind.FYI || m.ReplyTo != 7 {
				t.Fatalf("the wake changed the mail: %+v", m)
			}
		}
	}
	if got == nil || !got.wake || got.urgent {
		t.Fatalf("spooled %+v", got)
	}
	if err := sp.Drained(answer.Ref()); err != nil {
		t.Fatal(err)
	}
	until(t, "the ask closed once the reply was shown and acked", func() bool { return tb.Len() == 0 })
	// a second reply follows its own kind: it does not wake
	if _, err := b.SendWith(ctx, "a", "and again", SendOptions{Kind: kind.FYI, InReplyTo: 7}); err != nil {
		t.Fatal(err)
	}
	until(t, "second reply spooled", func() bool { return len(waiting(t, sp)) == 5 })
	// The held answer may ring again by the ordinary backoff, as a held urgent mail does; the second reply itself is
	// ordinary fyi mail: it carries no answer wake and the ask stays closed.
	for _, e := range waiting(t, sp) {
		if string(e.Body) == "and again" && (e.AnswerWake || e.Urgent) {
			t.Fatalf("a second reply was treated as the answer: %+v", e)
		}
	}
	if tb.Len() != 0 {
		t.Fatal("the ask reopened")
	}
}

func TestAskTableReserveIsAtomicAndReleases(t *testing.T) {
	tb := OpenAskTable(filepath.Join(t.TempDir(), "asks.json"))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var rels []func()
	for i := 0; i < MaxOpenAsks+5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rel, err := tb.Reserve(); err == nil {
				mu.Lock()
				rels = append(rels, rel)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(rels) != MaxOpenAsks {
		t.Fatalf("granted %d reservations, want %d", len(rels), MaxOpenAsks)
	}
	if _, err := tb.Reserve(); err == nil {
		t.Fatal("a full table granted a reservation")
	}
	rels[0]()
	rels[0]() // idempotent
	if _, err := tb.Reserve(); err != nil {
		t.Fatalf("a released slot was not reusable: %v", err)
	}
	if _, err := tb.Reserve(); err == nil {
		t.Fatal("double release freed two slots")
	}
}

// A reply whose channel delivery fails leaves its ask open (it is redelivered and matches again); the ask closes only
// once the reply is delivered.
func TestAnswerNowAskStaysOpenUntilDelivered(t *testing.T) {
	f := newFixture(t, "a", "b")
	a, ev := f.run(t, "a")
	b := f.client(t, "b")
	ctx := context.Background()
	rec, err := a.lookup(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	tb := OpenAskTable("")
	a.SetReplyWaker(tb)
	if _, err := tb.Add("b", rec.User, 7, time.Minute); err != nil {
		t.Fatal(err)
	}
	ev.fail.Store(1000)
	if _, err := b.SendWith(ctx, "a", "the answer", SendOptions{Kind: kind.FYI, InReplyTo: 7}); err != nil {
		t.Fatal(err)
	}
	until(t, "a failed delivery", func() bool { return ev.fail.Load() < 1000 })
	time.Sleep(300 * time.Millisecond)
	if tb.Len() != 1 || !tb.AnswerMatches(rec.User, 7) {
		t.Fatal("the ask closed before the reply was delivered")
	}
	ev.fail.Store(0)
	ev.waitFor(t, 1, 5*time.Second)
	until(t, "the ask closed", func() bool { return tb.Len() == 0 })
}

// A reply spooled but not yet shown leaves its ask open in the file, so a serve that restarts before the drain still
// matches the redelivered reply; once the hook drains it and serve acks it, the ask is closed.
func TestAnswerNowAskSurvivesRestartUntilSpoolDrained(t *testing.T) {
	f := newFixture(t, "a", "b")
	a, _, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	b := f.client(t, "b")
	ctx := context.Background()
	rec, err := a.lookup(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "asks.json")
	tb := OpenAskTable(path)
	a.SetReplyWaker(tb)
	if _, err := tb.Add("b", rec.User, 7, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SendWith(ctx, "a", "the answer", SendOptions{Kind: kind.FYI, InReplyTo: 7}); err != nil {
		t.Fatal(err)
	}
	until(t, "reply spooled", func() bool { return len(waiting(t, sp)) == 1 })
	time.Sleep(300 * time.Millisecond)
	// a restart: a new table loaded from the file still matches the redelivered reply
	if re := OpenAskTable(path); re.Len() != 1 || !re.AnswerMatches(rec.User, 7) {
		t.Fatal("the ask was closed by the spool write")
	}
	ent := waiting(t, sp)[0]
	if err := sp.Drained(ent.Ref()); err != nil {
		t.Fatal(err)
	}
	until(t, "the ask closed after drain and ack", func() bool { return OpenAskTable(path).Len() == 0 })
}

// Add is idempotent on (recipient key, receipt sequence): a repeat keeps the first ask, adds none, extends nothing.
func TestAskTableAddIsIdempotent(t *testing.T) {
	tb := OpenAskTable(filepath.Join(t.TempDir(), "asks.json"))
	first, err := tb.Add("b", "UB", 5, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	again, err := tb.Add("b", "UB", 5, time.Hour)
	if err != nil || again.ID != first.ID || !again.Deadline.Equal(first.Deadline) || tb.Len() != 1 {
		t.Fatalf("repeat: %+v %v len %d", again, err, tb.Len())
	}
	if _, err := tb.Add("b", "UB", 6, time.Minute); err != nil || tb.Len() != 2 {
		t.Fatal("a different receipt must add an ask")
	}
	for i := tb.Len(); i < MaxOpenAsks; i++ {
		if _, err := tb.Add("b", "UB", uint64(100+i), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tb.Add("b", "UB", 5, time.Minute); err != nil {
		t.Fatalf("a repeat of a recorded ask needs no slot: %v", err)
	}
}
