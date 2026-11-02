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

func (e *RefusedError) Error() string { return "refused by policy: you may not mail " + e.To }

// Is makes errors.Is(err, &RefusedError{}) true for any refusal, and for one with the same recipient and subject.
func (e *RefusedError) Is(target error) bool {
	t, ok := target.(*RefusedError)
	if !ok {
		return false
	}
	return (t.To == "" || t.To == e.To) && (t.Subject == "" || t.Subject == e.Subject)
}

// refusals routes the connection's asynchronous "Permissions Violation for Publish" errors to the sends waiting on
// that subject. A denied JetStream publish gets no reply, so PublishMsg alone reports only "context deadline
// exceeded" after the whole timeout; the server's -ERR reaches only the connection's error handler, naming the
// subject in double quotes.
//
// Matching is by subject only: an error cannot be attributed to one send. Two concurrent sends to one denied subject
// are both signalled, and each fails fast, which is right. The residual ambiguity is a violation raised by another
// goroutine's send to a subject that this send, registered later, also uses; under a static permission set a subject
// is denied for everyone or for no one, so it cannot mislabel an allowed send, and SendDetailed also prefers an ack
// that has already arrived. A violation with no waiter is dropped.
type refusals struct {
	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
	// parse extracts the denied subject from a connection error; a test replaces it.
	parse func(error) (string, bool)
}

func newRefusals() *refusals {
	return &refusals{waiters: map[string]map[chan struct{}]struct{}{}, parse: parseViolation}
}

// register starts waiting for a refusal of a publish to subject. Call it before the publish: the server's error is a
// reply to bytes the client wrote, so it cannot arrive earlier. The channel is signalled at most once (buffered 1);
// release must be called when the send completes and removes the waiter.
func (r *refusals) register(subject string) (refused <-chan struct{}, release func()) {
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	set := r.waiters[subject]
	if set == nil {
		set = map[chan struct{}]struct{}{}
		r.waiters[subject] = set
	}
	set[ch] = struct{}{}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if set := r.waiters[subject]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(r.waiters, subject)
			}
		}
	}
}

// note is fed every connection error. A publish permission violation signals the waiters on its subject; anything
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
	for ch := range r.waiters[subject] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// pending is the number of registered waiters (tests: no leak).
func (r *refusals) pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.waiters {
		n += len(s)
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
