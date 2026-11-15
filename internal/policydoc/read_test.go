package policydoc

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// fakeReader serves a list set in force (decoded from doc) and a status.
type fakeReader struct {
	doc []byte
	st  *policyapply.Status
	// pend is the pending record; err is what the read fails with.
	pend *listset.Record
	err  error
}

func (f fakeReader) Account() string { return testAccount }

func (f fakeReader) InForceViewAndStatus(context.Context) (listset.Record, listset.ListSet, bool, listset.Record, bool, policyapply.Status, bool, error) {
	var st policyapply.Status
	var pend listset.Record
	if f.pend != nil {
		pend = *f.pend
	}
	hasPend := f.pend != nil
	if f.err != nil {
		return listset.Record{}, listset.ListSet{}, false, listset.Record{}, false, policyapply.Status{}, false, f.err
	}
	stOK := f.st != nil
	if stOK {
		st = *f.st
	}
	if len(f.doc) == 0 {
		return listset.Record{}, listset.ListSet{}, false, pend, hasPend, st, stOK, nil
	}
	d, err := Decode(f.doc, DecodeOptions{})
	if err != nil {
		return listset.Record{}, listset.ListSet{}, false, pend, hasPend, st, stOK, err
	}
	c, err := listset.Compile(d.ListSet)
	if err != nil {
		return listset.Record{}, listset.ListSet{}, false, pend, hasPend, st, stOK, err
	}
	return listset.Record{Generation: d.Generation, Signed: listset.DigestCompiled(c)}, d.ListSet, true, pend, hasPend, st, stOK, nil
}

func u(n uint64) *uint64 { return &n }

func applied(gen uint64) *policyapply.Status {
	return &policyapply.Status{Generation: gen, State: policyapply.StateApplied, InForce: true,
		Servers: []policyapply.ServerGeneration{{Server: "nats-b", Generation: u(gen)}, {Server: "nats-a", Generation: u(gen)}}}
}

func partial() *policyapply.Status {
	return &policyapply.Status{Generation: 7, State: policyapply.StatePartial,
		Servers: []policyapply.ServerGeneration{{Server: "nats-b", Generation: u(6)}, {Server: "nats-a", Generation: u(7)}, {Server: "nats-c"}}}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s differs from its golden file (run with -update):\n%s", name, got)
	}
}

