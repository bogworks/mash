package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
)

func TestLabelUnlabelRoundTripKeepsComments(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
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
	h := newFleetEnv(t, addPolicy)
	orig, _ := os.ReadFile(h.policy)
	out, errb, code := h.run("label", "b", "net=green", "--dry-run")
	if code != 0 || !strings.Contains(out, "dry run:") || strings.Contains(out, "needed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if got, _ := os.ReadFile(h.policy); !bytes.Equal(got, orig) {
		t.Error("dry run wrote the policy")
	}
}

func TestUnlabelUnsetWarns(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
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
	h := newFleetEnv(t, addPolicy)
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

// Labels no longer change anything an endpoint's credentials carry (ADR 0091): no verb says a reissue or an account
// update is needed.
func TestLabelConsequences(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	out, errb, code := h.run("label", "b", "kind=tool")
	if code != 0 || strings.Contains(out, "needed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	out, errb, code = h.run("label", "a", "net=blue")
	if code != 0 || strings.Contains(out, "needed") || strings.Contains(out, "set-zone") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	// the zone label is refused in a file (ADR 0091), by the verb too
	if _, errb, code = h.run("label", "a2", "zone=green"); code != 1 || !strings.Contains(errb, "ADR 0091") {
		t.Fatalf("zone label: %d %q", code, errb)
	}
	// a label no rule reads changes nothing to do
	out, _, code = h.run("label", "b", "note=hello")
	if code != 0 || strings.Contains(out, "needed") || strings.Contains(out, "moves") ||
		!strings.Contains(out, "directory labels not updated: run mashctl set-meta b --label note=hello (or re-run with --sync) to refresh who --long\n") {
		t.Fatalf("%d %q", code, out)
	}
	out, _, code = h.run("unlabel", "b", "note")
	if code != 0 || !strings.Contains(out, "directory labels not updated: run mashctl set-meta b --label note=- (or re-run with --sync) to refresh who --long\n") {
		t.Fatalf("%d %q", code, out)
	}
}

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

func TestLabelSyncUpdatesAndClearsRecord(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	h.provisionDeclared("b")
	out, errb, code := h.run("label", "b", "note=hello", "--sync")
	if code != 0 || !strings.Contains(out, "labeled b: note=hello\n") || !strings.Contains(out, "directory labels updated for b\n") ||
		strings.Contains(out, "directory labels not updated") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if l, _ := h.recordLabels("b"); l["note"] != "hello" {
		t.Errorf("record labels %v", l)
	}
	out, errb, code = h.run("unlabel", "b", "note", "--sync")
	if code != 0 || !strings.Contains(out, "unlabeled b: -note\n") || !strings.Contains(out, "directory labels updated for b\n") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if l, _ := h.recordLabels("b"); len(l) != 0 {
		t.Errorf("record labels %v", l)
	}
	// the re-run the follow-up names: the file already says it, --sync still reconciles the record
	h.run("label", "b", "note=hello")
	out, _, code = h.run("label", "b", "note=hello", "--sync")
	if code != 0 || !strings.Contains(out, "nothing to change") || !strings.Contains(out, "directory labels updated for b\n") {
		t.Fatalf("%d %q", code, out)
	}
	if l, _ := h.recordLabels("b"); l["note"] != "hello" {
		t.Errorf("record labels %v", l)
	}
	noSecrets(t, out, errb)
}

func TestLabelSyncWithNoRecord(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	out, errb, code := h.run("label", "b", "note=hello", "--sync")
	if code != 0 || !strings.Contains(out, "b has no directory record yet; labels will be set when it is added\n") || errb != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if !strings.Contains(h.policyText(), "note: hello") {
		t.Error("policy not saved")
	}
}

// With a server configured that cannot be reached, label refuses before it edits the file: it cannot tell whether the
// account is adopted (REQ-016, ADR 0111). The edit and the directory step are never split by an unreachable server.
func TestLabelSyncWithAnUnreachableServerRefusesFirst(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	orig := h.policyText()
	h.env["MASH_URL"] = "nats://127.0.0.1:1" // nothing listens
	for _, args := range [][]string{{"label", "b", "note=hello", "--sync"}, {"unlabel", "b", "note", "--sync"}, {"label", "b", "note=hello", "--dry-run", "--sync"}} {
		out, errb, code := h.run(args...)
		if code != 1 || !strings.Contains(errb, "is refused: cannot tell whether this account is adopted") || !strings.Contains(errb, "nothing was changed") || out != "" {
			t.Fatalf("%v: %d %q %q", args, code, out, errb)
		}
		noSecrets(t, out, errb)
	}
	if h.policyText() != orig {
		t.Error("a refused label wrote the policy")
	}
}

func TestLabelSurfacesConcurrentEdit(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
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
