package ctlkeys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bogworks/mash/internal/config"
)

// lockName is the advisory lock file in the key directory that serializes `mashctl policy apply`.
const lockName = "apply.lock"

// ErrLocked is the error of Lock when another holder keeps the lock past the wait.
var ErrLocked = errors.New("another mashctl policy apply holds the key directory lock")

// ErrLockBusy is the error of LockFile when another holder keeps the lock past the wait.
var ErrLockBusy = errors.New("another process holds the lock")

// Lock takes an exclusive advisory lock on <keydir>/apply.lock (0600), waiting up to wait for another holder, and
// returns the function that releases it. The lock covers appliers that share this key directory; it does nothing
// for copies of the keys on other hosts, and on platforms without flock it is a no-op.
func (a *Authority) Lock(wait time.Duration) (unlock func(), err error) {
	if _, err := checkDir(a.dir); err != nil {
		return nil, err
	}
	path := filepath.Join(a.dir, lockName)
	unlock, err = LockFile(path, wait)
	if errors.Is(err, ErrLockBusy) {
		return nil, fmt.Errorf("%w (%s): wait for it to finish and run the command again", ErrLocked, path)
	}
	return unlock, err
}

// LockFile takes an exclusive advisory lock on the file at path, creating it 0600 if it is missing, waiting up to wait
// for another holder, and returns the function that releases it (safe to call once, on every exit path). The file is
// opened without following a symlink and must be a regular file owned by the effective uid. The caller checks the
// directory. A holder that outlasts wait yields an error wrapping ErrLockBusy. On platforms without flock it is a
// no-op. It is the one locking scheme of the key store and of the files that share its custody.
func LockFile(path string, wait time.Duration) (unlock func(), err error) {
	f, err := config.OpenNoFollow(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		if config.IsSymlinkRefusal(err) {
			return nil, fmt.Errorf("mashctl: the lock file %s is a symlink: refusing it", path)
		}
		return nil, fmt.Errorf("mashctl: open the lock file %s: %w", path, err)
	}
	fi, err := f.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = fmt.Errorf("mashctl: the lock file %s is not a regular file", path)
	} else if err == nil {
		err = ownerErr(fi, path, "lock file")
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("mashctl: lock %s: %w", path, err)
		}
		if ok {
			return func() { unlockFile(f); f.Close() }, nil
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, fmt.Errorf("%w (%s)", ErrLockBusy, path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// Dir is the key directory.
func (a *Authority) Dir() string { return a.dir }
