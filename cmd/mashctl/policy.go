package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/bogworks/mash/internal/config"
)

// policyUsage is the usage of the policy group. The policy file is gone (ADR 0091). adopt marks the account as owned by
// an outside system; serve applies its list sets; reconcile plans the adoption reconcile; show, explain and diff read the
// list set in force and change nothing.
const policyUsage = `usage: mashctl policy adopt --submitter NKEY_PUBLIC_KEY [--sys-creds FILE] [--exempt USER_KEY]... | --show [--json]
       mashctl policy serve --range RANGE --servers N [--sys-creds FILE]
       mashctl policy reconcile [--json] [--listset FILE] [--range R] [--bucket NAME] [--account KEY] [--sys-creds FILE] [--servers N]
       mashctl policy reconcile --yes --plan DIGEST [--range R] [--bucket NAME] [--account KEY] [--sys-creds FILE] [--servers N]
       mashctl policy show [--json] [--range RANGE] [--bucket NAME] [--account KEY]
       mashctl policy explain SENDER RECEIVER [--json] [--range RANGE] [--bucket NAME] [--account KEY]
       mashctl policy diff FILE [--json] [--submitter PUBKEY] [--range RANGE] [--bucket NAME] [--account KEY]
       (show, explain and diff read the list set in force from the policy bucket and change nothing;
        SENDER and RECEIVER are three-part addresses; FILE is a list-set document, canonical JSON or a compact JWS;
        the policy bucket is named by the MASH_POLICY_BUCKET setting, a bucket name that may contain {range};
        --range fills {range} and is required only when the setting has it; with a literal bucket name, diff uses --range
        as the range to validate the document against when nothing is in force; --bucket NAME overrides the setting;
        --account defaults to the key store's account key;
        diff checks the signature only when --submitter is given, and exits 1 when the plan is refused)
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

// policyHelpUsage is the usage a help request prints for a verb: the verb's own constant where it has one, else the group's.
func policyHelpUsage(verb string) string {
	switch verb {
	case "adopt":
		return policyAdoptUsage
	case "serve":
		return policyServeUsage
	case "reconcile":
		return policyReconcileUsage
	}
	return policyUsage
}

// runPolicy handles `mashctl policy ...`: adopt, serve, reconcile, show, explain and diff. `--help` or `-h` (first, or
// anywhere after a known verb) prints the usage to stdout and exits 0. Any other subcommand prints the usage to stderr
// and exits 2.
func runPolicy(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, policyUsage)
		return 0
	}
	if len(args) > 0 {
		switch args[0] {
		case "adopt", "serve", "reconcile", "show", "explain", "diff":
			for _, a := range args[1:] {
				if a == "--help" || a == "-h" {
					fmt.Fprint(stdout, policyHelpUsage(args[0]))
					return 0
				}
			}
		}
	}
	if len(args) > 0 && args[0] == "adopt" { // needs the admin credentials: the marker lives on the server
		return runPolicyAdopt(args[1:], env, lookup, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "serve" { // a long-running service for one adopted account
		return runPolicyServe(args[1:], env, lookup, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "reconcile" { // the first-list-set reconcile: a dry run (AT-015)
		return runPolicyReconcile(args[1:], env, lookup, stdout, stderr)
	}
	if len(args) > 0 {
		switch args[0] {
		case "show":
			return policyListSetShow(args[1:], env, lookup, stdout, stderr)
		case "explain":
			return policyListSetExplain(args[1:], env, lookup, stdout, stderr)
		case "diff":
			return policyListSetDiff(args[1:], env, lookup, stdout, stderr)
		}
		fmt.Fprintf(stderr, "mashctl: policy: unknown subcommand %s\n", config.RejectedArg(args[0]))
	}
	fmt.Fprint(stderr, policyUsage)
	return 2
}
