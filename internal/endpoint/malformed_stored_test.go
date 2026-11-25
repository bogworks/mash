package endpoint

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// The stored path of REQ-006 on a live stream: the ten shapes, the X2 and X3 candidates and the baseline malformed cases
// are recorded with their class and never delivered; the one-time scan runs before consumption. These start an in-process
// NATS server: run them in a container (see the work order).

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// serveLogged is serveCfg with the endpoint's log going to a buffer, and a hook run on the endpoint before Run.
func serveLogged(t *testing.T, cfg config.Config, before func(*Endpoint)) (*Endpoint, *events, *syncBuf, func(), <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	buf := &syncBuf{}
	ep.log = log.New(buf, "", 0)
	if before != nil {
		before(ep)
	}
	ev := &events{}
	runErr := make(chan error, 1)
	done := make(chan struct{})
	go func() { runErr <- ep.Run(ctx, ev.deliver); close(done) }()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done; ep.Close() }) }
	t.Cleanup(stop)
	return ep, ev, buf, stop, runErr
}

type malformedCase struct {
	name   string
	values []string // every Agent-Sender value; nil for no header
	class  string   // "" when the message is not malformed
}

// malformedHeaders builds the fixtures for the sender b of f, publishing to subj. The headers are written by hand: the
// parser refuses them before it checks a signature, so a signature of the right form that does not verify is enough.
func malformedHeaders(account, user, subj string) []malformedCase {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	zeroSig := base64.RawURLEncoding.EncodeToString(make([]byte, 64))
	const ts, nonce = "2026-11-15T12:00:00Z", "nonce-1"
	member := func(k, v string) string { return q(k) + ":" + v }
	obj := func(ms ...string) string { return "{" + strings.Join(ms, ",") + "}" }
	signed := func(over map[string]string, extra ...string) string {
		f := map[string]string{"v": "1", "account": q(account), "user": q(user), "sub": q(subj), "ts": q(ts), "nonce": q(nonce), "sig": q(zeroSig)}
		for k, v := range over {
			f[k] = v
		}
		ms := []string{}
		for _, k := range []string{"v", "account", "user", "sub", "ts", "nonce", "sig"} {
			if f[k] != "" {
				ms = append(ms, member(k, f[k]))
			}
		}
		return obj(append(ms, extra...)...)
	}
	unsigned := func(ms ...string) string {
		return obj(append([]string{member("v", "1"), member("account", q(account)), member("user", q(user))}, ms...)...)
	}
	return []malformedCase{
		// the ten shapes (REQ-005)
		{"1 header too long", []string{signed(nil, member("pad", q(strings.Repeat("p", 2100))))}, "header too long"},
		{"2 time not in UTC", []string{signed(map[string]string{"ts": q("2026-11-15T13:00:00+01:00")})}, "time not in UTC"},
		{"3 control character in name", []string{unsigned(member("name", `"a\u0001b"`))}, "control character in name"},
		{"4 name too long", []string{unsigned(member("name", q(strings.Repeat("n", 65))))}, "name too long"},
		{"5 broken character in name", []string{unsigned(member("name", `"a\ud800b"`))}, "broken character in name"},
		{"6 a field appears twice", []string{signed(nil, member("user", q(user)))}, "a field appears twice"},
		{"7 space in subject", []string{signed(map[string]string{"sub": q("agents.mail.a b.o.n")})}, "space in subject"},
		{"8 signature unreadable", []string{signed(map[string]string{"sig": q("!!")})}, "signature unreadable"},
		{"9 empty sender header", []string{""}, "empty sender header"},
		{"10 two sender headers", []string{unsigned(), unsigned()}, "two sender headers"},
		// X2: invalid UTF-8; a sub with an empty token
		{"X2 invalid UTF-8", []string{"{\"v\":1,\"account\":\"\xff\"}"}, "unreadable sender header"},
		{"X2 empty token in sub", []string{signed(map[string]string{"sub": q("agents..a.o.n")})}, "unreadable sender header"},
		// X3: unsigned claims whose ts or nonce is not valid
		{"X3 time not valid", []string{unsigned(member("ts", q("yesterday")))}, "time not valid"},
		{"X3 message id not valid", []string{unsigned(member("nonce", q("bad nonce!")))}, "message id not valid"},
		// the baseline: what mash already refused as malformed
		{"baseline not JSON", []string{"not json"}, "header is not a JSON object"},
		{"baseline version missing", []string{obj(member("account", q(account)), member("user", q(user)))}, "version missing"},
		{"baseline v is a string", []string{obj(member("v", q("1")), member("account", q(account)), member("user", q(user)))}, "version is not a number"},
	}
}

