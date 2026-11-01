//go:build !linux && !darwin

package host

import (
	"os/exec"
	"strconv"
	"strings"
)

// parentPID asks ps for pid's parent, on an OS with no cheaper lookup.
func parentPID(pid int) (int, error) {
	b, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}
