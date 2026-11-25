package main

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/testenv"
)

func ctlRun(t *testing.T, e *testenv.Env, args ...string) (string, string, int) {
	t.Helper()
	env := map[string]string{"HOME": t.TempDir(), "MASH_SERVER": e.URL, "MASH_ENDPOINT": e.Owner + ".dev." + "x", "MASH_ADMIN_CREDS": e.AdminCreds, "MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION": "1"}
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
	makeStorageFor(t, e)
	provisionAs(t, e, "c", admin.Meta{Role: "r"})
	if out, _, code := ctlRun(t, e, "endpoint", "list"); code != 0 || out != "OWNER  GROUP  NAME  ADDRESS  STATE\no      g      c     o.g.c    offline\n" {
		t.Fatalf("endpoint list: %d %q", code, out)
	}
	if out, errb, code := ctlRun(t, e, "endpoint", "set", "o.g.c", "host=h1"); code != 0 || out != "Updated endpoint o.g.c\n" {
		t.Fatalf("endpoint set: %d %q %q", code, out, errb)
	}
	if out, _, code := ctlRun(t, e, "limits", "show"); code != 0 || out != "inbox size:  16 MiB\ninbox age:   30 days\n" {
		t.Fatalf("limits show: %d %q", code, out)
	}
	if out, _, code := ctlRun(t, e, "limits", "apply", "o.g.c"); code != 0 || out != "Nothing to raise\n" {
		t.Fatalf("limits apply: %d %q", code, out)
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
			if out, errb, code := ctlRun(t, e, args...); code != 2 || out != "" || errb != "error: unknown command \""+verb+"\"\n" {
				t.Errorf("%v: %d %q %q", args, code, out, errb)
			}
		}
	}
	if out, errb, code := ctlRun(t, e, "frob"); code != 2 || out != "" || errb != "error: unknown command \"frob\"\n" {
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
	makeStorageFor(t, e)
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
	if _, errb, code := ctlRun(t, e, "endpoint", "delete", "leg", "--yes"); code != 2 || !strings.Contains(errb, "write owner.group.name") {
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

// A global option stands before the verb, as it does for mash: `mashctl --json status` is `mashctl status --json`.
// An unknown flag is an unknown flag wherever it stands, never an unknown command; neither error carries a hint
// (ADR 0168), and a flag's value is never echoed.
func TestMashctlGlobalOptionsBeforeVerb(t *testing.T) {
	e := testenv.Start(t)
	after, aerr, acode := ctlRun(t, e, "status", "--json")
	before, berr, bcode := ctlRun(t, e, "--json", "status")
	if acode != bcode || after != before || aerr != berr || !strings.HasPrefix(before, "{") {
		t.Fatalf("--json before the verb: %d %q %q; after: %d %q %q", bcode, before, berr, acode, after, aerr)
	}
	if out, errb, code := ctlRun(t, e, "--verbose", "status"); code != 2 || out != "" || errb != "error: unknown flag --verbose\n" {
		t.Errorf("unknown flag before the verb: %d %q %q", code, out, errb)
	}
	if out, errb, code := ctlRun(t, e, "status", "--bogus"); code != 2 || out != "" || errb != "error: unknown flag --bogus\n" {
		t.Errorf("unknown flag after the verb: %d %q %q", code, out, errb)
	}
	if out, errb, code := ctlRun(t, e, "status", "--x=secret"); code != 2 || out != "" || errb != "error: unknown flag --x\n" {
		t.Errorf("flag value echoed after the verb: %d %q %q", code, out, errb)
	}
	if out, errb, code := ctlRun(t, e, "nope"); code != 2 || out != "" || errb != "error: unknown command \"nope\"\n" {
		t.Errorf("unknown command: %d %q %q", code, out, errb)
	}
	if out, errb, code := ctlRun(t, e, "--x=secret", "status"); code != 2 || out != "" || errb != "error: unknown flag --x\n" || strings.Contains(errb, "secret") {
		t.Errorf("flag value echoed: %d %q %q", code, out, errb)
	}
	if out, errb, code := ctlRun(t, e, "--version"); code != 0 || errb != "" || !strings.HasPrefix(out, "mashctl v") {
		t.Errorf("--version: %d %q %q", code, out, errb)
	}
}

// TestVersionJSONPolicyByDestination: `mashctl version --json` is one compact line when stdout is a pipe or a file and
// indented at a terminal (the rule "--json prints one object", #1043).
func TestVersionJSONPolicyByDestination(t *testing.T) {
	old := cliframe.IsTerminal
	t.Cleanup(func() { cliframe.IsTerminal = old })
	for _, tc := range []struct {
		name string
		tty  bool
		re   string
	}{
		{"non-terminal", false, `^\{"version":"[^"\n]+","commit":"[^"\n]+"\}\n$`},
		{"terminal", true, `^\{\n  "version": "[^"\n]+",\n  "commit": "[^"\n]+"\n\}\n$`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cliframe.IsTerminal = func(io.Writer) bool { return tc.tty }
			var out, errb bytes.Buffer
			code := runEnv([]string{"version", "--json"}, func(string) string { return "" }, &out, &errb)
			if code != 0 || errb.Len() != 0 || !regexp.MustCompile(tc.re).MatchString(out.String()) {
				t.Errorf("code %d, stdout %q, stderr %q", code, out.String(), errb.String())
			}
		})
	}
}
