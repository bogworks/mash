package endpoint

import (
	"errors"
	"strconv"
	"syscall"
)

// holderGone reports whether the process a lease names no longer exists (G270); a variable so tests can stub it.
var holderGone = holderGoneOS

// holderGoneOS is conservative: it says gone only when sure. A pid that does not parse, or is <= 1, is not gone.
// kill(pid, 0) ESRCH means gone; success or EPERM means a process exists. That process is a reused pid only when the
// lease recorded a start time and the pid's current start time is readable and differs. A lease without a start, or a
// start that cannot be read, is live: the process name is not consulted (codex H1 on #44).
func holderGoneOS(pid, start string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 1 {
		return false
	}
	if err := syscall.Kill(n, 0); err != nil {
		return errors.Is(err, syscall.ESRCH)
	}
	if start == "" {
		return false
	}
	cur, err := procStart(n)
	return err == nil && cur != start
}
