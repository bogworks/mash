package main

import (
	"strings"

	"github.com/bogworks/mash/internal/ctl"
)

// helpUsage reports the usage text for `mash VERB -h|--help`, and whether args asks for help at all. Only the first
// argument after the verb (after the sub-verb for admin and plugin) asks; the caller prints the text and runs nothing, so
// help never reads config, dials, spools, writes or waits.
func helpUsage(args []string) (string, bool) {
	if len(args) < 2 {
		return "", false
	}
	u, known := verbUsage[args[0]]
	switch args[0] {
	case "serve", "hook":
		u, known = linesOf(usage, "  mash "+args[0]), true
	case "plugin":
		u, known = pluginUsage, true
	case "key", "credential":
		first := keycredHelpFirst(args)
		if first < len(args) && (args[first] == "-h" || args[first] == "--help") {
			return keycredUsage(args), true
		}
		return "", false
	case "admin":
		u, known = adminUsage(args[1]), true
	}
	if !known {
		return "", false
	}
	// Help is only the first argument after the verb (after the sub-verb for admin and plugin): text further along, such as the
	// words of a message, is never read for it.
	first := 1
	if (args[0] == "admin" || args[0] == "plugin") && !strings.HasPrefix(args[1], "-") {
		first = 2
	}
	if first < len(args) && (args[first] == "-h" || args[first] == "--help") {
		return u + "\n", true
	}
	return "", false
}

// linesOf is the lines of text that begin with prefix followed by a space or the end of the line.
func linesOf(text, prefix string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(text, "\n") {
		if rest, ok := strings.CutPrefix(l, prefix); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\n') {
			b.WriteString(l)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// adminUsage is the `mash admin` usage lines of sub, or all of them when sub names none.
func adminUsage(sub string) string {
	full := ctl.Usage("mash admin")
	if l := linesOf(full, "  mash admin "+sub); l != "" {
		return "usage:\n" + l
	}
	return strings.TrimSuffix(full, "\n")
}
