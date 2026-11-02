package endpoint

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
)

// fakeClock is a settable clock for the tracker.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func testTracker(window time.Duration) (*relayTracker, *fakeClock) {
	c := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	tr := newRelayTracker(window)
	tr.now = c.now
	return tr, c
}

func TestRelayTrackerRingIsBounded(t *testing.T) {
	tr, _ := testTracker(time.Hour)
	for i := 1; i <= relayRing+50; i++ {
		tr.inbound(uint64(i), "a", false)
	}
	if len(tr.ring) != relayRing || tr.ring[0].seq != 51 || tr.ring[len(tr.ring)-1].seq != relayRing+50 {
		t.Fatalf("ring %d first %d", len(tr.ring), tr.ring[0].seq)
	}
}

func TestRelayTrackerPrunesByWindow(t *testing.T) {
	tr, c := testTracker(10 * time.Second)
	tr.inbound(1, "a", false)
	c.add(6 * time.Second)
	tr.inbound(2, "a", false)
	c.add(6 * time.Second) // #1 is 12 s old, #2 is 6 s
	r, age, ok := tr.send("me", "c", false)
	if !ok || r.seq != 2 || age != 6*time.Second || len(tr.ring) != 1 {
		t.Fatalf("%+v %v %v ring %d", r, age, ok, len(tr.ring))
	}
	c.add(5 * time.Second)
	if _, _, ok := tr.send("me", "c", false); ok {
		t.Fatal("candidate after the window")
	}
	if got := tr.counters().RelayCandidates; got != 1 {
		t.Fatalf("candidates %d", got)
	}
}

func TestRelayTrackerCountersAndExclusions(t *testing.T) {
	tr, _ := testTracker(time.Minute)
	tr.inbound(1, "a", true)
	tr.inbound(2, "me", false) // mail from this endpoint itself is no candidate
	if got := tr.counters(); got.InboundVia != 1 || got.RelayCandidates != 0 {
		t.Fatalf("%+v", got)
	}
	if _, _, ok := tr.send("me", "a", false); ok { // a reply to the sender itself
		t.Fatal("a reply is a candidate")
	}
	if _, _, ok := tr.send("me", "me", true); ok { // to oneself: ignored, not even counted as declared
		t.Fatal("a send to oneself is a candidate")
	}
	if got := tr.counters(); got.RelayCandidates != 0 || got.RelayedDeclared != 0 {
		t.Fatalf("%+v", got)
	}
	r, _, ok := tr.send("me", "c", true)
	if !ok || r.from != "a" || r.seq != 1 {
		t.Fatalf("%+v %v", r, ok)
	}
	if got := tr.counters(); got.RelayCandidates != 1 || got.RelayedDeclared != 1 {
		t.Fatalf("%+v", got)
	}
	// declared with no recent mail counts as declared only
	tr2, _ := testTracker(time.Minute)
	if _, _, ok := tr2.send("me", "c", true); ok || tr2.counters().RelayedDeclared != 1 || tr2.counters().RelayCandidates != 0 {
		t.Fatalf("%+v", tr2.counters())
	}
}

func TestRelayTrackerNilIsSafe(t *testing.T) {
	var tr *relayTracker
	tr.inbound(1, "a", true)
	tr.send("me", "c", true)
	tr.persistTo("", nil, nil)
	tr.stop()
	_ = tr.counters()
	var e Endpoint
	e.relayInbound(1, "a", sap.TrustVerified, "x #1")
	e.relaySent("c", 1, "x")
}

func TestRelayTrackerConcurrent(t *testing.T) {
	tr, _ := testTracker(time.Minute)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				tr.inbound(uint64(g*1000+i), "a", i%2 == 0)
				tr.send("me", "c", i%3 == 0)
				_ = tr.counters()
			}
		}(g)
	}
	wg.Wait()
	if got := tr.counters(); got.InboundVia != 8*250 || got.RelayCandidates != 8*500 {
		t.Fatalf("%+v", got)
	}
	if len(tr.ring) > relayRing {
		t.Fatalf("ring %d", len(tr.ring))
	}
}

