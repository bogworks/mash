package endpoint

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// cfgState is cfg(name) with its own state directory and the refusal of unverified mail on or off.
func (f *fixture) cfgState(name, state string, accept bool) config.Config {
	c := f.cfg(name)
	c.StateDir, c.AcceptUnverified = state, accept
	return c
}

// serveCfg runs the endpoint cfg names until the returned stop is called.
func serveCfg(t *testing.T, cfg config.Config) (*Endpoint, *events, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done; ep.Close() }) }
	t.Cleanup(stop)
	return ep, ev, stop
}

// publishUnverified stores one message of each unverified label in a's inbox and returns their labels in order.
func (f *fixture) publishUnverified(t *testing.T, to string) []sap.Trust {
	t.Helper()
	subj := taddr(to).Subjects().Mail
	nc := f.e.ConnAs(t, f.creds["b"], tkey("b"))
	id := identity(t, f.creds["b"])
	publishRaw(t, nc, subj, "", "absent body")
	xcreds, _ := f.e.Mint(t, taddr("x")) // a valid user, never provisioned
	xh, _ := identity(t, xcreds).Sign(subj, []byte("unknown body"), tkey("x"), time.Now(), "")
	publishRaw(t, f.e.ConnAs(t, xcreds, tkey("x")), subj, xh.Encode(), "unknown body")
	claimed := sap.SenderHeader{V: 1, Account: id.Account, User: id.User, Name: tkey("b")}
	publishRaw(t, nc, subj, claimed.Encode(), "claimed body")
	sh, _ := id.Sign(subj, []byte("stale body"), tkey("b"), time.Now().Add(-10*time.Minute), "")
	publishRaw(t, nc, subj, sh.Encode(), "stale body")
	return []sap.Trust{sap.TrustAbsent, sap.TrustUnknownSigner, sap.TrustClaimed, sap.TrustStale}
}

func waitRefused(t *testing.T, cfg config.Config, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if RefusedCount(cfg) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("refused count = %d, want %d", RefusedCount(cfg), want)
}

