package policydirect

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyintake"
	"github.com/bogworks/mash/internal/policyserve"
)

func TestExitCode(t *testing.T) {
	want := map[string]int{
		OutcomeApplied: 0, OutcomePlanned: 0, OutcomeUnchanged: 0,
		OutcomeInvalid: 1, OutcomeUnsupported: 1, OutcomeStale: 1, OutcomeBlocked: 1,
		OutcomeSignatureFailed: 4, OutcomePartial: 5, OutcomeError: 6, "something else": 6, "": 6,
	}
	for o, code := range want {
		if got := (Result{Outcome: o}).ExitCode(); got != code {
			t.Errorf("outcome %q: exit %d, want %d", o, got, code)
		}
	}
}

// constStrings reads the string constants named name* (and, with typ, of that type) of the Go files of dir.
func constStrings(t *testing.T, dir, prefix, typ string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, p := range pkgs {
		for _, f := range p.Files {
			for _, d := range f.Decls {
				gd, ok := d.(*ast.GenDecl)
				if !ok || gd.Tok != token.CONST {
					continue
				}
				for _, sp := range gd.Specs {
					vs := sp.(*ast.ValueSpec)
					if typ != "" {
						id, ok := vs.Type.(*ast.Ident)
						if !ok || id.Name != typ {
							continue
						}
					}
					for i, n := range vs.Names {
						if !strings.HasPrefix(n.Name, prefix) || i >= len(vs.Values) {
							continue
						}
						if bl, ok := vs.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
							s, err := strconv.Unquote(bl.Value)
							if err != nil {
								t.Fatal(err)
							}
							out[filepath.Base(dir)+"."+n.Name] = s
						}
					}
				}
			}
		}
	}
	return out
}

// Every stage the engine can name, in the packages that name them, has exactly one outcome in the table; so has every
// state of a status. A new stage or state in any of them fails here until a decision is made for it.
func TestEveryStageMapsToOneOutcome(t *testing.T) {
	known := map[string]bool{}
	for _, dir := range []string{"../listset", "../policyapply", "../policydoc", "../policyintake", "../policyserve"} {
		consts := constStrings(t, dir, "Stage", "")
		for name, v := range consts {
			known[v] = true
			o, ok := stageOutcomes[v]
			if !ok {
				t.Errorf("%s = %q has no outcome in stageOutcomes: decide one", name, v)
				continue
			}
			if got := outcomeOfStage(v); got != o {
				t.Errorf("%s: outcomeOfStage = %q, table says %q", name, got, o)
			}
		}
	}
	if len(known) < 10 {
		t.Fatalf("found only %d stage values: the scan is not reading the packages", len(known))
	}
	for stage, o := range stageOutcomes {
		if !known[stage] {
			t.Errorf("stageOutcomes names %q (%s), which no package defines", stage, o)
		}
		if (Result{Outcome: o}).ExitCode() == 6 && o != OutcomeError {
			t.Errorf("stage %q maps to %q, which is in no exit group", stage, o)
		}
	}
	states := constStrings(t, "../policyapply", "State", "State")
	if len(states) != 4 {
		t.Fatalf("states found: %v", states)
	}
	for name, v := range states {
		st := policyapply.Status{State: policyapply.State(v), Generation: 3}
		if st.State == policyapply.StateRefused {
			continue // by its refusals, below
		}
		if _, ok := stateOutcomes[st.State]; !ok {
			t.Errorf("%s = %q has no outcome in stateOutcomes: decide one", name, v)
		}
		o, _, msg := outcomeOfStatus(st)
		if o == OutcomeError || msg == "" {
			t.Errorf("state %s gives outcome %q, message %q", v, o, msg)
		}
	}
	for stage, want := range stageOutcomes {
		st := policyapply.Status{State: policyapply.StateRefused, Refusals: []listset.Refusal{{Stage: stage, Reason: "why"}}}
		if o, s, _ := outcomeOfStatus(st); o != want || s != stage {
			t.Errorf("a refusal at stage %q gives %q at %q, want %q", stage, o, s, want)
		}
	}
	// Anything unknown is an error, never a guess.
	if o, _, _ := outcomeOfStatus(policyapply.Status{State: "mystery"}); o != OutcomeError {
		t.Errorf("an unknown state gives %q", o)
	}
	if o := outcomeOfStage("mystery"); o != OutcomeError {
		t.Errorf("an unknown stage gives %q", o)
	}
}

