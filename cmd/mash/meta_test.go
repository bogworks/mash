package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

func (w *world) admin(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := adminCmd(config.Config{InsecureLoopback: true, URL: w.e.URL, Owner: w.e.Owner, AdminCreds: w.e.AdminCreds}, args, &out, &errb)
	return out.String() + strings.TrimPrefix(errb.String(), adminDeprecation+"\n"), code // the notice has its own test
}

// whoLine is name's line from out.
func whoLine(t *testing.T, out, name string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, tTo(name)+"\t") {
			return l
		}
	}
	t.Fatalf("no line for %s in %q", name, out)
	return ""
}

func TestAdminProvisionAndSetMetaCLI(t *testing.T) {
	w := newWorld(t, "a")
	_, u := w.e.Mint(t, tAddr("c"))
	if out, code := w.admin(t, "provision", tTo("c"), "--user", u, "--account", w.e.Account, "--note", "the note",
		"--role", "reviewer", "--host", "laptop", "--label", "tier=1", "--label", "team=acme"); code != 0 {
		t.Fatalf("provision: %d %s", code, out)
	}
	// Default who: the exact line a launcher parses, with no new columns.
	out, _ := w.cli(t, "a", "who")
	if got := whoLine(t, out, "c"); got != tTo("c")+"\toffline\tthe note" {
		t.Fatalf("default who line changed: %q", got)
	}
	// admin list: the existing columns, unchanged.
	lout, code := w.admin(t, "list")
	if code != 0 || len(strings.Split(whoLine(t, lout, "c"), "\t")) != 4 {
		t.Fatalf("list: %d %q", code, lout)
	}
	out, _ = w.cli(t, "a", "who", "--long")
	if got := whoLine(t, out, "c"); got != tTo("c")+"\toffline\tthe note\tagent claude-code\trole reviewer\thost laptop\tlabel.team=acme\tlabel.tier=1" {
		t.Fatalf("who --long: %q", got)
	}
	if got := whoLine(t, out, "a"); got != tTo("a")+"\toffline\tagent claude-code" {
		t.Fatalf("who --long without metadata: %q", got)
	}

	// set-meta: only the given fields change; - clears.
	out, code = w.admin(t, "set-meta", tTo("c"), "--role", "builder", "--host", "-", "--label", "tier=-", "--label", "env=prod")
	if code != 0 || !strings.Contains(out, `updated o.dev.c: role "builder", host "", labels env=prod team=acme`) {
		t.Fatalf("set-meta: %d %s", code, out)
	}
	out, _ = w.cli(t, "a", "who", "--long")
	if got := whoLine(t, out, "c"); got != tTo("c")+"\toffline\tthe note\tagent claude-code\trole builder\tlabel.env=prod\tlabel.team=acme" {
		t.Fatalf("after set-meta: %q", got)
	}
	for _, bad := range [][]string{{"set-meta", tTo("c")}, {"set-meta", tTo("c"), "--role"}, {"set-meta", tTo("c"), "--bogus", "x"}, {"set-meta", tTo("c"), "--label", "novalue"}, {"set-meta", tTo("c"), "--label", "Bad=x"},
		{"set-meta", tTo("c"), "--label", "a=1", "--label", "a=2"}, {"provision", "d", "--label", "k=-"},
		{"set-meta", tTo("c"), "--role", "a\tb"}, {"set-meta", "ghost", "--role", "x"}} {
		if out, code := w.admin(t, bad...); code == 0 {
			t.Errorf("%v succeeded: %s", bad, out)
		}
	}
	if out, _ := w.admin(t, "set-meta", tTo("ghost"), "--role", "x"); !strings.Contains(out, "not provisioned") {
		t.Errorf("missing endpoint message: %s", out)
	}
	if out, code := w.admin(t, "provision", tTo("d"), "--user", u, "--account", w.e.Account, "--role", "a\nb"); code == 0 {
		t.Errorf("provision accepted a newline: %s", out)
	}
	if out, code := w.admin(t, "deprovision", tTo("c")); code != 0 {
		t.Fatalf("deprovision: %d %s", code, out)
	}
	if out, _ := w.cli(t, "a", "who"); strings.Contains(out, "c\t") {
		t.Fatalf("c still listed: %s", out)
	}
}

