package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

// A limit change reads an inbox stream's caps, checks them and writes them back, and JetStream has no
// compare-and-set on a stream configuration. The admin therefore takes a per-stream lock in the lease bucket before
// the read and holds it through the write (ADR-0048).
//
// The lock is the key limits-lock.<name>. No endpoint can reach it: an endpoint may publish to and read exactly
// $KV.mash-leases.<its own name> and names hold no dot, so the lock's subject matches no endpoint grant. The lease
// bucket's own 90 s TTL removes a lock nobody released, behind the lock's 30 s expiry.
//
// Expiry is judged by each admin's own clock, so admins' clocks must agree to within a few seconds; lockTTL is
// generous for that. A critical section must never outlast its lock, because another admin could then take the name
// (a resume, say) while this one carries on (a guarded deprovision deleting the stream after mail resumed). So the
// holder records a deadline, the local time before it acquired plus lockTTL, and every operation under the lock:
//   - runs on a context that ends lockSafety before that deadline (bound), so a stalled call fails instead of
//     running on;
//   - re-reads the lock key just before each write the lock protects (check), and refuses unless the key is still
//     its own revision and the deadline minus lockSafety is still ahead.
//
// A refused or stopped operation reports that nothing after that point was changed, and the caller may try again.
// Admin_timeout may therefore be longer than the lock, but an operation that needs longer than the lock fails.

// Lock timing. Variables so tests can shorten them.
var (
	lockTTL  = 30 * time.Second       // how long an acquired lock is valid
	lockWait = 20 * time.Second       // how long acquire waits for a held lock before giving up
	lockPoll = 200 * time.Millisecond // base wait between attempts; up to the same again is added as jitter

	// lockSafety is how long before its deadline a held lock stops being used: bound ends the operation's context
	// then, and check refuses from then on.
	lockSafety = 5 * time.Second

	// lockRefresh is how often a refreshing lock (the account lock) extends its expiry while held. It is below
	// lockTTL minus lockSafety, so a lock that refreshes on time never reaches the point where check refuses it.
	lockRefresh = 10 * time.Second
)

// ErrLockExpiring is the cause of a bound context that ended because its lock was about to expire.
var ErrLockExpiring = errors.New("the name lock is about to expire")

// lockCheckTimeout bounds the re-read check makes of the lock key.
const lockCheckTimeout = 3 * time.Second

// afterLockAcquired is a test seam that runs once the operation holds the lock and its context is bounded; nil in
// production.
var afterLockAcquired func()

// lockReleaseTimeout bounds the release call, which runs even when the caller's context is done.
const lockReleaseTimeout = 5 * time.Second

// lockKeyPrefix and lockKey name the lock of stream name.
const lockKeyPrefix = "limits-lock."

func lockKey(name string) string { return lockKeyPrefix + name }

// lockValue is the JSON stored at a lock key.
type lockValue struct {
	Owner string `json:"owner"`
	Until string `json:"until"` // RFC 3339, the holder's clock
}

// streamLock is a held lock. Release it exactly once.
//
// A lock taken with refresh also extends its own expiry in the background (lockRefresh) until released, so an
// operation of any length may hold it; a refresh that finds the key no longer its own revision marks the lock lost
// for good and check refuses from then on. A lock without refresh (the limits locks) is valid for lockTTL only.
type streamLock struct {
	kv    jetstream.KeyValue
	name  string
	key   string
	owner string

	mu       sync.Mutex // guards rev, deadline and lost; held across a refresh write and a check read
	rev      uint64
	deadline time.Time // local time before the acquiring call plus lockTTL: when this lock may be taken from us
	lost     bool      // a refresh found the lock gone or taken

	stop chan struct{} // closed by release; nil for a lock without refresh
	done chan struct{} // closed when the refresh goroutine has ended
}

// lockBusyError is the error of a lock that stayed held by another until the wait ran out.
type lockBusyError struct {
	name  string
	until time.Time
}

func (e *lockBusyError) Error() string {
	return fmt.Sprintf("mash: another admin is changing the limits of %s (lock held until %s); try again",
		e.name, e.until.UTC().Format(time.RFC3339))
}

