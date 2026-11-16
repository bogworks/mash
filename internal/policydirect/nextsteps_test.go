package policydirect

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
)

func TestNextStepsFromEveryListOfThePlan(t *testing.T) {
	p := &listset.Plan{
		FirstCredential: []string{"o.a"}, NewCredential: []string{"o.b"}, RotateAndRevoke: []string{"o.c"}, Revoke: []string{"o.d"},
	}
	got := nextSteps(p)
	type want struct{ sender, step, action, purpose string }
	wants := []want{
		{"o.a", StepFirstCredential, ActionIssue, "issue"},
		{"o.b", StepNewCredential, ActionIssue, "issue"},
		{"o.c", StepRotateAndRevoke, ActionIssueThenRevokeOld, "rotation"},
		{"o.d", StepRevoke, ActionRevoke, ""},
	}
	if len(got) != len(wants) {
		t.Fatalf("%d steps, want %d: %+v", len(got), len(wants), got)
	}
	for i, w := range wants {
		g := got[i]
		if g.Sender != w.sender || g.Address != w.sender || g.Step != w.step || g.Action != w.action || g.Purpose != w.purpose || g.Detail == "" {
			t.Errorf("step %d: %+v, want %+v", i, g, w)
		}
		if strings.Contains(strings.ToLower(g.Detail), "mashctl ") && !strings.Contains(g.Detail, "mashctl never") {
			t.Errorf("step %d names a verb: %q", i, g.Detail)
		}
	}
	if !strings.Contains(got[0].Detail, "a public key the endpoint made") {
		t.Errorf("first credential detail: %q", got[0].Detail)
	}
	if empty := nextSteps(&listset.Plan{}); empty == nil || len(empty) != 0 {
		t.Errorf("an empty plan gives %#v", empty)
	}
}

// inForceHarness holds the first generation, which lists the sender c.core.
func inForceHarness(t *testing.T) (*harness, []byte) {
	t.Helper()
	first := docJSON(1, 0, func(m map[string]any) {
		m["senders"] = map[string]any{"c.core": map[string]any{"allow": []string{}, "deny": []string{}}}
	})
	d, err := policydoc.Decode(first, policydoc.DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rec, _, err := listset.Accept(listset.Record{}, d.ListSet, first)
	if err != nil {
		t.Fatal(err)
	}
	ls := d.ListSet
	h := newHarness(t)
	h.ledger = &fakeLedger{MemLedger: policyapply.NewMemLedgerWith(rec, policyapply.InForceDetail{ListSet: &ls})}
	h.app.ledger = h.ledger
	return h, docJSON(2, 1, nil) // the second generation drops the sender
}

func TestDryRunAndRealRunCarryTheNextSteps(t *testing.T) {
	for _, dry := range []bool{true, false} {
		h, doc := inForceHarness(t)
		r := run(context.Background(), h.env(), doc, Options{DryRun: dry})
		if dry && r.Outcome != OutcomePlanned || !dry && r.Outcome != OutcomeApplied {
			t.Fatalf("dry=%v: %+v", dry, r)
		}
		if len(r.NextSteps) != 1 || r.NextSteps[0].Sender != "c.core" || r.NextSteps[0].Step != StepRevoke || r.NextSteps[0].Action != ActionRevoke {
			t.Fatalf("dry=%v next steps: %+v", dry, r.NextSteps)
		}
		b, err := json.Marshal(r)
		if err != nil || !strings.Contains(string(b), `"next_steps":[{"sender":"c.core","step":"revoke"`) {
			t.Errorf("dry=%v json %s, %v", dry, b, err)
		}
	}
}

func TestNextStepsAreEmptyNotNullWhenThereIsNone(t *testing.T) {
	r := run(context.Background(), newHarness(t).env(), []byte("nope"), Options{DryRun: true})
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"next_steps":[]`) {
		t.Errorf("json %s", b)
	}
	// A refused real run has no next steps.
	h, doc := inForceHarness(t)
	h.app.err = context.Canceled
	r = run(context.Background(), h.env(), doc, Options{})
	if r.Outcome == OutcomeApplied || len(r.NextSteps) != 0 {
		t.Errorf("a failed run: %+v", r)
	}
	if !slices.Equal(r.NextSteps, []NextStep{}) {
		t.Errorf("next steps %#v", r.NextSteps)
	}
}
