package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/testenv"
)

func ctlRun(t *testing.T, e *testenv.Env, args ...string) (string, string, int) {
	t.Helper()
	env := map[string]string{"HOME": t.TempDir(), "MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_LOOPBACK": "1"}
	var out, errb bytes.Buffer
	code := run(args, func(k string) string { return env[k] }, &out, &errb)
	return out.String(), errb.String(), code
}

func TestMashctlVerbs(t *testing.T) {
	e := testenv.Start(t)
	if out, errb, code := ctlRun(t, e, "init"); code != 0 || !strings.Contains(out, "mashctl: directory and lease buckets ready") {
		t.Fatalf("init: %d %q %q", code, out, errb)
	}
	_, u := e.Mint(t, "c")
	if out, errb, code := ctlRun(t, e, "provision", "c", "--user", u, "--account", e.Account, "--role", "r"); code != 0 || !strings.Contains(out, "provisioned c:") {
		t.Fatalf("provision: %d %q %q", code, out, errb)
	}
	if out, _, code := ctlRun(t, e, "list"); code != 0 || !strings.HasPrefix(out, "c\t"+u) {
		t.Fatalf("list: %d %q", code, out)
	}
	if out, errb, code := ctlRun(t, e, "set-meta", "c", "--host", "h1"); code != 0 || !strings.Contains(out, `host "h1"`) {
		t.Fatalf("set-meta: %d %q %q", code, out, errb)
	}
	if out, _, code := ctlRun(t, e, "limits", "--show"); code != 0 || !strings.Contains(out, "inbox_max_bytes") {
		t.Fatalf("limits --show: %d %q", code, out)
	}
	if out, _, code := ctlRun(t, e, "limits", "c"); code != 0 {
		t.Fatalf("limits: %d %q", code, out)
	}
	if out, _, code := ctlRun(t, e, "permissions", "c", "--nsc"); code != 0 || out == "" {
		t.Fatalf("permissions: %d %q", code, out)
	}
	if _, errb, code := ctlRun(t, e, "deprovision"); code == 0 || !strings.Contains(errb, "usage: mashctl deprovision <name> [--user <public-key> | --drained-to SEQ --suspension ID]") {
		t.Fatalf("deprovision usage: %d %q", code, errb)
	}
	if _, errb, code := ctlRun(t, e, "provision"); code == 0 || !strings.Contains(errb, "usage: mashctl provision") {
		t.Fatalf("provision usage: %d %q", code, errb)
	}
	if out, errb, code := ctlRun(t, e, "deprovision", "c"); code != 0 || !strings.Contains(out, "deprovisioned c") {
		t.Fatalf("deprovision: %d %q %q", code, out, errb)
	}
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if recs, err := adm.List(context.Background()); err != nil || len(recs) != 0 {
		t.Fatalf("after deprovision: %v %v", recs, err)
	}
}

func TestMashctlUsageAndErrors(t *testing.T) {
	e := testenv.Start(t)
	if out, errb, code := ctlRun(t, e); code != 2 || out != "" || !strings.Contains(errb, "mashctl list") || strings.Contains(errb, "mash admin") {
		t.Fatalf("no args: %d %q %q", code, out, errb)
	}
	for _, h := range []string{"help", "-h", "--help"} {
		if out, errb, code := ctlRun(t, e, h); code != 0 || errb != "" || !strings.Contains(out, "mashctl deprovision <name> [--user U | --drained-to SEQ --suspension ID]") {
			t.Fatalf("%s: %d %q %q", h, code, out, errb)
		}
	}
	if out, errb, code := ctlRun(t, e, "frob"); code != 2 || out != "" || !strings.Contains(errb, `mashctl: unknown command "frob"`) || !strings.Contains(errb, "mashctl init") {
		t.Fatalf("unknown: %d %q %q", code, out, errb)
	}
	if out, _, code := ctlRun(t, e, "version"); code != 0 || !strings.HasPrefix(out, "mashctl ") {
		t.Fatalf("version: %d %q", code, out)
	}
	// config errors fail the verbs that need config
	var out, errb bytes.Buffer
	env := map[string]string{"HOME": t.TempDir(), "MASH_URL": e.URL} // no owner
	if code := run([]string{"list"}, func(k string) string { return env[k] }, &out, &errb); code != 1 || !strings.Contains(errb.String(), "mashctl: no owner") {
		t.Fatalf("no owner: %d %q", code, errb.String())
	}
}

func TestMashctlZone(t *testing.T) {
	e := testenv.Start(t)
	if _, errb, code := ctlRun(t, e, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, errb)
	}
	_, u := e.Mint(t, "zed")
	_, v := e.Mint(t, "leg")
	if out, errb, code := ctlRun(t, e, "provision", "zed", "--user", u, "--account", e.Account, "--zone", "z1"); code != 0 ||
		!strings.Contains(out, "mail agents.mail.cc."+e.Owner+".z1.zed") || !strings.Contains(out, "zone z1") {
		t.Fatalf("provision --zone: %d %q %q", code, out, errb)
	}
	if _, errb, code := ctlRun(t, e, "provision", "leg", "--user", v, "--account", e.Account); code != 0 {
		t.Fatalf("provision leg: %d %q", code, errb)
	}
	if out, errb, code := ctlRun(t, e, "set-zone", "leg", "z2"); code != 0 || !strings.Contains(out, "zone z2") {
		t.Fatalf("set-zone: %d %q %q", code, out, errb)
	}
	out, _, code := ctlRun(t, e, "list")
	if code != 0 || !strings.Contains(out, "\tzone=z1\n") || !strings.Contains(out, "\tzone=z2\n") {
		t.Fatalf("list: %d %q", code, out)
	}
	if _, errb, code := ctlRun(t, e, "set-zone", "leg", "-"); code == 0 || !strings.Contains(errb, "mashctl: set-zone") {
		t.Fatalf("set-zone -: %d %q", code, errb)
	}
	if _, errb, code := ctlRun(t, e, "set-zone", "leg"); code == 0 || !strings.Contains(errb, "usage: mashctl set-zone <name> <zone>") {
		t.Fatalf("set-zone usage: %d %q", code, errb)
	}
	var h bytes.Buffer
	run([]string{"help"}, func(string) string { return "" }, &h, &h)
	if !strings.Contains(h.String(), "mashctl set-zone <name> <zone>") || !strings.Contains(h.String(), "[--zone Z]") {
		t.Fatalf("help lacks zone:\n%s", h.String())
	}
}

