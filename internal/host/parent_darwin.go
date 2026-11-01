package host

import "golang.org/x/sys/unix"

// parentPID asks the kernel for pid's parent (sysctl kern.proc.pid): no forked ps.
func parentPID(pid int) (int, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	return int(k.Eproc.Ppid), nil
}
