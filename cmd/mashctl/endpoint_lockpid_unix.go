//go:build unix

package main

import (
	"errors"
	"syscall"
)

// epPidState reports whether process pid runs on this host: 1 running, 0 not running, -1 unknown. A process this user
// cannot signal (EPERM) exists, so it counts as running.
func epPidState(pid int) int {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return 1
	case errors.Is(err, syscall.ESRCH):
		return 0
	}
	return -1
}
