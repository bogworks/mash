package main

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/testenv"
)

// seedShaped looks like a NATS user seed: SU, then 54 base32 characters.
var seedShaped = "SU" + strings.Repeat("ABCDEFGH234567", 4)[:54]

func TestUsageErrorsExit2(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	provisionAs(t, e, "c", admin.Meta{})
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"suspend"}, "usage: mashctl suspend <owner.group.instance>"},
		{[]string{"resume"}, "usage: mashctl resume <owner.group.instance>"},
		{[]string{"suspended", "o.g.c", "extra"}, "usage: mashctl suspended <owner.group.instance>"},
		{[]string{"permissions"}, "usage: mashctl permissions"},
		{[]string{"permissions", "o.g.c", "--bogus"}, "usage: mashctl permissions"},
		{[]string{"permissions", "o.g.c", "--nsc", "x"}, "usage: mashctl permissions"},
		{[]string{"set-meta", "o.g.c"}, "usage: mashctl set-meta"},
		{[]string{"limits", "--bogus"}, "usage: mashctl limits"},
		{[]string{"limits", "--show", "x"}, "usage: mashctl limits"},
		{[]string{"init", "--bogus"}, "usage: mashctl init"},
		{[]string{"init", "extra"}, "usage: mashctl init"},
	}
	for _, c := range cases {
		out, errb, code := ctlRun(t, e, c.args...)
		if code != 2 || !strings.Contains(errb, c.want) || out != "" {
			t.Errorf("%v: code %d out %q err %q", c.args, code, out, errb)
		}
	}
	// the endpoint was not touched by any of them
	if out, _, code := ctlRun(t, e, "endpoint", "list"); code != 0 || !strings.Contains(out, "o/g/c") {
		t.Fatalf("endpoint list: %d %q", code, out)
	}
}

func TestPermissionsNoteIsOnStderr(t *testing.T) {
	e := testenv.Start(t)
	for _, args := range [][]string{{"permissions", "o.g.c"}, {"permissions", "o.g.c", "--nsc"}} {
		out, errb, code := ctlRun(t, e, args...)
		if code != 0 || !strings.Contains(errb, "the owner-line set") || false || strings.Contains(out, "NATS user") {
			t.Fatalf("%v: %d out %q err %q", args, code, out, errb)
		}
		if len(args) == 2 && !strings.HasPrefix(out, "{") {
			t.Errorf("stdout is not the JSON alone: %q", out)
		}
	}
}

func TestInvalidArgumentsAreNotEchoed(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	provisionAs(t, e, "c", admin.Meta{})
	for _, args := range [][]string{
		{"set-meta", "o.g.c", "--label", seedShaped + "=x"},
		{"suspend", seedShaped},
		{"permissions", seedShaped},
		{"limits", seedShaped},
	} {
		out, errb, code := ctlRun(t, e, args...)
		if code == 0 || strings.Contains(out+errb, seedShaped) || errb == "" {
			t.Errorf("%v: code %d out %q err %q", args[:2], code, out, errb)
		}
	}
}

func TestLabelAndPolicyDoNotEchoInvalidArguments(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("a"))
	for _, args := range [][]string{
		{"label", seedShaped, "team=x"},
		{"unlabel", seedShaped, "team"},
		{"label", "o.g.a", seedShaped},
		{"label", "o.g.a", seedShaped + "=x"},
		{"unlabel", "o.g.a", seedShaped + "=x"},
		{"endpoint", "delete", seedShaped},
		{"policy", seedShaped},
		{"policy", "--" + seedShaped + "=1"},
	} {
		out, errb, code := h.run(args...)
		if code == 0 || strings.Contains(out+errb, seedShaped) || errb == "" {
			t.Errorf("%v: code %d out %q err %q", args, code, out, errb)
		}
		if code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}
