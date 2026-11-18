//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// epLock takes an advisory lock (flock) on the file at path, creating it when needed. The kernel releases the lock when
// the process ends, so a killed run leaves nothing to remove. The file is never deleted: removing a flock file races.
// A lock that is held is retried every 50 ms for epLockWait, then errEpBusy. A directory at path is a lock of an older
// mash: errEpOldLock. After taking the lock one line, "pid=N host=H start=S", is written for the refusal message of a
// waiting run; nothing is decided from it.
func epLock(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		if errors.Is(err, syscall.EISDIR) {
			return nil, errEpOldLock
		}
		return nil, err
	}
	deadline := time.Now().Add(epLockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EINTR {
			f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errEpBusy
		}
		time.Sleep(50 * time.Millisecond)
	}
	host, _ := os.Hostname()
	f.Truncate(0)
	f.WriteAt([]byte(fmt.Sprintf("pid=%d host=%s start=%d\n", os.Getpid(), host, time.Now().Unix())), 0)
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
