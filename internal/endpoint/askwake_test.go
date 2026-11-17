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
// and drops entries long past their deadline.
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
	old.now = func() time.Time { return time.Now().Add(time.Minute + AskSettleGrace + time.Minute) }
	if old.Len() != 0 {
		t.Fatalf("%d asks survived their deadline plus the settle grace", old.Len())
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

// Reload picks up asks another holder wrote to the file, and a malformed file keeps the asks in memory.
func TestAskTableReloadSeesOtherHoldersAsks(t *testing.T) {
	path := AskTablePath(t.TempDir(), "o", "a")
	waiting := OpenAskTable(path)
	holder := OpenAskTable(path)
	if _, err := holder.Add("b", "UB", 7, time.Minute); err != nil {
		t.Fatal(err)
	}
	waiting.Reload()
	if !waiting.AnswerMatches("UB", 7) || waiting.Len() != 1 {
		t.Fatal("Reload must make the new ask visible")
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	waiting.Reload()
	if !waiting.AnswerMatches("UB", 7) {
		t.Fatal("a malformed file must keep the current asks")
	}
}

// Two tables of one file (an older serve of a session beside a newer one): a write by one never drops an ask the other
// added, and never brings back one the other closed.
func TestAskTableTwoWritersMerge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asks.json")
	seed := OpenAskTable(path)
	old, err := seed.Add("p", "UP", 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	a, b := OpenAskTable(path), OpenAskTable(path)
	added, err := b.Add("q", "UQ", 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Close(old.ID) { // a's expiry of the old ask must keep b's new one
		t.Fatal("old ask not open in a")
	}
	got := OpenAskTable(path).Asks()
	if len(got) != 1 || got[0].ID != added.ID {
		t.Fatalf("after a's close the file holds %v, want only %s", got, added.ID)
	}
	// b closes the new ask; a's next write must not resurrect it.
	if !b.CloseAnswered("UQ", 2) {
		t.Fatal("reply did not close the ask in b")
	}
	z, err := a.Add("r", "UR", 3, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got = OpenAskTable(path).Asks()
	if len(got) != 1 || got[0].ID != z.ID {
		t.Fatalf("the file holds %v, want only %s", got, z.ID)
	}
}

// An ask with the longest answer_within (24h) is still in the table just after its deadline, so the watcher can settle
// it; so is one that a restart finds past its deadline. Only the settle grace past the deadline removes it.
func TestAskTableKeepsExpiredAskUntilSettled(t *testing.T) {
	path := AskTablePath(t.TempDir(), "o", "a")
	clock := time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)
	tb := OpenAskTable(path)
	tb.now = func() time.Time { return clock }
	if _, err := tb.Add("b", "UB", 7, MaxAnswerWithin); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(MaxAnswerWithin + time.Second)
	if got := tb.Expired(clock); len(got) != 1 || got[0].ReceiptSeq != 7 {
		t.Fatalf("expired just after the deadline: %+v", got)
	}
	re := OpenAskTable(path)
	re.now = func() time.Time { return clock.Add(time.Hour) }
	if got := re.Expired(re.now()); len(got) != 1 {
		t.Fatalf("a restart after the deadline lost the ask: %+v", got)
	}
	if !re.Close(re.Expired(re.now())[0].ID) || len(OpenAskTable(path).Asks()) != 0 {
		t.Fatal("settling the ask did not remove it")
	}
	if _, err := tb.Add("b", "UB", 8, time.Minute); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute + AskSettleGrace + time.Second)
	if got := tb.Expired(clock); len(got) != 0 {
		t.Fatalf("an ask past the settle grace is still held: %+v", got)
	}
}

// Two tables on one file (two serve processes) adding and closing concurrently end with the union of the adds minus the
// closes, and no temp file is left.
func TestAskTableTwoWritersOneFile(t *testing.T) {
	dir := t.TempDir()
	path := AskTablePath(dir, "o", "a")
	tabs := []*AskTable{OpenAskTable(path), OpenAskTable(path)}
	const n = 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	want := map[string]bool{}
	for w, tb := range tabs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var mine []string
			for i := 0; i < n; i++ {
				a, err := tb.Add("b", "UB", uint64(w*1000+i+1), time.Hour)
				if err != nil {
					// the cap is shared through the file: close what this writer added and carry on
					for _, id := range mine {
						tb.Close(id)
					}
					mine = nil
					continue
				}
				mine = append(mine, a.ID)
				if i%3 == 0 {
					tb.Close(mine[0])
					mine = mine[1:]
				}
			}
			mu.Lock()
			for _, id := range mine {
				want[id] = true
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	got := map[string]bool{}
	for _, a := range OpenAskTable(path).Asks() {
		got[a.ID] = true
	}
	for id := range want {
		if !got[id] {
			t.Errorf("ask %s was lost to the other writer", id)
		}
	}
	ents, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), askTableTemp) {
			t.Errorf("stray temp file %s", e.Name())
		}
	}
}

