package endpoint

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
)

// Characterisation tests for naming plan 1.4a: with one stream, message numbering, the spool's file names, the
// dedupe, the batch map, the ack and the waking count behave as they did when everything was keyed by the bare
// stream sequence. They passed before the refactor to (stream, sequence) keys; only the helpers in numbering_keys_test.go changed with it.

// dblAck is a jetstream.Msg that counts what is done to it, DoubleAck included.
type dblAck struct {
	jetstream.Msg
	acks, dacks, naks int
}

func (a *dblAck) Ack() error                       { a.acks++; return nil }
func (a *dblAck) DoubleAck(context.Context) error  { a.dacks++; return nil }
func (a *dblAck) NakWithDelay(time.Duration) error { a.naks++; return nil }
func (a *dblAck) Subject() string                  { return "" }
func (a *dblAck) InProgress() error                { return nil }
func (a *dblAck) Headers() nats.Header             { return nil }

func gMail(seq uint64, body string) render.Mail {
	return render.Mail{Seq: seq, From: "b", Trust: sap.TrustVerified, At: time.Unix(1000+int64(seq), 0).UTC(), Body: []byte(body), Kind: "task"}
}

func gFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func gEndpoint(t *testing.T) (*Endpoint, spool.Spool) {
	t.Helper()
	sp := spool.Spool{Dir: filepath.Join(t.TempDir(), "spool", "o", "a")}
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	e := &Endpoint{log: log.New(io.Discard, "", 0), waking: newWakingTracker(), spool: &sp}
	e.cfg.Name, e.cfg.Owner = "a", "o"
	return e, sp
}