// Only verified mail is delivered. Each other label is dropped and counted in the file, which survives a restart; the
// sender is told nothing; neither the listing, a read nor the inbox counts show the refused mail.
func TestUnverifiedMailIsRefusedByDefault(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	labels := f.publishUnverified(t, "a")
	b := f.client(t, "b")
	seq, err := b.Send(context.Background(), tdot("a"), "hello a", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, ev, stop := serveCfg(t, cfg)
	got := ev.waitFor(t, 1, 5*time.Second)
	if !strings.Contains(got[0], "[verified]") || !strings.Contains(got[0], "hello a") {
		t.Fatalf("event: %s", got[0])
	}
	waitRefused(t, cfg, len(labels))
	time.Sleep(300 * time.Millisecond)
	if all := strings.Join(ev.all(), "\n"); strings.Contains(all, "absent body") || strings.Contains(all, "claimed body") ||
		strings.Contains(all, "unknown body") || strings.Contains(all, "stale body") || len(ev.all()) != 1 {
		t.Fatalf("unverified mail reached the session:\n%s", all)
	}
	dir, _ := SpoolDir(cfg)
	if b, err := os.ReadFile(filepath.Join(dir, RefusedFile)); err != nil || string(b) != "4\n" {
		t.Fatalf("count file %q %v", b, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 2 { // the count and its sequences: no temporary file left behind
		t.Fatalf("spool dir holds %v", ents)
	}
	// The sender is told nothing: b has no reply in its own inbox.
	if ms, err := f.client(t, "b").Inbox(context.Background(), 20); err != nil || len(ms) != 0 {
		t.Fatalf("the sender's inbox: %+v %v", ms, err)
	}

	// The listing, a read and the counts leave the refused mail out; the stream still holds it.
	c, err := OpenClient(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ms, err := c.Inbox(context.Background(), 20)
	if err != nil || len(ms) != 1 || ms[0].Seq != seq || ms[0].Trust != sap.TrustVerified {
		t.Fatalf("inbox: %+v %v", ms, err)
	}
	r, err := c.ReadInbox(context.Background(), InboxQuery{Limit: 20})
	if err != nil || len(r.Mail) != 1 || r.State.Total != 1 {
		t.Fatalf("ReadInbox: %d messages, total %d, %v", len(r.Mail), r.State.Total, err)
	}
	for n := uint64(1); n <= 5; n++ {
		m, err := c.Read(context.Background(), n)
		var rf *RefusedMessageError
		if n == seq {
			if err != nil || m.Trust != sap.TrustVerified {
				t.Fatalf("read #%d: %+v %v", n, m, err)
			}
		} else if !errors.As(err, &rf) || rf.Seq != n {
			t.Fatalf("read #%d of refused mail: %+v %v", n, m, err)
		}
	}
	var nm *NoMessageError
	if _, err := c.Read(context.Background(), 99); !errors.As(err, &nm) {
		t.Fatalf("read of a number that never existed: %v", err)
	}

	// The count persists across a restart, and mail refused before it is not counted again.
	stop()
	f.publishUnverified(t, "a")
	_, _, _ = serveCfg(t, cfg)
	waitRefused(t, cfg, 8)
	time.Sleep(300 * time.Millisecond)
	if n := RefusedCount(cfg); n != 8 {
		t.Fatalf("count after the restart %d, want 8", n)
	}
}

// With accept_unverified the mail is delivered with its label, as before, and nothing is counted.
func TestAcceptUnverifiedDeliversLabelled(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), true)
	f.publishUnverified(t, "a")
	_, ev, _ := serveCfg(t, cfg)
	// The four messages may arrive as separate deliveries: wait for every label, not for the first event.
	want := []string{"[absent]", "[unknown-signer]", "[claimed]", "[stale]"}
	var got string
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		got = strings.Join(ev.all(), "\n")
		missing := 0
		for _, l := range want {
			if !strings.Contains(got, l) {
				missing++
			}
		}
		if missing == 0 || time.Now().After(deadline) {
			break
		}
	}
	for _, l := range want {
		if !strings.Contains(got, l) {
			t.Errorf("no %s in\n%s", l, got)
		}
	}
	if n := RefusedCount(cfg); n != 0 {
		t.Fatalf("count %d, want 0", n)
	}
	c, err := OpenClient(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if ms, err := c.Inbox(context.Background(), 20); err != nil || len(ms) != 4 {
		t.Fatalf("inbox: %d %v", len(ms), err)
	}
}

// Forged mail stays dropped and uncounted as before (it is not unverified mail, it is never shown).
func TestForgedMailIsNotCountedAsRefused(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	subj := taddr("a").Subjects().Mail
	id := identity(t, f.creds["b"])
	h, _ := id.Sign(subj, []byte("forged body"), tkey("b"), time.Now(), "")
	h.Sig = strings.Repeat("A", len(h.Sig))
	publishRaw(t, f.e.ConnAs(t, f.creds["b"], tkey("b")), subj, h.Encode(), "forged body")
	a, _, _ := serveCfg(t, cfg)
	deadline := time.Now().Add(5 * time.Second)
	for a.Dropped() != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if a.Dropped() != 1 || RefusedCount(cfg) != 0 {
		t.Fatalf("dropped %d, refused %d", a.Dropped(), RefusedCount(cfg))
	}
}

// Concurrent arrivals lose no count, and the file is always whole.
func TestRefusedCountConcurrent(t *testing.T) {
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	e := &Endpoint{cfg: cfg}
	id := "inbox@now"
	e.refusedStream.Store(&id)
	const n = 60
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.recordRefusal(uint64(i)); err != nil {
				t.Error(err)
			}
			_ = RefusedCount(cfg)
		}()
	}
	wg.Wait()
	if got := RefusedCount(cfg); got != n {
		t.Fatalf("count %d, want %d", got, n)
	}
	if got := e.refusedStored(1, n, id); got != n {
		t.Fatalf("stored %d, want %d", got, n)
	}
	if got := e.refusedStored(31, n, id); got != n-30 {
		t.Fatalf("stored above 30: %d", got)
	}
}

