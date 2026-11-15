package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyserve"
	"github.com/nats-io/nkeys"
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
	e := newApplyEnv(t)
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
	unlock, err := l.Lock(ctx) // seeding is a writer: it takes the lease the readers never do
	if err != nil {
		e.t.Fatal(err)
	}
	defer unlock()
	if doc != nil {
		d, err := policydoc.Decode(doc, policydoc.DecodeOptions{})
		if err != nil {
			e.t.Fatal(err)
		}
		c, err := listset.Compile(d.ListSet)
		if err != nil {
			e.t.Fatal(err)
		}
		rec := listset.Record{Generation: gen, Received: listset.Digest{1}, Content: listset.Digest{2}, Signed: listset.DigestCompiled(c)}
		if err := l.CompareAndCommitDetail(0, rec, policyapply.InForceDetail{JWT: "jwt", ListSet: &d.ListSet}); err != nil {
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

// seedAdopted is seedInForce on an adopted account: the intake refuses every submission to one that is not.
func (e *listSetEnv) seedAdopted() {
	e.t.Helper()
	e.seedInForce()
	if o, eb, c := e.applyEnv.run("policy", "adopt", "--submitter", userPub(e.t)); c != 0 {
		e.t.Fatalf("adopt: %d %q %q", c, o, eb)
	}
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
	// A setting without {range} needs no --range, and accepts one (it fills nothing).
	e.env[policyapply.PolicyBucketEnv] = listSetBucket
	if _, errb, code := e.run("show", "--range", "foo"); code != 0 || errb != "" {
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

// A change that narrows an existing sender by tags is refused at stage narrowing with the apply's text: `policy serve`
// would refuse it before pushing anything.
func TestListSetDiffNarrowing(t *testing.T) {
	e := newListSetEnv(t)
	e.seedAdopted()
	next := listSetDoc(t, e.acctPub, 8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.core"] = map[string]any{"allow": []string{}, "deny": []string{}}
	})
	f := e.file("next.json", next)
	out, errb, code := e.run("diff", f)
	if code != 1 || errb != "" || !strings.HasPrefix(out, "refused: nothing would change\n  [narrowing] narrowing: "+policyserve.NarrowingDetail) {
		t.Fatalf("diff: %d\n%s\n%s", code, out, errb)
	}
	e.golden("diff_narrowing", out)

	out, _, code = e.run("diff", f, "--json")
	var v map[string]any
	if code != 1 || json.Unmarshal([]byte(out), &v) != nil || v["refused"] != true {
		t.Fatalf("diff --json: %d\n%s", code, out)
	}
}

// A change that does not narrow is accepted, and the plan says what the apply does not do.
func TestListSetDiffChange(t *testing.T) {
	e := newListSetEnv(t)
	e.seedAdopted()
	f := e.file("next.json", listSetDoc(t, e.acctPub, 8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.lead"] = map[string]any{"allow": []string{"foo.c.core"}, "deny": []string{}}
	}))
	out, errb, code := e.run("diff", f)
	if code != 0 || errb != "" || !strings.HasPrefix(out, "accepted: this would change\n"+policydoc.SizeLimitsLine+"\n") ||
		!strings.Contains(out, "new credential (not issued by the apply: request it from the issue service, `mashctl issue serve`)") {
		t.Fatalf("diff: %d\n%s\n%s", code, out, errb)
	}
	e.golden("diff_change", out)

	out, _, code = e.run("diff", f, "--json")
	var v map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v["size_limits"] != "default" {
		t.Fatalf("diff --json: %d\n%s", code, out)
	}
}

// A valid document for another range than the one selected is refused at stage range, with both ranges named.
func TestListSetDiffOtherRange(t *testing.T) {
	e := newListSetEnv(t)
	e.seedAdopted()
	f := e.file("bar.json", listSetDoc(t, e.acctPub, 8, 7, func(m map[string]any) { m["range"] = "bar" }))
	out, errb, code := e.run("diff", f)
	if code != 1 || !strings.Contains(out, "[range]") || !strings.Contains(out, "bar") || !strings.Contains(out, listSetRange) || strings.Contains(out, "accepted") {
		t.Fatalf("diff: %d\n%s\n%s", code, out, errb)
	}
	e.golden("diff_range", out)
	out, _, code = e.run("diff", f, "--json")
	if code != 1 || !strings.Contains(out, `"stage": "range"`) {
		t.Fatalf("diff --json: %d\n%s", code, out)
	}
}

// A bad --account or --range is a usage error (exit 2) before any connection, and the value is never shown.
func TestListSetBadScopeIsUsageError(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	f := e.file("next.json", listSetDoc(t, e.acctPub, 8, 7, nil))
	for _, args := range [][]string{
		{"show", "--account", "SECRETACCT"}, {"show", "--range", "SECRET_RANGE"},
		{"explain", "foo.c.core", "foo.c.core", "--account", "SECRETACCT"}, {"explain", "foo.c.core", "foo.c.core", "--range", "SECRET_RANGE"},
		{"diff", f, "--account", "SECRETACCT"}, {"diff", f, "--range", "SECRET_RANGE"},
	} {
		e.env["MASH_ADMIN_CREDS"] = "/nonexistent" // a connection attempt would fail with exit 1
		_, errb, code := e.run(args[0], args[1:]...)
		if code != 2 || !strings.Contains(errb, "the value is not shown") || strings.Contains(errb, "SECRET") {
			t.Fatalf("%v: %d %q", args, code, errb)
		}
	}
}

func TestListSetDiffRefused(t *testing.T) {
	e := newListSetEnv(t)
	e.seedAdopted()
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
	e.seedAdopted()
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
	if _, errb, code := e.run("diff", f, "--account", "SECRET"); code != 2 || strings.Contains(errb, "SECRET") {
		t.Fatalf("bad account: %d %q", code, errb)
	}
}

// runPolicyVia runs the verb through runPolicy, the way mashctl dispatches it, with the account and range of the seed.
func (e *listSetEnv) runPolicyVia(args ...string) (string, string, int) {
	e.t.Helper()
	var out, errb bytes.Buffer
	env := func(k string) string { return e.env[k] }
	lookup := func(k string) (string, bool) { v, ok := e.env[k]; return v, ok && v != "" }
	args = append(args, "--account", e.acctPub, "--range", listSetRange)
	code := runPolicy(args, env, lookup, &out, &errb)
	return out.String(), errb.String(), code
}

// The verbs are reached through runPolicy, not only through their handlers.
func TestRunPolicyListSetVerbs(t *testing.T) {
	e := newListSetEnv(t)
	e.seedAdopted()

	out, errb, code := e.runPolicyVia("show")
	if code != 0 || errb != "" || e.masked(out) != e.masked(mustRun(t, e, "show")) {
		t.Fatalf("show: %d\n%s\n%s", code, out, errb)
	}
	out, errb, code = e.runPolicyVia("show", "--json")
	var v map[string]any
	if code != 0 || errb != "" || json.Unmarshal([]byte(out), &v) != nil || v["list_set_in_force"] != true {
		t.Fatalf("show --json: %d\n%s\n%s", code, out, errb)
	}

	out, errb, code = e.runPolicyVia("explain", "foo.c.core", "foo.c.core")
	if code != 0 || errb != "" || out == "" {
		t.Fatalf("explain: %d\n%s\n%s", code, out, errb)
	}
	if want := mustRun(t, e, "explain", "foo.c.core", "foo.c.core"); out != want {
		t.Fatalf("explain differs from the handler:\n%s\n%s", out, want)
	}
	if _, errb, code = e.runPolicyVia("explain", "foo.c.core"); code != 2 || !strings.Contains(errb, "usage: mashctl policy adopt") {
		t.Fatalf("explain usage: %d %q", code, errb)
	}

	next := listSetDoc(t, e.acctPub, 8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.lead"] = map[string]any{"allow": []string{"foo.c.core"}, "deny": []string{}}
	})
	f := e.file("next.json", next)
	out, errb, code = e.runPolicyVia("diff", f)
	if code != 0 || errb != "" || !strings.HasPrefix(out, "accepted") {
		t.Fatalf("diff: %d\n%s\n%s", code, out, errb)
	}
	out, _, code = e.runPolicyVia("diff", f, "--json")
	if code != 0 || json.Unmarshal([]byte(out), &map[string]any{}) != nil {
		t.Fatalf("diff --json: %d\n%s", code, out)
	}
	if _, errb, code = e.runPolicyVia("diff"); code != 2 || !strings.Contains(errb, "usage:") {
		t.Fatalf("diff usage: %d %q", code, errb)
	}
}

func mustRun(t *testing.T, e *listSetEnv, args ...string) string {
	t.Helper()
	out, errb, code := e.run(args[0], args[1:]...)
	if code != 0 {
		t.Fatalf("%v: %d %s", args, code, errb)
	}
	return out
}

func TestRunPolicyNoArgsListsListSetVerbs(t *testing.T) {
	var out, errb bytes.Buffer
	code := runPolicy(nil, func(string) string { return "" }, func(string) (string, bool) { return "", false }, &out, &errb)
	if code != 2 || out.Len() != 0 {
		t.Fatalf("%d %q", code, out.String())
	}
	for _, v := range []string{"mashctl policy show", "mashctl policy explain", "mashctl policy diff", "MASH_POLICY_BUCKET"} {
		if !strings.Contains(errb.String(), v) {
			t.Fatalf("usage lacks %s:\n%s", v, errb.String())
		}
	}
}

// A diff of a list set identical in content to the one in force is refused at stage unchanged, as the apply refuses it,
// and exits 1.
func TestListSetDiffNothingWouldChange(t *testing.T) {
	e := newListSetEnv(t)
	e.seedAdopted()
	f := e.file("same.json", listSetDoc(t, e.acctPub, 8, 7, nil))
	out, errb, code := e.run("diff", f)
	if code != 1 || errb != "" || out != "refused: nothing would change\n  [unchanged] list_set: same content as generation 7 in force; nothing to apply\n" {
		t.Fatalf("diff: %d\n%q\n%s", code, out, errb)
	}
	e.golden("diff_unchanged", out)
}

// show, explain and diff read the bucket and write nothing: no key is added, no revision is made, no lease is taken.
func TestListSetVerbsWriteNothing(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	kv, err := e.js.KeyValue(ctx, listSetBucket)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		keys, err := kv.Keys(ctx)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(keys)
		var b strings.Builder
		for _, k := range keys {
			en, err := kv.Get(ctx, k)
			if err != nil {
				t.Fatal(err)
			}
			b.WriteString(k + "@" + strconv.FormatUint(en.Revision(), 10) + "\n")
		}
		return b.String()
	}
	before := snapshot()
	f := e.file("next.json", listSetDoc(t, e.acctPub, 8, 7, nil))
	e.run("show")
	e.run("explain", "foo.c.core", "foo.d.build")
	e.run("diff", f)
	if after := snapshot(); after != before {
		t.Fatalf("the bucket changed:\n%s\n%s", before, after)
	}
}

