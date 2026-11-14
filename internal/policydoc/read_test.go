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
}

func (f fakeReader) Account() string { return testAccount }

func (f fakeReader) InForceViewAndStatus(context.Context) (listset.Record, listset.ListSet, bool, policyapply.Status, bool, error) {
	var st policyapply.Status
	stOK := f.st != nil
	if stOK {
		st = *f.st
	}
	if len(f.doc) == 0 {
		return listset.Record{}, listset.ListSet{}, false, st, stOK, nil
	}
	d, err := Decode(f.doc, DecodeOptions{})
	if err != nil {
		return listset.Record{}, listset.ListSet{}, false, st, stOK, err
	}
	return listset.Record{Generation: d.Generation}, d.ListSet, true, st, stOK, nil
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
	if h.InForceGeneration != 7 || h.Latest == nil || h.Latest.Applied || !h.Partial || !strings.Contains(h.Notice, "partial application") || !strings.Contains(h.Notice, NoPolicyNotice) ||
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
	p, err := Diff(ctx, r, next, "", nil)
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
	if _, err := Diff(ctx, r, sign(t, kp, next), "", nil); err != nil {
		t.Fatal(err)
	}

	// A stale base is a refusal in the plan, not an error.
	p, err = Diff(ctx, r, doc(7, 6, nil), "", nil)
	if err != nil || !p.Refused || p.Refusals[0].Stage != StageBase {
		t.Fatalf("%+v, %v", p, err)
	}
	golden(t, "plan_stale", RenderPlan(p))

	// Another account is an error.
	if _, err := Diff(ctx, r, doc(8, 7, func(m map[string]any) { m["account"] = "ACXOTHER" }), "", nil); err == nil {
		t.Fatal("a document for another account was diffed")
	}
	if _, err := Diff(ctx, r, []byte("junk"), "", nil); !errors.Is(err, ErrBadDocument) {
		t.Fatal(err)
	}

	// Nothing in force: the first generation.
	p, err = Diff(ctx, fakeReader{}, doc(1, 0, nil), "", nil)
	if err != nil || p.Refused || len(p.FirstCredential) != 2 {
		t.Fatalf("%+v, %v", p, err)
	}
	golden(t, "plan_first", RenderPlan(p))
	p, err = Diff(ctx, fakeReader{}, next, "", nil)
	if err != nil || !p.Refused || p.Refusals[0].Stage != StageBase {
		t.Fatalf("%+v, %v", p, err)
	}
}

// A document identical in content to the one in force changes nothing: the plan says so and prints nothing else.
func TestDiffNothingChanges(t *testing.T) {
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	p, err := Diff(context.Background(), r, doc(8, 7, nil), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Refused {
		t.Fatalf("%+v", p)
	}
	text := RenderPlan(p)
	if text != "accepted: nothing would change\n"+SizeLimitsLine+"\n" {
		t.Fatalf("%q", text)
	}
	golden(t, "plan_unchanged", text)
}

// A valid document for another range is a refusal at stage range, whether the range is selected or the one in force.
func TestDiffRangeMismatch(t *testing.T) {
	ctx := context.Background()
	r := fakeReader{doc: doc(7, 6, nil), st: applied(7)}
	other := doc(8, 7, func(m map[string]any) { m["range"] = "bar" })
	for _, rng := range []string{"foo", ""} { // selected, then the range of the list set in force
		p, err := Diff(ctx, r, other, rng, nil)
		if err != nil || !p.Refused || p.Refusals[0].Stage != StageRange ||
			!strings.Contains(p.Refusals[0].Reason, "bar") || !strings.Contains(p.Refusals[0].Reason, "foo") {
			t.Fatalf("range %q: %+v, %v", rng, p, err)
		}
	}
	p, _ := Diff(ctx, r, other, "foo", nil)
	golden(t, "plan_range", RenderPlan(p))
	if p, err := Diff(ctx, r, doc(8, 7, nil), "foo", nil); err != nil || p.Refused {
		t.Fatalf("matching range: %+v, %v", p, err)
	}
}
