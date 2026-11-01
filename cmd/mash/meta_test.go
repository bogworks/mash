package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
)

func (w *world) admin(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := adminCmd(config.Config{URL: w.e.URL, Owner: w.e.Owner, AdminCreds: w.e.AdminCreds}, args, &out, &errb)
	return out.String() + errb.String(), code
}

// whoLine is name's line from out.
func whoLine(t *testing.T, out, name string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, name+"\t") {
			return l
		}
	}
	t.Fatalf("no line for %s in %q", name, out)
	return ""
}

func TestAdminProvisionAndSetMetaCLI(t *testing.T) {
	w := newWorld(t, "a")
	_, u := w.e.Mint(t, "c")
	if out, code := w.admin(t, "provision", "c", "--user", u, "--account", w.e.Account, "--note", "the note",
		"--role", "reviewer", "--host", "laptop", "--label", "tier=1", "--label", "team=acme"); code != 0 {
		t.Fatalf("provision: %d %s", code, out)
	}
	// Default who: the exact line a launcher parses, with no new columns.
	out, _ := w.cli(t, "a", "who")
	if got := whoLine(t, out, "c"); got != "c\toffline\tthe note" {
		t.Fatalf("default who line changed: %q", got)
	}
	// admin list: the existing columns, unchanged.
	lout, code := w.admin(t, "list")
	if code != 0 || len(strings.Split(whoLine(t, lout, "c"), "\t")) != 4 {
		t.Fatalf("list: %d %q", code, lout)
	}
	out, _ = w.cli(t, "a", "who", "--long")
	if got := whoLine(t, out, "c"); got != "c\toffline\tthe note\trole reviewer\thost laptop\tlabel.team=acme\tlabel.tier=1" {
		t.Fatalf("who --long: %q", got)
	}
	if got := whoLine(t, out, "a"); got != "a\toffline" {
		t.Fatalf("who --long without metadata: %q", got)
	}

	// set-meta: only the given fields change; - clears.
	out, code = w.admin(t, "set-meta", "c", "--role", "builder", "--host", "-", "--label", "tier=-", "--label", "env=prod")
	if code != 0 || !strings.Contains(out, `updated c: role "builder", host "", labels env=prod team=acme`) {
		t.Fatalf("set-meta: %d %s", code, out)
	}
	out, _ = w.cli(t, "a", "who", "--long")
	if got := whoLine(t, out, "c"); got != "c\toffline\tthe note\trole builder\tlabel.env=prod\tlabel.team=acme" {
		t.Fatalf("after set-meta: %q", got)
	}
	for _, bad := range [][]string{{"set-meta", "c"}, {"set-meta", "c", "--role"}, {"set-meta", "c", "--bogus", "x"}, {"set-meta", "c", "--label", "novalue"}, {"set-meta", "c", "--label", "Bad=x"},
		{"set-meta", "c", "--label", "a=1", "--label", "a=2"}, {"provision", "d", "--label", "k=-"},
		{"set-meta", "c", "--role", "a\tb"}, {"set-meta", "ghost", "--role", "x"}} {
		if out, code := w.admin(t, bad...); code == 0 {
			t.Errorf("%v succeeded: %s", bad, out)
		}
	}
	if out, _ := w.admin(t, "set-meta", "ghost", "--role", "x"); !strings.Contains(out, "not provisioned") {
		t.Errorf("missing endpoint message: %s", out)
	}
	if out, code := w.admin(t, "provision", "d", "--user", u, "--account", w.e.Account, "--role", "a\nb"); code == 0 {
		t.Errorf("provision accepted a newline: %s", out)
	}
	if out, code := w.admin(t, "deprovision", "c"); code != 0 {
		t.Fatalf("deprovision: %d %s", code, out)
	}
	if out, _ := w.cli(t, "a", "who"); strings.Contains(out, "c\t") {
		t.Fatalf("c still listed: %s", out)
	}
}

func TestWhoLongAndMCPWhoShowMetaAndLease(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.admin(t, "set-meta", "a", "--role", "lead", "--label", "team=h1", "--host", "elsewhere-box"); code != 0 {
		t.Fatalf("set-meta: %d %s", code, out)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "b", "who"); return strings.Contains(out, "a\tonline") })

	// Default who from another endpoint: unchanged shape.
	out, _ := w.cli(t, "b", "who")
	if got := whoLine(t, out, "a"); !strings.HasPrefix(got, "a\tonline\tseen ") || strings.Contains(got, "role") {
		t.Fatalf("default who: %q", got)
	}
	// --long as a: directory metadata plus a's own live lease, which differs from the directory host.
	out, _ = w.cli(t, "a", "who", "--long")
	got := whoLine(t, out, "a")
	for _, want := range []string{"\trole lead", "\tlabel.team=h1", "\thost elsewhere-box", "\tlease host ", "\tlease version ", "differs from directory host elsewhere-box"} {
		if !strings.Contains(got, want) {
			t.Errorf("who --long a lacks %q: %q", want, got)
		}
	}
	// The MCP tool shows the same.
	txt := resultText(s.call(t, 20, "who", `{}`))
	got = whoLine(t, txt, "a")
	for _, want := range []string{"a\tonline", "\trole lead", "\tlabel.team=h1", "\thost elsewhere-box", "\tlease host "} {
		if !strings.Contains(got, want) {
			t.Errorf("mcp who a lacks %q: %q", want, got)
		}
	}
	if got := whoLine(t, txt, "b"); got != "b\toffline" && !strings.HasPrefix(got, "b\t") {
		t.Errorf("mcp who b: %q", got)
	}
}
