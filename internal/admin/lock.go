package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
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
// generous for that. A critical section that outlasts lockTTL (a stream update that hangs) can overlap with another
// admin's, which is the old behaviour for that one change.

// Lock timing. Variables so tests can shorten them.
var (
	lockTTL  = 30 * time.Second       // how long an acquired lock is valid
	lockWait = 20 * time.Second       // how long acquire waits for a held lock before giving up
	lockPoll = 200 * time.Millisecond // base wait between attempts; up to the same again is added as jitter
)

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
type streamLock struct {
	kv  jetstream.KeyValue
	key string
	rev uint64
}

// leaseKV returns the lease bucket, creating it with the configured limits if a deployment from before the lock has
// not been re-initialised (`mash admin init` creates it).
func (a *Admin) leaseKV(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := a.js.KeyValue(ctx, store.LeaseBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		if kv, err = a.js.CreateKeyValue(ctx, store.LeaseConfigFor(a.limits)); err != nil {
			// Lost a race with another admin creating it, or no right to create: read it again.
			if kv2, err2 := a.js.KeyValue(ctx, store.LeaseBucket); err2 == nil {
				return kv2, nil
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("mash: lease bucket (run `mash admin init`): %w", err)
	}
	return kv, nil
}

func newLockValue(owner string) []byte {
	b, _ := json.Marshal(lockValue{Owner: owner, Until: time.Now().Add(lockTTL).UTC().Format(time.RFC3339)})
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
	giveUp := time.Now().Add(lockWait)
	for {
		rev, err := kv.Create(ctx, key, newLockValue(owner))
		if err == nil {
			return &streamLock{kv: kv, key: key, rev: rev}, nil
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
			rev, err := kv.Update(ctx, key, newLockValue(owner), e.Revision())
			if err == nil {
				return &streamLock{kv: kv, key: key, rev: rev}, nil
			}
			if !isRevisionConflict(err) {
				return nil, fmt.Errorf("mash: limits lock of %s: %w", name, err)
			}
			continue // someone else took it first; look again
		}
		if !time.Now().Before(giveUp) {
			return nil, fmt.Errorf("mash: another admin is changing the limits of %s (lock held until %s); try again",
				name, until.UTC().Format(time.RFC3339))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(min(lockPause(), time.Until(giveUp))):
		}
	}
}

// release removes the lock, but only the revision this admin wrote: if the lock expired and another admin took it
// over, that admin's lock stays. Errors are ignored: a lock that cannot be removed expires.
func (l *streamLock) release(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockReleaseTimeout)
	defer cancel()
	_ = l.kv.Purge(ctx, l.key, jetstream.LastRevision(l.rev))
}
