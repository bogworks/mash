package policydoc

import (
	"context"
	"encoding/base64"
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
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

const testAccount = "ACXTEST"

func doc(gen, base int, mod func(m map[string]any)) []byte {
	m := map[string]any{
		"schema": DocumentSchema, "account": testAccount, "range": "foo", "generation": gen, "base_generation": base,
		"issued_at": "2026-11-12T17:00:00Z",
		"groups": map[string]any{
			"c": map[string]any{"mail": "shared", "prompt": "closed", "status": "shared", "heartbeat": "shared"},
		},
		"senders": map[string]any{
			"foo.c.core": map[string]any{"allow": []string{"foo.d.build"}, "deny": []string{}},
			"foo.c.lead": map[string]any{"allow": []string{}, "deny": []string{"foo.c.core"}},
		},
	}
	if mod != nil {
		mod(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func sign(t *testing.T, kp nkeys.KeyPair, payload []byte) []byte {
	t.Helper()
	enc := base64.RawURLEncoding
	in := enc.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`)) + "." + enc.EncodeToString(payload)
	sig, err := kp.Sign([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(in + "." + enc.EncodeToString(sig))
}

func submitter(t *testing.T) (nkeys.KeyPair, string) {
	t.Helper()
	kp, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return kp, pub
}

func TestDecodeRaw(t *testing.T) {
	raw := doc(7, 6, nil)
	d, err := Decode(raw, DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Verified || d.Generation != 7 || d.BaseGeneration != 6 || d.ListSet.Base != 6 || d.ListSet.Range != "foo" || string(d.Raw) != string(raw) {
		t.Fatalf("%+v", d)
	}
	if len(d.ListSet.Switches) != 4 || d.ListSet.Switches[0] != (listset.SwitchEntry{Group: "foo.c", Verb: listset.Mail, Switch: listset.Shared}) {
		t.Fatalf("switches %+v", d.ListSet.Switches)
	}
	if len(d.ListSet.Senders) != 2 || d.ListSet.Senders[0].Sender != "foo.c.core" || d.ListSet.Senders[1].Deny[0] != "foo.c.core" {
		t.Fatalf("senders %+v", d.ListSet.Senders)
	}
	if _, err := Decode(raw, DecodeOptions{Submitter: "x"}); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("raw with a submitter: %v", err)
	}
}

func TestDecodeJWS(t *testing.T) {
	kp, pub := submitter(t)
	tok := sign(t, kp, doc(7, 6, nil))
	d, err := Decode(tok, DecodeOptions{Submitter: pub})
	if err != nil || !d.Verified || string(d.Raw) != string(tok) {
		t.Fatalf("verified decode: %+v, %v", d, err)
	}
	d, err = Decode(append(tok, '\n'), DecodeOptions{})
	if err != nil || d.Verified {
		t.Fatalf("unchecked decode: %+v, %v", d, err)
	}
	_, other := submitter(t)
	if _, err := Decode(tok, DecodeOptions{Submitter: other}); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("other key: %v", err)
	}
	// A payload changed after signing.
	parts := strings.Split(string(tok), ".")
	parts[1] = base64.RawURLEncoding.EncodeToString(doc(8, 7, nil))
	forged := []byte(strings.Join(parts, "."))
	if _, err := Decode(forged, DecodeOptions{Submitter: pub}); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("forged payload: %v", err)
	}
	if d, err := Decode(forged, DecodeOptions{}); err != nil || d.Verified {
		t.Fatalf("forged payload without a submitter must decode unverified: %+v, %v", d, err)
	}
	if _, err := Decode(tok, DecodeOptions{Submitter: "not a key"}); err == nil {
		t.Fatal("a bad submitter key was accepted")
	}
	none := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + parts[1] + "."
	if _, err := Decode([]byte(none), DecodeOptions{}); !errors.Is(err, ErrBadDocument) {
		t.Fatalf("alg none: %v", err)
	}
}

func TestDecodeRefusals(t *testing.T) {
	cases := map[string][]byte{
		"unknown field":       doc(7, 6, func(m map[string]any) { m["extra"] = 1 }),
		"unknown group verb":  doc(7, 6, func(m map[string]any) { m["groups"].(map[string]any)["c"].(map[string]any)["fax"] = "shared" }),
		"missing verb":        doc(7, 6, func(m map[string]any) { delete(m["groups"].(map[string]any)["c"].(map[string]any), "heartbeat") }),
		"bad switch value":    doc(7, 6, func(m map[string]any) { m["groups"].(map[string]any)["c"].(map[string]any)["mail"] = "open" }),
		"wrong schema":        doc(7, 6, func(m map[string]any) { m["schema"] = "mash.listset/2" }),
		"missing schema":      doc(7, 6, func(m map[string]any) { delete(m, "schema") }),
		"missing senders":     doc(7, 6, func(m map[string]any) { delete(m, "senders") }),
		"missing deny":        doc(7, 6, func(m map[string]any) { delete(m["senders"].(map[string]any)["foo.c.core"].(map[string]any), "deny") }),
		"generation skips":    doc(8, 6, nil),
		"generation repeats":  doc(6, 6, nil),
		"bad time":            doc(7, 6, func(m map[string]any) { m["issued_at"] = "yesterday" }),
		"duplicate key":       []byte(strings.Replace(string(doc(7, 6, nil)), `"range":"foo"`, `"range":"foo","range":"bar"`, 1)),
		"duplicate nested":    []byte(strings.Replace(string(doc(7, 6, nil)), `"mail":"shared"`, `"mail":"shared","mail":"closed"`, 1)),
		"trailing data":       append(doc(7, 6, nil), []byte(` {}`)...),
		"not json":            []byte("hello"),
		"empty":               nil,
		"base is max uint64":  []byte(strings.Replace(string(doc(7, 6, nil)), `"base_generation":6`, `"base_generation":18446744073709551615`, 1)),
		"negative generation": []byte(strings.Replace(string(doc(7, 6, nil)), `"generation":7`, `"generation":-1`, 1)),
	}
	for name, b := range cases {
		if _, err := Decode(b, DecodeOptions{}); !errors.Is(err, ErrBadDocument) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// fakeReader serves a document and a status.
type fakeReader struct {
	doc []byte
	st  *policyapply.Status
}

func (f fakeReader) Account() string                                 { return testAccount }
func (f fakeReader) InForceDocument(context.Context) ([]byte, error) { return f.doc, nil }
func (f fakeReader) LatestStatus(context.Context) (*policyapply.Status, error) {
	return f.st, nil
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
	if !res.ListSetInForce || res.View.Generation != 7 || res.Header.State != "applied" || res.Header.Partial || res.Header.Notice != "" {
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
	if !h.Partial || h.InForce || !strings.Contains(h.Notice, "partial application") || !strings.Contains(h.Notice, NoPolicyNotice) ||
		!strings.Contains(h.Notice, "nats-b: 6") || !strings.Contains(h.Notice, "nats-c: unknown") {
		t.Fatalf("%+v", h)
	}
	golden(t, "show_partial", RenderShow(res))
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

func TestShowRefusesOtherAccount(t *testing.T) {
	other := doc(7, 6, func(m map[string]any) { m["account"] = "ACXOTHER" })
	if _, err := Show(context.Background(), fakeReader{doc: other}); err == nil {
		t.Fatal("a document for another account was shown")
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
	p, err := Diff(ctx, r, next, nil)
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
	if _, err := Diff(ctx, r, sign(t, kp, next), nil); err != nil {
		t.Fatal(err)
	}

	// A stale base is a refusal in the plan, not an error.
	p, err = Diff(ctx, r, doc(7, 6, nil), nil)
	if err != nil || !p.Refused || p.Refusals[0].Stage != StageBase {
		t.Fatalf("%+v, %v", p, err)
	}
	golden(t, "plan_stale", RenderPlan(p))

	// Another account is an error.
	if _, err := Diff(ctx, r, doc(8, 7, func(m map[string]any) { m["account"] = "ACXOTHER" }), nil); err == nil {
		t.Fatal("a document for another account was diffed")
	}
	if _, err := Diff(ctx, r, []byte("junk"), nil); !errors.Is(err, ErrBadDocument) {
		t.Fatal(err)
	}

	// Nothing in force: the first generation.
	p, err = Diff(ctx, fakeReader{}, doc(1, 0, nil), nil)
	if err != nil || p.Refused || len(p.FirstCredential) != 2 {
		t.Fatalf("%+v, %v", p, err)
	}
	golden(t, "plan_first", RenderPlan(p))
	p, err = Diff(ctx, fakeReader{}, next, nil)
	if err != nil || !p.Refused || p.Refusals[0].Stage != StageBase {
		t.Fatalf("%+v, %v", p, err)
	}
}
