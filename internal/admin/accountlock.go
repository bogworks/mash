package admin

import (
	"context"
	"errors"
	"sync"
	"time"
)

// The account lock serialises every change to the account's policy against the adoption marker (REQ-016, ADR 0111).
// A guarded command (policy apply and repair, add, reissue, label, unlabel, retire, set-meta with labels, set-zone,
// provision with labels) takes it first, reads the marker, and holds the lock through all of its writes. `policy
// adopt` takes the same lock for its read-modify-write of the marker. So an adoption waits for a command already
// running and a command that starts after the adoption reads the marker and is refused; no command can change policy
// on an account after the marker is written.
//
// It is a name lock like the limits locks (ADR-0048), under the pseudo-name _policy-account, which is no endpoint's
// name. Unlike them it refreshes itself in the background every lockRefresh while held, because a policy apply with
// its gate or a reissue of many names may run for minutes. A lock that is lost anyway (the holder was stalled past
// its expiry and another took the name) is reported by Held, and a guarded command calls Held before each write
// that cannot be undone and stops when it fails.

// AccountLockName is the pseudo-name of the account-wide policy lock. It is not a valid endpoint name (the
// underscore), so its lock key limits-lock._policy-account is no name's lock.
const AccountLockName = "_policy-account"

// AccountLockWait is how long a command waits for the account lock before it refuses (see WithAccountLockWait).
const AccountLockWait = 30 * time.Second

// ErrAccountBusy is returned when the account lock stayed held by another policy change or an adoption for the
// whole wait. Nothing was changed.
var ErrAccountBusy = errors.New("mash: another policy change or an adoption is in progress; nothing was changed")

// AccountLock is a held account lock. Release it (defer it; a second Release does nothing); a nil *AccountLock is a lock that is not
// needed (a command with no server), and all its methods are no-ops.
type AccountLock struct {
	l    *streamLock
	once sync.Once
}

// LockAccount takes the account lock, waiting up to AccountLockWait for its holder. It returns ErrAccountBusy when
// the wait ends with the lock still held.
func (a *Admin) LockAccount(ctx context.Context) (*AccountLock, error) {
	wait := a.accountWait
	if wait == 0 {
		wait = AccountLockWait
	}
	l, err := a.lockNamed(ctx, AccountLockName, wait, true)
	if err != nil {
		var busy *lockBusyError
		if errors.As(err, &busy) {
			return nil, ErrAccountBusy
		}
		return nil, err
	}
	return &AccountLock{l: l}, nil
}

// Held returns nil only while the lock is still this holder's: its key still has the revision this holder wrote and
// its expiry is not close. Call it immediately before each write the lock protects; on an error stop, for the lock
// may have been taken by an adoption.
func (k *AccountLock) Held(ctx context.Context) error {
	if k == nil {
		return nil
	}
	return k.l.check(ctx)
}

// Release stops the refresh and removes the lock, if it is still this holder's. It is safe to call on a nil lock.
func (k *AccountLock) Release() {
	if k == nil {
		return
	}
	k.once.Do(func() { k.l.release(context.Background()) })
}
