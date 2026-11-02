package endpoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
)

// Relay tracing (ADR 0064): from signed headers and timing only, mash records when an inbound mail declares a relay
// (a verified Mash-Via) and when this endpoint sends to a third party soon after mail from someone else. It writes
// trace lines and counts them. Nothing is blocked or delayed, no message body is ever read or kept, and a failure of
// the tracker never reaches delivery or a send.

const (
	// relayRing bounds the recent inbound mails the tracker remembers.
	relayRing = 256
	// relayFlushEvery is the shortest time between two writes of the counters file.
	relayFlushEvery = time.Second
	// relayStopWait bounds how long stop waits for the final write.
	relayStopWait = 2 * time.Second
	// RelayCountersFile is the counters file's name in the endpoint's state directory (spool.Dir).
	RelayCountersFile = "relay-counters.json"
)

// RelayCounters is the three relay counters of one `mash serve` run, as `mash status` shows them and as serve writes
// them to RelayCountersFile for the status command, which runs as another process. Instance and Started name the run,
// so a file left by an earlier run is told from a current one.
type RelayCounters struct {
	Instance        string    `json:"instance"`
	Started         time.Time `json:"started"`
	WindowS         int       `json:"window_s"`
	InboundVia      int64     `json:"inbound_via"`
	RelayCandidates int64     `json:"relay_candidates"`
	RelayedDeclared int64     `json:"relayed_declared"`
}

type relayRecv struct {
	seq  uint64
	from string
	at   time.Time
}

// relayTracker holds the last relayRing inbound mails (stream sequence, sender name, receipt time: never a body) and the
// counters. It is safe for concurrent use and every method is a no-op on nil.
type relayTracker struct {
	now    func() time.Time // the clock; tests replace it
	window time.Duration

	mu      sync.Mutex
	ring    []relayRecv // oldest first
	c       RelayCounters
	started time.Time

	// Persistence (Run only): the counters file, written by one background worker at most once per relayFlushEvery.
	pmu         sync.Mutex // guards the fields below; never held across I/O
	path        string
	instance    func() string
	logf        func(format string, args ...any)
	wake        chan struct{} // size 1: a non-blocking nudge to the worker
	quit, done  chan struct{}
	writeFn     func(path string, v any) error // writeJSONAtomic unless a test replaces it
	stopTimeout time.Duration                  // relayStopWait unless a test replaces it
	dirty       atomic.Bool
	warned      bool // the worker only
}

func newRelayTracker(window time.Duration) *relayTracker {
	if window <= 0 {
		window = config.DefaultRelayWindow
	}
	t := &relayTracker{now: time.Now, window: window}
	t.started = t.now()
	return t
}

// prune drops the mails older than the window; the ring is in receipt order. Called with mu held.
func (t *relayTracker) prune(now time.Time) {
	i := 0
	for i < len(t.ring) && now.Sub(t.ring[i].at) > t.window {
		i++
	}
	if i > 0 {
		t.ring = append(t.ring[:0], t.ring[i:]...)
	}
}

// inbound records one received mail from a sender named from; viaDeclared bumps the inbound-with-via counter.
func (t *relayTracker) inbound(seq uint64, from string, viaDeclared bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	now := t.now()
	t.prune(now)
	if len(t.ring) >= relayRing {
		t.ring = append(t.ring[:0], t.ring[len(t.ring)-relayRing+1:]...)
	}
	t.ring = append(t.ring, relayRecv{seq: seq, from: from, at: now})
	if viaDeclared {
		t.c.InboundVia++
	}
	t.mu.Unlock()
	if viaDeclared {
		t.changed()
	}
}

// send notes a send to "to" from the endpoint "self". It returns the most recent inbound mail received within the
// window from an endpoint other than the recipient and other than self, and its age; a send to oneself is ignored.
// Finding one counts a relay candidate; declared counts a declared relay.
func (t *relayTracker) send(self, to string, declared bool) (r relayRecv, age time.Duration, found bool) {
	if t == nil || to == self {
		return relayRecv{}, 0, false
	}
	t.mu.Lock()
	now := t.now()
	t.prune(now)
	for i := len(t.ring) - 1; i >= 0; i-- {
		if c := t.ring[i]; c.from != to && c.from != self {
			r, age, found = c, now.Sub(c.at), true
			break
		}
	}
	if found {
		t.c.RelayCandidates++
	}
	if declared {
		t.c.RelayedDeclared++
	}
	t.mu.Unlock()
	if found || declared {
		t.changed()
	}
	return r, age, found
}

// counters is a snapshot.
func (t *relayTracker) counters() RelayCounters {
	if t == nil {
		return RelayCounters{}
	}
	t.mu.Lock()
	c := t.c
	started, window := t.started, t.window
	t.mu.Unlock()
	c.Started, c.WindowS = started, int(window/time.Second)
	t.pmu.Lock()
	inst := t.instance
	t.pmu.Unlock()
	if inst != nil {
		c.Instance = inst()
	}
	return c
}

