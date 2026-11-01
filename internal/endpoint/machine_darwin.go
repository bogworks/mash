package endpoint

import (
	"errors"

	"golang.org/x/sys/unix"
)

// machineID is this boot's session uuid (unique per boot); macOS has no pid namespaces.
func machineID() (string, error) {
	s, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", errors.New("empty kern.bootsessionuuid")
	}
	return s, nil
}
