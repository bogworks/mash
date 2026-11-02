//go:build unix

package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// checkCredsFile refuses, from the open descriptor, a creds file that is not a regular file or that other users can
// read. The owner is not checked and a symlink is followed (the descriptor is checked, not the link): creds on a
// Kubernetes secret mount are symlinks to files another uid owns, and an endpoint's creds are the endpoint's own.
// The key store and the key directory, which hold authority, are held to the stricter rules in internal/ctlkeys.
func checkCredsFile(f *os.File, path string) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("mash: credentials file %s is not a regular file", path)
	}
	if mode := fi.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("mash: credentials file %s has mode %04o: it is readable by other users. Run: chmod 600 %s", path, mode, path)
	}
	return nil
}

func openCreds(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := checkCredsFile(f, path); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// CheckCredsMode refuses a credentials file that is not a regular file or is readable by other users. The checks run
// on an open descriptor (fstat), so a file swapped after the open is not the one checked. A missing file is not an error
// here: the later open reports it. Prefer ReadCredsFile, which reads from the descriptor it checked.
func CheckCredsMode(path string) error {
	f, err := openCreds(path)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return nil // missing or unreadable: the later open reports it
		}
		return err
	}
	return f.Close()
}

// ReadCredsFile opens path, applies the CheckCredsMode checks to the descriptor and reads the file
// from that same descriptor, so nothing can swap the file between the check and the read. A missing file is an
// error wrapping fs.ErrNotExist.
func ReadCredsFile(path string) ([]byte, error) {
	f, err := openCreds(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
