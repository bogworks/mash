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

// pidGone reports whether no process pid exists on this host (signal 0 finds ESRCH). A process that exists but is not
// ours (EPERM) is there.
func pidGone(pid int) bool {
	if pid <= 0 {
		return false
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