func publishValues(t *testing.T, nc *nats.Conn, subj string, values []string, body string) {
	t.Helper()
	m := nats.NewMsg(subj)
	m.Data = []byte(body)
	for _, v := range values {
		m.Header.Add(sap.HeaderName, v)
	}
	if _, err := nc.RequestMsg(m, 15*time.Second); err != nil {
		t.Fatalf("raw publish: %v", err)
	}
}

// storedSeqOf is the stream sequence the message just published got: the stream's last.
func lastInboxSeq(t *testing.T, f *fixture, name string) uint64 {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	s, err := js.Stream(context.Background(), taddr(name).InboxStream())
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return info.State.LastSeq
}

// consumerState is the inbox consumer's ack floor and the number it holds unacked.
func malformedConsumerState(t *testing.T, f *fixture, name string) (floor uint64, unacked int) {
	t.Helper()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	c, err := js.Consumer(context.Background(), taddr(name).InboxStream(), store.Consumer)
	if err != nil {
		t.Fatal(err)
	}
	ci, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ci.AckFloor.Stream, ci.NumAckPending
}

// publishMalformedSet stores every case in a's inbox, from b, plus one ordinary message and one with no header at all; it
// returns each case's stream sequence, the sequence of the unsigned one and of the ordinary one.
func publishMalformedSet(t *testing.T, f *fixture) (seqs map[string]uint64, cases []malformedCase, absent, ok uint64) {
	t.Helper()
	subj := taddr("a").MailSubject()
	nc := f.e.ConnAs(t, f.creds["b"], tkey("b"))
	id := identity(t, f.creds["b"])
	cases = malformedHeaders(id.Account, id.User, subj)
	seqs = map[string]uint64{}
	for _, c := range cases {
		publishValues(t, nc, subj, c.values, "BODY "+c.name)
		seqs[c.name] = lastInboxSeq(t, f, "a")
	}
	publishValues(t, nc, subj, nil, "BODY absent")
	absent = lastInboxSeq(t, f, "a")
	if _, err := f.client(t, "b").Send(context.Background(), tdot("a"), "ordinary mail", 0); err != nil {
		t.Fatal(err)
	}
	ok = lastInboxSeq(t, f, "a")
	return
}

func requireNoBodies(t *testing.T, ev *events) {
	t.Helper()
	if all := strings.Join(ev.all(), "\n"); strings.Contains(all, "BODY ") {
		t.Fatalf("refused mail reached the session:\n%s", all)
	}
}

// Each shape, X2, X3 and the baseline is recorded with its class, logged once with the class only, never delivered, and
// terminally acknowledged; a message with no header at all is not malformed and stays unverified. The scan and the serve
// path agree: the set is stored before the first start (the scan records it), and a second set arrives while serving.
func TestMalformedMailIsRecordedNeverDeliveredAndAcked(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	seqs, cases, absent, okSeq := publishMalformedSet(t, f)

	_, ev, buf, stop, _ := serveLogged(t, cfg, nil)
	got := ev.waitFor(t, 1, 10*time.Second)
	if !strings.Contains(got[0], "ordinary mail") {
		t.Fatalf("event: %s", got[0])
	}
	dir, _ := SpoolDir(cfg)
	mp := filepath.Join(dir, malformedSeqsFile)
	check := func(label string, seqs map[string]uint64, firstRun bool) {
		t.Helper()
		l := readSeqList(mp)
		for _, c := range cases {
			var class string
			for _, en := range l.Entries {
				if en.Seq == seqs[c.name] {
					class = en.Class
				}
			}
			if class != c.class {
				t.Errorf("%s: %s (#%d): recorded as %q, want %q", label, c.name, seqs[c.name], class, c.class)
			}
			if n := strings.Count(buf.String(), "refused message "+itoa(seqs[c.name])+": "+c.class+"\n"); n != 1 {
				t.Errorf("%s: %s: %d log lines, want 1\n%s", label, c.name, n, buf.String())
			}
		}
	}
	check("stored before the start", seqs, true)
	requireNoBodies(t, ev)

	// The same set arriving while serving takes the serve path.
	seqs2, _, absent2, _ := publishMalformedSet(t, f)
	ev.waitFor(t, 2, 10*time.Second)
	waitFor(t, "the second set to be recorded", 15*time.Second, func() bool {
		return len(readSeqList(mp).Entries) == 2*len(cases)
	})
	check("arriving while serving", seqs2, false)
	requireNoBodies(t, ev)

	// The absent-header messages are not malformed: they are on the unverified list (accept_unverified is off), not here.
	l := readSeqList(mp)
	for _, en := range l.Entries {
		if en.Seq == absent || en.Seq == absent2 || en.Seq == okSeq {
			t.Fatalf("#%d is not malformed but is on the malformed list", en.Seq)
		}
	}
	waitFor(t, "the unverified refusals", 15*time.Second, func() bool { return RefusedCount(cfg) == 2 })
	if _, seqsU := readRefusedSeqs(filepath.Join(dir, refusedSeqsFile)); len(seqsU) != 2 || seqsU[0] != absent || seqsU[1] != absent2 {
		t.Fatalf("unverified list %v, want %d and %d", seqsU, absent, absent2)
	}
	if strings.Contains(buf.String(), "claimed") || strings.Contains(buf.String(), id0(t, f)) {
		t.Fatalf("the log carries header content:\n%s", buf.String())
	}

	// Terminal disposition: every message is acked, none held, none redelivered.
	last := lastInboxSeq(t, f, "a")
	waitFor(t, "the ack floor to pass every message", 15*time.Second, func() bool {
		floor, unacked := malformedConsumerState(t, f, "a")
		return floor == last && unacked == 0
	})
	stop()
}

