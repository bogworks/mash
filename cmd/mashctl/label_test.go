package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestLabelUnlabelRoundTripKeepsComments(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	orig, _ := os.ReadFile(h.policy)
	out, errb, code := h.run("label", "a2", "team=x", "kind=tool", "--policy", h.policy)
	if code != 0 || !strings.HasPrefix(out, "labeled a2: kind=tool team=x\n") {
		t.Fatalf("label: %d %q %q", code, out, errb)
	}
	got, _ := os.ReadFile(h.policy)
	for _, want := range []string{"# the fleet", "# a keeps its comment", "# team x may mail any tool", "kind: tool"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("policy lacks %q:\n%s", want, got)
		}
	}
	if len(h.records()) != 0 {
		t.Error("label touched the server")
	}
	out, errb, code = h.run("unlabel", "a2", "team", "kind")
	if code != 0 || !strings.HasPrefix(out, "unlabeled a2: -kind -team\n") || errb != "" {
		t.Fatalf("unlabel: %d %q %q", code, out, errb)
	}
	got, _ = os.ReadFile(h.policy)
	if !bytes.Equal(got, orig) {
		t.Errorf("round trip changed the file:\n%s\nwas:\n%s", got, orig)
	}
	noSecrets(t, out, errb)
}

func TestLabelDryRunWritesNothing(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	orig, _ := os.ReadFile(h.policy)
	out, errb, code := h.run("label", "b", "zone=green", "--dry-run")
	if code != 0 || !strings.Contains(out, "dry run:") || !strings.Contains(out, "b moves from zone blue to zone green") ||
		!strings.Contains(out, "account update needed: run mashctl apply") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if got, _ := os.ReadFile(h.policy); !bytes.Equal(got, orig) {
		t.Error("dry run wrote the policy")
	}
}

func TestUnlabelUnsetWarns(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	orig, _ := os.ReadFile(h.policy)
	out, errb, code := h.run("unlabel", "b", "team")
	if code != 0 || !strings.Contains(errb, "label team is not set") || strings.Contains(out, "needed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if got, _ := os.ReadFile(h.policy); !bytes.Equal(got, orig) {
		t.Error("policy changed")
	}
}

func TestLabelRefusals(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	orig, _ := os.ReadFile(h.policy)
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"label", "nobody", "k=v"}, 1, "not declared"},
		{[]string{"label", "a", "Bad=v"}, 2, "label key"},
		{[]string{"label", "a", "k"}, 2, "k=v"},
		{[]string{"label", "a", "k="}, 2, "k=v"},
		{[]string{"label", "a", "k=1", "k=2"}, 2, "twice"},
		{[]string{"unlabel", "a", "team=x"}, 2, "keys"},
		{[]string{"label", "a"}, 2, "NAME and at least one"},
		{[]string{"label", "a", "k=v", "--bogus"}, 2, "unknown option"},
		{[]string{"label", "a", "k=" + strings.Repeat("v", 300)}, 1, "label k"},
	} {
		_, errb, code := h.run(c.args...)
		if code != c.code || !strings.Contains(errb, c.want) {
			t.Errorf("%v: %d %q, want %d %q", c.args, code, errb, c.code, c.want)
		}
	}
	if got, _ := os.ReadFile(h.policy); !bytes.Equal(got, orig) {
		t.Error("policy changed by refusals")
	}
}

func TestLabelConsequences(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	// b becomes a tool: team x (a) now needs a tag for it
	out, errb, code := h.run("label", "b", "kind=tool")
	if code != 0 || !strings.Contains(out, "reissue needed: a (run: mashctl reissue a)") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	// a moves zones: the verbs, and the creds are signed by the old zone until reissued
	out, errb, code = h.run("label", "a", "zone=blue")
	if code != 0 || !strings.Contains(out, "a moves from zone red to zone blue: its mail address changes.") ||
		!strings.Contains(out, "mashctl set-zone a blue") || !strings.Contains(out, "mashctl reissue a") ||
		!strings.Contains(out, "creds are signed by zone red's key and keep zone red's permissions") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if strings.Contains(out, "reissue needed: a") {
		t.Errorf("the moved endpoint is named in the tag line: %q", out)
	}
	// a new zone changes the account JWT
	out, _, code = h.run("label", "a2", "zone=green")
	if code != 0 || !strings.Contains(out, "account update needed: run mashctl apply") {
		t.Fatalf("%d %q", code, out)
	}
	// unsetting the subject label moves the endpoint to the default zone
	out, _, code = h.run("unlabel", "a2", "zone")
	if code != 0 || !strings.Contains(out, "a2 moves from zone green to zone default") {
		t.Fatalf("%d %q", code, out)
	}
	// a label no rule reads changes nothing to do
	out, _, code = h.run("label", "b", "note=hello")
	if code != 0 || strings.Contains(out, "needed") || strings.Contains(out, "moves") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestLabelSurfacesConcurrentEdit(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	afterLabelLoad = func() {
		f, _ := os.OpenFile(h.policy, os.O_APPEND|os.O_WRONLY, 0)
		f.WriteString("# edited by hand\n")
		f.Close()
	}
	t.Cleanup(func() { afterLabelLoad = nil })
	out, errb, code := h.run("label", "b", "team=y")
	if code != 1 || !strings.Contains(errb, "changed since it was loaded") || !strings.Contains(errb, "Re-run: mashctl label b team=y") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	got, _ := os.ReadFile(h.policy)
	if strings.Contains(string(got), "team: \"y\"") || !strings.HasSuffix(string(got), "# edited by hand\n") {
		t.Errorf("policy:\n%s", got)
	}
}