// leaseKV returns the lease bucket, creating it with the configured limits if a deployment from before the lock has
// not been re-initialised (`mash admin init` creates it). The bucket it returns refuses rollup: a new one is created refusing
// rollup, an existing one is verified and hardened, and one that cannot be is an error.
func (a *Admin) leaseKV(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := a.js.KeyValue(ctx, store.LeaseBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		if kv, err = store.CreateLeaseBucket(ctx, a.js, store.LeaseConfigFor(a.limits)); err != nil {
			// Lost a race with another admin creating it, or no right to create: read it again.
			if kv2, err2 := a.js.KeyValue(ctx, store.LeaseBucket); err2 == nil {
				kv, err = kv2, nil
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("mash: lease bucket (run `mash admin init`): %w", err)
	}
	if err := store.HardenLeaseBucket(ctx, a.js); err != nil {
		return nil, fmt.Errorf("mash: %w", err)
	}
	return kv, nil
}

// newLockValue is the value of a lock acquired at start; it expires at start plus lockTTL.
func newLockValue(owner string, start time.Time) []byte {
	b, _ := json.Marshal(lockValue{Owner: owner, Until: start.Add(lockTTL).UTC().Format(time.RFC3339)})
	return b
}

// lockPause is the wait before the next attempt: lockPoll plus up to lockPoll of jitter.
func lockPause() time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(lockPoll)+1))
	if err != nil {
		return lockPoll
	}
	return lockPoll + time.Duration(n.Int64())
}

// lockStream takes name's limits lock, waiting up to lockWait for a holder. A lock whose time has passed is taken
// over with a compare-and-set on its revision, so exactly one of several takers wins.
func (a *Admin) lockStream(ctx context.Context, name string) (*streamLock, error) {
	return a.lockNamed(ctx, name, lockWait, false)
}

// lockNamed is lockStream with its own wait bound, and with background refresh when refresh is set. A lock still
// held by another when the wait ends is a *lockBusyError.
func (a *Admin) lockNamed(ctx context.Context, name string, wait time.Duration, refresh bool) (*streamLock, error) {
	kv, err := a.leaseKV(ctx)
	if err != nil {
		return nil, err
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	owner := hex.EncodeToString(raw[:])
	key := lockKey(name)
	giveUp := time.Now().Add(wait)
	for {
		start := time.Now() // before the call, so the deadline is never later than the key's expiry
		rev, err := kv.Create(ctx, key, newLockValue(owner, start))
		if err == nil {
			return newHeldLock(kv, name, key, owner, rev, start, refresh), nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return nil, fmt.Errorf("mash: limits lock of %s: %w", name, err)
		}
		e, err := kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue // released between the two calls
		}
		if err != nil {
			return nil, fmt.Errorf("mash: limits lock of %s: %w", name, err)
		}
		var cur lockValue
		perr := json.Unmarshal(e.Value(), &cur)
		var until time.Time
		if perr == nil {
			until, perr = time.Parse(time.RFC3339, cur.Until)
		}
		if perr != nil || !time.Now().Before(until) {
			// Expired, or unreadable and so no one's: take it over only if it is still the revision we read.
			start := time.Now()
			rev, err := kv.Update(ctx, key, newLockValue(owner, start), e.Revision())
			if err == nil {
				return newHeldLock(kv, name, key, owner, rev, start, refresh), nil
			}
			if !isRevisionConflict(err) {
				return nil, fmt.Errorf("mash: limits lock of %s: %w", name, err)
			}
			continue // someone else took it first; look again
		}
		if !time.Now().Before(giveUp) {
			return nil, &lockBusyError{name: name, until: until}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(min(lockPause(), time.Until(giveUp))):
		}
	}
}

// newHeldLock builds the handle of a lock just acquired at start and, with refresh, starts its refresh goroutine.
func newHeldLock(kv jetstream.KeyValue, name, key, owner string, rev uint64, start time.Time, refresh bool) *streamLock {
	l := &streamLock{kv: kv, name: name, key: key, owner: owner, rev: rev, deadline: start.Add(lockTTL)}
	if refresh {
		l.stop, l.done = make(chan struct{}), make(chan struct{})
		go l.refreshLoop()
	}
	return l
}

