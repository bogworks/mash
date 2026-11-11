package endpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

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
//   - held by another session at Open (or fenced by a deprovision: a tombstone counts as held): the endpoint does NOT
//     consume the legacy stream and says so (a log line, and the legacy inbox line of `mash status`). It still reads its
//     own stream. The legacy stream stays readable by number and in the inbox listing (a read-only GetMsg changes
//     nothing in the consumer); only the consumer is left alone. Nothing here retries: the endpoint reads the legacy
//     stream from the next start, once the other session is gone.
//   - lost during the session: the legacy consume loop stops, and from then on nothing of that stream is acknowledged
//     by this endpoint, whatever its state: mail still queued or in the deliver loop's batch is dropped (dropUnleased),
//     mail held for a hook (struct held) is forgotten with its waiting spool file (dropHeldLegacy), and every ack path
//     (ack, the duplicate ack of dedupe, deliverBatch, the sender-header drop) is gated on mayAck. JetStream redelivers
//     all of it to the new holder. Mail already shown before the loss gets no ack either: the next holder may show it
//     once more (a repeat is the safe side; an ack the holder never earned would be mail loss). Its spool tombstone, if a
//     hook had drained it, stays: it is inert while the stream is not read, and if this endpoint holds the lease again at
//     a later start the redelivery finds the tombstone and is acked then, without being shown again. Waiting spool files
//     never survive a start (ClearWaiting), so a restart cannot resurrect dropped legacy mail into an ack. It is logged;
//     the own stream's consumer keeps running and its mail is unaffected.
//   - a legacy-form session started later asks for its own key's lease, which this endpoint holds, and is refused as
//     today. A legacy-only endpoint never reaches this code (it has no alias).