// The spool entry of a message is the same bytes it was, and a spooled message comes back as the same Mail.
func TestGoldenEntryAndMailRoundTrip(t *testing.T) {
	m := gMail(7, "hi")
	b, err := json.Marshal(EntryOf(m))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"seq":7,"from":"b","trust":"verified","kind":"task","at":"1970-01-01T00:16:47Z","body":"aGk="}`
	if string(b) != want {
		t.Fatalf("entry %s\nwant  %s", b, want)
	}
	if got := MailOf(EntryOf(m)); !reflect.DeepEqual(got, m) {
		t.Fatalf("round trip %+v", got)
	}
}

// What the model sees: the numbers in a batch, a digest, a one-line listing and a single message are the bare #n.
func TestGoldenRenderedNumbers(t *testing.T) {
	ms := []render.Mail{gMail(3, "one"), gMail(12, "two")}
	out := render.Batch("a", "o", ms, 10, 10000)
	for _, want := range []string{"#3 from b", "#12 from b"} {
		if !strings.Contains(out, want) {
			t.Errorf("batch lacks %q:\n%s", want, out)
		}
	}
	if got := render.One(ms[1]); !strings.HasPrefix(got, "--- #12 from b [verified] at ") {
		t.Errorf("one: %q", got)
	}
	if got := render.Line(ms[0]); !strings.HasPrefix(got, "#3\tfrom b [verified]\t") {
		t.Errorf("line: %q", got)
	}
	many := make([]render.Mail, 0, 40)
	for i := 1; i <= 40; i++ {
		many = append(many, gMail(uint64(i), "m"))
	}
	dig := render.Batch("a", "o", many, 5, 10000)
	if !strings.Contains(dig, "#40\tfrom b") && !strings.Contains(dig, "#1\tfrom b") {
		t.Errorf("digest carries no bare numbers:\n%s", dig)
	}
	re := gMail(4, "x")
	re.Redelivered = true
	if got := render.One(re); !strings.Contains(got, "#4") {
		t.Errorf("redelivered note: %q", got)
	}
}

// Spooling, redelivery of a held message, ack of a drained one and the waking count, for one stream.
func TestGoldenSpoolBatchDedupeAckAndWaking(t *testing.T) {
	e, sp := gEndpoint(t)
	h := gHeld()
	seen := newSeenSet(0)
	m5, m6 := &dblAck{}, &dblAck{}
	p5 := pending{mail: gMail(5, "five"), msg: m5, key: "u\x00n5"}
	p6 := pending{mail: gMail(6, "six"), msg: m6, key: "u\x00n6"}
	gAdd(e.waking, 5, true)
	gAdd(e.waking, 6, true)
	e.spoolBatch(nil, []pending{p5, p6}, h, seen)
	if got := gFiles(t, sp.Dir); !reflect.DeepEqual(got, []string{"5.json", "6.json"}) {
		t.Fatalf("spool files %v", got)
	}
	if !gHeldHas(h, 5) || !gHeldHas(h, 6) || len(h.msgs) != 2 || h.bytes != 7 || gSeen(seen) != 2 {
		t.Fatalf("held %d, bytes %d, seen %d", len(h.msgs), h.bytes, gSeen(seen))
	}
	if _, ok := e.waking.snapshot(); ok {
		t.Fatal("count known before the consumer started")
	}
	e.waking.start(0, 0, true, nil)
	if n, ok := e.waking.snapshot(); !ok || n != 2 {
		t.Fatalf("waking count %d ok=%v, want 2", n, ok)
	}
	// A redelivery of 5 replaces the held handle and is neither acked nor shown.
	fresh := &dblAck{}
	if got := e.dedupe([]pending{{mail: gMail(5, "five"), msg: fresh, key: "u\x00n5"}}, seen, h); len(got) != 0 || fresh.acks+fresh.dacks != 0 || gHeldMsg(h, 5) != jetstream.Msg(fresh) {
		t.Fatalf("redelivery: shown %d, handle replaced %v", len(got), gHeldMsg(h, 5) == jetstream.Msg(fresh))
	}
	// A hook drains 5: settleHeld double-acks it with the newest handle, forgets it, and drops the count.
	gDrain(t, sp, 5)
	e.settleHeld(nil, h)
	if fresh.dacks != 1 || m5.dacks != 0 || gHeldHas(h, 5) || !gHeldHas(h, 6) || h.bytes != 3 {
		t.Fatalf("after drain: dacks fresh=%d old=%d held5=%v held6=%v bytes=%d", fresh.dacks, m5.dacks, gHeldHas(h, 5), gHeldHas(h, 6), h.bytes)
	}
	if gSpoolDrained(sp, 5) || gSpoolHas(sp, 5) {
		t.Fatal("Acked left 5's tombstone or file")
	}
	if n, _ := e.waking.snapshot(); n != 1 {
		t.Fatalf("waking count after the ack: %d, want 1", n)
	}
	if got := gFiles(t, sp.Dir); !reflect.DeepEqual(got, []string{"6.json"}) {
		t.Fatalf("spool files after ack %v", got)
	}
	// A tombstone that survived a restart: a delivery of a drained message is acked, never spooled.
	gDrain(t, sp, 9)
	late := &dblAck{}
	e.spoolBatch(nil, []pending{{mail: gMail(9, "nine"), msg: late, key: "u\x00n9"}}, h, seen)
	if late.dacks != 1 || gHeldHas(h, 9) || gSpoolHas(sp, 9) {
		t.Fatalf("drained redelivery: dacks=%d held=%v", late.dacks, gHeldHas(h, 9))
	}
}

// healHeld rewrites a held message whose file went missing, under the same name.
func TestGoldenHealHeld(t *testing.T) {
	e, sp := gEndpoint(t)
	h := gHeld()
	e.spoolBatch(nil, []pending{{mail: gMail(4, "four"), msg: &dblAck{}}}, h, newSeenSet(0))
	if err := os.Remove(filepath.Join(sp.Dir, "4.json")); err != nil {
		t.Fatal(err)
	}
	e.healHeld(h)
	if got := gFiles(t, sp.Dir); !reflect.DeepEqual(got, []string{".lock", "4.json"}) {
		t.Fatalf("after heal %v", got)
	}
}

// The waking tracker: listed stops counting but keeps the message; a message added twice counts once; backlog
// accounting counts each startup sequence once.
func TestGoldenWakingTracker(t *testing.T) {
	w := newWakingTracker()
	w.start(2, 10, true, nil)
	if _, ok := w.snapshot(); ok {
		t.Fatal("known before the backlog arrived")
	}
	gAdd(w, 3, true)
	gAdd(w, 3, true)
	gAdd(w, 4, false)
	gReceived(w, 3)
	gReceived(w, 3)
	if _, ok := w.snapshot(); ok {
		t.Fatal("a redelivery stood in for a message still outstanding")
	}
	gReceived(w, 4)
	gReceived(w, 11) // after start: never counts
	if n, ok := w.snapshot(); !ok || n != 1 {
		t.Fatalf("count %d ok=%v, want 1 known", n, ok)
	}
	gListedW(w, 3)
	if n, _ := w.snapshot(); n != 0 {
		t.Fatalf("after listed: %d", n)
	}
	gDone(w, 3)
	gDone(w, 4)
	gDone(w, 4)
	if n, _ := w.snapshot(); n != 0 || len(w.unread) != 0 {
		t.Fatalf("after done: %d, unread %d", n, len(w.unread))
	}
}

// The seen set: the same sequence is a redelivery; a new sequence with a known nonce inside the window is a replay.
func TestGoldenSeenSet(t *testing.T) {
	s := newSeenSet(time.Minute)
	a := pending{key: "u\x00n", mail: gMail(5, "")}
	s.mark(a)
	if !s.dup(pending{key: "u\x00other", mail: gMail(5, "")}) {
		t.Fatal("same sequence under another nonce is a redelivery")
	}
	if !s.dup(pending{key: "u\x00n", mail: gMail(6, "")}) {
		t.Fatal("a new sequence inside the window is a replay")
	}
	if s.dup(pending{key: "u\x00new", mail: gMail(7, "")}) {
		t.Fatal("a new message is not a dup")
	}
}