// fakeLedger is a MemLedger that keeps the statuses written to it.
type fakeLedger struct {
	*policyapply.MemLedger
	mu     sync.Mutex
	status *policyapply.Status
}

func (l *fakeLedger) WriteRefusal(_ context.Context, st policyapply.Status) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status = &st
	return nil
}

func (l *fakeLedger) LatestStatus(context.Context) (policyapply.Status, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.status == nil {
		return policyapply.Status{}, false, nil
	}
	return *l.status, true, nil
}

// scriptApplier is the engine of a test: it returns what the test says and counts its calls.
type scriptApplier struct {
	mu     sync.Mutex
	calls  int
	res    policyintake.Result
	err    error
	status *policyapply.Status // written to the ledger before returning
	ledger *fakeLedger
	before []string // the steps it asks the guard about
}

func (a *scriptApplier) Apply(ctx context.Context, d policyintake.Doc, g policyintake.Guard) (policyintake.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	for _, s := range a.before {
		if err := g.Before(ctx, s); err != nil {
			return policyintake.Result{}, err
		}
	}
	if a.status != nil {
		a.ledger.mu.Lock()
		a.ledger.status = a.status
		a.ledger.mu.Unlock()
	}
	return a.res, a.err
}

// harness counts what the run touches.
type harness struct {
	t          *testing.T
	adopted    bool
	adoptedErr error
	ledger     *fakeLedger
	// noBucket makes opening without create fail as a missing bucket does; opens records the create flags.
	noBucket  bool
	opens     []bool
	locks     int
	released  int
	heldErr   error
	serverErr error
	servers   int
	app       *scriptApplier
}

const testAccount = "ACCTTESTKEY"

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, ledger: &fakeLedger{MemLedger: policyapply.NewMemLedger(listset.Record{})}}
	h.app = &scriptApplier{ledger: h.ledger}
	return h
}

func (h *harness) env() env {
	return env{
		account: testAccount,
		adopted: func(context.Context) (bool, error) { return h.adopted, h.adoptedErr },
		lock: func(context.Context) (func(), func(context.Context) error, error) {
			h.locks++
			return func() { h.released++ }, func(context.Context) error { return h.heldErr }, nil
		},
		openLedger: func(_ context.Context, create bool) (ledger, error) {
			h.opens = append(h.opens, create)
			if h.noBucket && !create {
				return nil, fmt.Errorf("%w (the bucket does not exist)", policyapply.ErrNoPolicyBucket)
			}
			h.noBucket = false // created
			return h.ledger, nil
		},
		servers: func(context.Context) ([]policyserve.ServerInfo, error) {
			h.servers++
			if h.serverErr != nil {
				return nil, h.serverErr
			}
			return []policyserve.ServerInfo{
				{Name: "s1", URL: "nats://s1", Limits: listset.ServerLimits{MaxConnectLine: 4096, MaxAccountJWT: 1 << 20}},
				{Name: "s2", URL: "nats://s2", Limits: listset.ServerLimits{MaxConnectLine: 4096, MaxAccountJWT: 1 << 20}},
			}, nil
		},
		applier: func(context.Context, ledger, policyintake.Guard) (policyintake.Applier, error) { return h.app, nil },
	}
}

