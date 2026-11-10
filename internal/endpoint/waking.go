package endpoint

import (
	"context"
	"sync"
	"time"

	"github.com/bogworks/mash/internal/spool"
)

// Waking-pending count (the lease's waking_pending and waking_pending_at), for an orchestrator that wants to know
// cheaply whether this endpoint has mail that should wake it, without opening its inbox.
//
// Design. The count is kept in memory, by the one worker that already sees every message: the consumer's handler adds a
// message (by stream sequence, with whether it wakes: kind.Wakes or a verified urgent mark) when it arrives, and the
// places that ack it (shown on the channel, drained by a hook, or recognised as already shown) remove it. So "unread" is
// "received and not yet acked": mail held in the spool for a hook, held unacked by a no-channel host, or nak'd for
// redelivery still counts; mail the session was shown, by channel or hook, does not. The backlog of a restarted
// session needs no scan: the consumer redelivers every unacked message to the handler, so startup adds no round trip.
//
// Limits. (1) Reading a message with the read tool does not ack it, so it still counts until shown. (2) The backlog
// streams in after startup: until it is accounted for the count is unknown (absent from the lease), not a possibly low
// number. It is accounted for when the handler has received as many messages as the consumer held unacked or pending
// at start (counting each message that existed at start once, and no later arrival), or when, at a lease refresh, the server says nothing is pending and no more is unacked than the handler
// holds. Mail left unacked by a previous session is redelivered only after the consumer's AckWait (30 s), so the
// count can stay unknown that long; there is no time-based fallback. (3) Only mail the
// consumer has delivered is counted: unacked mail beyond its MaxAckPending, or nak'd with a delay and not yet
// redelivered (up to spoolFullDelay), is missing until it arrives. (4) A message the stream drops by its own limits
// before it is acked is forgotten at the next lease refresh (a sequence below the stream's first), so the count can
// lag by up to LeaseRefresh. (5) The count is the
// serve's view: it is written only while serve holds the lease.
var (
	// WakingWriteMin is the least time between two lease writes when a change in the count asks for one: the lease is
	// also refreshed every LeaseRefresh with the current count regardless.
	WakingWriteMin = 5 * time.Second
	// wakingCoalesce is how long a change waits for others, so a burst of arrivals is one write.
	wakingCoalesce = 250 * time.Millisecond
	// wakingClock stamps waking_pending_at and times the throttle; replaceable in tests.
	wakingClock = time.Now
)

// wakingTracker is the in-memory unread set (a nil one ignores everything), safe for the consumer's callback and the delivery worker.
//
// An endpoint reads one stream (the primary, ref stream "") or two (the primary and the legacy stream, naming plan 1.4b).
// The startup backlog, its bound and the server probe are kept per stream, and the count is known only when every
// stream's backlog is accounted for.
type wakingTracker struct {
	mu       sync.Mutex
	unread   map[spool.Ref]bool     // (stream, sequence) -> wakes
	count    int                    // the number of waking entries in unread
	expect   map[string]int         // per stream: messages its consumer held unacked or pending when it started
	last     map[string]uint64      // per stream: its last sequence when the consumer started; the startup messages are at or below it
	haveLast bool                   // last was read; without it no delivery counts toward the backlog
	seen     map[spool.Ref]bool     // distinct startup messages (sequence <= that stream's last) the handler has received
	seenN    map[string]int         // per stream: the number of seen
	sReady   map[string]bool        // per stream: its startup backlog is accounted for
	started  bool                   // the consumers have started
	ready    bool                   // every stream's startup backlog is accounted for: the count is known
	probes   map[string]wakingProbe // per stream: reads the server's view at a lease refresh; nil before the consumers start
	notify   chan struct{}          // a change in the count (or readiness) wants a lease write; capacity 1
}

// wakingProbe is what the server says about one stream: its consumer's messages not yet delivered and delivered but
// unacked, and the stream's first sequence.
type wakingProbe func(ctx context.Context) (pending uint64, ackPending int, firstSeq uint64, err error)

// streamStart is one stream's state when its consumer started.
type streamStart struct {
	Backlog int    // messages the consumer held unacked or pending
	Last    uint64 // the stream's last sequence (delivered + pending)
}

func newWakingTracker() *wakingTracker {
	return &wakingTracker{unread: map[spool.Ref]bool{}, notify: make(chan struct{}, 1)}
}

func (w *wakingTracker) poke() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

// start is startStreams for the primary stream alone.
func (w *wakingTracker) start(backlog int, last uint64, haveLast bool, probe wakingProbe) {
	w.startStreams(map[string]streamStart{"": {Backlog: backlog, Last: last}}, haveLast, map[string]wakingProbe{"": probe})
}

