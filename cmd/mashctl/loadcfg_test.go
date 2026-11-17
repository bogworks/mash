package main

import (
	"io"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/config"
)

// lk is the lookup tests pass when they inject only an env func: a variable is present iff it is not empty.
func lk(env func(string) string) config.Lookup { return config.LookupFromFunc(env) }

// runEnv is run with the lookup derived from env.
func runEnv(args []string, env func(string) string, stdout, stderr io.Writer) int {
	return run(args, env, config.LookupFromFunc(env), stdout, stderr)
}

func TestParseAddr(t *testing.T) {
	for _, in := range []string{"acme.team1.ops1", "acme_team1_ops1"} {
		a, err := parseAddr(in)
		if err != nil || a.String() != "acme.team1.ops1" {
			t.Errorf("%q: %v %v", in, a, err)
		}
	}
	for _, in := range []string{"ops1", "acme.ops1", "acme.team1.ops1.x", "Acme.team1.ops1", "acme_team1", ""} {
		if _, err := parseAddr(in); err == nil || !strings.Contains(err.Error(), "give owner.group.instance") {
			t.Errorf("%q: err %v", in, err)
		}
	}
}
