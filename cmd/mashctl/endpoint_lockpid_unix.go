//go:build unix

package main

import (
	"errors"
	"os"
	"runtime"
	"strings"
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

// epHostID names the machine and PID namespace that pids seen by this process belong to, or "" when that cannot be
// established. On Linux it is "<boot_id>/<pid namespace link>". Elsewhere there is no namespace to compare and it is
// "host:" and the hostname.
func epHostID() string {
	if runtime.GOOS != "linux" {
		h, err := os.Hostname()
		if err != nil || h == "" {
			return ""
		}
		return "host:" + h
	}
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	boot := strings.TrimSpace(string(b))
	ns, nerr := os.Readlink("/proc/self/ns/pid")
	if err != nil || nerr != nil || boot == "" || ns == "" {
		return ""
	}
	return boot + "/" + ns
}
