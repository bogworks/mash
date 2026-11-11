package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/spool"
	"github.com/bogworks/mash/internal/store"
)

// The legacy identity's lease (naming plan 1.4b, security finding S1).
//
// A three-token endpoint whose own record carries an alias reads the legacy inbox stream through that stream's existing
// durable consumer. A legacy-form session of the same identity holds the lease keyed by the flat legacy name and pulls
// the same consumer. Two sessions with two different leases (the own key's and the legacy key's) would split the legacy
// mail between them: one would show and acknowledge what the other should have received. So the new-form endpoint takes
// the legacy identity's lease too, in the same lease bucket, by the same compare-and-set and with the same refresh and
// release as its own, and reads (consumes and acknowledges) the legacy stream only while it holds it:
//
//   - free at Open: taken after the own lease and the alias read; refreshed and released like the own lease.
//   - held by another session at Open (or fenced by a deprovision: a tombstone counts as held), or not takeable for a
//     transport error: the endpoint does NOT consume the legacy stream and says so (a log line, and the legacy inbox line
//     of `mash status`). It still reads its own stream. The legacy stream stays readable by number and in the inbox
//     listing (a read-only GetMsg changes nothing in the consumer); only the consumer is left alone. A retry loop
//     (legacyRetryLoop) asks for the lease again with exponential backoff and jitter (LegacyRetryMin up to LegacyRetryMax),
//     so once the other session is gone the mail queued meanwhile is read by this session, not left to age out of the
//     stream; the consume loop starts reading the legacy stream the moment the lease is held (legacyManager).
//   - lost during the session: the legacy consume loop stops, and from then on nothing of that stream is acknowledged
//     by this endpoint, whatever its state: mail still queued or in the deliver loop's batch is dropped (dropUnleased),
//     mail held for a hook (struct held) is forgotten with its waiting spool file (dropHeldLegacy), and every ack path
//     (ack, the duplicate ack of dedupe, deliverBatch, the sender-header drop) is gated on mayAck. JetStream redelivers
//     all of it to the new holder. Mail already shown before the loss gets no ack either: the next holder may show it
//     once more (a repeat is the safe side; an ack the holder never earned would be mail loss). Its spool tombstone, if a
//     hook had drained it, stays: it is inert while the stream is not read, and if this endpoint holds the lease again at
//     a later hold the redelivery finds the tombstone and is acked then, without being shown again. Waiting spool files
//     never survive a start (ClearWaiting), so a restart cannot resurrect dropped legacy mail into an ack. It is logged;
//     the own stream's consumer keeps running and its mail is unaffected. The retry loop then runs again: the other
//     session may exit later.
//   - a legacy-form session started later asks for its own key's lease, which this endpoint holds, and is refused as
//     today. A legacy-only endpoint never reaches this code (it has no alias).

// LegacyRetryMin and LegacyRetryMax bound the wait between two attempts to take the legacy lease while it is not held:
// the wait starts at the first, doubles after each failed attempt up to the second, and is jittered (half to full).
var (
	LegacyRetryMin = 5 * time.Second
	LegacyRetryMax = time.Minute
)

// legacyEpochTries is how often the epoch of a new hold is written before the hold is given up.
var legacyEpochTries = 3

// legacyNoteEpoch writes the epoch file; replaceable in tests.
var legacyNoteEpoch = func(sp *spool.Spool, epoch uint64) error { return sp.NoteEpoch(epoch) }

// legacyLockSpool takes the spool lock to record the epoch; replaceable in tests.
var legacyLockSpool = func(sp *spool.Spool) (func(), error) { return sp.Lock() }

// legacyStopWarn is how long the manager waits for a stopped consume loop to close before it says it is still waiting.
var legacyStopWarn = 5 * time.Second

// legacyLease is the endpoint's hold on the legacy identity's lease.
type legacyLease struct {
	state atomic.Int32           // legacyState
	why   atomic.Pointer[string] // the detail of a state other than none and read
	rev   atomic.Uint64
	epoch atomic.Uint64 // the revision the lease was taken at: fixed for this hold, another hold has another

	mu     sync.Mutex // guards cancel, done and lost: each hold has its own, never reused
	cancel context.CancelFunc
	done   chan struct{}
	lost   chan error // buffered; a loss is sent once

	wake    chan struct{}                         // capacity 1: the state changed (the consume loop re-checks)
	changed chan struct{}                         // capacity 1: the state changed (the retry loop re-checks)
	floor   atomic.Pointer[func() (uint64, bool)] // the legacy consumer's ack floor, once it exists
	loops   atomic.Int32                          // consume loops of the legacy stream not yet closed: never more than one

	retryCtx    context.Context
	retryCancel context.CancelFunc
	retryDone   chan struct{}
	ownLost     atomic.Bool // the endpoint's own lease is gone: no retry
}

