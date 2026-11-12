package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
)

var updateListSetGolden = flag.Bool("update-listset", false, "rewrite cmd/mashctl/testdata/policy_listset_*.golden")

const listSetRange = "foo"

// listSetPattern is the operator's MASH_POLICY_BUCKET setting in the tests; listSetBucket is its name for the range foo.
const (
	listSetPattern = "test_{range}_policy"
	listSetBucket  = "test_foo_policy"
)

// listSetDoc is a list-set document for account in the range foo.
func listSetDoc(t *testing.T, account string, gen, base int, mod func(m map[string]any)) []byte {
	t.Helper()
	m := map[string]any{
		"schema": "mash.listset/1", "account": account, "range": listSetRange, "generation": gen, "base_generation": base,
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
		t.Fatal(err)
	}
	return b
}

// listSetEnv is the applyEnv server with the policy bucket made, and the account key the document is for.
type listSetEnv struct {
	*applyEnv
	js jetstream.JetStream
}

func newListSetEnv(t *testing.T) *listSetEnv {
	t.Helper()
	e := newApplyEnv(t, twoZones)
	e.env["MASH_ADMIN_CREDS"] = e.pl.Creds
	e.env[policyapply.PolicyBucketEnv] = listSetPattern
	nc, err := nats.Connect(e.srv.ClientURL(), natsauth.UserCredentials(e.pl.Creds))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	return &listSetEnv{applyEnv: e, js: js}
}

// seed makes the policy bucket and stores doc as in force at generation gen with status st (nil: none written).
func (e *listSetEnv) seed(doc []byte, gen uint64, st *policyapply.Status) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := e.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{Bucket: listSetBucket, History: 8}); err != nil {
		e.t.Fatal(err)
	}
	l, err := policyapply.NewKVLedger(ctx, e.js, e.acctPub, policyapply.KVLedgerOptions{Bucket: listSetBucket})
	if err != nil {
		e.t.Fatal(err)
	}
	if doc != nil {
		rec := listset.Record{Generation: gen, Received: listset.Digest{1}, Content: listset.Digest{2}, Signed: listset.Digest{3}}
		if err := l.CommitDocument(0, rec, doc); err != nil {
			e.t.Fatal(err)
		}
	}
	if st != nil {
		if err := l.WriteStatus(ctx, *st); err != nil {
			e.t.Fatal(err)
		}
	}
}

func u64(n uint64) *uint64 { return &n }

func appliedStatus(gen uint64) *policyapply.Status {
	return &policyapply.Status{Generation: gen, State: policyapply.StateApplied, InForce: true, UpdatedAt: "2026-11-12T17:00:00Z",
		Servers: []policyapply.ServerGeneration{{Server: "nats-b", Generation: u64(gen)}, {Server: "nats-a", Generation: u64(gen)}}}
}

// run calls handler directly, with --range foo and --account for the seeded account unless args already name them.
func (e *listSetEnv) run(handler string, args ...string) (string, string, int) {
	e.t.Helper()
	var out, errb bytes.Buffer
	env := func(k string) string { return e.env[k] }
	lookup := func(k string) (string, bool) { v, ok := e.env[k]; return v, ok && v != "" }
	if !slices.Contains(args, "--account") {
		args = append(args, "--account", e.acctPub)
	}
	if !slices.Contains(args, "--range") && !slices.Contains(args, "--bucket") {
		args = append(args, "--range", listSetRange)
	}
	var code int
	switch handler {
	case "show":
		code = policyListSetShow(args, env, lookup, &out, &errb)
	case "explain":
		code = policyListSetExplain(args, env, lookup, &out, &errb)
	case "diff":
		code = policyListSetDiff(args, env, lookup, &out, &errb)
	default:
		e.t.Fatal("handler " + handler)
	}
	return out.String(), errb.String(), code
}

// masked replaces the account key, which differs per run, so output can be compared with a golden file.
func (e *listSetEnv) masked(s string) string { return strings.ReplaceAll(s, e.acctPub, "ACXTEST") }

func (e *listSetEnv) golden(name, got string) {
	e.t.Helper()
	got = e.masked(got)
	p := filepath.Join("testdata", "policy_listset_"+name+".golden")
	if *updateListSetGolden {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		e.t.Fatal(err)
	}
	if string(want) != got {
		e.t.Errorf("%s differs from its golden file (run with -update-listset):\n%s", name, got)
	}
}