func itoa(n uint64) string { return strconv.FormatUint(n, 10) }

func l0(t *testing.T, cfg config.Config) seqList {
	t.Helper()
	dir, _ := SpoolDir(cfg)
	return readSeqList(filepath.Join(dir, malformedSeqsFile))
}

func id0(t *testing.T, f *fixture) string { return identity(t, f.creds["b"]).User }

// A restart does not repeat a finished scan and logs no recorded message again; the scanned line persists.
func TestScanRunsOnceAndTheScannedLinePersists(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	_, cases, _, _ := publishMalformedSet(t, f)
	last := lastInboxSeq(t, f, "a")

	steps := 0
	_, ev, buf1, stop, _ := serveLogged(t, cfg, func(e *Endpoint) { e.scanStep = func(uint64) error { steps++; return nil } })
	ev.waitFor(t, 1, 10*time.Second)
	if steps == 0 {
		t.Fatal("the scan did not run on the first start")
	}
	if l := l0(t, cfg); !l.Done || l.Scanned != last || len(l.Entries) != len(cases) {
		t.Fatalf("after the scan: %+v (want scanned %d and %d entries)", l, last, len(cases))
	}
	if n := malformedLines(buf1.String()); n != len(cases) {
		t.Fatalf("%d log lines on the first start, want %d", n, len(cases))
	}
	stop()

	before := steps
	_, ev2, buf2, stop2, _ := serveLogged(t, cfg, func(e *Endpoint) { e.scanStep = func(uint64) error { steps++; return nil } })
	time.Sleep(500 * time.Millisecond)
	_ = ev2
	if steps != before {
		t.Fatalf("a restart repeated the finished scan (%d steps)", steps-before)
	}
	if malformedLines(buf2.String()) != 0 {
		t.Fatalf("a restart logged a recorded message again:\n%s", buf2.String())
	}
	if l := l0(t, cfg); !l.Done || l.Scanned != last {
		t.Fatalf("the scanned line did not persist: %+v", l)
	}
	stop2()
}

