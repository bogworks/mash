package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/bogworks/mash/internal/config"
)

// policyUsage is the usage of the policy group. The policy file is gone (ADR 0091); the adoption marker is the one
// subcommand there is, and the list-set verbs that replace the file are added to the group when they land.
const policyUsage = `usage: mashctl policy adopt --submitter NKEY_PUBLIC_KEY | --show [--json]
`

// policyOpts are the flags and positional arguments of a policy verb.
type policyOpts struct {
	json bool
	args []string
}

// parsePolicyArgs splits a policy verb's arguments into flags and positional arguments. Only the flags named in
// allowed (for example "--json") are accepted; any other flag is an unknown-argument error. There is no --policy flag.
func parsePolicyArgs(args []string, allowed ...string) (policyOpts, error) {
	var o policyOpts
	ok := func(a string) bool {
		for _, w := range allowed {
			if a == w {
				return true
			}
		}
		return false
	}
	for _, a := range args {
		switch {
		case ok(a):
			if a == "--json" {
				o.json = true
			}
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown %s", config.RejectedArg(a))
		default:
			o.args = append(o.args, a)
		}
	}
	return o, nil
}

// runPolicy handles `mashctl policy ...`. The group has no subcommands yet: every call prints the usage and exits 2.
func runPolicy(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "adopt" { // needs the admin credentials: the marker lives on the server
		return runPolicyAdopt(args[1:], env, lookup, stdout, stderr)
	}
	if len(args) > 0 {
		fmt.Fprintf(stderr, "mashctl: policy: unknown subcommand %s\n", config.RejectedArg(args[0]))
	}
	fmt.Fprint(stderr, policyUsage)
	return 2
}
