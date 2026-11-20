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
	env := map[string]string{"HOME": t.TempDir(), "MASH_SERVER": e.URL, "MASH_ENDPOINT": e.Owner + ".dev." + "x", "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_INSECURE_PLAINTEXT": "1"}
	var out, errb bytes.Buffer
	code := runEnv(args, func(k string) string { return env[k] }, &out, &errb)
	return out.String(), errb.String(), code
}

// provisionAs creates the directory record of ad(instance) through the admin API the endpoint verbs use, and returns
// the user key it belongs to.
func provisionAs(t *testing.T, e *testenv.Env, instance string, meta admin.Meta) string {
	t.Helper()
	_, u := e.Mint(t, ad(instance))
	a, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Provision(context.Background(), ad(instance), u, e.Account, "", meta); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestMashctlVerbs(t *testing.T) {
	e := testenv.Start(t)
	if out, errb, code := ctlRun(t, e, "init"); code != 0 || !strings.Contains(out, "mashctl: directory and lease buckets ready") {
		t.Fatalf("init: %d %q %q", code, out, errb)
	}
	provisionAs(t, e, "c", admin.Meta{Role: "r"})
	if out, _, code := ctlRun(t, e, "endpoint", "list"); code != 0 || out != "OWNER  GROUP  NAME  STATE\no      g      c     offline\n" {
		t.Fatalf("endpoint list: %d %q", code, out)
	}
	if out, errb, code := ctlRun(t, e, "endpoint", "set", "o.g.c", "host=h1"); code != 0 || out != "Updated endpoint o.g.c\n" {
		t.Fatalf("endpoint set: %d %q %q", code, out, errb)
	}
	if out, _, code := ctlRun(t, e, "limits", "--show"); code != 0 || !strings.Contains(out, "inbox_max_bytes") {
		t.Fatalf("limits --show: %d %q", code, out)
	}
	if out, _, code := ctlRun(t, e, "limits", "o.g.c"); code != 0 {
		t.Fatalf("limits: %d %q", code, out)
	}
	if out, errb, code := ctlRun(t, e, "endpoint", "permissions", "o.g.c"); code != 0 || !strings.HasPrefix(out, "{") || errb != "" {
		t.Fatalf("permissions: %d %q %q", code, out, errb)
	}
}

func TestMashctlUsageAndErrors(t *testing.T) {
	e := testenv.Start(t)
	if out, errb, code := ctlRun(t, e); code != 2 || out != "" || !strings.Contains(errb, "mashctl init") || strings.Contains(errb, "mash admin") {
		t.Fatalf("no args: %d %q %q", code, out, errb)
	}
	for _, h := range []string{"help", "-h", "--help"} {
		if out, errb, code := ctlRun(t, e, h); code != 0 || errb != "" || !strings.Contains(out, "mashctl endpoint set ADDRESS key=value...") || strings.Contains(out, "set-meta") {
			t.Fatalf("%s: %d %q %q", h, code, out, errb)
		}
	}
	// the removed verbs are unknown commands, like any other
	for _, verb := range []string{"provision", "deprovision", "list", "show", "set-meta", "label", "unlabel", "suspend", "resume", "suspended", "permissions", "inbox"} {
		for _, args := range [][]string{{verb}, {verb, "o.g.c"}, {verb, "--help"}} {
			if out, errb, code := ctlRun(t, e, args...); code != 2 || out != "" || !strings.Contains(errb, `mashctl: unknown command "`+verb+`"`) || !strings.Contains(errb, "mashctl init") {
				t.Errorf("%v: %d %q %q", args, code, out, errb)
			}
		}
	}
	if out, errb, code := ctlRun(t, e, "frob"); code != 2 || out != "" || !strings.Contains(errb, `mashctl: unknown command "frob"`) || !strings.Contains(errb, "mashctl init") {
		t.Fatalf("unknown: %d %q %q", code, out, errb)
	}
	if out, _, code := ctlRun(t, e, "version"); code != 0 || !regexp.MustCompile(`^mashctl v\S+ \(\S+\)\n$`).MatchString(out) {
		t.Fatalf("version: %d %q", code, out)
	}
	// config errors fail the verbs that need config
	var out, errb bytes.Buffer
	env := map[string]string{"HOME": t.TempDir(), "MASH_SERVER": e.URL} // no owner
	if code := runEnv([]string{"endpoint", "suspend", "o.g.c"}, func(k string) string { return env[k] }, &out, &errb); code != 1 || !strings.HasPrefix(errb.String(), "error: no owner") {
		t.Fatalf("no owner: %d %q", code, errb.String())
	}
}

// TestMashctlAddressOnly: every verb that names an endpoint takes an address, and a bare name is refused with the form to
// give. The removed verbs are unknown commands.
func TestMashctlAddressOnly(t *testing.T) {
	e := testenv.Start(t)
	if _, errb, code := ctlRun(t, e, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, errb)
	}
	provisionAs(t, e, "leg", admin.Meta{})
	for _, args := range [][]string{
		{"endpoint", "set", "leg", "role=r"}, {"endpoint", "unset", "leg", "role"},
		{"endpoint", "suspend", "leg"}, {"endpoint", "resume", "leg"}, {"endpoint", "permissions", "leg"},
		{"endpoint", "inbox", "leg"}, {"endpoint", "show", "leg"},
	} {
		if _, errb, code := ctlRun(t, e, args...); code != 2 || !strings.Contains(errb, "write owner.group.name") {
			t.Errorf("%v: %d %q", args, code, errb)
		}
	}
	if _, errb, code := ctlRun(t, e, "endpoint", "delete", "leg"); code != 2 || !strings.Contains(errb, "write owner/group/instance") {
		t.Errorf("endpoint delete leg: %d %q", code, errb)
	}
	for _, verb := range []string{"unzone", "set-zone"} {
		if out, errb, code := ctlRun(t, e, verb, "o.g.leg"); code != 2 || out != "" || !strings.Contains(errb, `unknown command "`+verb+`"`) {
			t.Errorf("%s: %d %q %q", verb, code, out, errb)
		}
	}
	var h bytes.Buffer
	runEnv([]string{"help"}, func(string) string { return "" }, &h, &h)
	for _, gone := range []string{"zone", "agent_token", "mashctl reissue", "mashctl retire", "mashctl add"} {
		if strings.Contains(strings.ToLower(h.String()), gone) {
			t.Errorf("help mentions %q:\n%s", gone, h.String())
		}
	}
}
