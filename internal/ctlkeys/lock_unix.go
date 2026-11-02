//go:build unix

package ctlkeys

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes the exclusive flock without blocking; false means another holder has it.
func tryLock(f *os.File) (bool, error) {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return false, nil
		}
		return false, err
	}
}

func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
