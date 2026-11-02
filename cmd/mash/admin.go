package main

import (
	"fmt"
	"io"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
)

const adminDeprecation = "mash admin is deprecated and will be removed; use mashctl (same verbs, e.g. mashctl list)"

// adminCmd is the deprecated `mash admin`: the mashctl verbs under the old name. The notice goes to stderr, once per
// invocation that names a verb.
func adminCmd(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	fmt.Fprintln(stderr, adminDeprecation)
	return ctl.Run("mash admin", cfg, args, stdout, stderr)
}
