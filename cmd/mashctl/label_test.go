package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
)

// recordLabels reads a directory record's labels from the server.
func (h *addEnv) recordLabels(name string) (map[string]string, bool) {
	h.t.Helper()
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	recs, err := adm.List(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	for _, r := range recs {
		if r.Name == name {
			return r.Labels, true
		}
	}
	return nil, false
}

// `label` sets labels on the directory record and `unlabel` clears them; no file is read or written.
func TestLabelSetsAndClearsRecordLabels(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	filesBefore := dirNames(t, h.dir)
	out, errb, code := h.run("label", "b", "team=x", "kind=tool")
	if code != 0 || out != "labeled b: kind=tool team=x\n" {
		t.Fatalf("label: %d %q %q", code, out, errb)
	}
	if l, _ := h.recordLabels("b"); l["team"] != "x" || l["kind"] != "tool" || len(l) != 2 {
		t.Errorf("record labels %v", l)
	}
	// changing a value, setting another and the same value again (no change)
	if out, _, code = h.run("label", "b", "team=y", "kind=tool"); code != 0 || out != "labeled b: kind=tool team=y\n" {
		t.Fatalf("relabel: %d %q", code, out)
	}
	if l, _ := h.recordLabels("b"); l["team"] != "y" || l["kind"] != "tool" {
		t.Errorf("record labels %v", l)
	}
	if out, _, code = h.run("label", "b", "team=y"); code != 0 || out != "b: nothing to change\n" {
		t.Fatalf("same value: %d %q", code, out)
	}
	out, errb, code = h.run("unlabel", "b", "team", "kind")
	if code != 0 || out != "unlabeled b: -kind -team\n" || errb != "" {
		t.Fatalf("unlabel: %d %q %q", code, out, errb)
	}
	if l, _ := h.recordLabels("b"); len(l) != 0 {
		t.Errorf("record labels %v", l)
	}
	noSecrets(t, out, errb)
	if got := dirNames(t, h.dir); !slices.Equal(got, filesBefore) {
		t.Errorf("label wrote files in the config dir: %v", got)
	}
}

func TestLabelDryRunWritesNothing(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	out, errb, code := h.run("label", "b", "net=green", "--dry-run")
	if code != 0 || !strings.Contains(out, "dry run: would have labeled b") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if l, _ := h.recordLabels("b"); len(l) != 0 {
		t.Errorf("dry run changed the record: %v", l)
	}
	noSecrets(t, out, errb)
}

func TestUnlabelUnsetWarns(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	out, errb, code := h.run("unlabel", "b", "team")
	if code != 0 || !strings.Contains(errb, "label team is not set") || !strings.Contains(out, "nothing to change") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

func TestLabelRefusals(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"label", "nobody", "k=v"}, 1, "no directory record"},
		{[]string{"unlabel", "nobody", "k"}, 1, "no directory record"},
		{[]string{"label", "a", "Bad=v"}, 2, "label key"},
		{[]string{"label", "a", "k"}, 2, "k=v"},
		{[]string{"label", "a", "k="}, 2, "k=v"},
		{[]string{"label", "a", "k=1", "k=2"}, 2, "twice"},
		{[]string{"unlabel", "a", "team=x"}, 2, "keys"},
		{[]string{"label", "a"}, 2, "NAME and at least one"},
		{[]string{"label", "a", "k=v", "--bogus"}, 2, "unknown option"},
		{[]string{"label", "a", "k=v", "--sync"}, 2, "unknown option"},
		{[]string{"label", "a", "k=" + strings.Repeat("v", 300)}, 1, "label k"},
	} {
		_, errb, code := h.run(c.args...)
		if code != c.code || !strings.Contains(errb, c.want) {
			t.Errorf("%v: %d %q, want %d %q", c.args, code, errb, c.code, c.want)
		}
	}
	if l, _ := h.recordLabels("a"); len(l) != 0 {
		t.Errorf("refusals changed the record: %v", l)
	}
}

// Labels no longer change anything an endpoint's credentials carry (ADR 0091): no verb says a reissue or an account
// update is needed.
func TestLabelConsequences(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	h.provisionDeclared("b")
	for _, args := range [][]string{{"label", "b", "kind=tool"}, {"label", "a", "net=blue"}, {"unlabel", "b", "kind"}} {
		out, errb, code := h.run(args...)
		if code != 0 || strings.Contains(out, "needed") || strings.Contains(out, "set-zone") || strings.Contains(out, "reissue") {
			t.Fatalf("%v: %d %q %q", args, code, out, errb)
		}
	}
}

// A server that cannot be reached is an error that names nothing changed.
func TestLabelWithoutAServer(t *testing.T) {
	h := newFleetEnv(t)
	h.env["MASH_URL"] = "nats://127.0.0.1:1" // nothing listens
	out, errb, code := h.run("label", "b", "note=hello")
	if code != 1 || !strings.Contains(errb, "label is refused: cannot tell whether this account is adopted") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
}
