//go:build unix

package config

import (
	"fmt"
	"os"
)

// checkConfigMode refuses a config.json that anyone can write: it names the server, the credentials and the TLS
// files mash trusts. The owner is deliberately not checked, since a config map is often a symlink owned by another uid.
func checkConfigMode(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if mode := fi.Mode().Perm(); mode&0o002 != 0 {
		return fmt.Errorf("mash: %s has mode %04o: it is writable by every user. Run: chmod go-w %s", path, mode, path)
	}
	return nil
}
