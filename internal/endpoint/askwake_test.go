package endpoint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/sap"
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
	if re.AnswerClosed("UX", 3) || re.AnswerClosed("UB", 99) {
		t.Fatal("matched the wrong sender or sequence")
	}
	if !re.AnswerClosed("UB", 3) || re.AnswerClosed("UB", 3) {
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
	if tb.Len() != 0 {
		t.Fatal("the ask stayed open")
	}
	var got *struct{ wake, urgent bool }
	for _, e := range waiting(t, sp) {
		if string(e.Body) == "the answer" {
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
	// a second reply follows its own kind: it does not wake
	if _, err := b.SendWith(ctx, "a", "and again", SendOptions{Kind: kind.FYI, InReplyTo: 7}); err != nil {
		t.Fatal(err)
	}
	until(t, "second reply spooled", func() bool { return len(waiting(t, sp)) == 6 })
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
