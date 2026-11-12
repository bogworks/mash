package main

import (
	"bytes"
	"strings"
	"testing"
)

// The policy group has no subcommands yet: the policy file is gone. Every call prints the usage and exits 2, whatever
// the subcommand was (the verbs the file supported are removed, not renamed).
func TestPolicyGroupHasNoSubcommands(t *testing.T) {
	for _, args := range [][]string{
		{"policy"},
		{"policy", "check"}, {"policy", "test"}, {"policy", "apply"}, {"policy", "apply", "--dry-run"},
		{"policy", "drift"}, {"policy", "repair"}, {"policy", "explain", "a", "b"}, {"policy", "show"},
		{"policy", "check", "--policy", "p.yaml"}, {"policy", "list"}, {"policy", "--json"},
	} {
		var out, errb bytes.Buffer
		code := runEnv(args, func(string) string { return "" }, &out, &errb)
		if code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), "usage: mashctl policy") || !strings.Contains(errb.String(), "no subcommands yet") {
			t.Errorf("%v: code %d out %q err %q", args, code, out.String(), errb.String())
		}
	}
}

// The top-level drift verb is gone with the file it compared against.
func TestDriftVerbIsGone(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runEnv([]string{"drift"}, func(string) string { return "" }, &out, &errb); code != 2 || !strings.Contains(errb.String(), "unknown command") {
		t.Errorf("drift: %d %q", code, errb.String())
	}
}

func TestParsePolicyArgs(t *testing.T) {
	o, err := parsePolicyArgs([]string{"--json", "x"}, "--json")
	if err != nil || !o.json || len(o.args) != 1 || o.args[0] != "x" {
		t.Errorf("%+v %v", o, err)
	}
	// a flag that is not allowed is unknown, --policy included
	for _, args := range [][]string{{"--json"}, {"--policy", "f"}, {"--policy=f"}} {
		if _, err := parsePolicyArgs(args); err == nil || !strings.Contains(err.Error(), "unknown option") {
			t.Errorf("%v: %v", args, err)
		}
	}
}