func TestWhoLongAndMCPWhoShowMetaAndLease(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.admin(t, "set-meta", tTo("a"), "--role", "chief", "--label", "team=h1", "--host", "elsewhere-box"); code != 0 {
		t.Fatalf("set-meta: %d %s", code, out)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "b", "who"); return strings.Contains(out, tTo("a")+"\tonline") })

	// Default who from another endpoint: unchanged shape.
	out, _ := w.cli(t, "b", "who")
	if got := whoLine(t, out, "a"); !strings.HasPrefix(got, tTo("a")+"\tonline\tseen ") || strings.Contains(got, "role") {
		t.Fatalf("default who: %q", got)
	}
	// --long as a: directory metadata plus a's own live lease, which differs from the directory host.
	out, _ = w.cli(t, "a", "who", "--long")
	got := whoLine(t, out, "a")
	for _, want := range []string{"\trole chief", "\tlabel.team=h1", "\thost elsewhere-box", "\tlease host ", "\tlease version ", "differs from directory host elsewhere-box"} {
		if !strings.Contains(got, want) {
			t.Errorf("who --long a lacks %q: %q", want, got)
		}
	}
	// The MCP tool shows the same.
	txt := resultText(s.call(t, 20, "who", `{}`))
	got = whoLine(t, txt, "a")
	for _, want := range []string{tTo("a") + "\tonline", "\trole chief", "\tlabel.team=h1", "\thost elsewhere-box", "\tlease host "} {
		if !strings.Contains(got, want) {
			t.Errorf("mcp who a lacks %q: %q", want, got)
		}
	}
	if got := whoLine(t, txt, "b"); !strings.HasPrefix(got, tTo("b")+"\toffline") {
		t.Errorf("mcp who b: %q", got)
	}
}

func TestAdminLimitsCLI(t *testing.T) {
	w := newWorld(t, "a")
	run := func(l store.Limits, args ...string) (string, int) {
		var out, errb bytes.Buffer
		code := adminCmd(config.Config{InsecureLoopback: true, URL: w.e.URL, Owner: w.e.Owner, AdminCreds: w.e.AdminCreds, Limits: l}, args, &out, &errb)
		return out.String() + strings.TrimPrefix(errb.String(), adminDeprecation+"\n"), code
	}
	// --show needs no server and prints the defaults for a Config with no limits.
	const defaults = "inbox_max_bytes 16MiB\ninbox_max_age 30d\ndirectory_max_bytes 16MiB\nlease_max_bytes 4MiB\ninbox_max_consumers -1\n"
	if out, code := run(store.Limits{}, "limits", "--show"); code != 0 || out != defaults {
		t.Fatalf("show: %d %q", code, out)
	}
	custom := store.Limits{InboxMaxBytes: 64 << 20, InboxMaxAge: 36 * time.Hour, DirectoryMaxBytes: 2 << 20, LeaseMaxBytes: 5 << 19, InboxMaxConsumers: 3}
	if out, code := run(custom, "limits", "--show"); code != 0 ||
		out != "inbox_max_bytes 64MiB\ninbox_max_age 36h0m0s\ndirectory_max_bytes 2MiB\nlease_max_bytes 2560KiB\ninbox_max_consumers 3\n" {
		t.Fatalf("show custom: %d %q", code, out)
	}
	// limits [name...] raises an existing inbox.
	_, u := w.e.Mint(t, tAddr("c"))
	if out, code := w.admin(t, "provision", tTo("c"), "--user", u, "--account", w.e.Account); code != 0 {
		t.Fatalf("provision: %d %s", code, out)
	}
	up := store.DefaultLimits()
	up.InboxMaxBytes = 32 << 20
	if out, code := run(up, "limits", tTo("c")); code != 0 || !strings.Contains(out, "o_dev_c: max_bytes 16777216 -> 33554432") {
		t.Fatalf("limits: %d %q", code, out)
	}
	if out, code := run(up, "limits", tTo("c")); code != 0 || strings.TrimSpace(out) != "o_dev_c: unchanged" {
		t.Fatalf("again: %d %q", code, out)
	}
	if out, code := run(store.DefaultLimits(), "limits", tTo("c")); code != 1 || !strings.Contains(out, "max_bytes") {
		t.Fatalf("lower: %d %q", code, out)
	}
}

func TestAdminDeprecatedAlias(t *testing.T) {
	w := newWorld(t, "a")
	var out, errb bytes.Buffer
	code := adminCmd(config.Config{InsecureLoopback: true, URL: w.e.URL, Owner: w.e.Owner, AdminCreds: w.e.AdminCreds}, []string{"list"}, &out, &errb)
	if code != 0 || !strings.HasPrefix(out.String(), tTo("a")+"\t") {
		t.Fatalf("list: %d %q %q", code, out.String(), errb.String())
	}
	if got, want := errb.String(), "mash admin is deprecated and will be removed; use mashctl (same verbs, e.g. mashctl list)\n"; got != want {
		t.Fatalf("stderr %q, want %q", got, want)
	}
	out.Reset()
	errb.Reset()
	if code := adminCmd(config.Config{}, nil, &out, &errb); code != 2 || strings.Contains(errb.String(), "deprecated and will") {
		t.Fatalf("no args: %d %q", code, errb.String())
	}
}
