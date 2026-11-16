// Command mashctl administers a mash deployment: it creates the directory and lease buckets, provisions and
// deprovisions endpoints, sets their metadata, and prints limits and the NATS permissions an endpoint user needs.
// It needs admin credentials (MASH_ADMIN_CREDS); the endpoint CLI is mash.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/endpoint"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.LookupEnv, os.Stdout, os.Stderr))
}

// run is main with its environment injected, for tests.
func run(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, ctl.Usage("mashctl"))
		return 2
	}
	switch args[0] {
	case "version":
		if helpRequested("version", args[1:]) {
			fmt.Fprintln(stdout, "usage: mashctl version")
			return 0
		}
		fmt.Fprintf(stdout, "mashctl %s\n", endpoint.CurrentVersion())
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, ctl.Usage("mashctl"))
		return 0
	}
	if isVerb(args[0]) && helpRequested(args[0], args[1:]) { // before any dispatch or config load: help changes nothing
		fmt.Fprint(stdout, verbUsage(args))
		return 0
	}
	switch args[0] {
	case "adopt": // takes over a server you already run: the config and the key store, not a server
		return runAdopt(args[1:], env, lookup, stdout, stderr)
	case "audit": // read-only: lists what the server and the creds directories hold; writes nothing
		return runAudit(args[1:], env, lookup, stdout, stderr)
	case "inbox": // read-only: `inbox inspect` shows one inbox's stored messages and their delivery state; writes nothing
		return runInbox(args[1:], env, lookup, stdout, stderr)
	case "issued": // read-only: the issue record and the check of it against the connected keys
		return runIssued(args[1:], env, lookup, stdout, stderr)
	case "add":
		return runAdd(args[1:], env, lookup, stdout, stderr)
	case "label", "unlabel": // the labels on the directory record
		return runLabel(args[0] == "unlabel", args[1:], env, lookup, stdout, stderr)
	case "retire":
		return runRetire(args[1:], env, lookup, stdout, stderr)
	case "unzone": // zones are removed (ADR 0091): makes a zoned record five-token, keeping its mail
		return runUnzone(args[1:], env, lookup, stdout, stderr)
	case "reissue": // needs the key directory and creds files, no server
		return runReissue(args[1:], env, lookup, stdout, stderr)
	case "init", "limits", "permissions", "provision", "set-meta", "suspend", "resume", "suspended", "deprovision", "list":
	default:
		fmt.Fprintf(stderr, "mashctl: unknown command %q\n\n%s", args[0], ctl.Usage("mashctl"))
		return 2
	}
	cfg, err := loadConfigFor(ctl.NewFormRequested(args), env, lookup)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctl.RevokeUsers = func(ctx context.Context, cfg config.Config, users []string, stdout io.Writer, now func() time.Time) error {
		n, err := parseServers(env)
		if err != nil {
			return err
		}
		return revokeUsers(ctx, cfg, env, n, users, stdout, now)
	}
	if announcesVerb(args) {
		announceTarget(cfg, env, stderr, nil)
	}
	return ctl.Run("mashctl", cfg, args, stdout, stderr)
}

// helpRequested reports whether -h or --help is the first argument after the verb, or, for a verb with sub-verbs
// (`issued check`, `inbox inspect`), the first argument after the sub-verb. Text further along is never read for help:
// a value, a note or a message may contain the words.
func helpRequested(verb string, args []string) bool {
	isHelp := func(a string) bool { return a == "-h" || a == "--help" }
	if len(args) > 0 && isHelp(args[0]) {
		return true
	}
	if (verb == "issued" || verb == "inbox") && len(args) > 1 && !strings.HasPrefix(args[0], "-") && isHelp(args[1]) {
		return true
	}
	return false
}

// verbUsage is the lines of the mashctl usage for the verb in args: those that begin with its first two words
// when some do (`issued check`, `inbox inspect`), else with its first word; the whole usage when none does (a verb
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
	case "adopt", "audit", "inbox", "issued", "add", "label", "unlabel", "retire", "unzone", "reissue",
		"init", "limits", "permissions", "provision", "set-meta", "suspend", "resume", "suspended", "deprovision", "list":
		return true
	}
	return false
}