func TestMashctlSuspendVerbs(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	_, u := e.Mint(t, "c")
	if _, errb, code := ctlRun(t, e, "provision", "c", "--user", u, "--account", e.Account); code != 0 {
		t.Fatalf("provision: %d %q", code, errb)
	}
	if out, _, code := ctlRun(t, e, "suspended", "c"); code != 3 || !strings.Contains(out, "c is not suspended") {
		t.Fatalf("suspended before: %d %q", code, out)
	}
	out, errb, code := ctlRun(t, e, "suspend", "c")
	if code != 0 || !regexp.MustCompile(`^suspended c: id [0-9a-f]{32}, last_seq 0, msgs 0 \(new mail parked; the endpoint's credentials are still valid\)\n$`).MatchString(out) {
		t.Fatalf("suspend: %d %q %q", code, out, errb)
	}
	if out, _, code := ctlRun(t, e, "suspended", "c"); code != 0 || !strings.HasPrefix(out, "suspended c since ") || !regexp.MustCompile(`: id [0-9a-f]{32}, last_seq 0, msgs 0\n$`).MatchString(out) {
		t.Fatalf("suspended: %d %q", code, out)
	}
	if out, errb, code := ctlRun(t, e, "resume", "c"); code != 0 || out != "resumed c\n" {
		t.Fatalf("resume: %d %q %q", code, out, errb)
	}
	if _, _, code := ctlRun(t, e, "suspended", "c"); code != 3 {
		t.Fatalf("suspended after resume: %d", code)
	}
	// Any error is 1, never 3.
	if _, errb, code := ctlRun(t, e, "suspended", "ghost"); code != 1 || !strings.Contains(errb, "no inbox stream") {
		t.Fatalf("suspended ghost: %d %q", code, errb)
	}
	if _, errb, code := ctlRun(t, e, "suspend"); code == 0 || !strings.Contains(errb, "usage: mashctl suspend <name>") {
		t.Fatalf("usage: %d %q", code, errb)
	}
}

func TestMashctlDeprovisionDrainedTo(t *testing.T) {
	e := testenv.Start(t)
	ctlRun(t, e, "init")
	_, u := e.Mint(t, "c")
	if _, errb, code := ctlRun(t, e, "provision", "c", "--user", u, "--account", e.Account); code != 0 {
		t.Fatalf("provision: %d %q", code, errb)
	}
	const bogus = "0123456789abcdef0123456789abcdef"
	if _, errb, code := ctlRun(t, e, "deprovision", "c", "--drained-to", "0", "--suspension", bogus); code != 1 || !strings.Contains(errb, "not suspended") {
		t.Fatalf("not suspended: %d %q", code, errb)
	}
	out, _, _ := ctlRun(t, e, "suspend", "c")
	m := regexp.MustCompile(`id ([0-9a-f]{32}),`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no id in %q", out)
	}
	id := m[1]
	if _, errb, code := ctlRun(t, e, "deprovision", "c", "--drained-to", "5", "--suspension", id); code != 1 || !strings.Contains(errb, "last_seq 0, not the drained 5") {
		t.Fatalf("mismatch: %d %q", code, errb)
	}
	if _, errb, code := ctlRun(t, e, "deprovision", "c", "--drained-to", "0", "--suspension", bogus); code != 1 || !strings.Contains(errb, "not the suspension you drained") {
		t.Fatalf("wrong id: %d %q", code, errb)
	}
	if _, errb, code := ctlRun(t, e, "deprovision", "c", "--drained-to", "-1", "--suspension", id); code == 0 || !strings.Contains(errb, "non-negative integer") {
		t.Fatalf("bad seq: %d %q", code, errb)
	}
	for _, alone := range [][]string{{"--drained-to", "0"}, {"--suspension", id}} {
		if _, errb, code := ctlRun(t, e, append([]string{"deprovision", "c"}, alone...)...); code == 0 || !strings.Contains(errb, "go together") {
			t.Fatalf("alone %v: %d %q", alone, code, errb)
		}
	}
	if _, errb, code := ctlRun(t, e, "deprovision", "c", "--user", u, "--drained-to", "0", "--suspension", id); code == 0 || !strings.Contains(errb, "cannot be combined") {
		t.Fatalf("combined: %d %q", code, errb)
	}
	if out, errb, code := ctlRun(t, e, "deprovision", "c", "--suspension", id, "--drained-to", "0"); code != 0 || !strings.Contains(out, "deprovisioned c") {
		t.Fatalf("drained: %d %q %q", code, out, errb)
	}
}