// A kill during the scan resumes: the half-done list stays, the restart finishes, logging only what is not yet recorded.
// Nothing is delivered before the scanned line is written, `scanning` shows while it runs (the shell's view reads the
// record it writes), and mail that arrives meanwhile waits in the stream.
func TestScanFinishesBeforeConsumptionAndResumesAfterAKill(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	_, cases, _, okSeq := publishMalformedSet(t, f)
	last := lastInboxSeq(t, f, "a")

	// First start: the scan is killed half-way.
	killAt := okSeq - 8
	_, ev, buf1, stop, runErr := serveLogged(t, cfg, func(e *Endpoint) {
		e.scanStep = func(seq uint64) error {
			if seq == killAt {
				return errors.New("killed")
			}
			return nil
		}
	})
	select {
	case err := <-runErr:
		if err == nil {
			t.Fatal("Run returned nil with the scan killed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the killed scan did not end the run")
	}
	if l := l0(t, cfg); l.Done || len(l.Entries) == 0 || len(l.Entries) >= len(cases) {
		t.Fatalf("after the kill: %+v (want some entries, no scanned line)", l)
	}
	if len(ev.all()) != 0 {
		t.Fatalf("delivered before the scan finished: %v", ev.all())
	}
	recorded := len(l0(t, cfg).Entries)
	stop()

	// Second start: held at a step so the state in the middle can be looked at.
	gate := make(chan struct{})
	hit := make(chan struct{})
	var once sync.Once
	sp := spool.Spool{Dir: t.TempDir()} // a bound spool, so serve records its state for a shell (no hook runs: delivery stays by event)
	ep, ev2, buf2, stop2, _ := serveLogged(t, cfg, func(e *Endpoint) {
		if err := e.SetSpool(sp, ""); err != nil {
			t.Fatal(err)
		}
		e.scanStep = func(seq uint64) error {
			if seq == last { // the last message: everything before it is scanned
				once.Do(func() { close(hit) })
				<-gate
			}
			return nil
		}
	})
	<-hit
	// mail arrives meanwhile
	if _, err := f.client(t, "b").Send(context.Background(), tdot("a"), "arrived during the scan", 0); err != nil {
		t.Fatal(err)
	}
	if !ep.StoredScanRunning() {
		t.Fatal("the scan is running and StoredScanRunning is false")
	}
	waitFor(t, "the serve to record the scan state", 15*time.Second, func() bool { return ScanOnDisk(sp) })
	time.Sleep(400 * time.Millisecond)
	if got := ev2.all(); len(got) != 0 {
		t.Fatalf("delivered while the scan ran: %v", got)
	}
	if l := l0(t, cfg); l.Done {
		t.Fatal("the scanned line is written before the scan finished")
	}
	close(gate)
	got := ev2.waitFor(t, 1, 10*time.Second) // the two waiting messages arrive in one batch
	if !l0(t, cfg).Done {
		t.Fatal("mail was delivered before the scanned line was written")
	}
	all := strings.Join(got, "\n")
	if !strings.Contains(all, "ordinary mail") || !strings.Contains(all, "arrived during the scan") {
		t.Fatalf("events: %s", all)
	}
	requireNoBodies(t, ev2)
	waitFor(t, "the scan state to clear", 15*time.Second, func() bool { return !ep.StoredScanRunning() && !ScanOnDisk(sp) })
	if l := l0(t, cfg); l.Scanned != last || len(l.Entries) != len(cases) {
		t.Fatalf("after the resume: %+v", l)
	}
	// Logged once overall: each class line appears in exactly one of the two runs.
	for _, en := range l0(t, cfg).Entries {
		line := "refused message " + itoa(en.Seq) + ": " + en.Class + "\n"
		if n := strings.Count(buf1.String(), line) + strings.Count(buf2.String(), line); n != 1 {
			t.Errorf("#%d logged %d times over the kill and the resume", en.Seq, n)
		}
	}
	if n := malformedLines(buf2.String()); n != len(cases)-recorded {
		t.Errorf("the resume logged %d lines, want %d (those not recorded before the kill)", n, len(cases)-recorded)
	}
	stop2()
}

// malformedLines counts the log lines of malformed refusals ("refused message N: <class>"), not those of unverified mail
// ("refused message N from ...").
func malformedLines(log string) int {
	n := 0
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, "refused message ") && !strings.Contains(l, " from ") {
			n++
		}
	}
	return n
}

// An endpoint that lost its lease stops the scan quietly: the refused-malformed list is not marked Done (a
// replacement serve may be scanning), and a later serve rescans.
func TestScanStopsOnLeaseLoss(t *testing.T) {
	f := newFixture(t, "a", "b")
	cfg := f.cfgState("a", t.TempDir(), false)
	_, cases, _, _ := publishMalformedSet(t, f)
	last := lastInboxSeq(t, f, "a")

	var steps atomic.Int32
	_, ev, _, stop, runErr := serveLogged(t, cfg, func(e *Endpoint) {
		e.scanStep = func(uint64) error {
			if steps.Add(1) == 2 {
				e.leaseGone.Store(true)
			}
			return nil
		}
	})
	waitFor(t, "the scan to reach its 2nd step", 10*time.Second, func() bool { return steps.Load() >= 2 })
	time.Sleep(500 * time.Millisecond) // a scan that did not stop would take more steps and write Done
	select {
	case err := <-runErr:
		t.Fatalf("Run ended: %v (a lost lease stops the scan, it does not fail startup)", err)
	default:
	}
	if n := steps.Load(); n != 2 {
		t.Fatalf("the scan took %d steps, want it to stop at the 2nd", n)
	}
	if l := l0(t, cfg); l.Done {
		t.Fatalf("the list is marked Done after a lost lease: %+v", l)
	}
	if len(ev.all()) != 0 {
		t.Fatalf("delivered after the scan stopped: %v", ev.all())
	}
	stop()

	// A later serve rescans and finishes.
	_, _, _, stop2, _ := serveLogged(t, cfg, nil)
	waitFor(t, "a later serve to finish the scan", 15*time.Second, func() bool { return l0(t, cfg).Done })
	if l := l0(t, cfg); l.Scanned != last || len(l.Entries) != len(cases) {
		t.Fatalf("after the rescan: %+v", l)
	}
	stop2()
}
