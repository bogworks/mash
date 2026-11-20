// Command mashctl administers a mash deployment: it creates the directory and lease buckets, sets endpoint metadata,
// and prints limits and the NATS permissions an endpoint user needs.
// It needs admin credentials (MASH_ADMIN_CREDS); the endpoint CLI is mash.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.LookupEnv, os.Stdout, os.Stderr))
}

// plainRefusalMarker is the text of natsauth's refusal of a plain URL, which every connecting verb prints verbatim.
const plainRefusalMarker = "every mash connection requires TLS, to any host"

// plainWatch passes writes through and records whether the plain-URL refusal went by.
type plainWatch struct {
	w    io.Writer
	seen bool
}

func (p *plainWatch) Write(b []byte) (int, error) {
	if strings.Contains(string(b), plainRefusalMarker) {
		p.seen = true
	}
	return p.w.Write(b)
}

// run is main with its environment injected, for tests. A plain URL refused for TLS is a usage error (exit 2) in every
// verb, whichever path reported it.
func run(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	pw := &plainWatch{w: stderr}
	pwOut := &plainWatch{w: stdout} // a --json run reports its error on stdout
	code := runVerbs(args, env, lookup, pwOut, pw)
	if code != 0 && code != 2 && (pw.seen || pwOut.seen) {
		return 2
	}
	return code
}

func runVerbs(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, ctl.Usage("mashctl"))
		return 2
	}
	if msg, ok := removedCommands[args[0]]; ok { // before any flag, config or dispatch: nothing runs
		fmt.Fprintln(stderr, msg)
		return 2
	}
	// The global options are taken out of the arguments wherever they stand; a flag shadows its environment variable.
	// mashctl's own --sys-creds and --servers stay as they are (extractServerFlags below).
	spec := cliframe.Spec{Tool: "mashctl", CredsEnv: "MASH_ADMIN_CREDS", CredsKey: "admin_creds"}
	global, rest0, gerr := cliframe.ExtractOwning(spec, args, map[string][]string{"endpoint": {"--json"}, "audit": {"--json"}, "issued": {"--json"}, "version": {"--json"}, "adopt": {"--server"}}) // these parse their own
	if gerr != nil {
		fmt.Fprintf(stderr, "mashctl: %s\n", gerr)
		return 2
	}
	if len(rest0) == 0 {
		fmt.Fprint(stderr, ctl.Usage("mashctl"))
		return 2
	}
	args = rest0
	env, lookup = global.Shadow(spec, env, lookup)
	if serverFlagVerbs[args[0]] {
		rest, env2, err := extractServerFlags(args[1:], env)
		if err != nil {
			fmt.Fprintf(stderr, "mashctl: %s: %v\n", args[0], err)
			return 2
		}
		args, env = append([]string{args[0]}, rest...), env2
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, ctl.Usage("mashctl"))
		return 0
	}
	if isVerb(args[0]) && args[0] != "endpoint" && helpRequested(args[0], args[1:]) { // before any dispatch or config load: help changes nothing
		fmt.Fprint(stdout, verbUsage(args))
		return 0
	}
	switch args[0] {
	case "adopt": // takes over a server you already run: the config and the key store, not a server
		return runAdopt(args[1:], env, lookup, stdout, stderr)
	case "audit": // read-only: lists what the server and the creds directories hold; writes nothing
		return runAudit(args[1:], env, lookup, stdout, stderr)
	case "issued": // read-only: the issue record and the check of it against the connected keys
		return runIssued(args[1:], env, lookup, stdout, stderr)
	case "endpoint":
		return runEndpoint(args[1:], env, lookup, stdout, stderr)
	case "init", "limits":
	default:
		fmt.Fprintf(stderr, "mashctl: unknown command %q\n\n%s", args[0], ctl.Usage("mashctl"))
		return 2
	}
	cfg, err := loadConfig(env, lookup)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if announcesVerb(args) {
		announceTarget(cfg, env, stderr, nil)
	}
	return ctl.Run("mashctl", cfg, args, stdout, stderr)
}

// helpRequested reports whether -h or --help is the first argument after the verb, or, for a verb with sub-verbs
// (`issued check`), the first argument after the sub-verb. Text further along is never read for help:
// a value, a note or a message may contain the words.
func helpRequested(verb string, args []string) bool {
	isHelp := func(a string) bool { return a == "-h" || a == "--help" }
	if len(args) > 0 && isHelp(args[0]) {
		return true
	}
	if verb == "issued" && len(args) > 1 && !strings.HasPrefix(args[0], "-") && isHelp(args[1]) {
		return true
	}
	return false
}

// verbUsage is the lines of the mashctl usage for the verb in args: those that begin with its first two words
// when some do (`issued check`), else with its first word; the whole usage when none does (a verb
// whose usage lives in its own file).
func verbUsage(args []string) string {
	words := []string{args[0]}
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		words = append(words, args[1])
	}
	for n := len(words); n > 0; n-- {
		prefix := "  mashctl " + strings.Join(words[:n], " ")
		var b strings.Builder
		for _, l := range strings.SplitAfter(ctl.Usage("mashctl"), "\n") {
			if rest, ok := strings.CutPrefix(l, prefix); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\n') {
				b.WriteString(l)
			}
		}
		if b.Len() > 0 {
			return "usage:\n" + b.String()
		}
	}
	return ctl.Usage("mashctl")
}

// isVerb reports whether name is a verb run dispatches.
func isVerb(name string) bool {
	switch name {
	case "adopt", "audit", "endpoint", "issued", "init", "limits":
		return true
	}
	return false
}

// removedCommands are the verbs `endpoint` replaced. They run nothing and name the replacement; they are not aliases.
var removedCommands = map[string]string{
	"add":     "mashctl: add was removed. Use: mashctl endpoint create alice/dev/design --public-key design.pub   (see: mashctl endpoint create --help)",
	"reissue": "mashctl: reissue was removed. Use: mashctl endpoint reissue alice/dev/design --public-key FILE   (see: mashctl endpoint reissue --help)",
	"retire":  "mashctl: retire was removed. Use: mashctl endpoint delete alice/dev/design   (see: mashctl endpoint delete --help)",
}
