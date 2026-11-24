package endpoint

import (
	"fmt"
	"os"
	"strings"
)

// procStart is pid's start time as clock ticks since boot (field 22 of /proc/<pid>/stat).
func procStart(pid int) (string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	// "pid (comm) state ppid ...": comm may hold spaces and ')', so parse after the LAST ')'; state is field 3.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return "", fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	return f[19], nil
}

// procZombie reports whether pid is a zombie: it has exited and only waits for its parent to reap it. Where nothing
// reaps (a container whose first process is not an init), a killed serve stays one, and kill(pid, 0) still succeeds
// for it. An unreadable state is not a zombie.
func procZombie(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return false
	}
	f := strings.Fields(s[i+1:])
	return len(f) > 0 && f[0] == "Z"
}