// A document with a next_submitter is never "nothing would change", and the diff makes the intake's checks against the
// adoption marker and the keys mash holds: not adopted, a key mash holds and a good key.
func TestListSetDiffNextSubmitter(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	next := userPub(t)
	withNext := func(k string) string {
		return e.file("next-"+k[:6]+".json", listSetDoc(t, e.acctPub, 8, 7, func(m map[string]any) { m["next_submitter"] = k }))
	}

	// Not adopted: refused at stage adoption.
	out, errb, code := e.run("diff", withNext(next))
	if code != 1 || errb != "" || !strings.Contains(out, "[adoption] adoption: this account is not adopted: list sets are ignored") {
		t.Fatalf("unadopted: %d\n%q\n%s", code, out, errb)
	}

	if o, eb, c := e.applyEnv.run("policy", "adopt", "--submitter", userPub(t)); c != 0 {
		t.Fatalf("adopt: %d %q %q", c, o, eb)
	}
	out, errb, code = e.run("diff", withNext(next))
	if code != 0 || errb != "" || !strings.HasPrefix(out, "accepted: this would change\n"+policydoc.SizeLimitsLine+"\nrotation: next submitter "+next+" would be recorded\n") {
		t.Fatalf("adopted: %d\n%q\n%s", code, out, errb)
	}
	e.golden("diff_rotation", strings.ReplaceAll(out, next, "NEXTKEY"))

	// A key mash holds (the account's own key) is refused at stage document, as the intake would.
	out, errb, code = e.run("diff", withNext(e.acctPub))
	if code != 1 || errb != "" || !strings.Contains(out, "[document] next_submitter:") {
		t.Fatalf("held: %d\n%q\n%s", code, out, errb)
	}
}

