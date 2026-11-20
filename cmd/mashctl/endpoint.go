package main

import (
	"fmt"
	"io"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
)

// endpointVerbs are the verbs of `mashctl endpoint`.
const endpointVerbs = "create, reissue, delete, list, show, set, unset, suspend, resume, inbox, permissions"

// endpointVerbTable is every verb of `mashctl endpoint`, in the order endpointVerbs lists them. runEndpoint dispatches
// through it and the command-tree test walks it, so a verb added here is dispatched and tested from one entry.
// read is a command that only reads: it takes --json and no --dry-run. changes is a command that changes state: it
// takes --dry-run.
var endpointVerbTable = []struct {
	name    string
	run     func(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int
	read    bool
	changes bool
}{
	{name: "create", run: runEndpointCreate, changes: true},
	{name: "reissue", run: runEndpointReissue, changes: true},
	{name: "delete", run: runEndpointDelete, changes: true},
	{name: "list", run: runEndpointList, read: true},
	{name: "show", run: runEndpointShow, read: true},
	{name: "set", run: runEndpointSet, changes: true},
	{name: "unset", run: runEndpointUnset, changes: true},
	{name: "suspend", run: runEndpointSuspend, changes: true},
	{name: "resume", run: runEndpointResume, changes: true},
	{name: "inbox", run: runEndpointInbox, read: true},
	{name: "permissions", run: runEndpointPermissions, read: true},
}

// endpointHelp is the text of `mashctl endpoint --help`.
const endpointHelp = `usage: mashctl endpoint <command> [args]
commands:
  list [prefix] [--wide]      list the endpoints
  show <address>              show one endpoint
  create <address> ...        make an endpoint and its credentials
  reissue <address> ...       give an endpoint new credentials
  delete <address> ...        remove an endpoint
  set <address> key=value...  set role, description, host, capability, labels
  unset <address> key...      remove those values
  suspend <address>           refuse new mail to an endpoint
  resume <address>            take new mail again
  inbox <address>             list what is in an inbox, without the text
  permissions <address>       print the NATS permissions an endpoint needs
Run mashctl endpoint <command> --help for a command's options.
`

// runEndpoint is `mashctl endpoint <verb>`. Each verb has the signature of the run functions of this package.
func runEndpoint(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return cliframe.Report(stdout, stderr, cliframe.Usagef("missing command").WithHint("mashctl endpoint --help"), false)
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, endpointHelp)
		return 0
	}
	rest := args[1:]
	for _, v := range endpointVerbTable {
		if v.name == args[0] {
			return v.run(rest, g, env, lookup, stdout, stderr)
		}
	}
	return cliframe.Report(stdout, stderr, cliframe.Usagef("unknown command %q", config.Clean(args[0])).WithHint("mashctl endpoint --help"), jsonRequested(rest))
}
