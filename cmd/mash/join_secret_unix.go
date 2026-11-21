//go:build unix

package main

import (
	"os"
	"syscall"
)

// secretOpenFlags: a file that is not plain must not block the open (a fifo has no writer).
const secretOpenFlags = syscall.O_NONBLOCK

// ownedByMe is whether the file belongs to the user running mash.
func ownedByMe(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// ownerOnly is whether no group or other permission bit is set.
func ownerOnly(fi os.FileInfo) bool { return fi.Mode().Perm()&0o077 == 0 }
