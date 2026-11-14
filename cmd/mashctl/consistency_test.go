package main

import (
	"os"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

// seedShaped looks like a NATS user seed: SU, then 54 base32 characters.
var seedShaped = "SU" + strings.Repeat("ABCDEFGH234567", 4)[:54]

func TestUsageErrorsExit2(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	_, u := e.Mint(t, "c")
	if _, errb, code := ctlRun(t, e, "provision", "c", "--user", u, "--account", e.Account); code != 0 {
		t.Fatalf("provision: %d %q", code, errb)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"suspend"}, "usage: mashctl suspend <name>"},
		{[]string{"resume"}, "usage: mashctl resume <name>"},
		{[]string{"suspended", "c", "extra"}, "usage: mashctl suspended <name>"},
		{[]string{"deprovision"}, "usage: mashctl deprovision"},
		{[]string{"deprovision", "c", "--bogus"}, "usage: mashctl deprovision"},
		{[]string{"provision"}, "usage: mashctl provision"},
		{[]string{"provision", "d", "--user", u, "--account", e.Account, "--bogus", "x"}, "usage: mashctl provision"},
		{[]string{"provision", "d", "--user", u, "--account", e.Account, "stray"}, "usage: mashctl provision"},
		{[]string{"provision", "d", "--user", u, "--account", e.Account, "--role"}, "usage: mashctl provision"},
		{[]string{"permissions"}, "usage: mashctl permissions"},
		{[]string{"permissions", "c", "--bogus"}, "usage: mashctl permissions"},
		{[]string{"permissions", "c", "--nsc", "x"}, "usage: mashctl permissions"},
		{[]string{"set-meta", "c"}, "usage: mashctl set-meta"},
		{[]string{"limits", "--bogus"}, "usage: mashctl limits"},
		{[]string{"limits", "--show", "x"}, "usage: mashctl limits"},
		{[]string{"list", "--bogus"}, "usage: mashctl list"},
		{[]string{"list", "extra"}, "usage: mashctl list"},
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
	if out, _, code := ctlRun(t, e, "list"); code != 0 || !strings.HasPrefix(out, "c\t") {
		t.Fatalf("list: %d %q", code, out)
	}
}

func TestDeprovisionMissingNameIsAnError(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	out, errb, code := ctlRun(t, e, "deprovision", "nonexist")
	if code != 1 || out != "" || errb != "no endpoint named nonexist\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	_, u := e.Mint(t, "real")
	ctlRun(t, e, "provision", "real", "--user", u, "--account", e.Account)
	if out, _, code := ctlRun(t, e, "deprovision", "real"); code != 0 || !strings.Contains(out, "deprovisioned real") {
		t.Fatalf("real: %d %q", code, out)
	}
	// gone now: the second one is the error
	if _, errb, code := ctlRun(t, e, "deprovision", "real"); code != 1 || !strings.Contains(errb, "no endpoint named real") {
		t.Fatalf("again: %d %q", code, errb)
	}
}

