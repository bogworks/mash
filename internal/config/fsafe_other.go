//go:build !unix

package config

import "os"

// OpenNoFollow is os.OpenFile where O_NOFOLLOW does not exist.
func OpenNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm)
}

// IsSymlinkRefusal is always false where OpenNoFollow cannot refuse a symlink.
func IsSymlinkRefusal(error) bool { return false }

// FileUID reports no owner where file ownership is not modelled.
func FileUID(os.FileInfo) (uint32, bool) { return 0, false }
