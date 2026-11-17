package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

// endpointVerbs are the verbs of `mashctl endpoint`.
const endpointVerbs = "create, reissue, delete, list, show"

// endpointVerbTable is every verb of `mashctl endpoint`, in the order endpointVerbs lists them. runEndpoint dispatches
// through it and the command-tree test walks it, so a verb added here is dispatched and tested from one entry.
// read is a command that only reads: it takes --json and no --dry-run. changes is a command that changes state: it
// takes --dry-run.
var endpointVerbTable = []struct {
	name    string
	run     func(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int
	read    bool
	changes bool
}{
	{name: "create", run: runEndpointCreate, changes: true},
	{name: "reissue", run: runEndpointReissue, changes: true},
	{name: "delete", run: runEndpointDelete, changes: true},
	{name: "list", run: runEndpointList, read: true},
	{name: "show", run: runEndpointShow, read: true},
}

// runEndpoint is `mashctl endpoint <verb>`. Each verb has the signature of the run functions of this package.
func runEndpoint(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(stdout, "usage: mashctl endpoint <verb> [args]\n  verbs: %s\n  try: mashctl endpoint create --help\n", endpointVerbs)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	rest := args[1:]
	for _, v := range endpointVerbTable {
		if v.name == args[0] {
			return v.run(rest, env, lookup, stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "mashctl: endpoint: unknown verb %q (the verbs are %s; try: mashctl endpoint --help)\n", args[0], endpointVerbs)
	return 2
}

// parseEndpointRef reads an endpoint reference: the shortcut owner/group/instance or the dotted owner.group.instance.
// The error never shows the value.
func parseEndpointRef(s string) (sap.Address, error) {
	if strings.Contains(s, "/") {
		s = strings.ReplaceAll(s, "/", ".")
	}
	a, err := sap.ParseAddress(s)
	if err != nil {
		return sap.Address{}, fmt.Errorf("not an endpoint address (the value is not shown): write owner/group/instance, for example alice/dev/design")
	}
	return a, nil
}
