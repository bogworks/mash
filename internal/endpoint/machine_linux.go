package endpoint

import (
	"errors"
	"os"
	"strings"
)

// machineID names this boot of this machine and this process's pid namespace: "<boot_id>/<pidns>". Two hosts, or two
// containers, can share a hostname; they do not share both. A reboot changes the boot id.
func machineID() (string, error) {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	boot := strings.TrimSpace(string(b))
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return "", err
	}
	if boot == "" || ns == "" {
		return "", errors.New("machine identity unreadable")
	}
	return boot + "/" + ns, nil
}