func (e *listSetEnv) file(name string, b []byte) string {
	e.t.Helper()
	p := filepath.Join(e.root, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *listSetEnv) seedInForce() {
	e.t.Helper()
	e.seed(listSetDoc(e.t, e.acctPub, 7, 6, nil), 7, appliedStatus(7))
}

func TestListSetShow(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	out, errb, code := e.run("show")
	if code != 0 || errb != "" {
		t.Fatalf("show: %d\n%s\n%s", code, out, errb)
	}
	e.golden("show", out)

	out, errb, code = e.run("show", "--json")
	if code != 0 || errb != "" {
		t.Fatalf("show --json: %d\n%s", code, errb)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil || v["list_set_in_force"] != true {
		t.Fatalf("not the show JSON: %v\n%s", err, out)
	}
	e.golden("show_json", out)
}

func TestListSetShowNothingInForce(t *testing.T) {
	e := newListSetEnv(t)
	e.seed(nil, 0, nil)
	out, errb, code := e.run("show")
	if code != 0 || errb != "" || !strings.Contains(out, "no list set is in force") {
		t.Fatalf("show: %d\n%s\n%s", code, out, errb)
	}
	e.golden("show_empty", out)
}

func TestListSetShowNoBucketAndUsage(t *testing.T) {
	e := newListSetEnv(t)
	if _, errb, code := e.run("show"); code != 1 || !strings.Contains(errb, "policy bucket does not exist") {
		t.Fatalf("no bucket: %d %q", code, errb)
	}
	var out, errb bytes.Buffer
	env := func(k string) string { return e.env[k] }
	lookup := func(k string) (string, bool) { v, ok := e.env[k]; return v, ok && v != "" }
	if code := policyListSetShow([]string{"extra"}, env, lookup, &out, &errb); code != 2 || !strings.Contains(errb.String(), "usage:") {
		t.Fatalf("usage: %d %q", code, errb.String())
	}
	errb.Reset()
	if code := policyListSetShow([]string{"--json"}, env, lookup, &out, &errb); code != 1 || !strings.Contains(errb.String(), "no range") {
		t.Fatalf("no range: %d %q", code, errb.String())
	}
}

func TestListSetBucketSetting(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	secret := "SECRET/value"

	// A missing setting is an error naming it, exit 1.
	delete(e.env, policyapply.PolicyBucketEnv)
	if _, errb, code := e.run("show"); code != 1 || !strings.Contains(errb, policyapply.PolicyBucketEnv) {
		t.Fatalf("missing setting: %d %q", code, errb)
	}
	// --bucket overrides a missing setting, and needs no range.
	if out, errb, code := e.run("show", "--bucket", listSetBucket); code != 0 || errb != "" || !strings.Contains(out, "generation") {
		t.Fatalf("--bucket: %d %q %q", code, out, errb)
	}
	// --bucket overrides a setting that names another bucket.
	e.env[policyapply.PolicyBucketEnv] = "other_{range}_policy"
	if _, errb, code := e.run("show", "--bucket", listSetBucket); code != 0 || errb != "" {
		t.Fatalf("--bucket over setting: %d %q", code, errb)
	}
	if _, errb, code := e.run("show"); code != 1 || !strings.Contains(errb, "other_foo_policy") {
		t.Fatalf("setting names another bucket: %d %q", code, errb)
	}
	// A setting without {range} needs no --range, and refuses one.
	e.env[policyapply.PolicyBucketEnv] = listSetBucket
	if _, errb, code := e.run("show", "--range", "foo"); code != 1 || !strings.Contains(errb, "{range}") {
		t.Fatalf("range without placeholder: %d %q", code, errb)
	}
	if _, errb, code := e.run("show", "--bucket", listSetBucket); code != 0 || errb != "" {
		t.Fatalf("fixed setting: %d %q", code, errb)
	}
	// An invalid value is refused and never shown.
	e.env[policyapply.PolicyBucketEnv] = secret
	if _, errb, code := e.run("show", "--range", "foo"); code != 1 || strings.Contains(errb, secret) || !strings.Contains(errb, "not shown") {
		t.Fatalf("invalid setting: %d %q", code, errb)
	}
	e.env[policyapply.PolicyBucketEnv] = listSetPattern
	if _, errb, code := e.run("show", "--bucket", secret); code != 1 || strings.Contains(errb, secret) || !strings.Contains(errb, "not shown") {
		t.Fatalf("invalid --bucket: %d %q", code, errb)
	}
}

func TestListSetExplain(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	out, errb, code := e.run("explain", "foo.c.core", "foo.d.build")
	if code != 0 || errb != "" {
		t.Fatalf("explain allowed: %d\n%s\n%s", code, out, errb)
	}
	e.golden("explain_allowed", out)

	out, errb, code = e.run("explain", "foo.c.lead", "foo.c.core")
	if code != 0 || errb != "" {
		t.Fatalf("explain refused: %d\n%s\n%s", code, out, errb)
	}
	e.golden("explain_refused", out)

	out, _, code = e.run("explain", "foo.c.lead", "foo.c.core", "--json")
	var v map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v["explanation"] == nil {
		t.Fatalf("explain --json: %d\n%s", code, out)
	}
}

func TestListSetExplainInvalidArgumentIsNotEchoed(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	secret := "SECRET-VALUE-not-an-address"
	for _, args := range [][]string{{secret, "foo.c.core"}, {"foo.c.core", secret}, {"foo.*.core", "foo.c.core"}} {
		out, errb, code := e.run("explain", args...)
		if code != 2 || !strings.Contains(errb, "the value is not shown") || strings.Contains(out+errb, "SECRET") || strings.Contains(errb, "foo.*") {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
	}
	if _, errb, code := e.run("explain", "foo.c.core"); code != 2 || !strings.Contains(errb, "usage:") {
		t.Fatalf("one argument: %d %q", code, errb)
	}
}

func TestListSetDiffNarrowing(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	next := listSetDoc(t, e.acctPub, 8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.core"] = map[string]any{"allow": []string{}, "deny": []string{}}
	})
	f := e.file("next.json", next)
	out, errb, code := e.run("diff", f)
	if code != 0 || errb != "" || !strings.HasPrefix(out, "accepted") {
		t.Fatalf("diff: %d\n%s\n%s", code, out, errb)
	}
	e.golden("diff_narrowing", out)

	out, _, code = e.run("diff", f, "--json")
	var v map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil {
		t.Fatalf("diff --json: %d\n%s", code, out)
	}
}

func TestListSetDiffRefused(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	// An entry naming an address outside the range is refused by the checker.
	next := listSetDoc(t, e.acctPub, 8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.core"] = map[string]any{"allow": []string{"bar.d.build"}, "deny": []string{}}
	})
	out, errb, code := e.run("diff", e.file("refused.json", next))
	if code != 1 || !strings.HasPrefix(out, "refused") {
		t.Fatalf("diff: %d\n%s\n%s", code, out, errb)
	}
	e.golden("diff_refused", out)
}

func TestListSetDiffStaleBase(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	out, errb, code := e.run("diff", e.file("stale.json", listSetDoc(t, e.acctPub, 4, 3, nil)))
	if code != 1 || !strings.Contains(out, "[base]") {
		t.Fatalf("diff: %d\n%s\n%s", code, out, errb)
	}
	e.golden("diff_stale", out)
}

func TestListSetDiffInputErrors(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	if _, errb, code := e.run("diff", e.file("junk.json", []byte("junk"))); code != 1 || errb == "" {
		t.Fatalf("junk: %d %q", code, errb)
	}
	if _, errb, code := e.run("diff", filepath.Join(e.root, "missing.json")); code != 1 || errb == "" {
		t.Fatalf("missing: %d %q", code, errb)
	}
	// --submitter makes an unsigned file an error; a bad key is a usage error that does not show it.
	f := e.file("raw.json", listSetDoc(t, e.acctPub, 8, 7, nil))
	if _, errb, code := e.run("diff", f, "--submitter", e.acctPub); code != 1 || !strings.Contains(errb, "not signed") {
		t.Fatalf("unsigned with submitter: %d %q", code, errb)
	}
	if _, errb, code := e.run("diff", f, "--submitter", "SECRET"); code != 2 || strings.Contains(errb, "SECRET") {
		t.Fatalf("bad submitter: %d %q", code, errb)
	}
	if _, errb, code := e.run("diff", f, "--account", "SECRET"); code != 1 || strings.Contains(errb, "SECRET") {
		t.Fatalf("bad account: %d %q", code, errb)
	}
}
