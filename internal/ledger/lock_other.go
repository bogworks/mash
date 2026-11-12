//go:build !unix

package ledger

import "time"

// lockFile is best effort where flock does not exist: it takes nothing and succeeds.
func lockFile(string, time.Duration) (func(), error) { return func() {}, nil }