// docJSON is a list-set document for testAccount; mod may change its fields.
func docJSON(gen, base int, mod func(m map[string]any)) []byte {
	m := map[string]any{
		"schema": policydoc.DocumentSchema, "account": testAccount, "generation": gen, "base_generation": base,
		"issued_at": "2026-11-14T12:00:00Z",
		"groups":    map[string]any{"c": map[string]any{"mail": "closed", "prompt": "closed", "status": "shared", "heartbeat": "closed"}},
		"senders":   map[string]any{},
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

func TestAdoptedAccountIsBlocked(t *testing.T) {
	for _, dry := range []bool{false, true} {
		h := newHarness(t)
		h.adopted = true
		r := run(context.Background(), h.env(), docJSON(1, 0, nil), Options{DryRun: dry})
		if r.Outcome != OutcomeBlocked || r.Stage != policyintake.StageAdoption || !r.Refused || r.ExitCode() != 1 {
			t.Fatalf("dry=%v: %+v", dry, r)
		}
		if len(h.opens) != 0 || h.app.calls != 0 || h.servers != 0 {
			t.Errorf("dry=%v: an adopted account was touched: opens %v, applier %d, servers %d", dry, h.opens, h.app.calls, h.servers)
		}
		if h.locks != h.released {
			t.Errorf("dry=%v: lock taken %d times and released %d", dry, h.locks, h.released)
		}
		if !strings.Contains(r.Message, "adopted") {
			t.Errorf("message %q", r.Message)
		}
	}
}

func TestUnreadableMarkerRefuses(t *testing.T) {
	h := newHarness(t)
	h.adoptedErr = errors.New("connection reset")
	r := run(context.Background(), h.env(), docJSON(1, 0, nil), Options{})
	if r.Outcome != OutcomeError || r.ExitCode() != 6 || h.app.calls != 0 || len(h.opens) != 0 {
		t.Fatalf("%+v, applier %d, opens %v", r, h.app.calls, h.opens)
	}
}

func TestUnsignedPlainJSONIsTheInput(t *testing.T) {
	h := newHarness(t)
	r := run(context.Background(), h.env(), docJSON(1, 0, nil), Options{DryRun: true})
	if r.Outcome != OutcomePlanned || r.ExitCode() != 0 {
		t.Fatalf("an unsigned document: %+v", r)
	}
	var sig *Check
	for i := range r.Checks {
		if r.Checks[i].Name == CheckSignature {
			sig = &r.Checks[i]
		}
	}
	if sig == nil || sig.Result != ResultNotApplicable {
		t.Errorf("the signature check: %+v", sig)
	}
}

func TestCompactJWSIsInvalidNotASignatureFailure(t *testing.T) {
	enc := base64.RawURLEncoding
	kp, _ := nkeys.CreateAccount()
	in := enc.EncodeToString([]byte(`{"alg":"EdDSA","typ":"JWT"}`)) + "." + enc.EncodeToString(docJSON(1, 0, nil))
	sig, _ := kp.Sign([]byte(in))
	jws := []byte(in + "." + enc.EncodeToString(sig))
	for _, dry := range []bool{false, true} {
		h := newHarness(t)
		r := run(context.Background(), h.env(), jws, Options{DryRun: dry})
		if r.Outcome != OutcomeInvalid || r.Outcome == OutcomeSignatureFailed || r.ExitCode() != 1 || r.Stage != policyintake.StageDocument {
			t.Fatalf("dry=%v: %+v", dry, r)
		}
		if !strings.Contains(r.Message, "compact JWS") || !strings.Contains(r.Message, "plain JSON") {
			t.Errorf("message %q", r.Message)
		}
		if h.app.calls != 0 {
			t.Error("the engine ran")
		}
	}
	// Garbage and an empty document are invalid too.
	for _, raw := range []string{"", "   ", "not json", `{"schema":"nope"}`} {
		r := run(context.Background(), newHarness(t).env(), []byte(raw), Options{DryRun: true})
		if r.Outcome != OutcomeInvalid {
			t.Errorf("%q: %+v", raw, r)
		}
	}
}

func TestDocumentForAnotherAccountAndNextSubmitterAreInvalid(t *testing.T) {
	r := run(context.Background(), newHarness(t).env(), docJSON(1, 0, func(m map[string]any) { m["account"] = "OTHER" }), Options{DryRun: true})
	if r.Outcome != OutcomeInvalid || r.Stage != policyintake.StageAccount {
		t.Errorf("another account: %+v", r)
	}
	next, _ := nkeys.CreateAccount()
	pub, _ := next.PublicKey()
	r = run(context.Background(), newHarness(t).env(), docJSON(1, 0, func(m map[string]any) { m["next_submitter"] = pub }), Options{DryRun: true})
	if r.Outcome != OutcomeInvalid || !strings.Contains(r.Message, "next_submitter") {
		t.Errorf("next_submitter: %+v", r)
	}
}

// A dry run makes no bucket, takes no lock, calls no engine and writes nothing: it says what would happen.
func TestDryRunCreatesNothingAndTouchesNoServer(t *testing.T) {
	h := newHarness(t)
	h.noBucket = true
	r := run(context.Background(), h.env(), docJSON(1, 0, nil), Options{DryRun: true})
	if r.Outcome != OutcomePlanned || !r.DryRun || r.ExitCode() != 0 || r.Refused {
		t.Fatalf("%+v", r)
	}
	if !slices.Equal(h.opens, []bool{false}) || !h.noBucket {
		t.Errorf("the bucket was opened with create flags %v (bucket still missing: %v)", h.opens, h.noBucket)
	}
	if h.locks != 0 || h.app.calls != 0 {
		t.Errorf("a dry run took the lock %d times and ran the engine %d times", h.locks, h.app.calls)
	}
	if r.Plan == nil || r.Plan.Refused || r.Generation != 1 {
		t.Errorf("plan %+v, generation %d", r.Plan, r.Generation)
	}
	if h.ledger.status != nil {
		t.Error("a dry run wrote a status")
	}
	if rec, _ := h.ledger.InForce(); rec.Generation != 0 {
		t.Error("a dry run changed the record in force")
	}
	// The result is JSON as it stands.
	b, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(b), `"outcome":"planned"`) {
		t.Errorf("json %s, %v", b, err)
	}
}

func TestDryRunStaleBaseAndInvalidAndSizeAndInbox(t *testing.T) {
	h := newHarness(t)
	r := run(context.Background(), h.env(), docJSON(2, 1, nil), Options{DryRun: true})
	if r.Outcome != OutcomeStale || r.Stage != policydoc.StageBase || !r.Refused || r.ExitCode() != 1 {
		t.Errorf("stale: %+v", r)
	}
	if !strings.Contains(r.Message, "generation 1") || !strings.Contains(r.Message, "in force") {
		t.Errorf("the engine's message was not kept: %q", r.Message)
	}
	// A generation that is not base+1 breaks a rule.
	r = run(context.Background(), newHarness(t).env(), docJSON(5, 0, nil), Options{DryRun: true})
	if r.Outcome != OutcomeInvalid {
		t.Errorf("bad generation: %+v", r)
	}
	// A list set that does not validate.
	r = run(context.Background(), newHarness(t).env(), docJSON(1, 0, func(m map[string]any) {
		m["senders"] = map[string]any{"nodot": map[string]any{"allow": []string{}, "deny": []string{}}}
	}), Options{DryRun: true})
	if r.Outcome != OutcomeInvalid || r.Plan == nil || !r.Plan.Refused {
		t.Errorf("invalid list set: %+v", r)
	}
	// An added address has no inbox: the fake inbox checker knows none.
	r = run(context.Background(), newHarness(t).env(), docJSON(1, 0, func(m map[string]any) {
		m["senders"] = map[string]any{"c.core": map[string]any{"allow": []string{}, "deny": []string{}}}
	}), Options{DryRun: true})
	if r.Outcome != OutcomeBlocked || r.Stage != policydoc.StageInbox || !strings.Contains(r.Message, "no inbox") {
		t.Errorf("no inbox: %+v", r)
	}
	// Servers that do not answer are an error, with nothing touched.
	h = newHarness(t)
	h.serverErr = errors.New("fewer servers answered")
	r = run(context.Background(), h.env(), docJSON(1, 0, nil), Options{DryRun: true})
	if r.Outcome != OutcomeError || r.ExitCode() != 6 {
		t.Errorf("servers: %+v", r)
	}
	// A size over a server's limit blocks.
	h = newHarness(t)
	e := h.env()
	e.servers = func(context.Context) ([]policyserve.ServerInfo, error) {
		return []policyserve.ServerInfo{{Name: "s1", URL: "nats://s1", Limits: listset.ServerLimits{MaxConnectLine: 8, MaxAccountJWT: 1 << 20}}}, nil
	}
	r = run(context.Background(), e, docJSON(1, 0, func(m map[string]any) {
		m["senders"] = map[string]any{"c.core": map[string]any{"allow": []string{}, "deny": []string{}}}
	}), Options{DryRun: true})
	if r.Outcome != OutcomeBlocked || r.Stage != policyserve.StageSize || r.ExitCode() != 1 {
		t.Errorf("tiny limit: %+v", r)
	}
}

func TestRealRunMapsWhatTheEngineReturns(t *testing.T) {
	gen := uint64(1)
	cases := []struct {
		name    string
		res     policyintake.Result
		err     error
		status  *policyapply.Status
		outcome string
		stage   string
		contain string
	}{
		{name: "applied", res: policyintake.Result{StatusWritten: true}, status: &policyapply.Status{Generation: 1, State: policyapply.StateApplied, InForce: true}, outcome: OutcomeApplied},
		{name: "applied with no status", outcome: OutcomeApplied},
		{name: "stale", err: &listset.StaleBaseError{Stated: 0, InForce: 4}, outcome: OutcomeStale, stage: policydoc.StageBase, contain: "generation 4 is in force"},
		{name: "pending", err: fmt.Errorf("x: %w", policyapply.ErrPendingGeneration), outcome: OutcomeStale, stage: policydoc.StagePending},
		{name: "bad generation", err: policyapply.ErrBadGeneration, outcome: OutcomeInvalid, stage: policyintake.StageDocument},
		{name: "jwt too large", err: policyapply.ErrAccountJWTTooLarge, outcome: OutcomeBlocked, stage: policyserve.StageSize},
		{
			name: "size", res: policyintake.Result{StatusWritten: true}, err: errors.New("the list set does not fit the servers' limits (see the status)"),
			status:  &policyapply.Status{Generation: 1, State: policyapply.StateRefused, Refusals: []listset.Refusal{{Stage: policyserve.StageSize, Entry: "c.core", Reason: "too long"}}},
			outcome: OutcomeBlocked, stage: policyserve.StageSize, contain: "too long",
		},
		{
			name: "validate", res: policyintake.Result{StatusWritten: true}, err: errors.New("not applied"),
			status:  &policyapply.Status{Generation: 1, State: policyapply.StateRefused, Refusals: []listset.Refusal{{Stage: listset.StageValidate, Reason: "bad"}}},
			outcome: OutcomeInvalid, stage: listset.StageValidate,
		},
		{
			name: "unchanged by the engine", res: policyintake.Result{StatusWritten: true}, err: errors.New("not applied"),
			status:  &policyapply.Status{Generation: 1, State: policyapply.StateRefused, Refusals: []listset.Refusal{{Stage: policyapply.StageUnchanged, Reason: "same"}}},
			outcome: OutcomeUnchanged, stage: policyapply.StageUnchanged,
		},
		{
			name: "partial", res: policyintake.Result{StatusWritten: true}, err: errors.New("generation 1 was not applied"),
			status: &policyapply.Status{
				Generation: 1, State: policyapply.StatePartial,
				Servers: []policyapply.ServerGeneration{{Server: "s1", Generation: &gen}, {Server: "s2"}},
				Failing: []policyapply.Failure{{Server: "s2", Check: policyapply.CheckGeneration, Detail: "holds nothing"}},
			},
			outcome: OutcomePartial, contain: "s2",
		},
		{
			name: "applying", res: policyintake.Result{StatusWritten: true}, err: errors.New("stopped"),
			status:  &policyapply.Status{Generation: 1, State: policyapply.StateApplying},
			outcome: OutcomePartial,
		},
		{
			name: "applied then a later step failed", res: policyintake.Result{StatusWritten: true}, err: errors.New("cleanup failed"),
			status:  &policyapply.Status{Generation: 1, State: policyapply.StateApplied},
			outcome: OutcomeApplied, contain: "cleanup failed",
		},
		{name: "plain error", err: errors.New("open the witness connections: refused; nothing was pushed"), outcome: OutcomeError, stage: StageApply, contain: "witness"},
		{name: "guard refused", err: errors.New("the account lock is no longer held"), outcome: OutcomeError, stage: StageApply},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.app.res, h.app.err, h.app.status = c.res, c.err, c.status
			r := run(context.Background(), h.env(), docJSON(1, 0, nil), Options{})
			if r.Outcome != c.outcome || r.Stage != c.stage {
				t.Fatalf("outcome %q stage %q: %+v", r.Outcome, r.Stage, r)
			}
			if !strings.Contains(r.Message, c.contain) {
				t.Errorf("message %q lacks %q", r.Message, c.contain)
			}
			if r.Refused != refusedOutcome(c.outcome) {
				t.Errorf("refused = %v for %s", r.Refused, c.outcome)
			}
			if h.app.calls != 1 || h.locks != 1 || h.released != 1 {
				t.Errorf("engine calls %d, locks %d, released %d", h.app.calls, h.locks, h.released)
			}
			if !slices.Equal(h.opens, []bool{true}) {
				t.Errorf("the ledger was opened with create flags %v", h.opens)
			}
			if c.status != nil && (r.Status == nil || r.Status.State != c.status.State) {
				t.Errorf("status %+v", r.Status)
			}
		})
	}
}

