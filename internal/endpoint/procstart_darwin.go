package endpoint

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// procStart is pid's start time as sec.usec (sysctl kern.proc.pid).
func procStart(pid int) (string, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	t := k.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", t.Sec, t.Usec), nil
}
