package main

import (
	"bytes"
	"strings"
	"testing"
)

// --help and -h print the usage to stdout and exit 0, for the group and for each verb.
func TestRunPolicyHelp(t *testing.T) {
	own := map[string]string{"adopt": policyAdoptUsage, "serve": policyServeUsage, "reconcile": policyReconcileUsage}
	type tc struct {
		args []string
		want string
	}
	cases := []tc{{[]string{"--help"}, policyUsage}, {[]string{"-h"}, policyUsage}, {[]string{"explain", "a", "b", "--help"}, policyUsage}}
	for _, v := range []string{"adopt", "serve", "reconcile", "show", "explain", "diff"} {
		want, ok := own[v]
		if !ok {
			want = policyUsage
		}
		cases = append(cases, tc{[]string{v, "--help"}, want}, tc{[]string{v, "-h"}, want})
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		code := runPolicy(c.args, func(string) string { return "" }, func(string) (string, bool) { return "", false }, &out, &errb)
		if code != 0 || errb.Len() != 0 || !strings.Contains(out.String(), "usage: mashctl policy") || out.String() != c.want {
			t.Errorf("%v: code %d stdout %q stderr %q", c.args, code, out.String(), errb.String())
		}
	}
}

// Without a help flag an unknown option still exits 2 with the usage on stderr.
func TestRunPolicyUnknownOptionStillExits2(t *testing.T) {
	var out, errb bytes.Buffer
	code := runPolicy([]string{"serve", "--bogus"}, func(string) string { return "" }, func(string) (string, bool) { return "", false }, &out, &errb)
	if code != 2 || out.Len() != 0 {
		t.Fatalf("%d %q %q", code, out.String(), errb.String())
	}
}