func TestShow(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	res, err := Show(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ListSetInForce || res.View.Generation != 7 || res.Header.InForceGeneration != 7 || res.Header.Latest == nil || res.Header.Latest.State != "applied" || res.Header.Partial || res.Header.Notice != "" {
		t.Fatalf("%+v", res)
	}
	if res.Header.Servers[0].Server != "nats-a" {
		t.Fatalf("servers not sorted: %+v", res.Header.Servers)
	}
	text := RenderShow(res)
	if !strings.Contains(text, listset.RawClientLimit) || !strings.Contains(text, "list set in force") {
		t.Fatalf("missing sentences:\n%s", text)
	}
	golden(t, "show_applied", text)
	if _, err := json.Marshal(res); err != nil {
		t.Fatal(err)
	}
}

func TestShowPartial(t *testing.T) {
	res, err := Show(context.Background(), fakeReader{doc: doc(7, 6, nil), st: partial()})
	if err != nil {
		t.Fatal(err)
	}
	h := res.Header
	if h.InForceGeneration != 7 || h.Latest == nil || h.Latest.Applied || !h.Partial || !strings.Contains(h.Notice, "partial application") || strings.Contains(h.Notice, NoPolicyNotice) ||
		!strings.Contains(h.Notice, "nats-b: 6") || !strings.Contains(h.Notice, "nats-c: unknown") {
		t.Fatalf("%+v", h)
	}
	golden(t, "show_partial", RenderShow(res))
}

// refusedAfter is the latest status when an attempt at generation 8 was refused after generation 7 was committed.
func refusedAfter() *policyapply.Status {
	return &policyapply.Status{Generation: 8, State: policyapply.StateRefused,
		Servers: []policyapply.ServerGeneration{{Server: "nats-a", Generation: u(7)}, {Server: "nats-b", Generation: u(7)}}}
}

func TestShowCommittedThenRefused(t *testing.T) {
	res, err := Show(context.Background(), fakeReader{doc: doc(7, 6, nil), st: refusedAfter()})
	if err != nil {
		t.Fatal(err)
	}
	h := res.Header
	if !res.ListSetInForce || h.InForceGeneration != 7 || h.Partial || h.Notice != "" ||
		h.Latest == nil || h.Latest.Generation != 8 || h.Latest.State != "refused" || h.Latest.Applied {
		t.Fatalf("%+v", res)
	}
	text := RenderShow(res)
	if !strings.Contains(text, "in force: generation 7\nlatest attempt: generation 8, refused\n") || strings.Contains(text, NoPolicyNotice) {
		t.Fatal(text)
	}
	golden(t, "show_committed_refused", text)
	ex, err := Explain(context.Background(), fakeReader{doc: doc(7, 6, nil), st: refusedAfter()}, "foo.c.lead", "foo.c.core")
	if err != nil || !ex.ListSetInForce || ex.Header.InForceGeneration != 7 || strings.Contains(RenderExplain(ex), NoPolicyNotice) {
		t.Fatalf("%+v, %v", ex, err)
	}
}

func TestShowCommittedThenPartial(t *testing.T) {
	st := partial()
	st.Generation = 8
	res, err := Show(context.Background(), fakeReader{doc: doc(7, 6, nil), st: st})
	if err != nil {
		t.Fatal(err)
	}
	h := res.Header
	if !res.ListSetInForce || res.View.Generation != 7 || h.InForceGeneration != 7 || !h.Partial ||
		h.Latest == nil || h.Latest.Generation != 8 || h.Latest.State != "partial" {
		t.Fatalf("%+v", res)
	}
	text := RenderShow(res)
	if !strings.Contains(text, "in force: generation 7\nlatest attempt: generation 8, partial\n") {
		t.Fatal(text)
	}
	if strings.Contains(text, NoPolicyNotice) {
		t.Fatal(text)
	}
	golden(t, "show_committed_partial", text)
}

func TestShowNothingInForce(t *testing.T) {
	res, err := Show(context.Background(), fakeReader{})
	if err != nil || res.ListSetInForce || res.View != nil {
		t.Fatalf("%+v, %v", res, err)
	}
	text := RenderShow(res)
	if !strings.Contains(text, "no list set is in force") || !strings.Contains(text, listset.RawClientLimit) {
		t.Fatal(text)
	}
	golden(t, "show_empty", text)
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), `"servers":[]`) {
		t.Fatal(string(b))
	}
}

func TestExplain(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	res, err := Explain(ctx, r, "foo.c.lead", "foo.c.core")
	if err != nil {
		t.Fatal(err)
	}
	if res.Explanation == nil || res.Explanation.Verbs[0].Allowed {
		t.Fatalf("lead denied core mail: %+v", res.Explanation)
	}
	text := RenderExplain(res)
	if !strings.Contains(text, listset.RawClientLimit) {
		t.Fatal(text)
	}
	golden(t, "explain_denied", text)
	if _, err := Explain(ctx, r, "foo.c.nobody", "foo.c.core"); err == nil {
		t.Fatal("unknown sender explained")
	}
	res, err = Explain(ctx, fakeReader{st: partial()}, "foo.c.lead", "foo.c.core")
	if err != nil || res.ListSetInForce || !res.Header.Partial {
		t.Fatalf("%+v, %v", res, err)
	}
	golden(t, "explain_empty_partial", RenderExplain(res))
}

