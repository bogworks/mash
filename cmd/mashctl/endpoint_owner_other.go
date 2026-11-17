//go:build !unix

package main

import "os"

// epOwnedByCaller has no owner to compare on this platform: the mode check stands alone.
func epOwnedByCaller(fi os.FileInfo) bool { return true }
