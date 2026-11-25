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
	makeStorageFor(t, e)
	provisionAs(t, e, "c", admin.Meta{})
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"endpoint", "suspend"}, "error: missing the endpoint address"},
		{[]string{"endpoint", "resume"}, "error: missing the endpoint address"},
		{[]string{"endpoint", "suspend", "o.g.c", "extra"}, "error: too many arguments"},
		{[]string{"endpoint", "permissions"}, "error: missing the endpoint address"},
		{[]string{"endpoint", "permissions", "o.g.c", "--bogus"}, `error: unknown option --bogus`},
		{[]string{"endpoint", "permissions", "o.g.c", "x"}, "error: too many arguments"},
		{[]string{"endpoint", "set", "o.g.c"}, "error: missing the endpoint address and at least one key=value"},
		{[]string{"endpoint", "unset", "o.g.c"}, "error: missing the endpoint address and at least one key"},
		{[]string{"limits", "--bogus"}, "error: unknown option --bogus\nhint: mashctl limits --help\n"},
		{[]string{"limits", "--show"}, "error: unknown option --show\nhint: mashctl limits --help\n"},
		{[]string{"limits", "show", "x"}, "error: too many arguments\nhint: mashctl limits show --help\n"},
		{[]string{"limits", "show", "--bogus"}, "error: unknown option --bogus\nhint: mashctl limits show --help\n"},
		{[]string{"limits", "apply", "--bogus"}, "error: unknown option --bogus\nhint: mashctl limits apply --help\n"},
		{[]string{"limits", "apply", "a.b.c.d"}, "error: not an owner, owner.group or owner.group.name prefix"},
		{[]string{"limits", "apply", "a", "b"}, "error: too many arguments\nhint: mashctl limits apply --help\n"},
		{[]string{"status", "--bogus"}, "error: unknown option --bogus\nhint: mashctl status --help\n"},
		{[]string{"status", "extra"}, "error: too many arguments\nhint: mashctl status --help\n"},
		{[]string{"init", "--bogus"}, "error: unknown option --bogus\nhint: mashctl init --help\n"},
		{[]string{"init", "extra"}, "error: too many arguments\nhint: mashctl init --help\n"},
		{[]string{"init"}, "error: mashctl init needs --config <file>\nhint: mashctl init --help\n"},
		{[]string{"limits"}, "usage: mashctl limits <command>"},
		{[]string{"adopt"}, "error: unknown command \"adopt\"\n"},
	}
	for _, c := range cases {
		out, errb, code := ctlRun(t, e, c.args...)
		if code != 2 || !strings.Contains(errb, c.want) || out != "" {
			t.Errorf("%v: code %d out %q err %q", c.args, code, out, errb)
		}
	}
	// the endpoint was not touched by any of them
	if out, _, code := ctlRun(t, e, "endpoint", "list"); code != 0 || !strings.Contains(out, "o      g      c") {
		t.Fatalf("endpoint list: %d %q", code, out)
	}
}

func TestPermissionsPrintsDataOnly(t *testing.T) {
	e := testenv.Start(t)
	out, errb, code := ctlRun(t, e, "endpoint", "permissions", "o.g.c")
	if code != 0 || errb != "" || !strings.HasPrefix(out, "{") || !strings.HasSuffix(out, "}\n") {
		t.Fatalf("%d out %q err %q", code, out, errb)
	}
}

func TestInvalidArgumentsAreNotEchoed(t *testing.T) {
	e := testenv.Start(t)
	makeStorageFor(t, e)
	provisionAs(t, e, "c", admin.Meta{})
	for _, args := range [][]string{
		{"endpoint", "set", "o.g.c", seedShaped + "=x"},
		{"endpoint", "set", "o.g.c", "label." + seedShaped + "=x"},
		{"endpoint", "unset", "o.g.c", seedShaped},
		{"endpoint", "suspend", seedShaped},
		{"endpoint", "permissions", seedShaped},
		{"limits", seedShaped},
	} {
		out, errb, code := ctlRun(t, e, args...)
		if code == 0 || strings.Contains(out+errb, seedShaped) || errb == "" {
			t.Errorf("%v: code %d out %q err %q", args[:2], code, out, errb)
		}
	}
}

func TestSetAndPolicyDoNotEchoInvalidArguments(t *testing.T) {
	h := newFleetEnv(t)
	h.seed(ad("a"))
	for _, args := range [][]string{
		{"endpoint", "set", seedShaped, "label.team=x"},
		{"endpoint", "unset", seedShaped, "label.team"},
		{"endpoint", "set", "o.g.a", seedShaped},
		{"endpoint", "set", "o.g.a", seedShaped + "=x"},
		{"endpoint", "unset", "o.g.a", seedShaped + "=x"},
		{"endpoint", "show", seedShaped},
		{"endpoint", "inbox", seedShaped},
		{"endpoint", "delete", seedShaped, "--yes"},
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
