package main

import (
	"github.com/bogworks/mash/internal/cmdcore"
)

// helpUsage reports the usage text for `mash VERB -h|--help` of the commands that are not on the table (serve, hook,
// prompt), and whether args asks for help at all. Only the first argument after the verb asks; the caller prints the
// text and runs nothing, so help never reads config, dials, spools, writes or waits.
func helpUsage(args []string) (string, bool) {
	if len(args) < 2 {
		return "", false
	}
	c, known := handHelp[args[0]]
	if !known {
		return "", false
	}
	// Help is only the first argument after the verb: text further along, such as the
	// words of a message, is never read for it.
	if args[1] == "-h" || args[1] == "--help" {
		return cmdcore.StaticHelp(helpFrame, c), true
	}
	return "", false
}
