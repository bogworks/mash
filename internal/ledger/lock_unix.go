//go:build unix

package ledger

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// lockFile takes an exclusive flock on the sidecar lock file at path, creating it 0600 and refusing a symlink as the
// final component (O_NOFOLLOW). It polls without blocking until wait has passed. The returned func releases the lock.
func lockFile(path string, wait time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EINTR) && !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errLockTimeout
		}
		time.Sleep(5 * time.Millisecond)
	}
}