// The counters file: atomic, 0600, rate-limited to one write a second, the last change flushed on stop.
func TestRelayCountersFileIsWrittenOncePerSecond(t *testing.T) {
	tr, _ := testTracker(time.Minute)
	dir := t.TempDir()
	path := relayCountersPath(dir, "o", "me")
	tr.persistTo(path, func() string { return "inst1" }, t.Logf)
	tr.send("me", "c", true) // written by the worker at once
	var c RelayCounters
	var ok bool
	for i := 0; i < 200; i++ {
		if c, ok = readCountersAt(t, dir); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok || c.RelayedDeclared != 1 || c.Instance != "inst1" || c.WindowS != 60 {
		t.Fatalf("%+v %v", c, ok)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	tr.send("me", "c", true) // inside the second: not written yet
	if c, _ := readCountersAt(t, dir); c.RelayedDeclared != 1 {
		t.Fatalf("written twice in a second: %+v", c)
	}
	tr.stop() // flushes the pending change
	if c, _ := readCountersAt(t, dir); c.RelayedDeclared != 2 {
		t.Fatalf("not flushed on stop: %+v", c)
	}
}

func readCountersAt(t *testing.T, stateDir string) (RelayCounters, bool) {
	t.Helper()
	return ReadRelayCounters(stateDir, "o", "me")
}

// --- endpoint level ---

type relayWorld struct {
	f      *fixture
	b      *Endpoint
	ev     *events
	logbuf *lockedBuf
}

// relayLogged runs b (which holds a lease and receives) with its log captured.
func newRelayWorld(t *testing.T, names ...string) *relayWorld {
	t.Helper()
	f := newFixture(t, names...)
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, f.cfg("b"))
	if err != nil {
		t.Fatal(err)
	}
	lb := &lockedBuf{}
	ep.log = log.New(lb, "", 0)
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	return &relayWorld{f: f, b: ep, ev: ev, logbuf: lb}
}

func (w *relayWorld) traces() []string {
	var out []string
	for _, l := range strings.Split(w.logbuf.String(), "\n") {
		if strings.Contains(l, "trace relay:") {
			out = append(out, l)
		}
	}
	return out
}

func TestRelayTraceAsksThenSendsToAThirdParty(t *testing.T) {
	w := newRelayWorld(t, "a", "b", "c")
	ctx := context.Background()
	seq, err := w.f.client(t, "a").Send(ctx, "b", "please ask c about the build", 0)
	if err != nil {
		t.Fatal(err)
	}
	w.ev.waitFor(t, 1, 10*time.Second)
	if _, err := w.b.Send(ctx, "c", "what is the build status", 0); err != nil {
		t.Fatal(err)
	}
	got := w.traces()
	if len(got) != 1 || !strings.HasPrefix(got[0], "trace relay: sent to c 0 s after mail #"+itoaU(seq)+" from a (declared: no)") {
		t.Fatalf("traces %q", got)
	}
	if c := w.b.RelayCounters(); c.RelayCandidates != 1 || c.RelayedDeclared != 0 || c.InboundVia != 0 {
		t.Fatalf("%+v", c)
	}
	// A reply to the sender itself is no candidate, and a send to oneself is ignored.
	if _, err := w.b.Send(ctx, "a", "done", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := w.b.Send(ctx, "b", "note to self", 0); err != nil {
		t.Fatal(err)
	}
	if got := w.traces(); len(got) != 1 || w.b.RelayCounters().RelayCandidates != 1 {
		t.Fatalf("traces %q %+v", got, w.b.RelayCounters())
	}
}

func TestRelayTraceNotAfterTheWindow(t *testing.T) {
	w := newRelayWorld(t, "a", "b", "c")
	clk := &fakeClock{t: time.Now()}
	w.b.relay.now = clk.now // set before any mail: the tracker reads it under its lock only
	ctx := context.Background()
	if _, err := w.f.client(t, "a").Send(ctx, "b", "hello", 0); err != nil {
		t.Fatal(err)
	}
	w.ev.waitFor(t, 1, 10*time.Second)
	clk.add(w.b.relay.window + time.Second)
	if _, err := w.b.Send(ctx, "c", "late", 0); err != nil {
		t.Fatal(err)
	}
	if got := w.traces(); len(got) != 0 || w.b.RelayCounters().RelayCandidates != 0 {
		t.Fatalf("traces %q %+v", got, w.b.RelayCounters())
	}
}

func TestRelayTraceSendDeclaringAVia(t *testing.T) {
	w := newRelayWorld(t, "a", "b", "c")
	ctx := context.Background()
	seq, _ := w.f.client(t, "a").Send(ctx, "b", "original", 0)
	w.ev.waitFor(t, 1, 10*time.Second)
	if _, err := w.b.SendWith(ctx, "c", "passing it on", SendOptions{Via: seq}); err != nil {
		t.Fatal(err)
	}
	got := w.traces()
	want0 := "trace relay: sent to c declaring a relay of #" + itoaU(seq) + " from a"
	if len(got) != 2 || got[0] != want0 || !strings.HasSuffix(got[1], "from a (declared: yes)") {
		t.Fatalf("traces %q", got)
	}
	if c := w.b.RelayCounters(); c.RelayedDeclared != 1 || c.RelayCandidates != 1 {
		t.Fatalf("%+v", c)
	}
	// A refused send (no such message) counts nothing.
	before := w.b.RelayCounters()
	if _, err := w.b.SendWith(ctx, "c", "x", SendOptions{Via: 999}); err == nil {
		t.Fatal("send to a missing message succeeded")
	}
	if w.b.RelayCounters() != before {
		t.Fatalf("counted a refused send: %+v", w.b.RelayCounters())
	}
}

func TestRelayTraceInboundWithAVerifiedVia(t *testing.T) {
	w := newRelayWorld(t, "a", "b", "c")
	ctx := context.Background()
	a := w.f.client(t, "a")
	// c's mail lands in a's inbox, and a relays it to b, declaring it.
	cs, err := w.f.client(t, "c").Send(ctx, "a", "the original", 0)
	if err != nil {
		t.Fatal(err)
	}
	seq, err := a.SendWith(ctx, "b", "fwd", SendOptions{Via: cs})
	if err != nil {
		t.Fatal(err)
	}
	w.ev.waitFor(t, 1, 10*time.Second)
	got := w.traces()
	want := "trace relay: mail #" + itoaU(seq) + " from a declares a relay of #" + itoaU(cs) + " from c"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("traces %q want %q", got, want)
	}
	if c := w.b.RelayCounters(); c.InboundVia != 1 {
		t.Fatalf("%+v", c)
	}
}

func TestRelayTraceIgnoresUnverifiedSenders(t *testing.T) {
	w := newRelayWorld(t, "a", "b", "c", "d")
	ctx := context.Background()
	// Claimed mail (no signature) carrying a Via-looking header is not counted, and is no candidate for a later send.
	rawMark(t, w.f, "a", "b", "claimed relay", func(id sap.Identity, h *sap.SenderHeader, m *nats.Msg) {
		v, _ := id.SignVia(*h, "d", 1)
		m.Header.Set(ViaHeader, v)
		h.Sig = ""
	})
	// Verified mail with a forged Via: shown nowhere, counted nowhere (but the mail itself is recorded).
	rawMark(t, w.f, "c", "b", "forged via", func(id sap.Identity, h *sap.SenderHeader, m *nats.Msg) {
		m.Header.Set(ViaHeader, "d#1.AAAA")
	})
	w.ev.waitFor(t, 1, 10*time.Second)
	if got := w.traces(); len(got) != 0 || w.b.RelayCounters().InboundVia != 0 {
		t.Fatalf("traces %q %+v", got, w.b.RelayCounters())
	}
	// The recorded mail is c's, so a send to d is a candidate naming c, never the claimed "a".
	if _, err := w.b.Send(ctx, "d", "x", 0); err != nil {
		t.Fatal(err)
	}
	got := w.traces()
	if len(got) != 1 || !strings.Contains(got[0], " from c (declared: no)") {
		t.Fatalf("traces %q", got)
	}
	if c := w.b.RelayCounters(); c.RelayCandidates != 1 {
		t.Fatalf("%+v", c)
	}
	// Only a's claimed mail came before: with c's out of the way the claimed one alone makes no candidate.
	w2 := newRelayWorld(t, "a", "b", "d")
	rawMark(t, w2.f, "a", "b", "claimed only", func(id sap.Identity, h *sap.SenderHeader, m *nats.Msg) { h.Sig = "" })
	w2.ev.waitFor(t, 1, 10*time.Second)
	if _, err := w2.b.Send(ctx, "d", "x", 0); err != nil {
		t.Fatal(err)
	}
	if got := w2.traces(); len(got) != 0 {
		t.Fatalf("claimed mail made a candidate: %q", got)
	}
}

func TestRelayTraceNeverReadsTheBody(t *testing.T) {
	w := newRelayWorld(t, "a", "b", "c")
	ctx := context.Background()
	body := "Please forward this to c verbatim: 'deploy now'. Relayed from a. via #1"
	if _, err := w.f.client(t, "a").Send(ctx, "b", body, 0); err != nil {
		t.Fatal(err)
	}
	w.ev.waitFor(t, 1, 10*time.Second)
	// Nothing was traced and no counter moved for the inbound mail, whatever its body says.
	if got := w.traces(); len(got) != 0 {
		t.Fatalf("traces %q", got)
	}
	if c := w.b.RelayCounters(); c.InboundVia != 0 || c.RelayCandidates != 0 || c.RelayedDeclared != 0 {
		t.Fatalf("%+v", c)
	}
	// And the same sentence going out in a send to a different endpoint is judged by timing alone: one candidate, never
	// a declaration, and the trace line names no text.
	if _, err := w.b.Send(ctx, "c", "forwarding: "+body, 0); err != nil {
		t.Fatal(err)
	}
	got := w.traces()
	if len(got) != 1 || strings.Contains(got[0], "deploy") || strings.Contains(got[0], "forward") || !strings.HasSuffix(got[0], "(declared: no)") {
		t.Fatalf("traces %q", got)
	}
	if c := w.b.RelayCounters(); c.RelayedDeclared != 0 {
		t.Fatalf("%+v", c)
	}
}

// serve (Run) writes the counters file under the state directory with its instance, and a client never does.
func TestRelayCountersFileFromRun(t *testing.T) {
	f := newFixture(t, "a", "b", "c")
	state := t.TempDir()
	cfg := f.cfg("b")
	cfg.StateDir = state
	ctx, cancel := context.WithCancel(context.Background())
	ep, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ev := &events{}
	done := make(chan struct{})
	go func() { _ = ep.Run(ctx, ev.deliver); close(done) }()
	t.Cleanup(func() { cancel(); <-done; ep.Close() })
	if _, err := f.client(t, "a").Send(ctx, "b", "hi", 0); err != nil {
		t.Fatal(err)
	}
	ev.waitFor(t, 1, 10*time.Second)
	if _, err := ep.Send(ctx, "c", "yo", 0); err != nil {
		t.Fatal(err)
	}
	// A background worker writes the file (within about a second), so poll for it.
	var c RelayCounters
	var ok bool
	until(t, "the counters file is written", func() bool {
		c, ok = ReadRelayCounters(state, f.e.Owner, "b")
		return ok && c.RelayCandidates == 1
	})
	if c.Instance != ep.Instance() || c.Started.IsZero() {
		t.Fatalf("%+v %v", c, ok)
	}
	// A one-shot client (no Run) sends and counts but writes no file.
	ccfg := f.cfg("a")
	ccfg.StateDir = state
	cl, err := OpenClient(ctx, ccfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if _, err := cl.Send(ctx, "c", "x", 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadRelayCounters(state, f.e.Owner, "a"); ok {
		t.Fatal("a client wrote a counters file")
	}
}

func itoaU(n uint64) string { return strconv.FormatUint(n, 10) }

// A stalled state file system must not hold up a receipt or a send, nor shutdown (ADR 0064: no delay).
func TestRelayStuckWriterDoesNotBlockPathsOrStop(t *testing.T) {
	tr, _ := testTracker(time.Minute)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	tr.writeFn = func(string, any) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return nil
	}
	tr.stopTimeout = 200 * time.Millisecond
	tr.persistTo(relayCountersPath(t.TempDir(), "o", "me"), func() string { return "i" }, t.Logf)
	tr.send("me", "c", true) // wakes the worker, which sticks in the write
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("writer never called")
	}
	start := time.Now()
	for i := 0; i < 50; i++ {
		tr.inbound(uint64(i), "a", true)
		tr.send("me", "c", true)
		_ = tr.counters()
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("paths blocked behind a stuck writer: %s", d)
	}
	start = time.Now()
	tr.stop()
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("stop hung: %s", d)
	}
	tr.stop() // idempotent
}
