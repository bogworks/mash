package admin

import (
	"context"
	"time"
)

// SetAfterMetaGet installs a hook that runs between SetMeta's read and its write; it returns the previous one.
func SetAfterMetaGet(f func()) (prev func()) { prev, afterMetaGet = afterMetaGet, f; return }

// SetAfterStreamRead installs a hook between an inbox stream read and its write; it returns the previous one.
func SetAfterStreamRead(f func(inboxMaxBytes int64)) (prev func(int64)) {
	prev, afterStreamRead = afterStreamRead, f
	return
}

// SetLockTiming sets the lock TTL, wait bound and poll interval; it returns a func that restores them.
func SetLockTiming(ttl, wait, poll time.Duration) (restore func()) {
	t, w, p := lockTTL, lockWait, lockPoll
	lockTTL, lockWait, lockPoll = ttl, wait, poll
	return func() { lockTTL, lockWait, lockPoll = t, w, p }
}

// LockKey is the lease-bucket key of name's limits lock.
func LockKey(name string) string { return lockKey(name) }

// AcquireLock takes name's limits lock as a does and returns its release.
func (a *Admin) AcquireLock(ctx context.Context, name string) (release func(), err error) {
	l, err := a.lockStream(ctx, name)
	if err != nil {
		return nil, err
	}
	return func() { l.release(ctx) }, nil
}

// SetAfterDeprovisionGet installs a hook that runs between DeprovisionIfUser's read and its delete; it returns the previous one.
func SetAfterDeprovisionGet(f func()) (prev func()) {
	prev, afterDeprovisionGet = afterDeprovisionGet, f
	return
}
