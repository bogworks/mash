package main

import (
	"fmt"
	"io"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
)

// adoptedRefusal is the guard of every command that would change an adopted account's policy by a path other than the
// list-set path (REQ-016, ADR 0111). It prints the refusal to stderr and returns true when the command must stop; the
// caller returns 1. Call it first: before a write, a signing, a key or a file change. what is the command as it reads
// in "mashctl: <what> is refused". With no server configured it allows (see ctl.RefuseIfAdoptedFor).
func adoptedRefusal(cfg config.Config, what string, stderr io.Writer) bool {
	if err := ctl.RefuseIfAdoptedFor("mashctl", cfg, what); err != nil {
		fmt.Fprintln(stderr, err)
		return true
	}
	return false
}
