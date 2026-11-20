package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// recordOf reads a directory record from the server.
func (h *fleetEnv) recordOf(name string) store.Record {
	h.t.Helper()
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	recs, _, err := adm.List(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	r, ok := findRecord(recs, ad(name))
	if !ok {
		h.t.Fatalf("no record for %s", name)
	}
	return r
}

// recordLabels reads a directory record's labels from the server.
func (h *fleetEnv) recordLabels(name string) (map[string]string, bool) {
	h.t.Helper()
	r := h.recordOf(name)
	return r.Labels, true
}

// `set` takes several pairs at once and changes only those fields.
func TestEndpointSetSeveralPairs(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	filesBefore := dirNames(t, h.dir)
	out, errb, code := h.run("endpoint", "set", "o.g.b", "role=reviewer", "host=laptop", "description=checks the diffs", "note=first pass", "capability=review,lint", "label.team=blue", "label.kind=tool")
	if code != 0 || out != "Updated endpoint o.g.b\n" || errb != "" {
		t.Fatalf("set: %d %q %q", code, out, errb)
	}
	r := h.recordOf("b")
	if r.Role != "reviewer" || r.Host != "laptop" || r.Description != "checks the diffs" || r.Note != "first pass" || !slices.Equal(r.Caps, []string{"lint", "review"}) ||
		r.Labels["team"] != "blue" || r.Labels["kind"] != "tool" || len(r.Labels) != 2 {
		t.Errorf("record %+v", r)
	}
	// another set changes only what it names: the rest stays
	if out, _, code = h.run("endpoint", "set", "o.g.b", "role=builder", "label.team=red", "capability=review,deploy"); code != 0 || out != "Updated endpoint o.g.b\n" {
		t.Fatalf("set again: %d %q", code, out)
	}
	r = h.recordOf("b")
	if r.Role != "builder" || r.Host != "laptop" || r.Note != "first pass" || r.Labels["team"] != "red" || r.Labels["kind"] != "tool" || !slices.Equal(r.Caps, []string{"deploy", "review"}) {
		t.Errorf("record %+v: capability= replaces the list", r)
	}
	// the same list in another order is no change
	if out, _, code = h.run("endpoint", "set", "o.g.b", "capability=review,deploy"); code != 0 || out != "Nothing to change on endpoint o.g.b\n" {
		t.Fatalf("same list: %d %q", code, out)
	}
	// a capability given twice, or a bad name in the list, is refused
	for _, a := range []string{"capability=a,b", "capability=a,,b", "capability=A"} {
		_, errb2, code := h.run("endpoint", "set", "o.g.b", a, "capability=x")
		if code != 2 || !strings.HasPrefix(errb2, "error: ") {
			t.Errorf("%q twice: %d %q", a, code, errb2)
		}
	}
	if _, errb2, code := h.run("endpoint", "set", "o.g.b", "capability=a,,b"); code != 2 || !strings.HasPrefix(errb2, "error: capability needs names") {
		t.Errorf("empty name: %d %q", code, errb2)
	}
	// the same values again change nothing
	if out, _, code = h.run("endpoint", "set", "o.g.b", "role=builder", "label.team=red"); code != 0 || out != "Nothing to change on endpoint o.g.b\n" {
		t.Fatalf("same values: %d %q", code, out)
	}
	noSecrets(t, out, errb)
	if got := dirNames(t, h.dir); !slices.Equal(got, filesBefore) {
		t.Errorf("set wrote files in the config dir: %v", got)
	}
}

func TestEndpointUnsetClearsValues(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	if _, _, code := h.run("endpoint", "set", "o.g.b", "role=r", "host=h", "description=d", "note=n", "capability=c,d", "label.team=x", "label.kind=tool"); code != 0 {
		t.Fatal(code)
	}
	out, errb, code := h.run("endpoint", "unset", "o.g.b", "host", "label.team")
	if code != 0 || out != "Updated endpoint o.g.b\n" || errb != "" {
		t.Fatalf("unset: %d %q %q", code, out, errb)
	}
	r := h.recordOf("b")
	if r.Host != "" || r.Role != "r" || r.Description != "d" || r.Note != "n" || len(r.Caps) != 2 || r.Labels["team"] != "" || r.Labels["kind"] != "tool" {
		t.Errorf("record %+v", r)
	}
	if _, _, code := h.run("endpoint", "unset", "o.g.b", "role", "description", "note", "capability", "label.kind"); code != 0 {
		t.Fatal(code)
	}
	if r := h.recordOf("b"); r.Role != "" || r.Description != "" || r.Note != "" || len(r.Caps) != 0 || len(r.Labels) != 0 {
		t.Errorf("record %+v", r)
	}
}

func TestEndpointUnsetUnsetLabelWarns(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	out, errb, code := h.run("endpoint", "unset", "o.g.b", "label.team")
	if code != 0 || !strings.Contains(errb, "label.team is not set") || out != "Nothing to change on endpoint o.g.b\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	// a field that is already empty is not a change
	if out, _, code := h.run("endpoint", "unset", "o.g.b", "host", "role"); code != 0 || out != "Nothing to change on endpoint o.g.b\n" {
		t.Fatalf("%d %q", code, out)
	}
}

func TestEndpointSetDryRunChangesNothing(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	before := h.recordOf("b")
	out, errb, code := h.run("endpoint", "set", "o.g.b", "label.net=green", "role=x", "--dry-run")
	if code != 0 || out != "Would update endpoint o.g.b: label.net=green, role=x\nNothing was changed.\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	out, _, code = h.run("endpoint", "unset", "o.g.b", "role", "--dry-run")
	if code != 0 || out != "Nothing to change on endpoint o.g.b\n" {
		t.Fatalf("unset dry run with nothing to unset: %d %q", code, out)
	}
	if after := h.recordOf("b"); !equalRecords(before, after) {
		t.Errorf("dry run changed the record: %+v -> %+v", before, after)
	}
	// with something to remove, the dry run says it and still removes nothing
	if _, _, code := h.run("endpoint", "set", "o.g.b", "role=keep"); code != 0 {
		t.Fatal(code)
	}
	out, _, code = h.run("endpoint", "unset", "o.g.b", "role", "--dry-run")
	if code != 0 || out != "Would update endpoint o.g.b: role\nNothing was changed.\n" || h.recordOf("b").Role != "keep" {
		t.Fatalf("unset dry run: %d %q", code, out)
	}
	out, _, code = h.run("endpoint", "set", "o.g.b", "role=z", "--dry-run", "--json")
	var j struct {
		Address string
		Set     map[string]any
		DryRun  bool `json:"dry_run"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &j) != nil || j.Address != "o.g.b" || j.Set["role"] != "z" || !j.DryRun || h.recordOf("b").Role != "keep" {
		t.Fatalf("set --dry-run --json: %d %q", code, out)
	}
	noSecrets(t, out, errb)
}

func equalRecords(a, b store.Record) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func TestEndpointSetUnsetJSON(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	out, errb, code := h.run("endpoint", "set", "o.g.b", "role=reviewer", "capability=b,a", "label.team=blue", "--json")
	if code != 0 || errb != "" || out != "{\n  \"address\": \"o.g.b\",\n  \"set\": {\n    \"capability\": [\n      \"a\",\n      \"b\"\n    ],\n    \"label.team\": \"blue\",\n    \"role\": \"reviewer\"\n  }\n}\n" {
		t.Fatalf("set --json: %d %q %q", code, out, errb)
	}
	out, errb, code = h.run("endpoint", "unset", "o.g.b", "label.team", "--json")
	if code != 0 || errb != "" || out != "{\n  \"address\": \"o.g.b\",\n  \"unset\": [\n    \"label.team\"\n  ]\n}\n" {
		t.Fatalf("unset --json: %d %q %q", code, out, errb)
	}
	// a refusal in JSON mode is one JSON line on stdout and nothing on stderr
	out, errb, code = h.run("endpoint", "set", "o.g.b", "bogus=1", "--json")
	if code != 2 || errb != "" || !strings.HasPrefix(out, `{"error":"argument 2 has a key that cannot be set`) {
		t.Fatalf("bad key --json: %d %q %q", code, out, errb)
	}
}

func TestEndpointSetUnsetRefusals(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	before := h.recordOf("a")
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"set", "o.g.nobody", "role=v"}, 1, "no endpoint o.g.nobody"},
		{[]string{"unset", "o.g.nobody", "role"}, 1, "no endpoint o.g.nobody"},
		{[]string{"set", "o.g.a", "bogus=v"}, 2, "key that cannot be set"},
		{[]string{"set", "o.g.a", "label=v"}, 2, "key that cannot be set"},
		{[]string{"set", "o.g.a", "cap=v"}, 2, "key that cannot be set"},
		{[]string{"set", "o.g.a", "label.Bad=v"}, 2, "a letter, then up to 31"},
		{[]string{"set", "o.g.a", "role"}, 2, "not key=value"},
		{[]string{"set", "o.g.a", "role="}, 2, "role has no value"},
		{[]string{"set", "o.g.a", "label.k=1", "label.k=2"}, 2, "given twice"},
		{[]string{"set", "o.g.a", "role=1", "role=2"}, 2, "given twice"},
		{[]string{"unset", "o.g.a", "role=x"}, 2, "keys, not key=value"},
		{[]string{"unset", "o.g.a", "bogus"}, 2, "not a key of the record"},
		{[]string{"unset", "o.g.a", "host", "host"}, 2, "given twice"},
		{[]string{"set", "o.g.a"}, 2, "missing the endpoint address and at least one key=value"},
		{[]string{"unset", "o.g.a"}, 2, "missing the endpoint address and at least one key"},
		{[]string{"set", "o.g.a", "role=v", "--bogus"}, 2, `unknown option --bogus`},
		{[]string{"set", "o.g.a", "role=v", "--sync"}, 2, `unknown option --sync`},
		{[]string{"set", "a", "role=v"}, 2, "write owner.group.name"},
		{[]string{"set", "o_g_a", "role=v"}, 2, "write owner.group.name"},
		{[]string{"set", "o/g/a", "role=v"}, 2, "write owner.group.name"},
		{[]string{"set", "o.g.a", "label.k=" + strings.Repeat("v", 300)}, 1, "label k"},
		{[]string{"set", "o.g.a", "role=a\tb"}, 1, ""},
	} {
		out, errb, code := h.run(append([]string{"endpoint"}, c.args...)...)
		if code != c.code || out != "" || !strings.HasPrefix(errb, "error: ") || !strings.Contains(errb, c.want) {
			t.Errorf("%v: %d %q %q, want %d %q", c.args, code, out, errb, c.code, c.want)
		}
	}
	if after := h.recordOf("a"); !equalRecords(before, after) {
		t.Errorf("refusals changed the record: %+v -> %+v", before, after)
	}
}

