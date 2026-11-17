//go:build unix

package main

import (
	"os"
	"syscall"
)

// epOwnedByCaller reports whether the file belongs to the user running the command. A staged seed that another user
// owns is not a secret this run made, so it is never used.
func epOwnedByCaller(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Getuid()
}
