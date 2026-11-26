package main

import "testing"

// One exit rule, the same in both tools: 0 it worked, 1 it failed, 2 the command line was wrong and nothing was
// attempted. Each case is a command that needs no server.
func TestExitRule(t *testing.T) {
	home := t.TempDir()
	for _, c := range []struct {
		name string
		args []string
		want int
	}{
		{"help worked", []string{"--help"}, 0},
		{"no endpoint installed failed", []string{"who"}, 1},
		{"missing argument was a wrong command line", []string{"send"}, 2},
		{"unknown option was a wrong command line", []string{"who", "--nope"}, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			if r := runMash(map[string]string{"HOME": home}, c.args...); r.code != c.want {
				t.Fatalf("mash %v: exit %d, want %d (stderr %q)", c.args, r.code, c.want, r.errs)
			}
		})
	}
}