func TestDiff(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	next := doc(8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.lead"] = map[string]any{"allow": []string{"foo.c.core"}, "deny": []string{}}
	})
	arc := adoptedRC(userKey(t))
	p, err := Diff(ctx, r, next, "", nil, arc)
	if err != nil {
		t.Fatal(err)
	}
	if p.Refused || len(p.Receivers) == 0 {
		t.Fatalf("%+v", p)
	}
	golden(t, "plan_change", RenderPlan(p))
	if _, err := json.Marshal(p); err != nil {
		t.Fatal(err)
	}

	// A JWS is accepted without a signature check.
	kp, _ := submitter(t)
	if _, err := Diff(ctx, r, sign(t, kp, next), "", nil, nil); err != nil {
		t.Fatal(err)
	}

	// A stale base is a refusal in the plan, not an error.
	p, err = Diff(ctx, r, doc(7, 6, nil), "", nil, nil)
	if err != nil || !p.Refused || p.Refusals[0].Stage != StageBase {
		t.Fatalf("%+v, %v", p, err)
	}
	golden(t, "plan_stale", RenderPlan(p))

	// Another account is an error.
	if _, err := Diff(ctx, r, doc(8, 7, func(m map[string]any) { m["account"] = "ACXOTHER" }), "", nil, nil); err == nil {
		t.Fatal("a document for another account was diffed")
	}
	if _, err := Diff(ctx, r, []byte("junk"), "", nil, nil); !errors.Is(err, ErrBadDocument) {
		t.Fatal(err)
	}

	// Nothing in force: the first generation.
	if _, err := Diff(ctx, fakeReader{}, doc(1, 0, nil), "", nil, nil); !errors.Is(err, ErrRangeNeeded) {
		t.Fatalf("no range with nothing in force: %v", err)
	}
	p, err = Diff(ctx, fakeReader{}, doc(1, 0, nil), "foo", nil, arc)
	if err != nil || p.Refused || len(p.FirstCredential) != 2 {
		t.Fatalf("%+v, %v", p, err)
	}
	golden(t, "plan_first", RenderPlan(p))
	p, err = Diff(ctx, fakeReader{}, next, "foo", nil, nil)
	if err != nil || !p.Refused || p.Refusals[0].Stage != StageBase {
		t.Fatalf("%+v, %v", p, err)
	}
}

// changedDoc is the generation 8 document with the lead's allow list changed, so that it is not unchanged content.
func changedDoc(mod func(m map[string]any)) []byte {
	return doc(8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.lead"] = map[string]any{"allow": []string{"foo.c.core"}, "deny": []string{}}
		if mod != nil {
			mod(m)
		}
	})
}

// adoptedRC is a read of an adopted account whose submitter is cur; no signer is named.
func adoptedRC(cur string) *RotationContext {
	return &RotationContext{Adopted: true, Marker: store.Marker{Account: testAccount, Submitter: cur}}
}

// A document identical in content to the one in force is refused at stage unchanged, as the apply refuses it.
func TestDiffNothingChanges(t *testing.T) {
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	p, err := Diff(context.Background(), r, doc(8, 7, nil), "", nil, adoptedRC(userKey(t)))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Refused || len(p.Refusals) != 1 || p.Refusals[0].Stage != "unchanged" ||
		p.Refusals[0].Reason != "same content as generation 7 in force; nothing to apply" {
		t.Fatalf("%+v", p)
	}
	text := RenderPlan(p)
	if text != "refused: nothing would change\n  [unchanged] list_set: same content as generation 7 in force; nothing to apply\n" {
		t.Fatalf("%q", text)
	}
	golden(t, "plan_unchanged", text)
}

// Unchanged content is accepted only as a rotation step: one that starts a rotation (a next_submitter, none pending) or
// completes one (the signer is the pending key). A next_submitter while a rotation is pending is not a step.
func TestDiffUnchangedRotationStep(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	cur, next, pendKey := userKey(t), userKey(t), userKey(t)
	same := doc(8, 7, nil)
	withNext := doc(8, 7, func(m map[string]any) { m["next_submitter"] = next })

	p, err := Diff(ctx, r, withNext, "", nil, &RotationContext{Adopted: true, Marker: store.Marker{Account: testAccount, Submitter: cur}, Held: []string{userKey(t)}})
	if err != nil || p.Refused || !strings.HasPrefix(RenderPlan(p), "accepted: this would change\n"+SizeLimitsLine+"\nrotation: next submitter "+next+" would be recorded\n") {
		t.Fatalf("starts a rotation: %+v, %v", p, err)
	}
	// The caller read no marker: the rotation is shown, not validated, and is a step.
	if p, err = Diff(ctx, r, withNext, "", nil, nil); err != nil || p.Refused {
		t.Fatalf("not validated: %+v, %v", p, err)
	}
	pend := &RotationContext{Adopted: true, Marker: store.Marker{Account: testAccount, Submitter: cur, NextSubmitter: pendKey}, Signer: pendKey}
	p, err = Diff(ctx, r, same, "", nil, pend)
	if err != nil || p.Refused || !strings.HasPrefix(RenderPlan(p), "accepted: this would change\n"+SizeLimitsLine+"\nrotation: generation would complete the pending rotation to "+pendKey+"\n") {
		t.Fatalf("completes a rotation: %+v, %v", p, err)
	}
	// Pending, but the signer is not the pending key and no next_submitter: not a step. With no signer named and none
	// to tell (raw JSON), the outcome is not known: see TestDiffUnchangedPendingSignerFromJWS.
	for _, signer := range []string{cur} {
		rc := &RotationContext{Adopted: true, Marker: store.Marker{Account: testAccount, Submitter: cur, NextSubmitter: pendKey}, Signer: signer}
		p, err = Diff(ctx, r, withNext, "", nil, rc)
		if err != nil || !p.Refused || p.Refusals[len(p.Refusals)-1].Stage != "unchanged" {
			t.Fatalf("pending, signer %q: %+v, %v", signer, p, err)
		}
	}
	// No next_submitter, none pending: refused.
	if p, err = Diff(ctx, r, same, "", nil, adoptedRC(cur)); err != nil || !p.Refused || p.Refusals[0].Stage != "unchanged" {
		t.Fatalf("%+v, %v", p, err)
	}
}