type legacyState int32

const (
	legacyNone    legacyState = iota // no alias: no legacy stream
	legacyRead                       // the lease is held: the legacy stream is consumed
	legacyHeld                       // held by another session (or fenced) at Open: not consumed
	legacyFailed                     // the lease could not be taken (transport): not consumed
	legacyLostNow                    // lost during the session: no longer consumed
)

// legacyReading reports whether this endpoint holds the legacy lease, and so may consume and acknowledge the legacy
// stream.
func (e *Endpoint) legacyReading() bool { return legacyState(e.legacy.state.Load()) == legacyRead }

func (e *Endpoint) setLegacy(st legacyState, why string) {
	e.legacy.why.Store(&why)
	e.legacy.state.Store(int32(st))
}

func (e *Endpoint) legacyWhy() string {
	if p := e.legacy.why.Load(); p != nil {
		return *p
	}
	return ""
}

func (e *Endpoint) legacyPoke() {
	for _, c := range []chan struct{}{e.legacy.wake, e.legacy.changed} {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

// legacyHold is the current hold's lost channel and epoch.
func (e *Endpoint) legacyHold() (lost chan error, epoch uint64) {
	e.legacy.mu.Lock()
	defer e.legacy.mu.Unlock()
	return e.legacy.lost, e.legacy.epoch.Load()
}

// startLegacy prepares the legacy state of an endpoint with an alias (before takeLegacyLease) and starts the retry loop.
func (e *Endpoint) startLegacy(ctx context.Context) {
	e.legacy.wake, e.legacy.changed = make(chan struct{}, 1), make(chan struct{}, 1)
	e.takeLegacyLease(ctx)
	e.legacy.retryCtx, e.legacy.retryCancel = context.WithCancel(context.Background())
	e.legacy.retryDone = make(chan struct{})
	go e.legacyRetryLoop()
}

// stopLegacyRetry stops the retry loop and waits for it. It is idempotent.
func (e *Endpoint) stopLegacyRetry() {
	if e.legacy.retryCancel == nil {
		return
	}
	e.legacy.retryCancel()
	<-e.legacy.retryDone
}

// legacyRetryLoop asks for the legacy lease again whenever it is not held, until Close (or the loss of the endpoint's own
// lease). The wait doubles after each failed attempt up to LegacyRetryMax and starts over after a take.
func (e *Endpoint) legacyRetryLoop() {
	defer close(e.legacy.retryDone)
	ctx := e.legacy.retryCtx
	backoff := LegacyRetryMin
	for {
		if e.legacyReading() {
			select {
			case <-ctx.Done():
				return
			case <-e.legacy.changed:
			}
			backoff = LegacyRetryMin
			continue
		}
		d := backoff/2 + time.Duration(rand.Int64N(int64(backoff/2)+1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		if e.legacy.ownLost.Load() {
			return // the own lease is gone: this endpoint is ending
		}
		if e.legacyReading() {
			continue
		}
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		e.takeLegacyLease(tctx)
		cancel()
		if e.legacyReading() {
			backoff = LegacyRetryMin
		} else {
			backoff = min(backoff*2, LegacyRetryMax)
		}
	}
}

// notReadAs sets a not-read state and logs it once: a retry that finds the same state again says nothing.
func (e *Endpoint) notReadAs(st legacyState, why string) {
	same := legacyState(e.legacy.state.Load()) == st && e.legacyWhy() == why
	e.setLegacy(st, why)
	if !same {
		e.log.Printf("legacy inbox: not read (%s)", why)
	}
	e.legacyPoke()
}

// takeLegacyLease takes the lease of e.legacyKey if it is free. It never fails the open: a lease held by someone else
// only means the legacy stream is not consumed by this session. It may be called again after a failure or a loss (the
// retry loop does); it does nothing while the lease is held.
func (e *Endpoint) takeLegacyLease(ctx context.Context) {
	if e.legacyReading() {
		return
	}
	val := e.lease()
	rev, err := e.leaseKV.Create(ctx, e.legacyKey, val)
	if err == nil {
		e.legacyTaken(rev)
		return
	}
	if !errors.Is(err, jetstream.ErrKeyExists) {
		e.notReadAs(legacyFailed, fmt.Sprintf("the lease of %s could not be taken: %v", e.legacyKey, err))
		return
	}
	if cur, gerr := e.leaseKV.Get(ctx, e.legacyKey); gerr == nil && !fenced(cur.Value()) {
		var lv leaseValue
		// A holder on this machine whose process is gone is taken over, as for the own lease (G270).
		if json.Unmarshal(cur.Value(), &lv) == nil {
			if me := ourMachine(); me != "" && lv.Machine == me && lv.PID != fmt.Sprint(os.Getpid()) && holderGone(lv.PID, lv.Start) {
				if rev, uerr := e.leaseKV.Update(ctx, e.legacyKey, val, cur.Revision()); uerr == nil {
					e.log.Printf("lease: took over %s from dead holder %s (pid %s)", e.legacyKey, lv.Instance, lv.PID)
					e.legacyTaken(rev)
					return
				}
			}
		}
	}
	e.notReadAs(legacyHeld, fmt.Sprintf("the lease of %s is held by another session", e.legacyKey))
}

// legacyTaken starts a new hold: its own epoch, cancel, done and lost (nothing of a previous hold is reused), its
// refresh goroutine, and the epoch recorded in the spool. If the epoch cannot be recorded the hold is given up.
func (e *Endpoint) legacyTaken(rev uint64) {
	e.legacy.mu.Lock()
	oldCancel, oldDone := e.legacy.cancel, e.legacy.done
	e.legacy.mu.Unlock()
	if oldCancel != nil { // a previous hold: its refresh goroutine has ended or is ending
		oldCancel()
		<-oldDone
	}
	ctx, cancel := context.WithCancel(context.Background())
	done, lost := make(chan struct{}), make(chan error, 1)
	e.legacy.mu.Lock()
	e.legacy.cancel, e.legacy.done, e.legacy.lost = cancel, done, lost
	e.legacy.mu.Unlock()
	e.legacy.rev.Store(rev)
	e.legacy.epoch.Store(rev)
	e.setLegacy(legacyRead, "")
	if err := e.noteLegacyEpoch(); err != nil { // a spool already set (a later hold): its epoch file now names this hold
		close(done) // no refresh goroutine was started
		e.failLegacyHold(err, false)
		return
	}
	e.log.Printf("legacy inbox: read as %s", e.legacyKey)
	go func() {
		defer close(done)
		t := time.NewTicker(LeaseRefresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := e.refreshKey(ctx, e.legacyKey, &e.legacy.rev); err != nil {
					if !e.legacy.state.CompareAndSwap(int32(legacyRead), int32(legacyLostNow)) {
						return // given up meanwhile (failLegacyHold)
					}
					why := fmt.Sprintf("the lease of %s was lost", e.legacyKey)
					e.legacy.why.Store(&why)
					e.log.Printf("legacy inbox: no longer read (%v)", err)
					lost <- err
					e.noteLegacyEpoch() // the instant the loss is learned, before dropHeld discards anything: a hook shows no legacy entry from here on
					e.legacyPoke()
					return
				}
			}
		}
	}()
	e.legacyPoke()
}

// failLegacyHold gives up a hold whose epoch could not be recorded (the legacy entries would be hidden and never
// drained): it is no longer read, the lease is released so another session can take it, and the retry loop tries again
// later. spoolLocked is true when the caller holds the spool lock.
func (e *Endpoint) failLegacyHold(cause error, spoolLocked bool) {
	why := fmt.Sprintf("the epoch of the hold on %s could not be recorded: %v", e.legacyKey, cause)
	e.setLegacy(legacyFailed, why) // not read from here on
	e.legacy.mu.Lock()
	cancel, done := e.legacy.cancel, e.legacy.done
	e.legacy.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	if spoolLocked {
		e.noteLegacyEpochLocked()
	} else {
		e.noteLegacyEpoch()
	}
	ctx, cancelRel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancelRel()
	if err := e.releaseKey(ctx, e.legacyKey, &e.legacy.rev); err != nil {
		e.log.Printf("lease release (%s): %v; a legacy session waits up to %s for it to expire", e.legacyKey, err, store.LeaseTTL)
	}
	e.log.Printf("legacy inbox: not read (%s); the lease is given back and taken again later", why)
	e.legacyPoke()
}

// The legacy epoch (a legacy message must not be shown by the old session and redelivered to the new holder). A hold of
// the legacy lease has an epoch, the revision it was taken at; the spool's .legacy-epoch file names the epoch of the hold
// in force, or "none". It is written under the spool lock when the lease is taken and the moment its loss is learned,
// before dropHeld runs; every legacy entry is written under that lock after a check of the lease flag and stamped with
// the epoch read there; and a hook (which holds the same lock) shows a stamped entry only while the file still names its
// epoch. A loss between the check and the write therefore cannot show: the loss records "none" under the lock, after
// the write, and the entry is then skipped by every hook read.
//
// Lock order: the spool lock is taken here by the refresh goroutine and by the deliver loop, and neither holds another
// mutex while it waits (the lease flags are atomics), so no spool-lock holder can wait on them.

// legacyEpoch is the epoch to record now: that of the hold while the lease is held, else 0 for none.
func (e *Endpoint) legacyEpoch() uint64 {
	if e.legacyReading() {
		return e.legacy.epoch.Load()
	}
	return 0
}

// noteLegacyEpochLocked records the epoch in force, the caller holding the spool lock. Without a spool, or an alias,
// there is nothing to record. The epoch of a hold is written up to legacyEpochTries times; the returned error is that
// of a hold's epoch that could not be recorded (the caller gives the hold up). A failed record of "none" is only logged:
// the file is removed then, which hides every stamped entry (fail closed).
func (e *Endpoint) noteLegacyEpochLocked() error {
	sp := e.epochSpool.Load()
	if sp == nil || !e.hasLegacyStream() {
		return nil
	}
	ep := e.legacyEpoch()
	tries := 1
	if ep != 0 {
		tries = legacyEpochTries
	}
	var err error
	for i := 0; i < tries; i++ {
		if err = legacyNoteEpoch(sp, ep); err == nil {
			return nil
		}
		e.log.Printf("spool: legacy epoch: %v", err)
	}
	if ep == 0 {
		return nil
	}
	return err
}

// noteLegacyEpoch takes the spool lock and records the epoch in force.
func (e *Endpoint) noteLegacyEpoch() error {
	sp := e.epochSpool.Load()
	if sp == nil || !e.hasLegacyStream() {
		return nil
	}
	tries := 1
	if e.legacyEpoch() != 0 {
		tries = legacyEpochTries
	}
	var err error
	for i := 0; i < tries; i++ {
		var unlock func()
		if unlock, err = legacyLockSpool(sp); err == nil {
			defer unlock()
			return e.noteLegacyEpochLocked()
		}
		e.log.Printf("spool: legacy epoch: %v", err)
	}
	if tries == 1 {
		// recording "none" without the lock: take the old epoch away instead (a missing file hides every stamped entry)
		e.removeLegacyEpoch(sp)
		return nil
	}
	return err // the epoch of a hold could not be recorded: the caller gives the hold up
}

// removeLegacyEpoch removes the epoch file without the spool lock, failing closed when "none" cannot be recorded.
func (e *Endpoint) removeLegacyEpoch(sp *spool.Spool) {
	if err := sp.RemoveEpoch(); err != nil {
		e.log.Printf("spool: legacy epoch: removing %v", err)
		return
	}
	e.log.Printf("spool: legacy epoch: removed without the spool lock, stamped legacy entries are hidden")
}

// testLegacyHandlerGate is a test hook run at the top of every legacy-stream message callback.
var testLegacyHandlerGate func()

// testLegacyCheckSeam and testLegacyPutSeam are test hooks, both run inside the spool lock of a legacy spool write: the
// first just before the check of the lease, the second between that check and the write.
var testLegacyCheckSeam, testLegacyPutSeam func()

// releaseLegacyLease stops the refresh and gives the lease back (called by Close, before the own lease is released).
func (e *Endpoint) releaseLegacyLease() {
	e.stopLegacyRetry() // first: nothing takes the lease again once it is given back
	e.legacy.mu.Lock()
	cancel, done := e.legacy.cancel, e.legacy.done
	e.legacy.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	if legacyState(e.legacy.state.Load()) != legacyRead {
		return // lost: the lease is not ours to release
	}
	e.noteLegacyReleased() // before the release: a hook of this session shows no legacy mail the next holder will get
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	if err := e.releaseKey(ctx, e.legacyKey, &e.legacy.rev); err != nil {
		e.log.Printf("lease release (%s): %v; a legacy session waits up to %s for it to expire", e.legacyKey, err, store.LeaseTTL)
	}
}

// noteLegacyReleased records epoch none under the spool lock and marks the lease no longer read, so no later spool write
// stamps the epoch of a hold that is about to be given back.
func (e *Endpoint) noteLegacyReleased() {
	sp := e.epochSpool.Load()
	if sp == nil {
		e.setLegacy(legacyLostNow, "released")
		return
	}
	unlock, err := legacyLockSpool(sp)
	if err != nil {
		e.log.Printf("spool: legacy epoch: %v", err)
		e.setLegacy(legacyLostNow, "released")
		e.removeLegacyEpoch(sp)
		return
	}
	defer unlock()
	e.setLegacy(legacyLostNow, "released")
	e.noteLegacyEpochLocked() // not read now, so this writes none
}

// LegacyInboxLine is the legacy inbox line of status: what this endpoint does with its legacy stream. ok is false for
// an endpoint with no legacy stream (no line).
func (e *Endpoint) LegacyInboxLine() (line string, ok bool) {
	switch legacyState(e.legacy.state.Load()) {
	case legacyRead:
		return "legacy inbox: read as " + e.legacyKey, true
	case legacyHeld, legacyFailed, legacyLostNow:
		return "legacy inbox: not read (" + e.legacyWhy() + ")", true
	}
	return "", false
}

// dropUnleased removes the mail of the legacy stream from batch once the lease is gone, without acknowledging it (it is
// nak'd, which redelivers it to the holder's consumer). It returns batch itself when nothing is removed.
func (e *Endpoint) dropUnleased(batch []pending) []pending {
	if !e.hasLegacyStream() || e.legacyReading() {
		return batch
	}
	kept := batch[:0:0]
	for _, p := range batch {
		if p.mail.Stream != LegacyStream {
			kept = append(kept, p)
		} else {
			if p.msg != nil {
				_ = p.msg.Nak() // not an ack: redelivered to the next holder at once, not after the ack wait
			}
			e.waking.done(refOf(p.mail)) // no longer ours to count
		}
	}
	return kept
}

// fenceLegacy is the last check before a batch is written to the channel: the lease may have been lost since
// dropUnleased looked, and mail of the legacy stream must not be shown by a session that no longer holds it. Such mail
// is dropped unacked and nak'd (the new holder's consumer is redelivered it at once); a duplicate around a takeover is
// acceptable, a loss is not. Own-stream mail and mail under a held lease pass through unchanged.
func (e *Endpoint) fenceLegacy(show []pending) []pending {
	return e.dropUnleased(show)
}

// mayAck reports whether this endpoint may acknowledge p: own-stream mail always; legacy mail only while the legacy
// lease is held. The check is made at each ack, so a loss between delivery and ack withholds the ack.
func (e *Endpoint) mayAck(p pending) bool {
	return p.mail.Stream != LegacyStream || !e.hasLegacyStream() || e.legacyReading()
}

// dropHeldLegacy forgets the legacy mail held for a hook that this endpoint no longer holds the lease for, acking none of
// it: mail held while the lease is not held at all, and mail spooled under an earlier hold than the current one (the lease
// was lost and taken again before the delivery worker ran: the new epoch hides those spool files, and a redelivery would
// be taken for mail already held, so it would never be shown). Its waiting spool file is removed (so a hook cannot show
// what the holder will deliver again), it leaves the held counts and the seen set, and it is nak'd so the current hold's
// consumer is redelivered it at once. A message a hook has already drained keeps its tombstone and is simply no longer
// tracked. Mail spooled under the current epoch is untouched.
func (e *Endpoint) dropHeldLegacy(h *held) {
	if !e.hasLegacyStream() || len(h.msgs) == 0 {
		return
	}
	stale := func(seq spool.Ref) bool {
		return seq.Stream == LegacyStream && (!e.legacyReading() || h.epochs[seq] != e.legacy.epoch.Load())
	}
	any := false
	for seq := range h.msgs {
		if stale(seq) {
			any = true
			break
		}
	}
	if !any {
		return
	}
	if e.spool != nil {
		u, err := e.spool.Lock()
		if err != nil {
			e.log.Printf("spool: %v", err)
			return // retried at the next tick
		}
		defer u()
		if !e.legacyReading() {
			e.noteLegacyEpochLocked() // before anything is discarded: a hook already shows none of it
		}
	}
	n := 0
	for seq, p := range h.msgs {
		if !stale(seq) {
			continue
		}
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
		delete(h.epochs, seq)
		h.seen.forget(p)
		h.full = false
		e.waking.done(seq)
		n++
	}
	if n > 0 {
		e.log.Printf("legacy inbox: dropped %d held message(s) unacked (the lease was lost); the holder is redelivered them", n)
	}
}