// listSetSigned is payload as a JWS signed by kp.
func listSetSigned(t *testing.T, kp nkeys.KeyPair, payload []byte) []byte {
	t.Helper()
	enc := base64.RawURLEncoding
	in := enc.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`)) + "." + enc.EncodeToString(payload)
	sig, err := kp.Sign([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(in + "." + enc.EncodeToString(sig))
}

// --submitter is checked against the adoption marker: an account that is not adopted is refused at stage adoption, a key
// the marker does not accept at stage signature, and the marker's key is accepted.
func TestListSetDiffSubmitterAgainstMarker(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	key := func() (nkeys.KeyPair, string) {
		kp, err := nkeys.CreateUser()
		if err != nil {
			t.Fatal(err)
		}
		pub, _ := kp.PublicKey()
		return kp, pub
	}
	good, goodPub := key()
	bad, badPub := key()
	payload := listSetDoc(t, e.acctPub, 8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.lead"] = map[string]any{"allow": []string{"foo.c.core"}, "deny": []string{}}
	})
	goodF, badF := e.file("good.jwt", listSetSigned(t, good, payload)), e.file("bad.jwt", listSetSigned(t, bad, payload))

	out, errb, code := e.run("diff", goodF, "--submitter", goodPub)
	if code != 1 || errb != "" || !strings.Contains(out, "[adoption] adoption: this account is not adopted") || strings.Contains(out, "accepted") {
		t.Fatalf("not adopted: %d\n%q\n%s", code, out, errb)
	}
	if o, eb, c := e.applyEnv.run("policy", "adopt", "--submitter", goodPub); c != 0 {
		t.Fatalf("adopt: %d %q %q", c, o, eb)
	}
	out, errb, code = e.run("diff", badF, "--submitter", badPub)
	if code != 1 || errb != "" || !strings.Contains(out, "[signature] signature: the adoption marker expects "+goodPub[:8]+"...") || strings.Contains(out, "accepted") {
		t.Fatalf("wrong key: %d\n%q\n%s", code, out, errb)
	}
	out, errb, code = e.run("diff", goodF, "--submitter", goodPub)
	if code != 0 || errb != "" || !strings.Contains(out, "accepted") || strings.Contains(out, "not checked") {
		t.Fatalf("right key: %d\n%q\n%s", code, out, errb)
	}
}

// With nothing in force and a literal bucket name, the range cannot be learned: --range is required (usage error).
func TestListSetDiffNeedsRangeForFirstSet(t *testing.T) {
	e := newListSetEnv(t)
	e.seed(nil, 0, nil)
	f := e.file("first.json", listSetDoc(t, e.acctPub, 1, 0, nil))
	_, errb, code := e.run("diff", f, "--bucket", listSetBucket)
	if code != 2 || !strings.Contains(errb, "--range is needed to check a first list set's range when nothing is in force") {
		t.Fatalf("%d %q", code, errb)
	}
}

// With a literal bucket name --range fills nothing but is the range the first list set is validated against.
func TestListSetDiffLiteralBucketWithRange(t *testing.T) {
	e := newListSetEnv(t)
	e.seed(nil, 0, nil)
	if o, eb, c := e.applyEnv.run("policy", "adopt", "--submitter", userPub(t)); c != 0 {
		t.Fatalf("adopt: %d %q %q", c, o, eb)
	}
	f := e.file("first.json", listSetDoc(t, e.acctPub, 1, 0, nil))
	out, errb, code := e.run("diff", f, "--bucket", listSetBucket, "--range", listSetRange)
	// Nothing is in force and the test account has no directory, so the inbox check is the refusal here: the range passed.
	if code != 1 || errb != "" || !strings.Contains(out, "[inbox]") || strings.Contains(out, "[range]") {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
	// The range is checked: another range is a refusal at stage range.
	out, _, code = e.run("diff", f, "--bucket", listSetBucket, "--range", "bar")
	if code != 1 || !strings.Contains(out, "[range]") {
		t.Fatalf("%d\n%s", code, out)
	}
}

// An account that is not adopted is refused at stage adoption without --submitter too.
func TestListSetDiffNotAdoptedWithoutSubmitter(t *testing.T) {
	e := newListSetEnv(t)
	e.seedInForce()
	out, errb, code := e.run("diff", e.file("next.json", listSetDoc(t, e.acctPub, 8, 7, func(m map[string]any) {
		m["senders"].(map[string]any)["foo.c.lead"] = map[string]any{"allow": []string{"foo.c.core"}, "deny": []string{}}
	})))
	if code != 1 || errb != "" || !strings.Contains(out, "[adoption] adoption: this account is not adopted: list sets are ignored") || strings.Contains(out, "accepted") {
		t.Fatalf("%d\n%s\n%s", code, out, errb)
	}
}

// diff checks a next_submitter against the same held keys the intake gets from `policy serve`: the key store's keys, an
// owner group's signing key and the admin user's key.
func TestListSetDiffNextSubmitterHeldSet(t *testing.T) {
	e := newListSetEnv(t)
	e.seedAdopted()
	gk, err := e.ks.GroupKey("foo.c", true)
	if err != nil {
		t.Fatal(err)
	}
	groupPub, err := gk.PublicKey()
	gk.Wipe()
	if err != nil {
		t.Fatal(err)
	}
	adminPub, err := ctl.AdminUserKey(config.Config{AdminCreds: e.pl.Creds})
	if err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]string{"owner group key": groupPub, "admin user key": adminPub} {
		f := e.file("held-"+key[:6]+".json", listSetDoc(t, e.acctPub, 8, 7, func(m map[string]any) { m["next_submitter"] = key }))
		out, errb, code := e.run("diff", f)
		if code != 1 || errb != "" || !strings.Contains(out, "[document] next_submitter: next_submitter is a key mash holds") {
			t.Fatalf("%s: %d\n%q\n%s", name, code, out, errb)
		}
	}
}
