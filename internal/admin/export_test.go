package admin

import (
	"context"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ClearLease runs clearLease on lease for name.
func ClearLease(ctx context.Context, lease jetstream.KeyValue, name string) error {
	return clearLease(ctx, lease, name)
}

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

// SetAfterDeprovisionPurge installs a hook that runs between DeprovisionIfUser's record delete and its fence; it returns the previous one.
func SetAfterDeprovisionPurge(f func()) (prev func()) {
	prev, afterDeprovisionPurge = afterDeprovisionPurge, f
	return
}

// SetDirectoryPurgeFault installs a hook that runs after a directory-record purge the server accepted; a non-nil
// error it returns replaces the purge's result (a timeout after the server did the work). It returns the previous one.
func SetDirectoryPurgeFault(f func() error) (prev func() error) {
	prev, directoryPurgeFault = directoryPurgeFault, f
	return
}

// SetLockSafety sets how long before its deadline a held lock stops being used; it returns a func that restores it.
func SetLockSafety(dur time.Duration) (restore func()) {
	old := lockSafety
	lockSafety = dur
	return func() { lockSafety = old }
}

// SetAfterLockAcquired installs a hook that runs once an operation holds its name lock; it returns the previous one.
func SetAfterLockAcquired(f func()) (prev func()) {
	prev, afterLockAcquired = afterLockAcquired, f
	return
}

// LockHandle exposes a held lock's check and release to tests.
type LockHandle struct {
	l *streamLock
}

// AcquireLockHandle takes name's limits lock as a does.
func AcquireLockHandle(ctx context.Context, a *Admin, name string) (*LockHandle, error) {
	l, err := a.lockStream(ctx, name)
	if err != nil {
		return nil, err
	}
	return &LockHandle{l: l}, nil
}

// Check is the lock's check.
func (h *LockHandle) Check(ctx context.Context) error { return h.l.check(ctx) }

// Release releases the lock.
func (h *LockHandle) Release() { h.l.release(context.Background()) }

// AdmissionLockName is the pseudo-name of the directory-wide admission lock.
const AdmissionLockName = admissionLockName

// SetAfterAdmissionLock installs a hook that runs once Provision holds the admission lock; it returns the previous one.
func SetAfterAdmissionLock(f func()) (prev func()) {
	prev, afterAdmissionLock = afterAdmissionLock, f
	return
}

// SetAfterDirectoryGet installs a hook that runs before each per-record directory read of admission; it returns the
// previous one.
func SetAfterDirectoryGet(f func(key string)) (prev func(key string)) {
	prev, afterDirectoryGet = afterDirectoryGet, f
	return
}

// SetLockRefresh sets how often a refreshing lock extends its expiry; it returns a func that restores it.
func SetLockRefresh(d time.Duration) (restore func()) {
	old := lockRefresh
	lockRefresh = d
	return func() { lockRefresh = old }
}

// SetMarkerWriteFault installs a hook that wraps a rotation's marker write: it gets the write and returns what the
// caller sees, so a test can run the write and report a lost answer, or not run it. It returns a func that restores
// the previous one.
func SetMarkerWriteFault(f func(write func() (uint64, error)) (uint64, error)) (restore func()) {
	prev := markerWriteFault
	markerWriteFault = f
	return func() { markerWriteFault = prev }
}
