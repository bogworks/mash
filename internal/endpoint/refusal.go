package endpoint

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/sap"
)

// RefusedError is a send the server refused by permissions: the sender's NATS user may not publish to the
// recipient's mail subject. Nothing was stored. It is not a timeout, and retrying with the same permissions cannot
// succeed.
type RefusedError struct {
	To      string // the recipient's name, as given to the send
	Subject string // the mail subject the server refused
	Key     string // the send's idempotency key; a retry with it stores the message at most once
}

func (e *RefusedError) Error() string {
	return "refused by policy: you may not mail " + sap.Display(e.To) + "." +
		" If the policy file allows this pair, or lists the recipient at all (mashctl policy explain)," +
		" your credentials may be older than the rule: ask the admin to run mashctl policy drift, then mashctl reissue." + e.retryClause()
}

// retryClause says how to retry safely. A refusal is attributed by subject and can, rarely, be a violation from an
// earlier send (see refusals), so the message may in fact have been delivered; the key makes a retry harmless.
func (e *RefusedError) retryClause() string {
	if e.Key == "" {
		return ""
	}
	return " If you retry, use --id " + e.Key + " (the send tool's id): it sends at most once within " + DuplicateWindowText() + "."
}

// Is makes errors.Is(err, &RefusedError{}) true for any refusal, and for one with the same recipient and subject.
func (e *RefusedError) Is(target error) bool {
	t, ok := target.(*RefusedError)
	if !ok {
		return false
	}
	return (t.To == "" || t.To == e.To) && (t.Subject == "" || t.Subject == e.Subject)
}

// refusals routes the connection's asynchronous "Permissions Violation for Publish" errors to the send waiting on
// that subject. A denied JetStream publish gets no reply, so PublishMsg alone reports only "context deadline
// exceeded" after the whole timeout; the server's -ERR reaches only the connection's error handler, naming the
// subject in double quotes.
//
// An error cannot be attributed to one send, only to a subject. So a violation is a definite refusal only when
// exactly one send is waiting on that subject at that moment, and that set is not ambiguous. The set is ambiguous
// (flagged, and no waiter in it is ever signalled until it empties) when a violation arrived while several were
// waiting, and when any send on the subject ended unsettled (timeout or cancellation) while other waiters remained:
// that send's publish may still be in flight, so a later violation may be its, not the remaining waiter's. Those
// sends keep waiting for their ack or deadline and end as a SendTimeoutError, which says an unattributed violation was
// seen. The flag is dropped when the set empties, so it never leaks to a later send. SendDetailed also prefers an ack
// that has already arrived. A violation with no waiter is dropped.
//
// One waiter alone still does not prove the violation is its own: a violation from an earlier send that ended
// unsettled could arrive after the next send registers. So such a send marks its subject dirty, and the next send on
// a dirty subject first runs a barrier before registering. The connection is an ordered stream: the server's -ERR for
// a publish is written before the PONG for a PING sent after it, so once the flush returns the violation has reached
// the client. nats.go hands it to the error handler on a separate goroutine, so the barrier then settles: it waits
// (at most settleCap) until the count of connection errors seen has not changed for two ticks. Violations delivered
// meanwhile find no waiter and are dropped. The barrier is per subject: while one runs, other sends on that subject
// wait for it (bounded by their contexts) and register only when it is done, and the dirty mark is cleared only after
// the flush returns. If the flush fails the send registers unattributable (blind): it never reports a definite
// refusal and keeps timeout semantics. A send that ended by ack or by a definite refusal leaves nothing in flight and
// does not mark the subject, so the barrier costs only after a timeout.
//
// Residual limit: the settle narrows the asynchronous window, it does not close it. nats.go exposes no way to learn
// that the error handler's queue has drained, so a violation still queued behind a slow handler for longer than the
// settle can be taken for a later send's. The refusal error carries the send's idempotency key, so acting on such a
// false refusal by retrying with that key is harmless: it sends at most once within the duplicate window.
type refusals struct {
	mu      sync.Mutex
	waiters map[string]*waiterSet
	dirty   map[string]int           // subject -> count of unsettled ends; cleared after the barrier it triggers
	gates   map[string]chan struct{} // subject -> closed when its running barrier is done
	seen    atomic.Uint64            // every connection error handed to note, counted before it is parsed
	// flush is the ordering barrier; a test replaces it. It is called with a deadline-bearing context.
	flush func(ctx context.Context) error
	// parse extracts the denied subject from a connection error; a test replaces it.
	parse func(error) (string, bool)
}

func newRefusals() *refusals {
	return &refusals{waiters: map[string]*waiterSet{}, dirty: map[string]int{}, gates: map[string]chan struct{}{}, parse: parseViolation}
}

// waiterSet is the sends waiting on one subject, and whether no violation may be taken as a definite refusal for
// them any more (ambiguous).
type waiterSet struct {
	chans     map[chan struct{}]struct{}
	blind     int // waiters that registered unattributable (their barrier failed)
	ambiguous bool
}

const (
	// barrierCap bounds the flush barrier regardless of the caller's deadline.
	barrierCap = 2 * time.Second
	// settleCap bounds the wait for the error handler's goroutine after the flush; settleTick is its polling step.
	settleCap  = 25 * time.Millisecond
	settleTick = 5 * time.Millisecond
)

