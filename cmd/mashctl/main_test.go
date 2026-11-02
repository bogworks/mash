package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/testenv"
)

func ctlRun(t *testing.T, e *testenv.Env, args ...string) (string, string, int) {
	t.Helper()
	env := map[string]string{"HOME": t.TempDir(), "MASH_URL": e.URL, "MASH_OWNER": e.Owner, "MASH_ADMIN_CREDS": e.AdminCreds}
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
	if _, errb, code := ctlRun(t, e, "deprovision"); code == 0 || !strings.Contains(errb, "usage: mashctl deprovision <name> [--user <public-key>]") {
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
		if out, errb, code := ctlRun(t, e, h); code != 0 || errb != "" || !strings.Contains(out, "mashctl deprovision <name> [--user U]") {
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
