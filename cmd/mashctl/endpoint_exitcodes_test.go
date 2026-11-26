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
		{"endpoint", "delete", "o.g.a", "--yes"},
	} {
		h := newFleetEnv(t)
		h.env["MASH_SERVER"] = "nats://127.0.0.1:1"
		_, se, code := h.run(args...)
		if code != 1 || se == "" {
			t.Errorf("%v: exit %d, want 1 with a message on stderr: %q", args, code, se)
		}
	}
}

// One exit rule: a usage error exits 2, a failure exits 1, success exits 0. A refused plain URL and the refusal of the
// encryption override are failures, so exit 1 too.
func TestExitRuleZeroOneTwo(t *testing.T) {
	h := newFleetEnv(t)
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"success", []string{"version"}, 0},
		{"success, help", []string{"--help"}, 0},
		{"usage, no address", []string{"endpoint", "show"}, 2},
		{"usage, unknown flag", []string{"status", "--x"}, 2},
		{"usage, unknown command", []string{"nope"}, 2},
	} {
		if _, se, code := h.run(tc.args...); code != tc.want {
			t.Errorf("%s: exit %d, want %d: %q", tc.name, code, tc.want, se)
		}
	}
	for name, server := range map[string]string{"unreachable server": "tls://127.0.0.1:1", "plain URL refused": "nats://127.0.0.1:1"} {
		h := newFleetEnv(t)
		h.env["MASH_SERVER"] = server
		if _, se, code := h.run("endpoint", "list"); code != 1 {
			t.Errorf("%s: exit %d, want 1: %q", name, code, se)
		}
	}
	h.env["MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION"] = "yes"
	if _, se, code := h.run("version"); code != 1 {
		t.Errorf("a bad encryption override: exit %d, want 1: %q", code, se)
	}
}