// The intake refuses every submission to an account that is not adopted, so the plan does too, with or without a signer;
// an unreadable marker is a refusal at the same stage. A caller that read no marker (nil) is told the check was skipped.
func TestDiffAdoption(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	changed := doc(8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.lead"] = map[string]any{"allow": []string{"foo.c.core"}, "deny": []string{}}
	})
	for name, tc := range map[string]struct {
		rc     *RotationContext
		reason string
	}{
		"not adopted":    {&RotationContext{}, "this account is not adopted: list sets are ignored"},
		"unreadable":     {&RotationContext{Err: errors.New("boom"), MarkerErr: errors.New("boom")}, "the adoption marker cannot be read: boom"},
		"not, signer":    {&RotationContext{Signer: userKey(t)}, "this account is not adopted: list sets are ignored"},
		"unreadable, sg": {&RotationContext{Signer: userKey(t), Err: errors.New("boom"), MarkerErr: errors.New("boom")}, "the adoption marker cannot be read: boom"},
	} {
		p, err := Diff(ctx, r, changed, "", nil, tc.rc)
		if err != nil || !p.Refused || len(p.Refusals) != 1 || p.Refusals[0].Stage != StageAdoption || p.Refusals[0].Reason != tc.reason {
			t.Fatalf("%s: %+v, %v", name, p, err)
		}
	}
	p, err := Diff(ctx, r, changed, "", nil, nil)
	if err != nil || p.Refused || !strings.Contains(RenderPlan(p), "adoption not checked: the adoption marker was not read\n") {
		t.Fatalf("nil: %+v, %v", p, err)
	}
	if p, err = Diff(ctx, r, changed, "", nil, adoptedRC(userKey(t))); err != nil || p.Refused || strings.Contains(RenderPlan(p), "adoption not checked") {
		t.Fatalf("adopted: %+v, %v", p, err)
	}
}

// A valid document for another range is a refusal at stage range, whether the range is selected or the one in force.
func TestDiffRangeMismatch(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	other := doc(8, 7, func(m map[string]any) { m["range"] = "bar" })
	for _, rng := range []string{"foo", ""} { // selected, then the range of the list set in force
		p, err := Diff(ctx, r, other, rng, nil, nil)
		if err != nil || !p.Refused || p.Refusals[0].Stage != StageRange ||
			!strings.Contains(p.Refusals[0].Reason, "bar") || !strings.Contains(p.Refusals[0].Reason, "foo") {
			t.Fatalf("range %q: %+v, %v", rng, p, err)
		}
	}
	p, _ := Diff(ctx, r, other, "foo", nil, nil)
	golden(t, "plan_range", RenderPlan(p))
	if p, err := Diff(ctx, r, changedDoc(nil), "foo", nil, nil); err != nil || p.Refused {
		t.Fatalf("matching range: %+v, %v", p, err)
	}
}

