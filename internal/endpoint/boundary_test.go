package endpoint

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/kind"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// Boundary tests for the limits that meet at the inbox (issue #48): the 1 MiB message cap, the 16 MiB inbox cap with
// discard-old, and the burst wake thresholds (SpoolWakeCount, SpoolWakeBytes). They run against the in-process
// server with the production permissions and streams. The hook's output at Claude Code's 10,000-unit cap is
// cmd/mash/boundary_test.go, which also covers what a drain of 601 messages shows and in what order.
//
// The 10-second hook timeout is not covered here or there: see the header of cmd/mash/boundary_test.go.

// payload is n pseudo-random bytes that are the same for the same seed and are valid text for a message body (printable
// ASCII, so the size in bytes is the size in characters).
func payload(seed int64, n int) string {
	r := rand.New(rand.NewSource(seed))
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 \n"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[r.Intn(len(letters))]
	}
	return string(b)
}

var sizeInError = regexp.MustCompile(`message is (\d+) bytes with headers`)

// maxBody is the largest body, in bytes, the sender can send to a: the body plus the signed headers is at most
// store.MaxMsgSize. The size of the headers is read off the refusal of a probe, not guessed, then checked by the
// caller's own sends (the headers do not change length from one send to the next).
func maxBody(t *testing.T, b *Endpoint) int {
	t.Helper()
	_, err := b.Send(context.Background(), "a", strings.Repeat("x", store.MaxMsgSize), 0)
	if err == nil {
		t.Fatal("a message of 1 MiB plus headers was accepted")
	}
	m := sizeInError.FindStringSubmatch(err.Error())
	if m == nil {
		t.Fatalf("the refusal does not say the size: %v", err)
	}
	size, _ := strconv.Atoi(m[1])
	return store.MaxMsgSize - (size - store.MaxMsgSize)
}

// Messages at the 1 MiB limit to an endpoint that is offline: one whose body and headers add up to exactly the limit
// is accepted and stored whole; one byte more is refused with the existing error, before anything is published.
func TestBoundaryMessageAtTheOneMiBLimit(t *testing.T) {
	f := newFixture(t, "a", "b") // "a" never runs: its inbox is a stream with no reader
	b := f.client(t, "b")
	ctx := context.Background()
	exact := maxBody(t, b)
	if exact > store.MaxMsgSize || store.MaxMsgSize-exact > 2048 {
		t.Fatalf("the headers take %d bytes: more than a signed header should", store.MaxMsgSize-exact)
	}
	cases := []struct {
		name    string
		body    int
		wantErr string // "" = accepted
	}{
		{"exactly at the limit", exact, ""},
		{"one byte under", exact - 1, ""},
		{"one byte over", exact + 1, fmt.Sprintf("bytes with headers; the limit is %d", store.MaxMsgSize)},
		{"1 MiB of body alone", store.MaxMsgSize, fmt.Sprintf("bytes with headers; the limit is %d", store.MaxMsgSize)},
	}
	stored := map[uint64][sha256.Size]byte{}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := payload(int64(i), c.body)
			seq, err := b.Send(ctx, "a", body, 0)
			if c.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), "mash: message is ") || !strings.Contains(err.Error(), c.wantErr) || strings.Contains(err.Error(), "nats:") {
					t.Fatalf("error %v, want one containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			stored[seq] = sha256.Sum256([]byte(body))
		})
	}
	if got := inboxMsgs(t, f, "a"); got != uint64(len(stored)) || got != 2 {
		t.Fatalf("inbox holds %d messages, want the 2 accepted ones and nothing of the refused", got)
	}
	a := f.client(t, "a")
	for seq, want := range stored {
		m, err := a.Read(ctx, seq)
		if err != nil || sha256.Sum256(m.Body) != want {
			t.Fatalf("#%d read back wrong: %v", seq, err)
		}
	}
}