// persistTo starts the one background worker that writes the counters to path (see changed). The receipt and send paths
// never touch the file system: they only count in memory and wake the worker.
func (t *relayTracker) persistTo(path string, instance func() string, logf func(string, ...any)) {
	if t == nil || path == "" {
		return
	}
	t.pmu.Lock()
	defer t.pmu.Unlock()
	if t.quit != nil {
		return // already running
	}
	t.path, t.instance, t.logf = path, instance, logf
	t.wake = make(chan struct{}, 1)
	t.quit = make(chan struct{})
	t.done = make(chan struct{})
	go t.worker(t.wake, t.quit, t.done)
}

// changed marks the counters dirty and wakes the worker. It never blocks and never does I/O.
func (t *relayTracker) changed() {
	t.pmu.Lock()
	wake := t.wake
	t.pmu.Unlock()
	if wake == nil {
		return
	}
	t.dirty.Store(true)
	select {
	case wake <- struct{}{}:
	default:
	}
}

// worker writes the counters at most once per relayFlushEvery, and once more when told to quit.
func (t *relayTracker) worker(wake, quit <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	defer func() { _ = recover() }()
	var last time.Time
	flush := func() {
		if !t.dirty.Swap(false) {
			return
		}
		last = time.Now()
		t.write()
	}
	for {
		select {
		case <-quit:
			flush()
			return
		case <-wake:
		}
		if wait := relayFlushEvery - time.Since(last); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-quit:
				timer.Stop()
				flush()
				return
			}
		}
		flush()
	}
}

// stop ends persistence: the worker writes a change still waiting, but stop waits for it no longer than
// relayStopWait, so a stalled state file system cannot hang shutdown.
func (t *relayTracker) stop() {
	if t == nil {
		return
	}
	t.pmu.Lock()
	quit, done, logf := t.quit, t.done, t.logf
	t.wake, t.quit, t.done = nil, nil, nil
	t.pmu.Unlock()
	if quit == nil {
		return
	}
	close(quit)
	timer := time.NewTimer(t.stopWait())
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		if logf != nil {
			logf("relay counters: final write gave up after %s (state directory not answering)", t.stopWait())
		}
	}
}

func (t *relayTracker) stopWait() time.Duration {
	if t.stopTimeout > 0 {
		return t.stopTimeout
	}
	return relayStopWait
}

// write snapshots the counters (no lock held during I/O) and writes them atomically (0600, temporary file and
// rename). A failure is logged once and otherwise ignored. Only the worker calls it.
func (t *relayTracker) write() {
	t.pmu.Lock()
	path, logf, wf := t.path, t.logf, t.writeFn
	t.pmu.Unlock()
	if wf == nil {
		wf = writeJSONAtomic
	}
	err := wf(path, t.counters())
	if err != nil && !t.warned && logf != nil {
		t.warned = true
		logf("relay counters not written: %v", err)
	}
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".relay-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once renamed
	err = f.Chmod(0o600)
	if err == nil {
		_, err = f.Write(b)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// relayCountersPath is where serve keeps its counters for the status command; "" without a state directory.
func relayCountersPath(stateDir, owner, name string) string {
	if stateDir == "" || owner == "" || name == "" {
		return ""
	}
	return filepath.Join(spool.Dir(stateDir, owner, name), RelayCountersFile)
}

// ReadRelayCounters reads the counters file of the serve of endpoint name, as the status command (another process) does.
// ok is false when it is missing, unreadable or not the expected JSON.
func ReadRelayCounters(stateDir, owner, name string) (RelayCounters, bool) {
	p := relayCountersPath(stateDir, owner, name)
	if p == "" {
		return RelayCounters{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return RelayCounters{}, false
	}
	var c RelayCounters
	if json.Unmarshal(b, &c) != nil || c.Started.IsZero() || c.Instance == "" {
		return RelayCounters{}, false
	}
	return c, true
}

// RelayCounters is the live counters of this run.
func (e *Endpoint) RelayCounters() RelayCounters { return e.relay.counters() }

// relayInbound records a mail the serve path received. Only a verified or stale sender has a name worth recording;
// via is the verified declaration "<origin> #<seq>" (empty when none). Nothing here reads the body.
func (e *Endpoint) relayInbound(seq uint64, from string, trust sap.Trust, via string) {
	defer func() { _ = recover() }() // tracing never affects delivery
	if e.relay == nil || (trust != sap.TrustVerified && trust != sap.TrustStale) {
		return
	}
	e.relay.inbound(seq, from, via != "")
	if via != "" {
		origin, n, _ := strings.Cut(via, " #")
		e.log.Printf("trace relay: mail #%d from %s declares a relay of #%s from %s", seq, from, n, origin)
	}
}

// relaySent traces a send that was published: a declared relay (via is the sender's inbox sequence, origin its
// sender), and a send to a third party within the window of another endpoint's mail. It proves only adjacency in time.
func (e *Endpoint) relaySent(to string, via uint64, origin string) {
	defer func() { _ = recover() }() // tracing never affects a send
	if e.relay == nil || to == e.cfg.Name {
		return
	}
	r, age, found := e.relay.send(e.cfg.Name, to, via != 0)
	if via != 0 {
		e.log.Printf("trace relay: sent to %s declaring a relay of #%d from %s", to, via, origin)
	}
	if found {
		declared := "no"
		if via != 0 {
			declared = "yes"
		}
		e.log.Printf("trace relay: sent to %s %d s after mail #%d from %s (declared: %s)", to, int(age/time.Second), r.seq, r.from, declared)
	}
}