// A table that loaded the file before another serve added an ask still matches that ask's reply and reports its deadline,
// with no write of its own in between.
func TestAskTableRefreshesFromFileBeforeReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asks.json")
	a, b := OpenAskTable(path), OpenAskTable(path) // both load the empty (missing) file
	if _, err := b.Add("q", "UQ", 5, time.Minute); err != nil {
		t.Fatal(err)
	}
	if !a.AnswerMatches("UQ", 5) {
		t.Fatal("a did not see the ask b added after a loaded")
	}
	if !a.CloseAnswered("UQ", 5) {
		t.Fatal("a did not close the ask b added after a loaded")
	}
	if got := OpenAskTable(path).Asks(); len(got) != 0 {
		t.Fatalf("the ask is still in the file after its reply: %v", got)
	}
	if a.AnswerMatches("UQ", 5) || b.AnswerMatches("UQ", 5) || a.CloseAnswered("UQ", 5) || b.CloseAnswered("UQ", 5) {
		t.Fatal("a closed ask matched again")
	}

	// A deadline too: d adds an ask that is already expired by c's clock.
	c := OpenAskTable(path)
	d := OpenAskTable(path)
	d.now = func() time.Time { return time.Now().Add(-time.Hour) }
	added, err := d.Add("r", "UR", 6, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got := c.Expired(time.Now())
	if len(got) != 1 || got[0].ID != added.ID {
		t.Fatalf("c.Expired = %v, want the ask d added", got)
	}
	if !c.Close(added.ID) || d.Len() != 0 {
		t.Fatal("c's close did not reach d")
	}
}

// Two serves of one session share one file: reservations count across them, so exactly one gets the last slot.
func TestAskTableReservationsAreSharedAcrossServes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asks.json")
	a, b := OpenAskTable(path), OpenAskTable(path)
	for i := 0; i < MaxOpenAsks-1; i++ {
		if _, err := a.Add("p", "UP", uint64(i+1), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var got []func()
	for _, tb := range []*AskTable{a, b, a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rel, err := tb.Reserve(); err == nil {
				mu.Lock()
				got = append(got, rel)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(got) != 1 {
		t.Fatalf("%d serves got the last slot, want exactly 1", len(got))
	}
	if a.Check() == nil || b.Check() == nil {
		t.Fatal("Check ignored a live reservation")
	}
	got[0]()
	got[0]()
	if _, err := b.Reserve(); err != nil {
		t.Fatalf("a released slot was not free for the other serve: %v", err)
	}
	if _, err := a.Reserve(); err == nil {
		t.Fatal("the slot was granted twice")
	}
}

func TestAskTableExpiredReservationIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asks.json")
	a, b := OpenAskTable(path), OpenAskTable(path)
	for i := 0; i < MaxOpenAsks-1; i++ {
		if _, err := a.Add("p", "UP", uint64(i+1), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Reserve(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Reserve(); err == nil {
		t.Fatal("a live reservation did not hold the slot")
	}
	b.now = func() time.Time { return time.Now().Add(ReservationLease + time.Second) }
	rel, err := b.Reserve()
	if err != nil {
		t.Fatalf("a lapsed reservation still held the slot: %v", err)
	}
	rel()
}

func TestAskTableFileWithoutReservationsLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asks.json")
	old := `{"asks":[{"id":"1-1","recipient_key":"UB","recipient":"b","receipt_seq":4,"sent_at":"` +
		time.Now().UTC().Format(time.RFC3339) + `","deadline":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	tb := OpenAskTable(path)
	if tb.Len() != 1 || !tb.AnswerMatches("UB", 4) {
		t.Fatal("old-format file did not load")
	}
	rel, err := tb.Reserve()
	if err != nil {
		t.Fatal(err)
	}
	rel()
	if OpenAskTable(path).Len() != 1 {
		t.Fatal("reserving rewrote the asks")
	}
}