// 15 messages of 1 MiB (body and headers at the limit) to an offline endpoint fit under the 16 MiB inbox cap: all are
// there and read back byte for byte. Sending on past the cap then evicts the OLDEST messages (discard-old, as
// store.InboxConfig sets it), never refuses a send, keeps the newest, and leaves the stream at or under InboxMaxBytes.
// This is the test of the "inbox overflow eviction" claim in docs/architecture.md (TestInboxOverflowEvictsTheOldest
// shows it with 400 KiB messages; this does with messages of the size cap, which is where the cap is hit in few
// sends and where one message is a sixteenth of the inbox).
func TestBoundaryInboxCapWithMaxSizeMessages(t *testing.T) {
	f := newFixture(t, "a", "b")
	b, a := f.client(t, "b"), f.client(t, "a")
	ctx := context.Background()
	body := maxBody(t, b)
	sums := map[uint64][sha256.Size]byte{}
	send := func(i int) uint64 {
		t.Helper()
		p := payload(int64(i), body)
		seq, err := b.Send(ctx, "a", p, 0)
		if err != nil {
			t.Fatalf("send %d refused: a full inbox must evict, not refuse: %v", i, err)
		}
		if seq != uint64(i) {
			t.Fatalf("send %d got seq %d", i, seq)
		}
		sums[seq] = sha256.Sum256([]byte(p))
		return seq
	}
	state := func() jetstream.StreamState {
		t.Helper()
		js := adminJS(t, f)
		s, err := js.Stream(ctx, sap.InboxStream("a"))
		if err != nil {
			t.Fatal(err)
		}
		info, err := s.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return info.State
	}
	readsBack := func(seq uint64) {
		t.Helper()
		m, err := a.Read(ctx, seq)
		if err != nil {
			t.Fatalf("#%d: %v", seq, err)
		}
		if len(m.Body) != body || sha256.Sum256(m.Body) != sums[seq] {
			t.Fatalf("#%d reads back %d bytes, not what was sent (%d)", seq, len(m.Body), body)
		}
	}

	const underCap = 15
	for i := 1; i <= underCap; i++ {
		send(i)
	}
	if st := state(); st.Msgs != underCap || st.FirstSeq != 1 || st.LastSeq != underCap || st.Bytes > uint64(store.InboxMaxBytes) {
		t.Fatalf("after %d messages under the cap: %+v", underCap, st)
	}
	for i := uint64(1); i <= underCap; i++ {
		readsBack(i)
	}

	// Past the cap: 15 held, so one or two more cross 16 MiB (a message is 1 MiB with its headers and the stream adds its
	// own record overhead). Send three; whatever the exact count that fits, the rules are the same.
	const total = underCap + 3
	for i := underCap + 1; i <= total; i++ {
		send(i)
	}
	st := state()
	if st.Bytes > uint64(store.InboxMaxBytes) {
		t.Fatalf("the stream holds %d bytes, over the cap %d", st.Bytes, store.InboxMaxBytes)
	}
	if st.LastSeq != total || st.FirstSeq <= 1 || st.Msgs >= total || st.FirstSeq+st.Msgs-1 != total {
		t.Fatalf("state %+v: want the oldest evicted and the newest %d kept, with no gap", st, total)
	}
	for seq := uint64(1); seq < st.FirstSeq; seq++ {
		if _, err := a.Read(ctx, seq); err == nil {
			t.Fatalf("#%d is below the first kept (#%d) and still reads", seq, st.FirstSeq)
		}
	}
	for seq := st.FirstSeq; seq <= total; seq++ {
		readsBack(seq) // everything that survived is whole and byte-identical
	}
	t.Logf("%d sent, #%d..#%d kept, %d bytes of %d", total, st.FirstSeq, st.LastSeq, st.Bytes, store.InboxMaxBytes)
}

// Held fyi mail wakes the session at the backlog thresholds only. A burst of 600 fyi, past SpoolWakeCount (500), gives
// exactly ONE wake (the crowded rule wakes at once, at the 500th held message; the 100 after it add none, and the
// re-wake is backed off to 2x the idle wake). An urgent task sent last, behind the backlog, rings once more at once
// (each urgent message rings once, whatever is held): two wakes in all, the second leading with "urgent". After the
// drain every one of the 601 messages is acked exactly once and nothing wakes again.
//
// The idle wake is set far past the test so that the re-wake backoff (a different rule, covered by
// TestKindDecidesTheIdleWake) never falls inside it: serve spools about 250 messages a second (a file each, synced),
// so the burst takes seconds to land. The seq numbers drained here stand in for a hook; what a hook shows, and in what
// order, is TestBoundaryBurstDrainShowsUrgentFirstAndEachOnce in cmd/mash.
func TestBoundaryBurstWakesOnce(t *testing.T) {
	if SpoolWakeCount != 500 {
		t.Fatalf("SpoolWakeCount is %d: this test is written for the default 500", SpoolWakeCount)
	}
	const fyi, total = 600, 601
	f := newFixture(t, "a", "b")
	f.idle = 20 * time.Second
	a, ev, sp := f.runSpooled(t, "a")
	if err := sp.Touch(); err != nil {
		t.Fatal(err)
	}
	b := f.client(t, "b")
	ctx := context.Background()
	for i := 1; i <= fyi; i++ {
		if _, err := b.SendWith(ctx, "a", "fyi "+strconv.Itoa(i), SendOptions{Kind: kind.FYI}); err != nil {
			t.Fatal(err)
		}
	}
	for start := time.Now(); len(waiting(t, sp)) != fyi; time.Sleep(50 * time.Millisecond) {
		if time.Since(start) > 20*time.Second {
			t.Fatalf("%d of %d spooled after 20 s", len(waiting(t, sp)), fyi)
		}
	}
	got := ev.waitFor(t, 1, 5*time.Second)
	time.Sleep(time.Second)
	if got = ev.all(); len(got) != 1 || strings.HasPrefix(got[0], "urgent") {
		t.Fatalf("%d wakes for the backlog, want exactly 1 and not urgent: %q", len(got), got)
	}
	if seq, err := b.SendWith(ctx, "a", "the build is red", SendOptions{Kind: kind.Task, Urgent: true}); err != nil || seq != total {
		t.Fatalf("urgent send: #%d %v", seq, err)
	}
	until(t, "601 spooled", func() bool { return len(waiting(t, sp)) == total })
	got = ev.waitFor(t, 2, 5*time.Second)
	time.Sleep(time.Second)
	if got = ev.all(); len(got) != 2 || !strings.HasPrefix(got[1], "urgent, 601 messages") {
		t.Fatalf("want the backlog wake and one urgent ring, got %d: %q", len(got), got)
	}

	// The urgent message is held with its flag, and a drain shows it before the older mail (Present's order; the hook
	// output is asserted in cmd/mash): drain as the hook does, urgent first, then oldest first, 10 a run.
	ws := waiting(t, sp)
	urgent := 0
	for _, e := range ws {
		if e.Urgent {
			urgent++
			if e.Seq != total {
				t.Fatalf("#%d is marked urgent, want #%d", e.Seq, total)
			}
		}
	}
	if urgent != 1 {
		t.Fatalf("%d urgent entries spooled, want 1", urgent)
	}
	drained := map[uint64]int{}
	drain := func(seq uint64) {
		t.Helper()
		if drained[seq]++; drained[seq] > 1 {
			t.Fatalf("#%d drained twice", seq)
		}
		time.Sleep(3 * time.Millisecond) // a hook run takes time: the held count stays above 500 across many settles, and none may wake
		if err := sp.Drained(spool.Ref{Seq: seq}); err != nil {
			t.Fatal(err)
		}
	}
	drain(total)
	for seq := uint64(1); seq <= fyi; seq++ {
		drain(seq)
	}
	until(t, "all 601 acked", func() bool { return unread(t, a) == 0 })
	if n := len(drained); n != total {
		t.Fatalf("%d drained, want %d", n, total)
	}
	time.Sleep(time.Second)
	if got = ev.all(); len(got) != 2 {
		t.Fatalf("%d wakes after the drain, want still 2: %q", len(got), got)
	}
	if w := waiting(t, sp); len(w) != 0 {
		t.Fatalf("%d entries still waiting after the drain", len(w))
	}
}

