package endpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The ack-time fence and the takeover grace (ADR 0090).
//
// watchLease notices a takeover within leaseWatchEvery, and until it does the earlier serve would keep delivering to its
// client and acking mail the new holder never sees. So before delivering a batch and before any ack the endpoint reads
// its lease key (and, for legacy mail, the legacy key) and compares the holder's instance with its own. Another
// instance, or a tombstone, marks the lease lost at once, exactly as the watch would. A read that fails or times out
// marks nothing, but nothing is acked on it either: the mail is nak'd (before delivery) or left unacked (an ack) and
// JetStream redelivers it. A confirmation is reused for leaseConfirmFor, so a burst costs one read, not one per message.
//
// The new serve, for its part, waits takeoverGrace() before it pulls anything when it took the lease from a live
// holder of its own session, so that holder has stopped (graceWait).

var (
	// leaseConfirmFor is how long a live check (held or unknown) is reused.
	leaseConfirmFor = 200 * time.Millisecond
	// leaseConfirmTimeout bounds the read of the lease key made at delivery or ack time.
	leaseConfirmTimeout = 500 * time.Millisecond
	// takeoverGrace is how long a serve that took the lease from a live same-session holder waits before consuming.
	takeoverGrace = func() time.Duration { return 2 * leaseWatchEvery }
)

type leaseVerdict int

const (
	verdictHeld    leaseVerdict = iota + 1 // the lease is ours, as just read (or as cached for leaseConfirmFor)
	verdictLost                            // another holder, a tombstone, or already marked lost
	verdictUnknown                         // the read failed or timed out: not lost, but not confirmed
)

// liveLease caches the latest live check of each of the two lease keys: [0] the own key, [1] the legacy identity's.
type liveLease struct {
	mu  sync.Mutex
	at  [2]time.Time
	res [2]leaseVerdict
	// get, when set, replaces the direct read of a lease key (tests); guarded by mu.
	get func(ctx context.Context, key string) (jetstream.KeyValueEntry, error)
}

// verdictFor is the live state of the lease that lets this endpoint deliver and acknowledge mail of stream.
func (e *Endpoint) verdictFor(stream string) leaseVerdict {
	if !e.leaseHeldFor(stream) {
		return verdictLost
	}
	if e.leaseKV == nil {
		return verdictHeld // no lease bucket (not a serving endpoint): nothing to confirm
	}
	v := e.confirmKey(0, e.key, e.loseOwn)
	if v == verdictHeld && stream == LegacyStream && e.hasLegacyStream() {
		v = e.confirmKey(1, e.legacyKey, e.legacyLoser())
	}
	return v
}

func (e *Endpoint) confirmKey(i int, key string, lose func(error)) leaseVerdict {
	e.live.mu.Lock()
	defer e.live.mu.Unlock()
	if e.live.res[i] != 0 && time.Since(e.live.at[i]) < leaseConfirmFor {
		return e.live.res[i]
	}
	v := e.readKey(key, lose)
	if v != verdictLost {
		e.live.res[i], e.live.at[i] = v, time.Now()
	}
	return v
}

func (e *Endpoint) readKey(key string, lose func(error)) leaseVerdict {
	ctx, cancel := context.WithTimeout(context.Background(), leaseConfirmTimeout)
	defer cancel()
	get := e.live.get
	if get == nil {
		get = func(ctx context.Context, key string) (jetstream.KeyValueEntry, error) { return e.leaseKV.Get(ctx, key) }
	}
	cur, err := get(ctx, key)
	if err != nil {
		return verdictUnknown
	}
	if fenced(cur.Value()) {
		if lose != nil {
			lose(fmt.Errorf("%w: %s", ErrDeprovisioned, key))
		}
		return verdictLost
	}
	var lv leaseValue
	if json.Unmarshal(cur.Value(), &lv) != nil {
		return verdictUnknown
	}
	if lv.Instance == e.instance {
		return verdictHeld
	}
	e.log.Printf("lease: %s is now held by another session (host %s, pid %s)", key, lv.Host, lv.PID)
	if lose != nil {
		lose(&LeaseLostError{Key: key, Host: lv.Host, PID: lv.PID, Since: lv.Since, SameSession: e.sameSession(lv)})
	}
	return verdictLost
}

// graceWait blocks until the takeover grace has passed, or ctx ends (false). It returns at once when no grace is owed.
func (e *Endpoint) graceWait(ctx context.Context) bool {
	e.graceMu.Lock()
	until := e.graceUntil
	e.graceMu.Unlock()
	d := time.Until(until)
	if d <= 0 {
		return true
	}
	e.log.Printf("lease: taken over from a live serve of this session; reading mail in %s, when it has stopped", d.Round(time.Millisecond))
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// startGrace records that the lease was taken over from a live holder of this session.
func (e *Endpoint) startGrace() {
	e.graceMu.Lock()
	defer e.graceMu.Unlock()
	if u := time.Now().Add(takeoverGrace()); u.After(e.graceUntil) {
		e.graceUntil = u
	}
}

// TakeoverPending reports whether this endpoint took its lease from a live holder of its own session, so that
// AwaitTakeover has something to wait for.
func (e *Endpoint) TakeoverPending() bool {
	e.graceMu.Lock()
	defer e.graceMu.Unlock()
	return !e.graceUntil.IsZero()
}

// AwaitTakeover returns nil at once when the lease was not taken over from a live serve of this session. Otherwise it
// waits until the takeover grace has passed (the earlier serve has stopped), then reads the own lease key afresh, not
// from a cached confirmation, and returns nil only when the lease is still held. A serve registers its SAP service
// after this, so the earlier serve and the new one never answer live prompts for one endpoint together. It returns
// ctx.Err() when ctx ends first and a *LeaseLostError when the lease was taken from this endpoint in the meantime.
func (e *Endpoint) AwaitTakeover(ctx context.Context) error {
	e.graceMu.Lock()
	until := e.graceUntil
	e.graceMu.Unlock()
	if until.IsZero() {
		return nil
	}
	if d := time.Until(until); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for {
		if e.leaseGone.Load() {
			return &LeaseLostError{Key: e.key}
		}
		var lostErr error
		lose := func(err error) {
			lostErr = err
			if e.loseOwn != nil {
				e.loseOwn(err)
			}
		}
		switch e.readKey(e.key, lose) {
		case verdictHeld:
			return nil
		case verdictLost:
			if lostErr == nil {
				lostErr = &LeaseLostError{Key: e.key}
			}
			return lostErr
		}
		select { // unknown: the read failed; try again
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
