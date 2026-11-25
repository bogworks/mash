// Command mashctl administers a mash deployment: it creates the directory and lease buckets, sets endpoint metadata,
// and prints limits and the NATS permissions an endpoint user needs.
// It needs admin credentials (MASH_ADMIN_CREDS); the endpoint CLI is mash.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/natsauth"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.LookupEnv, os.Stdout, os.Stderr))
}

// codedError is an error that says its message and carries exit code 2.
type codedError struct{ error }

func (codedError) ExitCode() int { return 2 }

func (e codedError) Unwrap() error { return e.error }

// withPlainCode is out, made to exit 2 when its cause is natsauth's refusal of a plain URL. The code comes from the
// error itself, never from the text a command printed.
func withPlainCode(out, cause error) error {
	var p *natsauth.PlainRefusalError
	if !errors.As(cause, &p) {
		return out
	}
	var e *cliframe.Error
	if errors.As(out, &e) && e != nil {
		c := *e
		c.Code = 2
		return &c
	}
	return codedError{out}
}

// run is main with its environment injected, for tests. The encryption override is checked once, here, before any
// command dispatches: a removed name or a bad value is refused for every command (exit 2, help and version included),
// and while the override is on every command prints the warning on stderr, once.
func run(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	return runVerbs(args, env, lookup, stdout, stderr)
}

func runVerbs(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	// The encryption override is the very first thing checked, before any argument is read (bare `mashctl`, a removed
	// command, a bad flag): a removed name or a bad value is refused (exit 2), and while the override is on every
	// invocation prints the warning on stderr, once.
	config.WarnSkipEncryption(lookup, stderr)
	if skipErr := config.CheckSkipEncryption(lookup, config.HomeDir(env)); skipErr != nil {
		return cliframe.Report(stdout, stderr, cliframe.Usagef("%s", skipErr.Msg()).WithHint(skipErr.Hint()), slices.Contains(args, "--json"))
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, ctl.Usage("mashctl"))
		return 2
	}
	if msg, ok := removedCommands[args[0]]; ok { // before any flag, config or dispatch: nothing runs
		fmt.Fprintln(stderr, msg)
		return 2
	}
	if args[0] == "--version" { // `mashctl --version` is `mashctl version`
		args = append([]string{"version"}, args[1:]...)
	}
	// The global options are taken out of the arguments wherever they stand; a flag shadows its environment variable.
	spec := cliframe.Spec{Tool: "mashctl", CredsEnv: "MASH_ADMIN_CREDS", CredsKey: "admin_creds"}
	global, rest0, gerr := cliframe.ExtractOwning(spec, args, map[string][]string{"endpoint": {"--json"}, "doctor": {"--json"}, "version": {"--json"}, "init": {"--json"}, "status": {"--json"}, "limits": {"--json"}}) // these parse their own
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
	// A folder mash 0.5.5 left is named for what it is, before any command runs (version and init included).
	home := config.HomeDir(env) // as cmd/mash: the folder is found the same way in both tools
	if code, refused := cliframe.RefuseLegacy055(stdout, stderr, env, home, global.JSON || jsonRequested(args[1:])); refused {
		return code
	}
	if skipErr := config.CheckSkipEncryption(lookup, config.HomeDir(env)); skipErr != nil { // a --config-dir flag may point at a file with the removed key: refusal only, the warning is out
		return cliframe.Report(stdout, stderr, cliframe.Usagef("%s", skipErr.Msg()).WithHint(skipErr.Hint()), jsonRequested(args[1:]) || global.JSON)
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, ctl.Usage("mashctl"))
		return 0
	}
	if args[0] == "doctor" && helpRequested(args[0], args[1:]) { // its usage is its own text
		fmt.Fprint(stdout, doctorUsage)
		return 0
	}
	if isVerb(args[0]) && args[0] != "endpoint" && helpRequested(args[0], args[1:]) { // before any dispatch or config load: help changes nothing
		fmt.Fprint(stdout, verbUsage(args))
		return 0
	}
	switch args[0] {
	case "init": // takes over a server you already run, from a setup file
		return runInit(args[1:], global, env, lookup, stdout, stderr)
	case "status":
		return runStatus(args[1:], global, env, lookup, stdout, stderr)
	case "limits":
		return runLimits(args[1:], global, env, lookup, stdout, stderr)
	case "doctor": // read-only: checks the server, the storage, the records and the connected keys; changes nothing
		return runDoctor(args[1:], global, env, lookup, stdout, stderr)
	case "endpoint":
		return runEndpoint(args[1:], global, env, lookup, stdout, stderr)
	default:
		return cliframe.Report(stdout, stderr, cliframe.Usagef("unknown command %q", config.Clean(args[0])).WithHint("mashctl --help"), jsonRequested(args[1:]))
	}
}

// helpRequested reports whether -h or --help is the first argument after the verb, or, for a verb with sub-verbs
// (`limits show`), the first argument after the sub-verb. Text further along is never read for help:
// a value, a note or a message may contain the words.
func helpRequested(verb string, args []string) bool {
	isHelp := func(a string) bool { return a == "-h" || a == "--help" }
	if len(args) > 0 && isHelp(args[0]) {
		return true
	}
	if verb == "limits" && len(args) > 1 && !strings.HasPrefix(args[0], "-") && isHelp(args[1]) {
		return true
	}
	return false
}

// verbUsage is the lines of the mashctl usage for the verb in args (with the wrapped lines under them): those that begin with its first two words
// when some do (`limits show`), else with its first word; the whole usage when none does (a verb
// whose usage lives in its own file).
func verbUsage(args []string) string {
	words := []string{args[0]}
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		words = append(words, args[1])
	}
	for n := len(words); n > 0; n-- {
		prefix := "  mashctl " + strings.Join(words[:n], " ")
		var b strings.Builder
		own := false // the lines under a command line (a wrapped option list, the description) belong to it
		for _, l := range strings.SplitAfter(ctl.Usage("mashctl"), "\n") {
			if strings.HasPrefix(l, "  mashctl ") {
				rest, ok := strings.CutPrefix(l, prefix)
				own = ok && (rest == "" || rest[0] == ' ' || rest[0] == '\n')
			} else if !strings.HasPrefix(l, "    ") {
				own = false
			}
			if own {
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
	case "doctor", "endpoint", "init", "limits", "status":
		return true
	}
	return false
}

// removedCommands are the verbs `endpoint` replaced. They run nothing and name the replacement; they are not aliases.
var removedCommands = map[string]string{
	"add":     "mashctl: add was removed. Use: mashctl endpoint create acme.demo.bob   (see: mashctl endpoint create --help)",
	"reissue": "mashctl: reissue was removed. Use: mashctl endpoint reissue acme.demo.bob   (see: mashctl endpoint reissue --help)",
	"retire":  "mashctl: retire was removed. Use: mashctl endpoint delete acme.demo.bob   (see: mashctl endpoint delete --help)",
}