// A kill between the sequence write and the count write leaves the count one low; the redelivery repairs it, a normal
// refusal counts once and a redelivery of a counted one does not count again (#1069).
func TestRefusalCountRepairedAfterKill(t *testing.T) {
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	e := &Endpoint{cfg: cfg}
	id := "inbox@now"
	e.refusedStream.Store(&id)
	for _, s := range []uint64{1, 2} {
		if n, added, err := e.recordRefusalNew(s); err != nil || !added || n != int(s) {
			t.Fatalf("refusal %d: n=%d added=%v err=%v", s, n, added, err)
		}
	}
	if n, added, _ := e.recordRefusalNew(2); added || n != 2 {
		t.Fatalf("redelivery double counted: n=%d added=%v", n, added)
	}
	// The kill: seq 3 recorded, the count still 2.
	dir, _ := SpoolDir(cfg)
	f, err := os.OpenFile(filepath.Join(dir, refusedSeqsFile), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("3\n")
	f.Close()
	if n := RefusedCount(cfg); n != 2 {
		t.Fatalf("setup: count %d", n)
	}
	if n, added, err := e.recordRefusalNew(3); err != nil || added || n != 3 {
		t.Fatalf("redelivery: n=%d added=%v err=%v", n, added, err)
	}
	if n := RefusedCount(cfg); n != 3 {
		t.Fatalf("count after repair = %d, want 3", n)
	}
	if n, added, _ := e.recordRefusalNew(3); added || n != 3 {
		t.Fatalf("second redelivery: n=%d added=%v", n, added)
	}
	if n, added, _ := e.recordRefusalNew(4); !added || n != 4 {
		t.Fatalf("next refusal: n=%d added=%v", n, added)
	}
}

// Without a state directory nothing is written (a relative path would litter the working directory).
func TestRefusalNeedsAStateDir(t *testing.T) {
	e := &Endpoint{cfg: config.Config{Owner: "acme", Group: "demo", Name: "bob"}}
	if _, err := e.recordRefusal(1); err == nil {
		t.Fatal("recorded with no state directory")
	}
}

// The log line of a refusal is exactly "refused message <seq> from <claimed address>: not verified (<label>)".
func TestRefusalLogLine(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	f.publishUnverified(t, "a")
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var buf lockedLog
	ep.log = log.New(&buf, "", 0)
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, (&events{}).deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	waitRefused(t, cfg, 4)
	// the refusal is recorded before its line is logged: wait for the fourth line rather than read the log at once
	for deadline := time.Now().Add(10 * time.Second); strings.Count(buf.String(), "not verified (") < 4 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	got := buf.String()
	for _, want := range []string{
		"refused message 1 from (unknown sender): not verified (absent)\n",
		"refused message 3 from " + tkey("b") + ": not verified (claimed)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "in all") || strings.Contains(got, "unverified mail") {
		t.Errorf("old wording in\n%s", got)
	}
	if n := strings.Count(got, "not verified ("); n != 4 {
		t.Errorf("%d refusal lines, want 4:\n%s", n, got)
	}
}

type lockedLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func TestRefusedSenderEscapes(t *testing.T) {
	for in, want := range map[string]string{
		"":                     "(unknown sender)",
		"?":                    "(unknown sender)",
		"acme.demo.x":          "acme.demo.x",
		"a\nrefused message 9": `a\x0arefused message 9`,
		"a\tb\x1b[31m":         `a\x09b\x1b[31m`,
	} {
		if got := refusedSender(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// A sender created after the directory was read is not unknown: its verified mail is delivered, not refused. The
// record is read once, for that sender, before the message is called unknown-signer.
func TestVerifiedMailFromANewSenderIsDelivered(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	_, ev, _ := serveCfg(t, cfg) // the directory is read now: it has a and b only
	f.add(t, "c")
	if _, err := f.client(t, "c").Send(context.Background(), tdot("a"), "hello from c", 0); err != nil {
		t.Fatal(err)
	}
	got := ev.waitFor(t, 1, 5*time.Second)
	if !strings.Contains(got[0], "[verified]") || !strings.Contains(got[0], "hello from c") {
		t.Fatalf("event: %s", got[0])
	}
	if n := RefusedCount(cfg); n != 0 {
		t.Fatalf("refused %d", n)
	}
}

// The count and the sequences belong to one stream: a recreated stream restarts its sequences, and mail that
// arrives in it is not read as refused by the old stream's list.
func TestRefusalFilesAreTiedToTheStream(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	f.publishUnverified(t, "a")
	_, _, stop := serveCfg(t, cfg)
	waitRefused(t, cfg, 4)
	stop()

	ctx := context.Background()
	ajs, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := ajs.DeleteStream(ctx, taddr("a").InboxStream()); err != nil {
		t.Fatal(err)
	}
	s, err := ajs.CreateStream(ctx, store.InboxConfig(taddr("a")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOrUpdateConsumer(ctx, store.ConsumerConfig()); err != nil {
		t.Fatal(err)
	}
	seq, err := f.client(t, "b").Send(ctx, tdot("a"), "after the recreation", 0)
	if err != nil {
		t.Fatal(err)
	}
	if seq > 4 {
		t.Fatalf("sequence %d did not restart", seq)
	}

	// Before any serve: the stale list is not applied to the new stream.
	c, err := OpenClient(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if m, err := c.Read(ctx, seq); err != nil || m.Trust != sap.TrustVerified {
		t.Fatalf("read of new mail: %+v %v", m, err)
	}
	if r, err := c.ReadInbox(ctx, InboxQuery{Limit: 20}); err != nil || r.State.Total != 1 || len(r.Mail) != 1 {
		t.Fatalf("ReadInbox: %+v %v", r, err)
	}

	// A serve on the new stream delivers the mail, and the first refusal there starts a new count.
	f.publishUnverified(t, "a")
	_, ev, _ := serveCfg(t, cfg)
	if got := ev.waitFor(t, 1, 5*time.Second); !strings.Contains(got[0], "after the recreation") {
		t.Fatalf("event: %s", got[0])
	}
	waitRefused(t, cfg, 4)
	time.Sleep(300 * time.Millisecond)
	if n := RefusedCount(cfg); n != 4 {
		t.Fatalf("the old stream's count survives: %d, want 4", n)
	}
}

// A record is kept for every sequence the stream still holds; only those below its first sequence are pruned.
func TestRefusedSeqsKeptWhileStreamHoldsThem(t *testing.T) {
	old := refusedSeqsKeep
	refusedSeqsKeep = 10
	t.Cleanup(func() { refusedSeqsKeep = old })
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	e := &Endpoint{cfg: cfg}
	id := "inbox@now"
	e.refusedStream.Store(&id)
	first := uint64(1)
	e.firstSeqFn = func(context.Context) (uint64, error) { return first, nil }
	const n = 3 * 10
	for i := uint64(1); i <= n; i++ {
		if _, err := e.recordRefusal(i); err != nil {
			t.Fatal(err)
		}
	}
	if got := RefusedCount(cfg); got != n {
		t.Fatalf("count %d", got)
	}
	for i := uint64(1); i <= n; i++ {
		if !e.refusedSeq(i, id) {
			t.Fatalf("sequence %d no longer reads as refused", i)
		}
	}
	first = 25 // the stream dropped everything below 25
	for i := uint64(n + 1); i <= n+2; i++ {
		if _, err := e.recordRefusal(i); err != nil {
			t.Fatal(err)
		}
	}
	for i := uint64(1); i <= n+2; i++ {
		if want := i >= 25; e.refusedSeq(i, id) != want {
			t.Fatalf("sequence %d refused=%v, want %v", i, !want, want)
		}
	}
}

// Recording is idempotent by sequence: a redelivery after a lost ack does not count twice.
func TestRecordRefusalIdempotent(t *testing.T) {
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	e := &Endpoint{cfg: cfg}
	id := "inbox@now"
	e.refusedStream.Store(&id)
	for i := 0; i < 3; i++ {
		if n, err := e.recordRefusal(7); err != nil || n != 1 {
			t.Fatalf("record %d: %d %v", i, n, err)
		}
	}
	if n, _ := e.recordRefusal(8); n != 2 {
		t.Fatalf("count %d", n)
	}
}

// A sequence that cannot be recorded is an error, and nothing is counted.
func TestRecordRefusalFailsWhenSequenceUnwritable(t *testing.T) {
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	e := &Endpoint{cfg: cfg}
	id := "inbox@now"
	e.refusedStream.Store(&id)
	if _, err := e.recordRefusal(1); err != nil {
		t.Fatal(err)
	}
	dir, _ := SpoolDir(cfg)
	p := filepath.Join(dir, refusedSeqsFile)
	if err := os.Remove(p); err != nil { // a directory in its place: neither append nor replace can work
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := e.recordRefusal(2); err == nil {
		t.Fatal("recorded with an unwritable sequence file")
	}
	if got := RefusedCount(cfg); got != 1 {
		t.Fatalf("count %d, want 1", got)
	}
}

// Seq 4 is refused before seq 3 is redelivered (out of order), and a handover left seq 3 on two lines. The count is
// exact after the redelivery, and the duplicate line does not keep an overcount (#1310).
func TestRefusalCountExactOutOfOrderWithDuplicateLine(t *testing.T) {
	cfg := config.Config{Owner: "acme", Group: "demo", Name: "bob", StateDir: t.TempDir()}
	e := &Endpoint{cfg: cfg}
	id := "inbox@now"
	e.refusedStream.Store(&id)
	for _, s := range []uint64{1, 2} {
		if _, added, err := e.recordRefusalNew(s); err != nil || !added {
			t.Fatalf("refusal %d: added=%v err=%v", s, added, err)
		}
	}
	// The kill: seq 3 recorded (twice, as two handover processes can append it), the count still 2.
	dir, _ := SpoolDir(cfg)
	p := filepath.Join(dir, refusedSeqsFile)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("3\n3\n")
	f.Close()
	// Seq 4 is refused before seq 3 is redelivered; the repair inside it counts 3 once, then 4.
	if n, added, err := e.recordRefusalNew(4); err != nil || !added || n != 4 {
		t.Fatalf("seq 4: n=%d added=%v err=%v", n, added, err)
	}
	if n := RefusedCount(cfg); n != 4 {
		t.Fatalf("count after seq 4 = %d, want 4", n)
	}
	// Now seq 3 is redelivered: already counted, nothing more.
	if n, added, err := e.recordRefusalNew(3); err != nil || added || n != 4 {
		t.Fatalf("redelivery of 3: n=%d added=%v err=%v", n, added, err)
	}
	if err := e.reconcileRefusedCount(id); err != nil {
		t.Fatal(err)
	}
	if n := RefusedCount(cfg); n != 4 {
		t.Fatalf("count after redelivery = %d, want 4", n)
	}
	if _, seqs := readRefusedSeqs(p); len(seqs) != 4 {
		t.Fatalf("distinct sequences %v, want 4", seqs)
	}
	if got := e.refusedStored(1, 4, id); got != 4 {
		t.Fatalf("stored %d, want 4", got)
	}
	// Duplicate line with the count already exact: the repair leaves it alone (no overcount).
	f, _ = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("4\n4\n")
	f.Close()
	if err := e.reconcileRefusedCount(id); err != nil {
		t.Fatal(err)
	}
	if n := RefusedCount(cfg); n != 4 {
		t.Fatalf("count with a duplicate line = %d, want 4", n)
	}
}
