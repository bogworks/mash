package host

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// parentPID reads pid's parent from /proc.
func parentPID(pid int) (int, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	// "pid (comm) state ppid ...": comm may hold spaces and ')', so parse after the LAST ')'.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	return strconv.Atoi(f[1])
}