func TestPermissionsNoteIsOnStderr(t *testing.T) {
	e := testenv.Start(t)
	for _, args := range [][]string{{"permissions", "c"}, {"permissions", "c", "--nsc"}} {
		out, errb, code := ctlRun(t, e, args...)
		if code != 0 || !strings.Contains(errb, "hand-made legacy users") || !strings.Contains(errb, "mashctl policy explain") || strings.Contains(out, "legacy users") {
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
	_, u := e.Mint(t, "c")
	ctlRun(t, e, "provision", "c", "--user", u, "--account", e.Account)
	for _, args := range [][]string{
		{"provision", seedShaped, "--user", u, "--account", e.Account},
		{"provision", "d", "--user", seedShaped, "--account", e.Account},
		{"provision", "d", "--user", u, "--account", seedShaped},
		{"provision", "d", "--user", u, "--account", e.Account, "--label", seedShaped + "=x"},
		{"provision", "d", "--user", u, "--account", e.Account, "--cap", seedShaped},
		{"provision", "d", "--user", u, "--account", e.Account, "--" + seedShaped},
		{"set-meta", "c", "--label", seedShaped + "=x"},
		{"suspend", seedShaped},
		{"deprovision", seedShaped},
		{"deprovision", "c", "--user", seedShaped},
		{"permissions", seedShaped},
		{"limits", seedShaped},
		{"list", seedShaped},
	} {
		out, errb, code := ctlRun(t, e, args...)
		if code == 0 || strings.Contains(out+errb, seedShaped) || errb == "" {
			t.Errorf("%v: code %d out %q err %q", args[:2], code, out, errb)
		}
	}
	// the message says which argument
	if _, errb, _ := ctlRun(t, e, "provision", "d", "--user", seedShaped, "--account", e.Account); !strings.Contains(errb, "--user is not a NATS user public key") {
		t.Errorf("which argument: %q", errb)
	}
}

func TestPolicyAndLabelDoNotEchoInvalidArguments(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	for _, args := range [][]string{
		{"policy", "explain", seedShaped, "a", "--policy", h.policy},
		{"policy", "explain", "a", seedShaped, "--policy", h.policy},
		{"label", seedShaped, "team=x", "--policy", h.policy},
		{"unlabel", seedShaped, "team", "--policy", h.policy},
		{"label", "a", seedShaped, "--policy", h.policy},
		{"label", "a", seedShaped + "=x", "--policy", h.policy},
		{"unlabel", "a", seedShaped + "=x", "--policy", h.policy},
		{"add", seedShaped},
		{"retire", seedShaped},
		{"reissue", "--" + seedShaped + "=1"},
		{"policy", "check", seedShaped},
	} {
		out, errb, code := h.run(args...)
		if code == 0 || strings.Contains(out+errb, seedShaped) || errb == "" {
			t.Errorf("%v: code %d out %q err %q", args, code, out, errb)
		}
		if args[0] != "policy" && code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}

func TestPolicyFileModeIsKept(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o600, 0o644} {
		t.Run(mode.String(), func(t *testing.T) {
			h := newFleetEnv(t, addPolicy)
			if err := os.Chmod(h.policy, mode); err != nil {
				t.Fatal(err)
			}
			if _, errb, code := h.run("label", "a2", "team=x", "--policy", h.policy); code != 0 {
				t.Fatalf("label: %d %q", code, errb)
			}
			if _, errb, code := h.run("unlabel", "a2", "team", "--policy", h.policy); code != 0 {
				t.Fatalf("unlabel: %d %q", code, errb)
			}
			if _, errb, code := h.run("add", "g", "--label", "net=red", "--policy", h.policy); code != 0 {
				t.Fatalf("add: %d %q", code, errb)
			}
			if fi, err := os.Stat(h.policy); err != nil || fi.Mode().Perm() != mode {
				t.Fatalf("mode %v, want %v (%v)", fi, mode, err)
			}
		})
	}
}

func TestPolicyCheckRefusesLog(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "p.yaml", "endpoints:\n  a: {}\nrules:\n  - name: r1\n    action: deny\n    from: {}\n    to: {}\n    log: true\n")
	out, _, code := policyRun(t, nil, "policy", "check", "--policy", p)
	if code != 1 || out != "error: rule r1 (line 4): log is not implemented yet; remove it\n" {
		t.Fatalf("%d %q", code, out)
	}
}

func TestPolicyCheckWarnsOnCaseVariantLabels(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "p.yaml", "default: allow\nendpoints:\n  a: {labels: {team: Finance}}\n  b: {labels: {team: finance}}\n")
	out, _, code := policyRun(t, nil, "policy", "check", "--policy", p)
	if code != 0 || !strings.Contains(out, "warning: label team: values differ only in case") ||
		!strings.Contains(out, `"Finance" on endpoint a (line 3)`) || !strings.Contains(out, `"finance" on endpoint b (line 4)`) || !strings.Contains(out, "check: OK") {
		t.Fatalf("%d %q", code, out)
	}
}