// legacyLease is the endpoint's hold on the legacy identity's lease.
type legacyLease struct {
	state  atomic.Int32 // legacyState
	why    string       // the detail of a state other than none and read; written before state
	rev    atomic.Uint64
	epoch  atomic.Uint64 // the revision the lease was taken at: fixed for this hold, another hold has another
	cancel context.CancelFunc
	done   chan struct{}
	lost   chan error // buffered; a loss is sent once
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

func (e *Endpoint) setLegacy(s legacyState, why string) {
	e.legacy.why = why
	e.legacy.state.Store(int32(s))
}

// takeLegacyLease takes the lease of e.legacyKey if it is free. It never fails the open: a lease held by someone else
// only means the legacy stream is not consumed by this session.
func (e *Endpoint) takeLegacyLease(ctx context.Context) {
	val := e.lease()
	rev, err := e.leaseKV.Create(ctx, e.legacyKey, val)
	if err == nil {
		e.legacyTaken(rev)
		return
	}
	if !errors.Is(err, jetstream.ErrKeyExists) {
		e.setLegacy(legacyFailed, fmt.Sprintf("the lease of %s could not be taken: %v", e.legacyKey, err))
		e.log.Printf("legacy inbox: not read (%s)", e.legacy.why)
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
	e.setLegacy(legacyHeld, fmt.Sprintf("the lease of %s is held by another session", e.legacyKey))
	e.log.Printf("legacy inbox: not read (%s)", e.legacy.why)
}

func (e *Endpoint) legacyTaken(rev uint64) {
	e.legacy.rev.Store(rev)
	e.legacy.epoch.Store(rev)
	e.setLegacy(legacyRead, "")
	e.noteLegacyEpoch() // a spool already set (a later hold): its epoch file now names this hold
	e.log.Printf("legacy inbox: read as %s", e.legacyKey)
	ctx, cancel := context.WithCancel(context.Background())
	e.legacy.cancel, e.legacy.done, e.legacy.lost = cancel, make(chan struct{}), make(chan error, 1)
	go func() {
		defer close(e.legacy.done)
		t := time.NewTicker(LeaseRefresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := e.refreshKey(ctx, e.legacyKey, &e.legacy.rev); err != nil {
					e.setLegacy(legacyLostNow, fmt.Sprintf("the lease of %s was lost", e.legacyKey))
					e.log.Printf("legacy inbox: no longer read (%v)", err)
					e.legacy.lost <- err
					e.noteLegacyEpoch() // the instant the loss is learned, before dropHeld discards anything: a hook shows no legacy entry from here on
					return
				}
			}
		}
	}()
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
// there is nothing to record.
func (e *Endpoint) noteLegacyEpochLocked() {
	sp := e.epochSpool.Load()
	if sp == nil || !e.hasLegacyStream() {
		return
	}
	if err := sp.NoteEpoch(e.legacyEpoch()); err != nil {
		e.log.Printf("spool: legacy epoch: %v", err)
	}
}

// noteLegacyEpoch takes the spool lock and records the epoch in force.
func (e *Endpoint) noteLegacyEpoch() {
	sp := e.epochSpool.Load()
	if sp == nil || !e.hasLegacyStream() {
		return
	}
	unlock, err := sp.Lock()
	if err != nil {
		e.log.Printf("spool: legacy epoch: %v", err)
		return // the spool write and dropHeldLegacy record it again
	}
	defer unlock()
	e.noteLegacyEpochLocked()
}

// testLegacyCheckSeam and testLegacyPutSeam are test hooks, both run inside the spool lock of a legacy spool write: the
// first just before the check of the lease, the second between that check and the write.
var testLegacyCheckSeam, testLegacyPutSeam func()

// releaseLegacyLease stops the refresh and gives the lease back (called by Close, before the own lease is released).
func (e *Endpoint) releaseLegacyLease() {
	if e.legacy.cancel == nil {
		return
	}
	e.legacy.cancel()
	<-e.legacy.done
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
	unlock, err := sp.Lock()
	if err != nil {
		e.log.Printf("spool: legacy epoch: %v", err)
		e.setLegacy(legacyLostNow, "released")
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
		return "legacy inbox: not read (" + e.legacy.why + ")", true
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
		} else if p.msg != nil {
			_ = p.msg.Nak() // not an ack: redelivered to the next holder at once, not after the ack wait
		}
	}
	return kept
}

// mayAck reports whether this endpoint may acknowledge p: own-stream mail always; legacy mail only while the legacy
// lease is held. The check is made at each ack, so a loss between delivery and ack withholds the ack.
func (e *Endpoint) mayAck(p pending) bool {
	return p.mail.Stream != LegacyStream || !e.hasLegacyStream() || e.legacyReading()
}

// dropHeldLegacy forgets the legacy mail held for a hook once the lease is gone, acking none of it: its waiting spool
// file is removed (so a hook cannot show what the next holder will deliver again) and it leaves the held counts. A
// message a hook has already drained keeps its tombstone and is simply no longer tracked.
func (e *Endpoint) dropHeldLegacy(h *held) {
	if !e.hasLegacyStream() || e.legacyReading() || len(h.msgs) == 0 {
		return
	}
	var unlock func()
	if e.spool != nil {
		u, err := e.spool.Lock()
		if err != nil {
			e.log.Printf("spool: %v", err)
			return // retried at the next tick
		}
		unlock = u
		defer unlock()
		e.noteLegacyEpochLocked() // before anything is discarded: a hook already shows none of it
	}
	n := 0
	for seq, p := range h.msgs {
		if seq.Stream != LegacyStream {
			continue
		}
		if e.spool != nil {
			if err := e.spool.Discard(seq); err != nil {
				e.log.Printf("%v", err)
				continue
			}
		}
		_ = p.msg.Nak() // not an ack: redelivered to the next holder at once, not after the ack wait
		h.bytes -= len(p.mail.Body)
		h.stored -= storedSize(p)
		delete(h.msgs, seq)
		delete(h.at, seq)
		delete(h.rung, seq)
		delete(h.listed, seq)
		delete(h.fails, seq)
		h.full = false
		e.waking.done(seq)
		n++
	}
	if n > 0 {
		e.log.Printf("legacy inbox: dropped %d held message(s) unacked (the lease was lost); the next holder is redelivered them", n)
	}
}
