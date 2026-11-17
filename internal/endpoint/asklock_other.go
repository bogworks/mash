//go:build !unix

package endpoint

import "os"

// lockAskFile is best effort where flock does not exist: it always succeeds.
func lockAskFile(*os.File) error { return nil }

func unlockAskFile(*os.File) {}

// pidGone cannot tell where there is no signal 0: every process counts as there, and the lease alone lapses a slot.
func pidGone(int) bool { return false }
