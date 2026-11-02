//go:build unix

package bootstrap

import (
	"os/exec"
	"syscall"
)

// detach puts the child in its own session so it outlives mashctl and a terminal hangup.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
