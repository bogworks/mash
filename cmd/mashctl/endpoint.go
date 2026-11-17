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
	switch args[0] {
	case "create":
		return runEndpointCreate(rest, env, lookup, stdout, stderr)
	case "reissue":
		return runEndpointReissue(rest, env, lookup, stdout, stderr)
	case "delete":
		return runEndpointDelete(rest, env, lookup, stdout, stderr)
	case "list":
		return runEndpointList(rest, env, lookup, stdout, stderr)
	case "show":
		return runEndpointShow(rest, env, lookup, stdout, stderr)
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
