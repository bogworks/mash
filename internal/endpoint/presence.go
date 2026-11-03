package endpoint

import (
	"context"
	"sync"
	"time"
)

// PresenceCacheTTL is how long the result of a presence probe is served from memory before `who` inside a session
// asks again (ADR 0066).
var PresenceCacheTTL = 30 * time.Second

// PresenceRetryGap is the shortest time between two probes after a failed one, so a failing probe is not repeated by
// every caller (ADR 0066).
var PresenceRetryGap = 5 * time.Second

// PresenceCache decides when a presence probe is due: at most one at a time (callers that arrive during one share
// its result), none within PresenceCacheTTL of the start of the last successful one, and none within
// PresenceRetryGap of a failed one. The zero value is ready to use and it is safe for concurrent use.
type PresenceCache struct {
	// Now is the clock; nil means time.Now. Set it before use.
	Now func() time.Time

	mu       sync.Mutex
	okAt     time.Time // start of the last successful probe
	ok       bool
	failAt   time.Time // when the last probe failed
	failErr  error
	failed   bool
	inflight *probeFlight
}

type probeFlight struct {
	done chan struct{}
	err  error
}

func (c *PresenceCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Ensure runs probe unless the presence it reports is still fresh. It returns probed true when this call ran the
// probe, and the probe's error. Within PresenceCacheTTL of the start of the last successful probe it returns
// (false, nil) and does not probe; while a probe is running it waits for it and returns its result with probed false;
// within PresenceRetryGap of a failed probe it returns that failure without probing. A probe that failed because its
// caller's context ended does not count as a failure for the other callers: they probe again.
func (c *PresenceCache) Ensure(ctx context.Context, probe func(ctx context.Context) error) (probed bool, err error) {
	for {
		c.mu.Lock()
		now := c.now()
		if c.ok && now.Sub(c.okAt) < PresenceCacheTTL {
			c.mu.Unlock()
			return false, nil
		}
		if f := c.inflight; f != nil {
			c.mu.Unlock()
			select {
			case <-f.done:
			case <-ctx.Done():
				return false, ctx.Err()
			}
			if f.err != nil && isCtxErr(f.err) && ctx.Err() == nil {
				continue // the probing caller gave up; this one has not
			}
			return false, f.err
		}
		if c.failed && now.Sub(c.failAt) < PresenceRetryGap {
			err := c.failErr
			c.mu.Unlock()
			return false, err
		}
		f := &probeFlight{done: make(chan struct{})}
		c.inflight = f
		start := now
		c.mu.Unlock()

		err := probe(ctx)

		c.mu.Lock()
		c.inflight = nil
		switch {
		case err == nil:
			c.ok, c.okAt, c.failed, c.failErr = true, start, false, nil
		case isCtxErr(err) && ctx.Err() != nil:
			// the caller left: nothing learned about whether probing works
		default:
			c.failed, c.failAt, c.failErr = true, c.now(), err
		}
		f.err = err
		close(f.done)
		c.mu.Unlock()
		return true, err
	}
}

func isCtxErr(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}
