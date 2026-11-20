package main

import "testing"

// mashctl exits with 0, 1 or 2 only. A server that cannot be reached is 1 for every endpoint verb that talks to it,
// and the message goes to standard error.
func TestEndpointUnreachableServerExitsOne(t *testing.T) {
	for _, args := range [][]string{
		{"endpoint", "list"},
		{"endpoint", "show", "o.g.a"},
		{"endpoint", "set", "o.g.a", "role=r"},
		{"endpoint", "unset", "o.g.a", "role"},
		{"endpoint", "suspend", "o.g.a"},
		{"endpoint", "resume", "o.g.a"},
		{"endpoint", "inbox", "o.g.a"},
		{"endpoint", "delete", "o/g/a", "--yes"},
	} {
		h := newFleetEnv(t)
		h.env["MASH_SERVER"] = "nats://127.0.0.1:1"
		_, se, code := h.run(args...)
		if code != 1 || se == "" {
			t.Errorf("%v: exit %d, want 1 with a message on stderr: %q", args, code, se)
		}
	}
}