// startStreams records each stream's consumer backlog size and the stream's last sequence when it started (haveLast
// false if that read failed: then only the server's rule makes the count known). Zero is known at once. probes holds
// one probe per stream (a missing or nil one is never asked).
func (w *wakingTracker) startStreams(streams map[string]streamStart, haveLast bool, probes map[string]wakingProbe) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.expect, w.last, w.sReady = map[string]int{}, map[string]uint64{}, map[string]bool{}
	for name, st := range streams {
		w.expect[name], w.last[name], w.sReady[name] = st.Backlog, st.Last, st.Backlog <= 0
	}
	w.haveLast, w.started, w.probes = haveLast, true, probes
	w.seen, w.seenN = map[spool.Ref]bool{}, map[string]int{}
	w.ready = w.allReady()
	w.mu.Unlock()
	w.poke()
}

// allReady reports whether every stream's startup backlog is accounted for. Hold w.mu.
func (w *wakingTracker) allReady() bool {
	for _, ok := range w.sReady {
		if !ok {
			return false
		}
	}
	return true
}

// received counts a message the handler saw (by stream sequence), whether or not it is kept (forged mail is acked at
// once), toward its stream's startup backlog: only a sequence at or below the stream's last at start counts, and each
// once, so a redelivery, or a message sent after start, never stands in for one still outstanding.
func (w *wakingTracker) received(seq spool.Ref) {
	if w == nil {
		return
	}
	w.mu.Lock()
	flip := false
	last, hasLast := w.last[seq.Stream]
	if w.started && w.haveLast && hasLast && !w.sReady[seq.Stream] && seq.Seq <= last && !w.seen[seq] {
		w.seen[seq] = true
		w.seenN[seq.Stream]++
		if w.seenN[seq.Stream] >= w.expect[seq.Stream] {
			w.sReady[seq.Stream] = true
			if !w.ready && w.allReady() {
				w.ready, flip = true, true
			}
		}
	}
	w.mu.Unlock()
	if flip {
		w.poke()
	}
}

// add records an unread message; adding one already there changes nothing.
func (w *wakingTracker) add(seq spool.Ref, wakes bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	if _, ok := w.unread[seq]; ok {
		w.mu.Unlock()
		return
	}
	w.unread[seq] = wakes
	changed := wakes
	if wakes {
		w.count++
	}
	w.mu.Unlock()
	if changed {
		w.poke()
	}
}

// listed stops counting a message a hook has listed in a digest: the model was told of it and to read it, so it is not
// mail that should wake it. It stays unread (unacked) and is forgotten by done when acked; a redelivery's add changes
// nothing.
func (w *wakingTracker) listed(seq spool.Ref) {
	if w == nil {
		return
	}
	w.mu.Lock()
	wakes, ok := w.unread[seq]
	if ok && wakes {
		w.unread[seq] = false
		w.count--
	}
	w.mu.Unlock()
	if ok && wakes {
		w.poke()
	}
}

// done forgets a message that has been shown (acked).
func (w *wakingTracker) done(seq spool.Ref) {
	if w == nil {
		return
	}
	w.mu.Lock()
	wakes, ok := w.unread[seq]
	if ok {
		delete(w.unread, seq)
		if wakes {
			w.count--
		}
	}
	w.mu.Unlock()
	if ok && wakes {
		w.poke()
	}
}

// reconcile asks the server about each stream (no lock held across the calls) and applies it: tracked sequences
// below a stream's first are gone (evicted before they were acked) and are dropped; and a stream's startup backlog is
// accounted for once nothing is pending and no more is unacked than the handler holds of that stream. A failed probe
// changes nothing for its stream. It runs at each lease refresh, not at start (start already has the consumer's info,
// and startup round trips are budgeted).
func (w *wakingTracker) reconcile(ctx context.Context) {
	if w == nil {
		return
	}
	w.mu.Lock()
	probes := make(map[string]wakingProbe, len(w.probes))
	for name, p := range w.probes {
		if p != nil {
			probes[name] = p
		}
	}
	w.mu.Unlock()
	type answer struct {
		pending    uint64
		ackPending int
		first      uint64
	}
	got := map[string]answer{}
	for name, probe := range probes {
		pending, ackPending, first, err := probe(ctx)
		if err != nil {
			continue
		}
		got[name] = answer{pending, ackPending, first}
	}
	w.mu.Lock()
	changed := false
	for seq, wakes := range w.unread {
		// first is its own stream's first sequence: another stream's refs are not judged by it.
		if a, ok := got[seq.Stream]; ok && seq.Seq < a.first {
			delete(w.unread, seq)
			if wakes {
				w.count--
				changed = true
			}
		}
	}
	held := map[string]int{}
	for seq := range w.unread {
		held[seq.Stream]++
	}
	if w.started {
		for name, a := range got {
			if !w.sReady[name] && a.pending == 0 && a.ackPending <= held[name] {
				w.sReady[name] = true
			}
		}
		if !w.ready && w.allReady() {
			w.ready, changed = true, true
		}
	}
	w.mu.Unlock()
	if changed {
		w.poke()
	}
}

// snapshot is the waking count, and false while it is unknown.
func (w *wakingTracker) snapshot() (int, bool) {
	if w == nil {
		return 0, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count, w.ready
}
