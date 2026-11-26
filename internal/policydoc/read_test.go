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

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
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
	p, err := Diff(ctx, r, next, "", nil, nil)
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
	if _, err := Diff(ctx, fakeReader{}, doc(1, 0, nil), "", nil, nil); !errors.Is(err, ErrOwnerNeeded) {
		t.Fatalf("no owner with nothing in force: %v", err)
	}
	p, err = Diff(ctx, fakeReader{}, doc(1, 0, nil), "foo", nil, nil)
	if err != nil || p.Refused || len(p.FirstCredential) != 2 {
		t.Fatalf("%+v, %v", p, err)
	}
	golden(t, "plan_first", RenderPlan(p))
	for _, sec := range []string{"first credential (not issued by the apply: request it from the issue service, `mashctl issue serve`): ", "new credential (not issued by the apply: request it from the issue service, `mashctl issue serve`): "} {
		if !strings.Contains(RenderPlan(p), sec) {
			t.Errorf("the plan does not say the apply issues no credential (%q):\n%s", sec, RenderPlan(p))
		}
	}
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

// A document identical in content to the one in force is refused at stage unchanged, as the apply refuses it.
func TestDiffNothingChanges(t *testing.T) {
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	p, err := Diff(context.Background(), r, doc(8, 7, nil), "", nil, nil)
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

func TestDiffOwnerMismatch(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	other := doc(8, 7, func(m map[string]any) { m["owner"] = "bar" })
	for _, owner := range []string{"foo", ""} { // selected, then the owner of the list set in force
		p, err := Diff(ctx, r, other, owner, nil, nil)
		if err != nil || !p.Refused || p.Refusals[0].Stage != StageOwner ||
			!strings.Contains(p.Refusals[0].Reason, "bar") || !strings.Contains(p.Refusals[0].Reason, "foo") {
			t.Fatalf("owner %q: %+v, %v", owner, p, err)
		}
	}
	p, _ := Diff(ctx, r, other, "foo", nil, nil)
	golden(t, "plan_owner", RenderPlan(p))
	if p, err := Diff(ctx, r, changedDoc(nil), "foo", nil, nil); err != nil || p.Refused {
		t.Fatalf("matching owner: %+v, %v", p, err)
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
	other := listset.DigestListSet(listset.ListSet{Owner: "other"})
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
