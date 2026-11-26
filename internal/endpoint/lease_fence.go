package endpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// The ack-time fence and the takeover grace (ADR 0092).
//
// watchLease notices a takeover within leaseWatchEvery, and until it does the earlier serve would keep delivering to its
// client and acking mail the new holder never sees. So before delivering a batch and before any ack the endpoint reads
// its lease key and compares the holder's instance with its own. Another
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

// liveLease caches the latest live check of the lease key.
type liveLease struct {
	mu  sync.Mutex
	at  time.Time
	res leaseVerdict
	// get, when set, replaces the direct read of a lease key (tests); guarded by mu.
	get func(ctx context.Context, key string) (jetstream.KeyValueEntry, error)
}

// verdict is the live state of the lease that lets this endpoint deliver and acknowledge mail.
func (e *Endpoint) verdict() leaseVerdict {
	if e.leaseGone.Load() {
		return verdictLost
	}
	if e.leaseKV == nil {
		return verdictHeld // no lease bucket (not a serving endpoint): nothing to confirm
	}
	return e.confirm()
}

func (e *Endpoint) confirm() leaseVerdict {
	e.live.mu.Lock()
	defer e.live.mu.Unlock()
	if e.live.res != 0 && time.Since(e.live.at) < leaseConfirmFor {
		return e.live.res
	}
	v := e.readKey(e.key, e.loseOwn)
	if v != verdictLost {
		e.live.res, e.live.at = v, time.Now()
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

// dropUnleased removes from batch the mail that may not be delivered, without acknowledging it: when the lease is gone
// the mail is nak'd (which redelivers it to the holder's consumer at once), and when the lease cannot be confirmed just
// now it is nak'd with a delay. The lease is read live, so a takeover the watch has not yet noticed is noticed here,
// before anything is delivered; the read is reused for leaseConfirmFor, so a batch costs one. It returns batch itself
// when nothing is removed.
func (e *Endpoint) dropUnleased(batch []pending) []pending {
	if len(batch) == 0 {
		return batch
	}
	v := e.verdict()
	if v == verdictHeld {
		return batch
	}
	for _, p := range batch {
		if p.msg != nil {
			if v == verdictLost {
				_ = p.msg.Nak() // not an ack: redelivered to the next holder at once, not after the ack wait
			} else {
				_ = p.msg.NakWithDelay(time.Second) // not confirmed: not delivered, not acked
			}
		}
		e.waking.done(refOf(p.mail)) // no longer ours to count
	}
	return nil
}

// mayAck reports whether this endpoint may acknowledge mail: it holds the lease, confirmed live (an unconfirmed lease
// is not enough: the ack is withheld and the mail redelivered).
func (e *Endpoint) mayAck(pending) bool { return e.verdict() == verdictHeld }

// dropHeld forgets the mail held for a hook once this endpoint no longer holds the lease, acking none of it. Its waiting
// spool file is removed (so a hook cannot show what the holder will deliver again), it leaves the held counts and the
// seen set, and it is nak'd so the holder's consumer is redelivered it at once. A message a hook has already drained
// keeps its tombstone and is simply no longer tracked.
func (e *Endpoint) dropHeld(h *held) {
	if len(h.msgs) == 0 || !e.leaseGone.Load() {
		return
	}
	if e.spool != nil {
		u, err := e.spool.LockContext(h.lockCtx())
		if err != nil {
			e.log.Printf("spool: %v", err)
			return // retried at the next tick
		}
		defer u()
	}
	n := 0
	for seq, p := range h.msgs {
		if e.spool != nil {
			if err := e.spool.Discard(seq); err != nil {
				e.log.Printf("%v", err)
				continue
			}
		}
		_ = p.msg.Nak() // not an ack: redelivered to the holder at once, not after the ack wait
		h.bytes -= len(p.mail.Body)
		h.stored -= storedSize(p)
		delete(h.msgs, seq)
		delete(h.at, seq)
		delete(h.rung, seq)
		delete(h.listed, seq)
		delete(h.fails, seq)
		h.seen.forget(p)
		h.full = false
		e.waking.done(seq)
		n++
	}
	if n > 0 {
		e.log.Printf("dropped %d held message(s) unacked (the lease was lost); the next holder is redelivered them", n)
	}
}
