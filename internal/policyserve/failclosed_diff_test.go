package policyserve

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
)

// diffReader serves the list set in force (nil: nothing in force) for policydoc.Diff.
type diffReader struct {
	account string
	inForce []byte // a document
}

func (r diffReader) Account() string { return r.account }

func (r diffReader) InForceViewAndStatus(context.Context) (listset.Record, listset.ListSet, bool, listset.Record, bool, policyapply.Status, bool, error) {
	none := func(err error) (listset.Record, listset.ListSet, bool, listset.Record, bool, policyapply.Status, bool, error) {
		return listset.Record{}, listset.ListSet{}, false, listset.Record{}, false, policyapply.Status{}, false, err
	}
	if r.inForce == nil {
		return none(nil)
	}
	d, err := policydoc.Decode(r.inForce, policydoc.DecodeOptions{})
	if err != nil {
		return none(err)
	}
	c, err := listset.Compile(d.ListSet)
	if err != nil {
		return none(err)
	}
	return listset.Record{Generation: d.Generation, Signed: listset.DigestCompiled(c)}, d.ListSet, true,
		listset.Record{}, false, policyapply.Status{}, false, nil
}

func fcDoc(t *testing.T, gen, base int, senders map[string][]string) []byte {
	t.Helper()
	ss := map[string]any{}
	for s, allow := range senders {
		ss[s] = map[string]any{"allow": allow, "deny": []string{}}
	}
	b, err := json.Marshal(map[string]any{
		"schema": policydoc.DocumentSchema, "account": "A", "owner": "foo", "generation": gen, "base_generation": base,
		"issued_at": "2026-11-14T10:00:00Z",
		"groups":    map[string]any{"c": map[string]any{"mail": "closed", "prompt": "closed", "status": "shared", "heartbeat": "closed"}},
		"senders":   ss,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// diffWith runs policydoc.Diff with FailClosed bound to inboxes, as mashctl does.
func diffWith(t *testing.T, inForce, next []byte, inboxes InboxChecker) policydoc.DiffPlan {
	t.Helper()
	fc := func(ctx context.Context, prev, target *listset.Compiled) ([]listset.Refusal, error) {
		return FailClosed(ctx, prev, target, inboxes, true)
	}
	p, err := policydoc.Diff(context.Background(), diffReader{account: "A", inForce: inForce}, next, "foo", nil,
		&policydoc.RotationContext{Adopted: true}, fc)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func hasInbox(addrs ...string) *fakeInboxes {
	f := &fakeInboxes{answers: map[string]admin.InboxCheck{}}
	for _, a := range addrs {
		f.answers[a] = admin.InboxCheck{Stream: "inbox-" + a, Consumer: true}
	}
	return f
}

func onlyRefusal(t *testing.T, p policydoc.DiffPlan, stage string) listset.Refusal {
	t.Helper()
	var got []listset.Refusal
	for _, r := range p.Refusals {
		if r.Stage == stage {
			got = append(got, r)
		}
	}
	if !p.Refused || len(got) != 1 {
		t.Fatalf("want one %s refusal in a refused plan, got %+v", stage, p)
	}
	if !strings.Contains(policydoc.RenderPlan(p), "["+stage+"]") {
		t.Fatalf("the text does not show stage %s: %s", stage, policydoc.RenderPlan(p))
	}
	return got[0]
}

func TestDiffRefusesANarrowingChangeAtStageNarrowing(t *testing.T) {
	gen1 := fcDoc(t, 1, 0, map[string][]string{"foo.c.l1": {}, "foo.c.l2": {"foo.c.lead"}, "foo.c.lead": {}})
	gen2 := fcDoc(t, 2, 1, map[string][]string{"foo.c.l1": {}, "foo.c.l2": {}, "foo.c.lead": {}})
	p := diffWith(t, gen1, gen2, hasInbox("foo.c.l1", "foo.c.l2", "foo.c.lead"))
	if r := onlyRefusal(t, p, policydoc.StageNarrowing); r.Reason != NarrowingDetail {
		t.Fatalf("reason %q", r.Reason)
	}
}

func TestDiffRefusesAnAddedAddressWithNoInboxAtStageInbox(t *testing.T) {
	gen1 := fcDoc(t, 1, 0, map[string][]string{"foo.c.core": {}})
	gen2 := fcDoc(t, 2, 1, map[string][]string{"foo.c.core": {}, "foo.c.new": {}})
	p := diffWith(t, gen1, gen2, hasInbox("foo.c.core"))
	if r := onlyRefusal(t, p, policydoc.StageInbox); r.Reason != NoInboxDetail([]string{"foo.c.new"}) {
		t.Fatalf("reason %q", r.Reason)
	}
	// With its inbox the same change is accepted.
	if p := diffWith(t, gen1, gen2, hasInbox("foo.c.core", "foo.c.new")); p.Refused {
		t.Fatalf("refused with the inbox present: %+v", p)
	}
}

func TestDiffRefusesAnAddedAddressWithASuspendedInboxAtStageInbox(t *testing.T) {
	gen1 := fcDoc(t, 1, 0, map[string][]string{"foo.c.core": {}})
	gen2 := fcDoc(t, 2, 1, map[string][]string{"foo.c.core": {}, "foo.c.new": {}})
	inb := hasInbox("foo.c.core")
	inb.answers["foo.c.new"] = admin.InboxCheck{Stream: "inbox-new", Consumer: true, Suspended: true}
	p := diffWith(t, gen1, gen2, inb)
	if r := onlyRefusal(t, p, policydoc.StageInbox); r.Reason != SuspendedInboxDetail([]string{"foo.c.new"}) {
		t.Fatalf("reason %q", r.Reason)
	}
}

func TestDiffWithoutTheChecksSaysInboxNotChecked(t *testing.T) {
	first := fcDoc(t, 1, 0, map[string][]string{"foo.c.core": {}})
	p, err := policydoc.Diff(context.Background(), diffReader{account: "A"}, first, "foo", nil, &policydoc.RotationContext{Adopted: true}, nil)
	if err != nil || p.Refused {
		t.Fatalf("%+v, %v", p, err)
	}
	if got := policydoc.RenderPlan(p); !strings.Contains(got, policydoc.InboxNotCheckedLine) {
		t.Fatalf("no note: %s", got)
	}
}

// The Applier and the diff give the same text for the same case: both come from FailClosed.
func TestApplyAndDiffGiveTheSameInboxRefusal(t *testing.T) {
	a, d, _ := newAddressApplier(t, &fakeInboxes{})
	_, err := a.Apply(context.Background(), d, okGuard{})
	if err == nil {
		t.Fatal("apply did not refuse")
	}
	p := diffWith(t, nil, d.Raw, &fakeInboxes{})
	if r := onlyRefusal(t, p, policydoc.StageInbox); r.Reason != err.Error() {
		t.Fatalf("diff says %q, apply says %q", r.Reason, err.Error())
	}
}
