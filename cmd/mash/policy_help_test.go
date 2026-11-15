package main

import (
	"bytes"
	"testing"

	"github.com/bogworks/mash/internal/config"
)

// show --help, show -h and explain --help print the usage to stdout and exit 0; explain with no receiver is still 2.
func TestPolicyCmdHelp(t *testing.T) {
	for _, args := range [][]string{{"show", "--help"}, {"show", "-h"}, {"explain", "--help"}, {"explain", "-h"}} {
		var out, errb bytes.Buffer
		if code := policyCmd(config.Config{}, args, &out, &errb); code != 0 || out.String() != policyUsage+"\n" || errb.Len() != 0 {
			t.Errorf("%v: %d %q %q", args, code, out.String(), errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := policyCmd(config.Config{}, []string{"explain"}, &out, &errb); code != 2 || out.Len() != 0 {
		t.Errorf("explain: %d %q %q", code, out.String(), errb.String())
	}
}
