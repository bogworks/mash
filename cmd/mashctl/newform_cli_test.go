package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

// ctlRunWith is ctlRun with extra environment, for the agent_token setting.
func ctlRunWith(t *testing.T, e *testenv.Env, extra map[string]string, args ...string) (string, string, int) {
	t.Helper()
	env := map[string]string{"HOME": t.TempDir(), "MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_LOOPBACK": "1"}
	for k, v := range extra {
		env[k] = v
	}
	var out, errb bytes.Buffer
	code := runEnv(args, func(k string) string { return env[k] }, &out, &errb)
	return out.String(), errb.String(), code
}

var settingOn = map[string]string{"MASH_AGENT_TOKEN": "ops"}

// Naming plan 1.6: provision and deprovision take the three tokens, behind the agent_token setting.
func TestMashctlNewFormProvisionDeprovision(t *testing.T) {
	e := testenv.Start(t)
	if _, errb, code := ctlRun(t, e, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, errb)
	}
	_, u := e.Mint(t, "nf")

	// Setting off: the three-token form is refused as a configuration refusal (exit 1), nothing is provisioned.
	_, errb, code := ctlRun(t, e, "provision", "recv", "--agent", "acme", "--user", u, "--account", e.Account)
	if code != 1 || !strings.Contains(errb, "agent_token") || !strings.Contains(errb, "MASH_AGENT_TOKEN") {
		t.Fatalf("setting off: %d %q", code, errb)
	}
	if out, _, _ := ctlRun(t, e, "list"); out != "" {
		t.Fatalf("something was provisioned: %q", out)
	}

	// Setting on: --agent A [--owner O] NAME; the owner defaults to the configured one.
	out, errb, code := ctlRunWith(t, e, settingOn, "provision", "recv", "--agent", "acme", "--owner", "team1", "--user", u, "--account", e.Account, "--role", "r")
	if code != 0 || !strings.Contains(out, "provisioned acme.team1.recv: inbox inbox-acme_team1_recv, mail agents.mail.acme.team1.recv") {
		t.Fatalf("provision: %d %q %q", code, out, errb)
	}
	out, _, code = ctlRunWith(t, e, settingOn, "provision", "other", "--agent", "acme", "--user", u, "--account", e.Account)
	if code != 0 || !strings.Contains(out, "provisioned acme."+e.Owner+".other:") {
		t.Fatalf("provision default owner: %d %q", code, out)
	}
	if out, _, _ := ctlRun(t, e, "list"); !strings.Contains(out, "acme_team1_recv\t"+u) {
		t.Fatalf("list: %q", out)
	}
	// Already provisioned.
	if _, errb, code := ctlRunWith(t, e, settingOn, "provision", "recv", "--agent", "acme", "--owner", "team1", "--user", u, "--account", e.Account); code != 1 || !strings.Contains(errb, "already provisioned") {
		t.Fatalf("again: %d %q", code, errb)
	}
	// A bad token is a usage error naming the rule.
	if _, errb, code := ctlRunWith(t, e, settingOn, "provision", "recv", "--agent", "cc", "--user", u, "--account", e.Account); code != 2 || !strings.Contains(errb, "cc") {
		t.Fatalf("legacy agent token: %d %q", code, errb)
	}
	if _, errb, code := ctlRunWith(t, e, settingOn, "provision", "recv", "--owner", "x", "--user", u, "--account", e.Account); code != 2 || !strings.Contains(errb, "--agent") {
		t.Fatalf("owner without agent: %d %q", code, errb)
	}

	// Deprovision by flags and by canonical positional; the retired marker then refuses a re-provision.
	if _, errb, code := ctlRun(t, e, "deprovision", "recv", "--agent", "acme", "--owner", "team1", "--no-revoke"); code != 1 || !strings.Contains(errb, "agent_token") {
		t.Fatalf("deprovision setting off: %d %q", code, errb)
	}
	out, errb, code = ctlRunWith(t, e, settingOn, "deprovision", "recv", "--agent", "acme", "--owner", "team1", "--no-revoke")
	if code != 0 || !strings.Contains(out, "deprovisioned acme.team1.recv") {
		t.Fatalf("deprovision: %d %q %q", code, out, errb)
	}
	_, errb, code = ctlRunWith(t, e, settingOn, "provision", "recv", "--agent", "acme", "--owner", "team1", "--user", u, "--account", e.Account)
	if code != 1 || !strings.Contains(errb, "acme.team1.recv is retired") || !strings.Contains(errb, "never reassigned") {
		t.Fatalf("retired: %d %q", code, errb)
	}
	out, errb, code = ctlRunWith(t, e, settingOn, "deprovision", "acme."+e.Owner+".other", "--no-revoke")
	if code != 0 || !strings.Contains(out, "deprovisioned acme."+e.Owner+".other") {
		t.Fatalf("deprovision canonical: %d %q %q", code, out, errb)
	}
	if _, errb, code := ctlRunWith(t, e, settingOn, "deprovision", "acme."+e.Owner+".other", "--no-revoke"); code != 1 || !strings.Contains(errb, "no endpoint named acme."+e.Owner+".other") {
		t.Fatalf("deprovision again: %d %q", code, errb)
	}
}

// --alias OWNER/NAME names the legacy endpoint the new one answers to (ADR 0076); a refusal of it is a CLI error.
func TestMashctlNewFormAlias(t *testing.T) {
	e := testenv.Start(t)
	if _, errb, code := ctlRun(t, e, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, errb)
	}
	_, u := e.Mint(t, "old")
	if _, errb, code := ctlRun(t, e, "provision", "old", "--user", u, "--account", e.Account); code != 0 {
		t.Fatalf("legacy provision: %d %q", code, errb)
	}
	_, u2 := e.Mint(t, "new")
	out, errb, code := ctlRunWith(t, e, settingOn, "provision", "fresh", "--agent", "acme", "--owner", "team1", "--alias", e.Owner+"/old", "--user", u2, "--account", e.Account)
	if code != 0 || !strings.Contains(out, "alias "+e.Owner+"/old") {
		t.Fatalf("alias: %d %q %q", code, out, errb)
	}
	_, errb, code = ctlRunWith(t, e, settingOn, "provision", "fresh2", "--agent", "acme", "--owner", "team1", "--alias", e.Owner+"/nope", "--user", u2, "--account", e.Account)
	if code != 1 || !strings.Contains(errb, "alias refused") {
		t.Fatalf("alias refused: %d %q", code, errb)
	}
	if _, errb, code := ctlRunWith(t, e, settingOn, "provision", "fresh3", "--agent", "acme", "--owner", "team1", "--alias", "nowhere", "--user", u2, "--account", e.Account); code != 2 || !strings.Contains(errb, "--alias") {
		t.Fatalf("alias shape: %d %q", code, errb)
	}
	// --alias without --agent is a usage error, with the setting on or off.
	if _, errb, code := ctlRun(t, e, "provision", "x", "--alias", e.Owner+"/old", "--user", u2, "--account", e.Account); code != 2 {
		t.Fatalf("alias with legacy name: %d %q", code, errb)
	}
}

// Legacy use under the setting stays as it was: the legacy form of these verbs is still refused with agent_token set.
func TestMashctlLegacyProvisionStillRefusedWithSetting(t *testing.T) {
	e := testenv.Start(t)
	_, errb, code := ctlRunWith(t, e, settingOn, "provision", "x")
	if code == 0 || !strings.Contains(errb, refusal) {
		t.Fatalf("%d %q", code, errb)
	}
}
