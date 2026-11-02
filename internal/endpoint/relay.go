package endpoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	// Persistence (Run only): the counters file, written at most once per relayFlushEvery.
	pmu       sync.Mutex
	path      string
	instance  func() string
	logf      func(format string, args ...any)
	lastWrite time.Time
	timer     *time.Timer
	stopped   bool
	warned    bool
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

// persistTo makes the tracker write its counters to path (see changed).
func (t *relayTracker) persistTo(path string, instance func() string, logf func(string, ...any)) {
	if t == nil {
		return
	}
	t.pmu.Lock()
	t.path, t.instance, t.logf, t.stopped = path, instance, logf, false
	t.pmu.Unlock()
}

// changed writes the counters now, or, when the last write was under a second ago, once the second is up.
func (t *relayTracker) changed() {
	t.pmu.Lock()
	defer t.pmu.Unlock()
	if t.path == "" || t.stopped || t.timer != nil {
		return
	}
	if wait := relayFlushEvery - time.Since(t.lastWrite); wait > 0 {
		t.timer = time.AfterFunc(wait, func() {
			t.pmu.Lock()
			defer t.pmu.Unlock()
			t.timer = nil
			if !t.stopped {
				t.writeLocked()
			}
		})
		return
	}
	t.writeLocked()
}

// stop ends persistence, writing a change that was waiting for its second.
func (t *relayTracker) stop() {
	if t == nil {
		return
	}
	t.pmu.Lock()
	defer t.pmu.Unlock()
	if t.path == "" || t.stopped {
		return
	}
	t.stopped = true
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
		t.writeLocked()
	}
}

// writeLocked writes the counters atomically (0600, temporary file and rename), with pmu held. A failure is logged
// once and otherwise ignored.
func (t *relayTracker) writeLocked() {
	t.lastWrite = time.Now()
	t.mu.Lock()
	c := t.c
	t.mu.Unlock()
	c.Started, c.WindowS = t.started, int(t.window/time.Second)
	if t.instance != nil {
		c.Instance = t.instance()
	}
	err := writeJSONAtomic(t.path, c)
	if err != nil && !t.warned && t.logf != nil {
		t.warned = true
		t.logf("relay counters not written: %v", err)
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
