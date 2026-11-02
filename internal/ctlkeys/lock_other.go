//go:build !unix

package ctlkeys

import "os"

// tryLock is best effort where flock does not exist: it always succeeds.
func tryLock(*os.File) (bool, error) { return true, nil }

func unlockFile(*os.File) {}