// markDirty records that a send on subject ended without a settled outcome, so a violation for it may still be in
// flight.
func (r *refusals) markDirty(subject string) {
	r.mu.Lock()
	r.dirty[subject]++
	r.mu.Unlock()
}

// settle waits for the connection's error handler to catch up after a flush: until the number of errors seen has
// not changed for two ticks, at most settleCap. It reports false when ctx ended first.
func (r *refusals) settle(ctx context.Context) bool {
	limit := time.NewTimer(settleCap)
	defer limit.Stop()
	tick := time.NewTicker(settleTick)
	defer tick.Stop()
	last, stable := r.seen.Load(), 0
	for {
		select {
		case <-ctx.Done():
			return false
		case <-limit.C:
			return true
		case <-tick.C:
			if n := r.seen.Load(); n != last {
				last, stable = n, 0
			} else if stable++; stable >= 2 {
				return true
			}
		}
	}
}

// runBarrier flushes and settles, bounded by ctx and barrierCap; true means every earlier violation is known to have
// been delivered.
func (r *refusals) runBarrier(ctx context.Context) bool {
	if r.flush == nil {
		return false
	}
	bctx, cancel := context.WithTimeout(ctx, barrierCap)
	defer cancel()
	if r.flush(bctx) != nil {
		return false
	}
	return r.settle(bctx)
}

// begin registers a send for subject, first running the barrier when an earlier send on the subject ended unsettled.
// While a barrier for the subject runs, begin waits for it (until ctx ends, which is its error). Check, barrier and
// registration are ordered so that no send registers between the flush and the clearing of the dirty mark. See
// register for the channel and release.
func (r *refusals) begin(ctx context.Context, subject string) (refused <-chan struct{}, release func(settled bool), err error) {
	for {
		r.mu.Lock()
		if gate := r.gates[subject]; gate != nil {
			r.mu.Unlock()
			select {
			case <-gate:
				continue
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		n := r.dirty[subject]
		if n == 0 {
			ch, rel := r.registerLocked(subject, false)
			r.mu.Unlock()
			return ch, rel, nil
		}
		gate := make(chan struct{})
		r.gates[subject] = gate
		r.mu.Unlock()

		ok := r.runBarrier(ctx) // no lock held: note must keep running

		r.mu.Lock()
		if r.dirty[subject] == n { // an unsettled end during the barrier keeps the mark
			delete(r.dirty, subject)
		}
		delete(r.gates, subject)
		close(gate)
		ch, rel := r.registerLocked(subject, !ok)
		r.mu.Unlock()
		return ch, rel, nil
	}
}

// register starts waiting for a refusal of a publish to subject, with no barrier. Call it before the publish: the
// server's error is a reply to bytes the client wrote, so it cannot arrive earlier. The channel is signalled at most
// once (buffered 1); release must be called when the send completes and removes the waiter. settled is false when the
// send ended without knowing its outcome (timeout, cancellation): other waiters on the subject then become ambiguous.
func (r *refusals) register(subject string, blind bool) (refused <-chan struct{}, release func(settled bool)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registerLocked(subject, blind)
}

func (r *refusals) registerLocked(subject string, blind bool) (<-chan struct{}, func(bool)) {
	ch := make(chan struct{}, 1)
	set := r.waiters[subject]
	if set == nil {
		set = &waiterSet{chans: map[chan struct{}]struct{}{}}
		r.waiters[subject] = set
	}
	set.chans[ch] = struct{}{}
	if blind {
		set.blind++
	}
	return ch, func(settled bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if set := r.waiters[subject]; set != nil {
			delete(set.chans, ch)
			if blind {
				set.blind--
			}
			if len(set.chans) == 0 {
				delete(r.waiters, subject)
			} else if !settled {
				set.ambiguous = true
			}
		}
	}
}

// note is fed every connection error. A publish permission violation signals the waiter on its subject when there is
// exactly one that is attributable and the set is not ambiguous, and flags the set ambiguous otherwise; anything
// else, or a text that does not parse, does nothing (the send then fails as it always did, on its timeout).
func (r *refusals) note(err error) {
	r.seen.Add(1)
	if err == nil || !errors.Is(err, nats.ErrPermissionViolation) {
		return
	}
	subject, ok := r.parse(err)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	set := r.waiters[subject]
	if set == nil {
		return
	}
	if len(set.chans) != 1 || set.blind > 0 || set.ambiguous {
		set.ambiguous = true
		return
	}
	for ch := range set.chans {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// sawAmbiguous reports whether no violation on subject can be taken as a definite refusal for its current waiters:
// one arrived while several were waiting, or a send ended unsettled while others remained.
func (r *refusals) sawAmbiguous(subject string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	set := r.waiters[subject]
	return set != nil && set.ambiguous
}

// pending is the number of registered waiters (tests: no leak).
func (r *refusals) pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.waiters {
		n += len(s.chans)
	}
	return n
}

// parseViolation reads the subject out of the server's text, `nats: permissions violation: Permissions Violation for
// Publish to "<subject>"`. The text is the server's and may change; when it does not parse the caller falls back to
// the timeout.
func parseViolation(err error) (string, bool) {
	const marker = `Publish to "`
	s := err.Error()
	i := strings.Index(s, marker)
	if i < 0 {
		return "", false
	}
	s = s[i+len(marker):]
	j := strings.IndexByte(s, '"')
	if j <= 0 {
		return "", false
	}
	return s[:j], true
}
