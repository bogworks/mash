package main

import (
	"fmt"
	"io"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
)

// accountGuard is the guard of every command that would change an adopted account's policy by a path other than the
// list-set path (REQ-016, ADR 0111). It takes the account lock, which `policy adopt` also takes, then reads the
// adoption marker; when the account is adopted, the marker cannot be read or another policy change or an adoption
// holds the lock, it prints the refusal to stderr and returns ok false: the caller returns 1.
//
// Call it first: before a write, a signing, a key or a file change. On ok the caller defers g.Release() and calls
// g.Held() before each write that cannot be undone (stopping when it fails): the lock stays held through all of the
// command's writes, so an adoption waits for it. what is the command as it reads in "mashctl: <what> is refused".
// With no server configured the guard holds nothing (a nil g, whose methods do nothing).
func accountGuard(cfg config.Config, what string, stderr io.Writer) (g *ctl.Guard, ok bool) {
	g, err := ctl.GuardAccountFor("mashctl", cfg, what)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, false
	}
	return g, true
}

// heldOrStop is Held for a guarded command: it prints what stopped the command to stderr and returns false when the
// lock was lost.
func heldOrStop(g *ctl.Guard, what string, stderr io.Writer) bool {
	if err := g.Held(); err != nil {
		fmt.Fprintf(stderr, "mashctl: %s is stopped: the account lock was lost, so an adoption or another policy change may have happened; %v\n", what, err)
		return false
	}
	return true
}