// A status the engine did not write in this run is never taken for its result.
func TestAStatusFromAnEarlierRunIsNotUsed(t *testing.T) {
	h := newHarness(t)
	h.ledger.status = &policyapply.Status{Generation: 9, State: policyapply.StateApplied}
	h.app.err = errors.New("the servers did not answer")
	r := run(context.Background(), h.env(), docJSON(1, 0, nil), Options{})
	if r.Outcome != OutcomeError || r.Status != nil {
		t.Fatalf("%+v", r)
	}
}

// The guard the engine is given checks the account lock before each protected write and keeps refusing once it has.
func TestGuardChecksTheAccountLock(t *testing.T) {
	h := newHarness(t)
	h.app.before = []string{"push"}
	h.heldErr = errors.New("the lock expired")
	r := run(context.Background(), h.env(), docJSON(1, 0, nil), Options{})
	if r.Outcome != OutcomeError || !strings.Contains(r.Message, "no longer held") || !strings.Contains(r.Message, "push") {
		t.Fatalf("%+v", r)
	}
	g := &lockGuard{held: func(context.Context) error { return h.heldErr }}
	if err := g.Before(context.Background(), "commit"); err == nil {
		t.Fatal("the guard allowed a write without the lock")
	}
	h.heldErr = nil
	if err := g.Before(context.Background(), "commit"); err == nil {
		t.Error("the guard allowed a write after it refused")
	}
	ok := &lockGuard{held: func(context.Context) error { return nil }}
	if err := ok.Before(context.Background(), "commit"); err != nil {
		t.Error(err)
	}
}

