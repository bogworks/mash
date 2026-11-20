package main

import "strings"

// helpUsage reports the usage text for `mash VERB -h|--help`, and whether args asks for help at all. Only the first
// argument after the verb (after the sub-verb for plugin) asks; the caller prints the text and runs nothing, so
// help never reads config, dials, spools, writes or waits.
func helpUsage(args []string) (string, bool) {
	if len(args) < 2 {
		return "", false
	}
	u, known := verbUsage[args[0]]
	switch args[0] {
	case "serve", "hook":
		u, known = commandLines(usage, "  mash "+args[0]), true
		if args[0] == "serve" {
			u += serveOptionsHelp
		}
	case "plugin":
		u, known = pluginUsage, true
	}
	if !known {
		return "", false
	}
	// Help is only the first argument after the verb (after the sub-verb for plugin): text further along, such as the
	// words of a message, is never read for it.
	first := 1
	if args[0] == "plugin" && !strings.HasPrefix(args[1], "-") {
		first = 2
	}
	if first < len(args) && (args[first] == "-h" || args[first] == "--help") {
		return u + "\n", true
	}
	return "", false
}

// serveOptionsHelp is the options of mash serve, hand-written until serve is on the command table; the text is the
// specification's.
const serveOptionsHelp = `

Options:
  --accept-unverified    Deliver mail that is not verified, with its trust label.
                         Default: off; such mail is refused.
                         Also MASH_ACCEPT_UNVERIFIED, or "accept_unverified".`

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

// commandLines is linesOf, with the lines that continue the command's description (indented to helpCol) after it.
func commandLines(text, prefix string) string {
	var b strings.Builder
	on := false
	for _, l := range strings.SplitAfter(text, "\n") {
		if rest, ok := strings.CutPrefix(l, prefix); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\n') {
			on = true
		} else if !strings.HasPrefix(l, strings.Repeat(" ", helpCol)) {
			on = false
		}
		if on {
			b.WriteString(l)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
