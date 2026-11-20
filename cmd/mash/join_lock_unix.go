//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

// lockDir takes an exclusive advisory lock on the configuration folder itself, so two joins into one folder run one
// after the other. The kernel drops it when the process ends, so there is no stale lock. unlock releases it.
func lockDir(dir string) (unlock func(), err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
