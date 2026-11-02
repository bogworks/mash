//go:build !unix

package bootstrap

import "os/exec"

// detach does nothing where there are no process groups.
func detach(*exec.Cmd) {}