// storedBytes is what the serve side counts for a held message (storedSize): body, subject and headers.
func storedBytes(t *testing.T, f *fixture, name string, seq uint64) int {
	t.Helper()
	s, err := adminJS(t, f).Stream(context.Background(), sap.InboxStream(name))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.GetMsg(context.Background(), seq)
	if err != nil {
		t.Fatal(err)
	}
	n := len(raw.Data) + len(raw.Subject)
	for k, vs := range raw.Header {
		if k == jetstream.MsgIDHeader || !strings.HasPrefix(k, "Nats-") { // GetMsg adds Nats-Sequence, -Stream, -Subject and -Time-Stamp
			for _, v := range vs {
				n += len(k) + len(v) + 4
			}
		}
	}
	return n
}

// The byte edge: fyi mail wakes the session once the held bytes (headers and subject included, as the inbox stream
// counts them) reach SpoolWakeBytes. One byte below the threshold nothing wakes, over several idle-wake periods; at
// it, exactly one wake. The threshold is set from the stored size of two held messages, measured on the stream, so
// the edge is exact; the default (a quarter of InboxMaxBytes) is checked to be a quarter by
// TestSpoolWakeBytesIsWellBelowTheInboxLimit.
func TestBoundaryFyiBytesAtTheWakeThreshold(t *testing.T) {
	for _, c := range []struct {
		name  string
		extra int // threshold = the two messages' stored size + extra
		wakes int
	}{
		{"one byte below", 1, 0},
		{"at the threshold", 0, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, "a", "b")
			f.idle = 300 * time.Millisecond
			b := f.client(t, "b")
			ctx := context.Background()
			for i := 0; i < 2; i++ {
				if _, err := b.SendWith(ctx, "a", payload(int64(i), 64<<10), SendOptions{Kind: kind.FYI}); err != nil {
					t.Fatal(err)
				}
			}
			held := storedBytes(t, f, "a", 1) + storedBytes(t, f, "a", 2)
			old := SpoolWakeBytes
			SpoolWakeBytes = held + c.extra // before serve starts: it reads the variable on every check
			t.Cleanup(func() { SpoolWakeBytes = old })
			_, ev, sp := f.runSpooled(t, "a")
			if err := sp.Touch(); err != nil {
				t.Fatal(err)
			}
			until(t, "2 spooled", func() bool { return len(waiting(t, sp)) == 2 })
			if c.wakes > 0 {
				ev.waitFor(t, 1, 5*time.Second)
				time.Sleep(f.idle) // the re-wake is 2x the idle wake after the first
			} else {
				time.Sleep(5 * f.idle) // past the idle wake and the first re-wake
			}
			if got := ev.all(); len(got) != c.wakes {
				t.Fatalf("%d wakes with %d bytes held and the threshold at %d, want %d: %q", len(got), held, SpoolWakeBytes, c.wakes, got)
			}
		})
	}
}
