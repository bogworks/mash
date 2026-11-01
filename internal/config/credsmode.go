//go:build unix

package config

import (
	"fmt"
	"os"
)

// CheckCredsMode refuses a credentials file that other users can read. A missing file is not an error here: the
// later open reports it.
func CheckCredsMode(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("mash: credentials file %s has mode %04o: it is readable by other users. Run: chmod 600 %s", path, mode, path)
	}
	return nil
}