// A generation is pending: a document with other content is refused at stage pending, naming the generation; the same
// content, a pending record left from an older generation and no pending record do not refuse.
func TestDiffPending(t *testing.T) {
	ctx := context.Background()
	changed := doc(8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.lead"] = map[string]any{"allow": []string{"foo.c.core"}, "deny": []string{}}
	})
	d, err := Decode(changed, DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pendingFor := func(gen uint64, content listset.Digest) fakeReader {
		return fakeReader{doc: doc(7, 6, nil), st: applied(7), pend: &listset.Record{Generation: gen, Content: content}}
	}
	// Other content than the pending generation's.
	other := listset.DigestListSet(listset.ListSet{Range: "other"})
	p, err := Diff(ctx, pendingFor(8, other), changed, "", nil, nil)
	if err != nil || !p.Refused || p.Refusals[0].Stage != StagePending || !strings.Contains(p.Refusals[0].Reason, "generation 8 is pending") {
		t.Fatalf("%+v, %v", p, err)
	}
	// The same content as the pending generation's.
	if p, err = Diff(ctx, pendingFor(8, listset.DigestListSet(d.ListSet)), changed, "", nil, nil); err != nil || p.Refused {
		t.Fatalf("same content: %+v, %v", p, err)
	}
	// A pending record from a generation that has since been committed is cleared by the apply: it does not count.
	if p, err = Diff(ctx, pendingFor(7, other), changed, "", nil, nil); err != nil || p.Refused {
		t.Fatalf("stale pending: %+v, %v", p, err)
	}
}

// A read that fails (a committed record with no stored list set) fails show, explain and diff: nothing is reported.
func TestUnreadableInForce(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{err: errors.New("the record in force (generation 7) has no stored list set: it cannot be read")}
	if _, err := Show(ctx, r); err == nil || !strings.Contains(err.Error(), "generation 7") {
		t.Fatal(err)
	}
	if _, err := Explain(ctx, r, "foo.c.lead", "foo.c.core"); err == nil {
		t.Fatal("explain read nothing")
	}
	if _, err := Diff(ctx, r, doc(8, 7, nil), "", nil, nil); err == nil {
		t.Fatal("diff compared against nothing")
	}
}

