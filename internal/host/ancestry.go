package host

import (
	"iter"
	"os"
)

// maxAncestors is how many levels above the hook the walk climbs.
const maxAncestors = 10

// Ancestors yields the process ids above this one, nearest first, lazily (at most 10 levels, stopping at pid 1 or on
// an error): each level is a lookup (a /proc read, a sysctl, or a forked ps), so a caller that finds what it wants
// stops early. A var so tests can stub it. The hook is a descendant of the Claude process whose serve child owns the
// spool, normally its parent or grandparent.
var Ancestors iter.Seq[int] = func(yield func(int) bool) {
	pid := os.Getppid()
	for i := 0; i < maxAncestors && pid > 1; i++ {
		if !yield(pid) {
			return
		}
		next, err := parentOf(pid)
		if err != nil {
			return
		}
		pid = next
	}
}

// parentOf returns pid's parent process id (the OS's parentPID). A var so tests can count and stub the lookups.
var parentOf = parentPID
