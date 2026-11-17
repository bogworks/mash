//go:build unix

package endpoint

import (
	"errors"
	"os"
	"syscall"
)

// lockAskFile takes the exclusive flock on f, waiting for another holder.
func lockAskFile(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func unlockAskFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