// refreshLoop extends the lock's expiry every lockRefresh until release.
func (l *streamLock) refreshLoop() {
	defer close(l.done)
	t := time.NewTicker(lockRefresh)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			l.refreshOnce()
		}
	}
}

// refreshOnce rewrites the lock key with a new expiry, as a compare-and-set on the revision it holds. A transient
// failure is retried at the next tick (check refuses meanwhile if the deadline gets close); a conflict or a missing
// key means the lock was taken or removed, and it is lost for good.
func (l *streamLock) refreshOnce() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lost {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), lockCheckTimeout)
	defer cancel()
	start := time.Now()
	rev, err := l.kv.Update(ctx, l.key, newLockValue(l.owner, start), l.rev)
	switch {
	case err == nil:
		l.rev, l.deadline = rev, start.Add(lockTTL)
	case isRevisionConflict(err) || errors.Is(err, jetstream.ErrKeyNotFound):
		l.lost = true
	}
}

// release removes the lock (a revision-aware delete: the lease bucket does not allow rollup, so no purge), but only the revision this admin wrote: if the lock expired and another admin took it
// over, that admin's lock stays. Errors are ignored: a lock that cannot be removed expires.
func (l *streamLock) release(ctx context.Context) {
	if l.stop != nil {
		close(l.stop)
		<-l.done
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockReleaseTimeout)
	defer cancel()
	l.mu.Lock()
	rev, lost := l.rev, l.lost
	l.mu.Unlock()
	if lost {
		return // not ours any more
	}
	_ = l.kv.Delete(ctx, l.key, jetstream.LastRevision(rev))
}

// bound returns ctx cut off lockSafety before the lock's deadline, with cause ErrLockExpiring. A caller's context
// that ends earlier keeps its own end.
func (l *streamLock) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	l.mu.Lock()
	d := l.deadline
	l.mu.Unlock()
	return context.WithDeadlineCause(ctx, d.Add(-lockSafety), ErrLockExpiring)
}

// check refuses unless the lock is still this admin's (the stored revision is the one it wrote) and not within
// lockSafety of its deadline. Call it immediately before a write that cannot be undone or that another admin could
// invalidate.
func (l *streamLock) check(ctx context.Context) error {
	lost := fmt.Errorf("mash: the lock of %s was lost or is about to expire; nothing further was changed; try again", l.name)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lost || !time.Now().Before(l.deadline.Add(-lockSafety)) {
		return lost
	}
	if err := ctx.Err(); err != nil {
		return err // explain words a bound context's end
	}
	cctx, cancel := context.WithTimeout(ctx, lockCheckTimeout)
	defer cancel()
	e, err := l.kv.Get(cctx, l.key)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", lost, err)
	}
	if e.Revision() != l.rev || l.lost || !time.Now().Before(l.deadline.Add(-lockSafety)) {
		return lost
	}
	return nil
}

// explain words err for an operation op whose context (from bound) ended because the lock was about to expire.
func (l *streamLock) explain(ctx context.Context, op string, err error) error {
	var p *partialError
	if errors.As(err, &p) {
		return err // already says what was and was not done
	}
	if err != nil && errors.Is(context.Cause(ctx), ErrLockExpiring) {
		return fmt.Errorf("mash: %s ran past the lock of %s; nothing after the point it stopped was changed; try again: %w", op, l.name, err)
	}
	return err
}

// guard bounds ctx by the lock and returns the bounded context and a func to defer that words *errp (the caller's
// named error result) with explain and then ends the bound.
func (l *streamLock) guard(ctx context.Context, op string, errp *error) (context.Context, func()) {
	bctx, cancel := l.bound(ctx)
	if afterLockAcquired != nil {
		afterLockAcquired()
	}
	return bctx, func() {
		*errp = l.explain(bctx, op, *errp)
		cancel()
	}
}
