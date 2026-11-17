//go:build !unix

package endpoint

import "os"

// lockAskFile is best effort where flock does not exist: it always succeeds.
func lockAskFile(*os.File) error { return nil }

func unlockAskFile(*os.File) {}
