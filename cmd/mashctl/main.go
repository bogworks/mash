// Command mashctl administers a mash deployment: it creates the directory and lease buckets, provisions and
// deprovisions endpoints, sets their metadata, and prints limits and the NATS permissions an endpoint user needs.
// It needs admin credentials (MASH_ADMIN_CREDS); the endpoint CLI is mash.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/endpoint"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run is main with its environment injected, for tests.
func run(args []string, env func(string) string, stdout, stderr io.Writer) int {
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
		return runPolicy(args[1:], env, stdout, stderr)
	case "adopt": // takes over a server you already run: the config and the key store, not a server
		return runAdopt(args[1:], env, stdout, stderr)
	case "add":
		return runAdd(args[1:], env, stdout, stderr)
	case "label", "unlabel": // the policy file; the server only with --sync
		return runLabel(args[0] == "unlabel", args[1:], env, stdout, stderr)
	case "retire":
		return runRetire(args[1:], env, stdout, stderr)
	case "reissue": // needs the key directory and creds files, no server
		return runReissue(args[1:], env, stdout, stderr)
	case "init", "limits", "permissions", "provision", "set-meta", "set-zone", "suspend", "resume", "suspended", "deprovision", "list":
	default:
		fmt.Fprintf(stderr, "mashctl: unknown command %q\n\n%s", args[0], ctl.Usage("mashctl"))
		return 2
	}
	cfg, err := config.Load(env, env("HOME"))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return ctl.Run("mashctl", cfg, args, stdout, stderr)
}
