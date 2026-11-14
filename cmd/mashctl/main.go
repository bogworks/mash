// Command mashctl administers a mash deployment: it creates the directory and lease buckets, provisions and
// deprovisions endpoints, sets their metadata, and prints limits and the NATS permissions an endpoint user needs.
// It needs admin credentials (MASH_ADMIN_CREDS); the endpoint CLI is mash.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
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
		fmt.Fprintf(stdout, "mashctl %s\n", endpoint.CurrentVersion())
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, ctl.Usage("mashctl"))
		return 0
	case "policy": // offline: no server, no admin credentials
		return runPolicy(args[1:], env, lookup, stdout, stderr)
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
	case "label", "unlabel": // the policy file; the server only with --sync
		return runLabel(args[0] == "unlabel", args[1:], env, lookup, stdout, stderr)
	case "retire":
		return runRetire(args[1:], env, lookup, stdout, stderr)
	case "reissue": // needs the key directory and creds files, no server
		return runReissue(args[1:], env, lookup, stdout, stderr)
	case "init", "limits", "permissions", "provision", "set-meta", "set-zone", "suspend", "resume", "suspended", "deprovision", "list":
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
