package policyintake

import (
	"context"
	"sync"

	"github.com/nats-io/nats.go/jetstream"
)

// queue hands the watch's entries to the handler in order, and lets the handler see whether a newer one waits.
type queue struct {
	mu     sync.Mutex
	items  []jetstream.KeyValueEntry
	closed bool
	wake   chan struct{}
}

func newQueue() *queue { return &queue{wake: make(chan struct{}, 1)} }

func (q *queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *queue) push(e jetstream.KeyValueEntry) {
	q.mu.Lock()
	q.items = append(q.items, e)
	q.mu.Unlock()
	q.signal()
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

// next returns the oldest entry; ok is false once ctx is done or the queue is closed and empty.
func (q *queue) next(ctx context.Context) (jetstream.KeyValueEntry, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			e := q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return e, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed || ctx.Err() != nil {
			return nil, false
		}
		select {
		case <-q.wake:
		case <-ctx.Done():
			return nil, false
		}
	}
}

// newerThan reports whether an entry of a later revision than rev is waiting.
func (q *queue) newerThan(rev uint64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.items {
		if e.Revision() > rev {
			return true
		}
	}
	return false
}