func userKey(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// A document whose list set is unchanged but which carries next_submitter is a change: it starts a planned rotation.
func TestDiffRotation(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	next, cur, held := userKey(t), userKey(t), userKey(t)
	withNext := func(k string) []byte { return doc(8, 7, func(m map[string]any) { m["next_submitter"] = k }) }
	adopted := func(m store.Marker) *RotationContext {
		m.Account, m.Submitter = testAccount, cur
		return &RotationContext{Adopted: true, Marker: m, Held: []string{held}}
	}

	p, err := Diff(ctx, r, withNext(next), "", nil, adopted(store.Marker{}))
	if err != nil || p.Refused || p.NextSubmitter != next {
		t.Fatalf("%+v, %v", p, err)
	}
	text := RenderPlan(p)
	want := "accepted: this would change\n" + SizeLimitsLine + "\nrotation: next submitter " + next + " would be recorded\n"
	if !strings.HasPrefix(text, want) || strings.Contains(text, "nothing would change") {
		t.Fatalf("%q", text)
	}
	golden(t, "plan_rotation", strings.ReplaceAll(text, next, "NEXTKEY"))
	if b, _ := json.Marshal(p); !strings.Contains(string(b), `"next_submitter":"`+next+`"`) {
		t.Fatalf("%s", b)
	}
	if b, _ := json.Marshal(mustDiff(t, r, doc(8, 7, nil), adopted(store.Marker{}))); !strings.Contains(string(b), `"next_submitter":""`) {
		t.Fatalf("%s", b)
	}

	// Not validated: nobody read the marker or the keys.
	p, _ = Diff(ctx, r, withNext(next), "", nil, nil)
	if !strings.Contains(RenderPlan(p), "rotation: next submitter "+next+" would be recorded (not validated: ") {
		t.Fatalf("%q", RenderPlan(p))
	}

	refused := func(name string, rc *RotationContext, doc []byte, stage, detail string) {
		t.Helper()
		p, err := Diff(ctx, r, doc, "", nil, rc)
		if err != nil || !p.Refused || p.Refusals[0].Stage != stage || !strings.Contains(p.Refusals[0].Reason, detail) {
			t.Fatalf("%s: %+v, %v", name, p, err)
		}
		if strings.Contains(RenderPlan(p), next) {
			t.Fatalf("%s: the key is echoed", name)
		}
	}
	refused("held", adopted(store.Marker{}), withNext(held), StageDocument, "next_submitter is a key mash holds")
	refused("admin", adopted(store.Marker{By: next}), withNext(next), StageDocument, "next_submitter is a key mash holds")
	refused("current", adopted(store.Marker{}), withNext(cur), StageDocument, "next_submitter is not acceptable")
	refused("unadopted", &RotationContext{}, withNext(next), StageAdoption, "this account is not adopted: list sets are ignored")
	refused("unreadable", &RotationContext{Adopted: true, Err: errors.New("boom")}, withNext(next), StageDocument, "next_submitter cannot be checked: boom")

	// A rotation already pending: a note, or the completion when the signer is the pending key.
	pend := store.Marker{NextSubmitter: userKey(t)}
	p, _ = Diff(ctx, r, changedDoc(func(m map[string]any) { m["next_submitter"] = next }), "", nil, adopted(pend))
	if p.Refused || !strings.Contains(RenderPlan(p), "rotation: a rotation to "+pend.NextSubmitter+" is pending\n") {
		t.Fatalf("%q", RenderPlan(p))
	}
	rc := adopted(pend)
	rc.Signer = pend.NextSubmitter
	p, _ = Diff(ctx, r, doc(8, 7, nil), "", nil, rc)
	if p.Refused || !strings.HasPrefix(RenderPlan(p), "accepted: this would change\n"+SizeLimitsLine+"\nrotation: generation would complete the pending rotation to "+pend.NextSubmitter+"\n") {
		t.Fatalf("%q", RenderPlan(p))
	}
}

// --submitter is checked against the adoption marker as the intake does: the marker's next submitter while a rotation is
// pending, else its submitter. Without one the plan says the signature was not checked.
func TestDiffSigner(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	cur, other, next := userKey(t), userKey(t), userKey(t)
	rc := func(m store.Marker, signer string) *RotationContext {
		m.Account, m.Submitter = testAccount, cur
		return &RotationContext{Adopted: true, Marker: m, Signer: signer}
	}
	refused := func(name string, rc *RotationContext, stage string, details ...string) {
		t.Helper()
		p, err := Diff(ctx, r, changedDoc(nil), "", nil, rc)
		if err != nil || !p.Refused || p.Refusals[0].Stage != stage {
			t.Fatalf("%s: %+v, %v", name, p, err)
		}
		for _, d := range details {
			if !strings.Contains(p.Refusals[0].Reason, d) {
				t.Fatalf("%s: %q lacks %q", name, p.Refusals[0].Reason, d)
			}
		}
		if strings.Contains(RenderPlan(p), other) || strings.Contains(RenderPlan(p), cur) {
			t.Fatalf("%s: a whole key is echoed", name)
		}
	}
	if p := mustDiff(t, r, changedDoc(nil), rc(store.Marker{}, cur)); p.Refused || strings.Contains(RenderPlan(p), "not checked") {
		t.Fatalf("the right key: %q", RenderPlan(p))
	}
	refused("wrong key", rc(store.Marker{}, other), StageSignature, "the adoption marker expects "+cur[:8]+"...")
	pend := store.Marker{NextSubmitter: next}
	refused("old key during a rotation", rc(pend, cur), StageSignature, "the adoption marker expects "+next[:8]+"...", "a rotation is pending: only the next submitter's key is accepted")
	if p := mustDiff(t, r, changedDoc(nil), rc(pend, next)); p.Refused {
		t.Fatalf("the next key during a rotation: %+v", p)
	}
	refused("not adopted", &RotationContext{Signer: cur}, StageAdoption, "this account is not adopted")
	refused("unreadable", &RotationContext{Signer: cur, Err: errors.New("boom"), MarkerErr: errors.New("boom")}, StageAdoption, "the adoption marker cannot be read: boom")
	if got := RenderPlan(mustDiff(t, r, changedDoc(nil), nil)); !strings.Contains(got, "signature not checked: give --submitter to check it against the adoption marker\n") {
		t.Fatalf("%q", got)
	}
}

func mustDiff(t *testing.T, r Reader, file []byte, rc *RotationContext) DiffPlan {
	t.Helper()
	p, err := Diff(context.Background(), r, file, "", nil, rc)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func userKP(t *testing.T) (nkeys.KeyPair, string) {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := kp.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	return kp, pub
}

// During a pending rotation, with no --submitter, a document of unchanged content is judged by who signed it, told from
// the JWS: the pending key completes the rotation (as the intake does), the current key is refused as unchanged, and a
// document whose signer cannot be told is not refused as unchanged: the outcome is reported as unknown.
func TestDiffUnchangedPendingSignerFromJWS(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	curKP, cur := userKP(t)
	nextKP, next := userKP(t)
	otherKP, _ := userKP(t)
	rc := &RotationContext{Adopted: true, Marker: store.Marker{Account: testAccount, Submitter: cur, NextSubmitter: next}}
	same := doc(8, 7, nil)

	p, err := Diff(ctx, r, sign(t, nextKP, same), "", nil, rc)
	if err != nil || p.Refused || p.Rotation == nil || p.Rotation.Action != RotationCompletes || p.Rotation.NextSubmitter != next {
		t.Fatalf("signed by the pending key: %+v, %v", p, err)
	}
	if !strings.Contains(RenderPlan(p), "rotation: generation would complete the pending rotation to "+next+"\n") {
		t.Fatalf("%q", RenderPlan(p))
	}
	p, err = Diff(ctx, r, sign(t, curKP, same), "", nil, rc)
	if err != nil || !p.Refused || p.Refusals[len(p.Refusals)-1].Stage != "unchanged" {
		t.Fatalf("signed by the old key: %+v, %v", p, err)
	}
	for name, file := range map[string][]byte{"raw JSON": same, "other key": sign(t, otherKP, same)} {
		p, err = Diff(ctx, r, file, "", nil, rc)
		if err != nil || p.Refused || p.Rotation == nil || p.Rotation.Action != RotationPending || !p.Rotation.Unknown {
			t.Fatalf("%s: %+v, %v", name, p, err)
		}
		if !strings.Contains(RenderPlan(p), "cannot be checked without --submitter") {
			t.Fatalf("%s: %q", name, RenderPlan(p))
		}
	}
}

// The plan's JSON says what the document does to the rotation, so a document that completes a pending rotation does not
// read as "nothing would change".
func TestDiffRotationJSON(t *testing.T) {
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	_, cur := userKP(t)
	nextKP, next := userKP(t)
	_, other := userKP(t)
	rotation := func(file []byte, m store.Marker) string {
		t.Helper()
		m.Account, m.Submitter = testAccount, cur
		b, err := json.Marshal(mustDiff(t, r, file, &RotationContext{Adopted: true, Marker: m, Held: []string{userKey(t)}}))
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Rotation json.RawMessage `json:"rotation"`
		}
		if err := json.Unmarshal(b, &v); err != nil || v.Rotation == nil {
			t.Fatalf("%s: %v", b, err)
		}
		return string(v.Rotation)
	}
	if got, want := rotation(sign(t, nextKP, doc(8, 7, nil)), store.Marker{NextSubmitter: next}), `{"action":"completes","next_submitter":"`+next+`"}`; got != want {
		t.Fatalf("completes: %s", got)
	}
	if got, want := rotation(doc(8, 7, func(m map[string]any) { m["next_submitter"] = other }), store.Marker{}), `{"action":"starts","next_submitter":"`+other+`"}`; got != want {
		t.Fatalf("starts: %s", got)
	}
	if got := rotation(changedDoc(nil), store.Marker{}); got != "null" {
		t.Fatalf("no rotation: %s", got)
	}
}

// The adoption marker is read over the admin connection and holds one account: a marker that names another account than
// the one diffed is refused at stage adoption, as the intake refuses it.
func TestDiffMarkerOfAnotherAccount(t *testing.T) {
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	cur := userKey(t)
	rc := &RotationContext{Adopted: true, Marker: store.Marker{Account: "ACXOTHER", Submitter: cur}}
	p := mustDiff(t, r, changedDoc(nil), rc)
	if !p.Refused || p.Refusals[0].Stage != StageAdoption || !strings.Contains(p.Refusals[0].Reason, "names another account") {
		t.Fatalf("%+v", p)
	}
	rc.Marker.Account = testAccount
	if p := mustDiff(t, r, changedDoc(nil), rc); p.Refused {
		t.Fatalf("%+v", p)
	}
}
