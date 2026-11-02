package endpoint

import (
	"errors"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"
)

// RefusedError is a send the server refused by permissions: the sender's NATS user may not publish to the
// recipient's mail subject. Nothing was stored. It is not a timeout, and retrying with the same permissions cannot
// succeed.
type RefusedError struct {
	To      string // the recipient's name, as given to the send
	Subject string // the mail subject the server refused
}

func (e *RefusedError) Error() string {
	return "refused by policy: you may not mail " + e.To + "." +
		" If the policy file allows this pair, or lists the recipient at all (mashctl policy explain)," +
		" your credentials may be older than the rule: ask the admin to run mashctl policy drift, then mashctl reissue."
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
// exactly one send is waiting on that subject at that moment. With several waiting, none is signalled and the set is
// flagged ambiguous: a delayed violation from an earlier send, or overlapping sends across a permission change, must
// not give a later send a definite refusal while its delivery is really unknown. Those sends keep waiting for their
// ack or deadline and end as a SendTimeoutError, which says an unattributed violation was seen. The flag lives as
// long as the waiter set and is dropped when the set empties, so it never leaks to a later send. SendDetailed also
// prefers an ack that has already arrived. A violation with no waiter is dropped.
type refusals struct {
	mu      sync.Mutex
	waiters map[string]*waiterSet
	// parse extracts the denied subject from a connection error; a test replaces it.
	parse func(error) (string, bool)
}

func newRefusals() *refusals {
	return &refusals{waiters: map[string]*waiterSet{}, parse: parseViolation}
}

// waiterSet is the sends waiting on one subject, and whether a violation arrived while several were.
type waiterSet struct {
	chans     map[chan struct{}]struct{}
	ambiguous bool
}

// register starts waiting for a refusal of a publish to subject. Call it before the publish: the server's error is a
// reply to bytes the client wrote, so it cannot arrive earlier. The channel is signalled at most once (buffered 1);
// release must be called when the send completes and removes the waiter.
func (r *refusals) register(subject string) (refused <-chan struct{}, release func()) {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	set := r.waiters[subject]
	if set == nil {
		set = &waiterSet{chans: map[chan struct{}]struct{}{}}
		r.waiters[subject] = set
	}
	set.chans[ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if set := r.waiters[subject]; set != nil {
			delete(set.chans, ch)
			if len(set.chans) == 0 {
				delete(r.waiters, subject)
			}
		}
	}
}

// note is fed every connection error. A publish permission violation signals the waiter on its subject when there is
// exactly one, and flags the set ambiguous when there are several; anything
// else, or a text that does not parse, does nothing (the send then fails as it always did, on its timeout).
func (r *refusals) note(err error) {
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
	if len(set.chans) > 1 {
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

// sawAmbiguous reports whether a violation on subject arrived while several sends were waiting on it.
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
