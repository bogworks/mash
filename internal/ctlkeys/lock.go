package ctlkeys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockName is the advisory lock file in the key directory that serializes `mashctl policy apply`.
const lockName = "apply.lock"

// ErrLocked is the error of Lock when another holder keeps the lock past the wait.
var ErrLocked = errors.New("another mashctl policy apply holds the key directory lock")

// Lock takes an exclusive advisory lock on <keydir>/apply.lock (0600), waiting up to wait for another holder, and
// returns the function that releases it. The lock covers appliers that share this key directory; it does nothing
// for copies of the keys on other hosts, and on platforms without flock it is a no-op.
func (a *Authority) Lock(wait time.Duration) (unlock func(), err error) {
	if _, err := checkDir(a.dir); err != nil {
		return nil, err
	}
	path := filepath.Join(a.dir, lockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mashctl: open the lock file %s: %w", path, err)
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
			return nil, fmt.Errorf("%w (%s): wait for it to finish and run the command again", ErrLocked, path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// Dir is the key directory.
func (a *Authority) Dir() string { return a.dir }