func TestReplayOfTheDocumentInForceIsUnchanged(t *testing.T) {
	raw := docJSON(1, 0, nil)
	d, err := policydoc.Decode(raw, policydoc.DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rec, _, err := listset.Accept(listset.Record{}, d.ListSet, raw)
	if err != nil {
		t.Fatal(err)
	}
	ls := d.ListSet
	h := newHarness(t)
	h.ledger = &fakeLedger{MemLedger: policyapply.NewMemLedgerWith(rec, policyapply.InForceDetail{ListSet: &ls})}
	h.app.ledger = h.ledger
	for _, dry := range []bool{false, true} {
		r := run(context.Background(), h.env(), raw, Options{DryRun: dry})
		if r.Outcome != OutcomeUnchanged || r.ExitCode() != 0 || r.Generation != 1 || r.Refused {
			t.Fatalf("dry=%v: %+v", dry, r)
		}
	}
	if h.app.calls != 0 {
		t.Error("the engine ran for the document in force")
	}
}

func TestConfigCheck(t *testing.T) {
	r, err := Apply(context.Background(), Config{}, docJSON(1, 0, nil), Options{})
	if !errors.Is(err, errConfig) || r.Outcome != OutcomeError || r.ExitCode() != 6 {
		t.Fatalf("%+v, %v", r, err)
	}
}
