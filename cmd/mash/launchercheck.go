package main

import (
	"os"

	"github.com/bogworks/mash/internal/cmdcore/cmds"
)

// launchedFromEnv is set by the plugin's launcher (plugin/scripts/mash) just before it execs mash.
const launchedFromEnv = "MASH_LAUNCHED_FROM"

// launcherGetenv reads the environment; tests replace it.
var launcherGetenv = os.Getenv

// launcherCheck is the launcher row: which mash the plugin's launcher ran. It is shown only when the launcher set
// MASH_LAUNCHED_FROM. A mash from PATH is a warning: the pin in mash.pins did not decide which mash runs. No fix is
// given, as none is a command of ours (see docs/troubleshooting.md).
func launcherCheck() (cmds.HealthCheck, bool) {
	c := cmds.HealthCheck{Name: cmds.CheckLauncher}
	switch v := launcherGetenv(launchedFromEnv); v {
	case "":
		return c, false
	case "managed":
		c.State, c.Detail = cmds.CheckOK, "pinned mash"
	case "override":
		c.State, c.Detail = cmds.CheckOK, "MASH_BIN"
	case "path":
		c.State, c.Detail = cmds.CheckWarn, "mash from PATH, not the pinned one"
	default:
		c.State, c.Detail = cmds.CheckWarn, "the launcher said an unknown value: "+v
	}
	return c, true
}