// The old verbs are gone: they are unknown commands.
func TestOldMetaVerbsAreUnknown(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	for _, args := range [][]string{{"set-meta", "o.g.a", "--role", "x"}, {"label", "o.g.a", "k=v"}, {"unlabel", "o.g.a", "k"}, {"suspend", "o.g.a"}, {"resume", "o.g.a"},
		{"suspended", "o.g.a"}, {"permissions", "o.g.a"}, {"inbox", "inspect", "o.g.a"}, {"list"}, {"show", "o.g.a"}} {
		if out, _, code := h.run(args...); code != 2 || out != "" {
			t.Errorf("%v: %d %q, want exit 2 and no output", args, code, out)
		}
	}
	if r := h.recordOf("a"); r.Role != "" || len(r.Labels) != 0 {
		t.Errorf("an old verb changed the record: %+v", r)
	}
}

// Labels no longer change anything an endpoint's credentials carry (ADR 0091): no command says a reissue or an account
// update is needed.
func TestEndpointSetConsequences(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	h.provisionDeclared("b")
	for _, args := range [][]string{{"set", "o.g.b", "label.kind=tool"}, {"set", "o.g.a", "label.net=blue"}, {"unset", "o.g.b", "label.kind"}} {
		out, errb, code := h.run(append([]string{"endpoint"}, args...)...)
		if code != 0 || strings.Contains(out, "needed") || strings.Contains(out, "reissue") {
			t.Fatalf("%v: %d %q %q", args, code, out, errb)
		}
	}
}

// A server that cannot be reached is an error that names nothing changed.
func TestEndpointSetWithoutAServer(t *testing.T) {
	h := newFleetEnv(t)
	h.env["MASH_SERVER"] = "nats://127.0.0.1:1" // nothing listens
	out, errb, code := h.run("endpoint", "set", "o.g.b", "label.note=hello")
	if code != 1 || !strings.Contains(errb, "no servers available") || !strings.Contains(errb, "check MASH_SERVER and that at least one listed server is up") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
}
