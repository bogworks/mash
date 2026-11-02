//go:build unix

package config

import (
	"errors"
	"os"
	"syscall"
)

// OpenNoFollow opens path like os.OpenFile but refuses a symlink as the final component (O_NOFOLLOW), so the caller
// can fstat the descriptor it got instead of checking the path and reopening it.
func OpenNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}

// IsSymlinkRefusal reports whether err is OpenNoFollow refusing a symlink.
func IsSymlinkRefusal(err error) bool { return errors.Is(err, syscall.ELOOP) }

// FileUID is the owner uid of the file fi describes; ok is false where the platform does not say.
func FileUID(fi os.FileInfo) (uid uint32, ok bool) {
	st, isStat := fi.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, false
	}
	return st.Uid, true
}
