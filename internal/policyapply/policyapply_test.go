package policyapply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policy"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

type world struct {
	t        *testing.T
	k        keys
	c1, c2   *listset.Compiled
	j1, j2   string
	cluster  *fakeCluster
	prober   fakeProber
	wit      fakeWitnesses
	servers  []string
	expect   Expect
	previous *Candidate
}

func newWorld(t *testing.T, onGen1 ...string) *world {
	t.Helper()
	w := &world{t: t, k: newKeys(t), servers: []string{"s1", "s2"}}
	w.c1, w.c2 = compile(t, set1()), compile(t, set2())
	w.j1, w.j2 = w.k.build(t, w.c1), w.k.build(t, w.c2, w.k.revoked)
	w.cluster = newCluster(w.j1, w.servers...)
	w.prober = fakeProber{cluster: w.cluster, byGen: map[uint64]*listset.Compiled{1: w.c1, 2: w.c2}, allow: map[string]bool{}}
	w.wit = witnessesFor(w.servers, "foo.c.core", "foo.c.docs", "foo.c.sdk")
	var err error
	if w.expect, err = NewExpect(w.c1, w.c2, []string{w.k.revoked}); err != nil {
		t.Fatal(err)
	}
	w.previous = &Candidate{Generation: 1, Compiled: w.c1, Signers: w.k.signers}
	return w
}

func (w *world) gate() GateInput {
	rec, _, _ := listset.Accept(listset.Record{Generation: 1}, set2(), []byte("x"))
	return GateInput{
		Cluster: w.cluster, Prober: w.prober, Witnesses: w.wit, Account: w.k.acctPub,
		Record: rec, Compiled: w.c2, Previous: w.previous, Expect: w.expect, Signers: w.k.signers, OldKeys: w.oldKeys(), Now: fixedNow,
	}
}

// oldKeys is the inventory of the old credentials: core's is the revoked key, the others have none.
func (w *world) oldKeys() map[string][]string {
	return map[string][]string{"foo.c.core": {w.k.revoked}, "foo.c.docs": {}, "foo.c.sdk": {}}
}

func (w *world) run(l Ledger, ls listset.ListSet, received string) RunInput {
	return RunInput{
		Cluster: w.cluster, Prober: w.prober, Witnesses: w.wit, Ledger: l, Account: w.k.acctPub,
		ListSet: ls, Received: []byte(received), Previous: w.c1,
		RevokedKeys: []string{w.k.revoked}, Now: fixedNow,
		Signers: w.k.signers, PreviousSigners: w.k.signers, OldKeys: w.oldKeys(),
		Signer: SignerFuncs{
			SignPreviousFn: func(_ PreviousClaims, revoked []string) (string, error) { return w.k.build(w.t, w.c1, revoked...), nil },
			SignFn: func(_ listset.Record, c *listset.Compiled) (string, error) {
				return w.k.build(w.t, c, w.k.revoked), nil
			},
		},
	}
}

func gens(st Status) map[string]string {
	out := map[string]string{}
	for _, s := range st.Servers {
		if s.Generation == nil {
			out[s.Server] = "null"
		} else {
			out[s.Server] = string(rune('0' + *s.Generation))
		}
	}
	return out
}

func hasFailure(st Status, server, sender, check string) bool {
	for _, f := range st.Failing {
		if f.Server == server && f.Sender == sender && f.Check == check {
			return true
		}
	}
	return false
}

func TestNewExpect(t *testing.T) {
	w := newWorld(t)
	if len(w.expect.Removed) != 5 {
		t.Fatalf("removed = %+v", w.expect.Removed)
	}
	r := w.expect.Removed[0]
	if r.Sender != "foo.c.core" || r.Receiver != "foo.c.sdk" || r.Verb != listset.Mail || r.Probe.Name != "core" {
		t.Errorf("first removed = %+v", r)
	}
	if len(r.Probe.Tags) == 0 {
		t.Error("probe has no old tags")
	}
	// core's allow of docs goes with the tag: the old tags still reach it under the new template.
	if len(w.expect.CredentialOnly) != 4 || w.expect.CredentialOnly[0].Receiver != "foo.c.docs" {
		t.Errorf("credential only = %+v", w.expect.CredentialOnly)
	}
	for _, r := range append(slices.Clone(w.expect.Removed), w.expect.CredentialOnly...) {
		if r.Current.Sender != r.Sender || r.Current.Name == "" {
			t.Errorf("current credential = %+v", r.Current)
		}
	}
	for _, r := range w.expect.CredentialOnly {
		if slices.Equal(r.Current.Tags, r.Probe.Tags) {
			t.Errorf("tag-only narrowing keeps the tags: %+v", r)
		}
	}
	for _, r := range w.expect.Removed {
		if (r.Verb == listset.Heartbeat) != (r.Op == OpSub) {
			t.Errorf("op %s for verb %s", r.Op, r.Verb)
		}
	}
	first, err := NewExpect(nil, w.c1, nil)
	if err != nil || len(first.Removed) != 0 {
		t.Errorf("first generation: %+v %v", first, err)
	}
}

func TestGenerationOf(t *testing.T) {
	w := newWorld(t)
	cands := []Candidate{{Generation: 2, Compiled: w.c2, RevokedKeys: []string{w.k.revoked}}, {Generation: 1, Compiled: w.c1}}
	if g, ok := GenerationOf(w.j2, cands); !ok || g != 2 {
		t.Errorf("j2 = %d %v", g, ok)
	}
	if g, ok := GenerationOf(w.j1, cands); !ok || g != 1 {
		t.Errorf("j1 = %d %v", g, ok)
	}
	if _, ok := GenerationOf("not a jwt", cands); ok {
		t.Error("garbage matched")
	}
	other := compile(t, listset.ListSet{Range: "foo", Switches: switches("foo.c", "shared", "shared", "shared", "shared"),
		Senders: []listset.SenderLists{{Sender: "foo.c.core"}}})
	if _, ok := GenerationOf(w.k.build(t, other), cands); ok {
		t.Error("unknown templates matched")
	}
	// Target templates without the revocation do not count as the target.
	if _, ok := GenerationOf(w.k.build(t, w.c2), cands); ok {
		t.Error("missing revocation matched")
	}
}

func TestGateApplied(t *testing.T) {
	w := newWorld(t)
	w.cluster.set("s1", w.j2)
	w.cluster.set("s2", w.j2)
	st, err := Gate(context.Background(), w.gate())
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateApplied || !st.InForce || len(st.Failing) != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestGateHeldBackServerIsPartial(t *testing.T) {
	w := newWorld(t)
	w.cluster.set("s1", w.j2)
	st, _ := Gate(context.Background(), w.gate())
	if st.State != StatePartial || st.InForce {
		t.Fatalf("%+v", st)
	}
	if g := gens(st); g["s1"] != "2" || g["s2"] != "1" {
		t.Errorf("servers = %v", g)
	}
	if !hasFailure(st, "s2", "", CheckGeneration) || hasFailure(st, "s1", "", CheckGeneration) {
		t.Errorf("failing = %+v", st.Failing)
	}
	if !hasFailure(st, "s2", "foo.c.core", CheckRefused) {
		t.Errorf("s2 should allow the removed reach: %+v", st.Failing)
	}
	if !hasFailure(st, "s2", "", CheckRevoked) {
		t.Errorf("s2 lacks the revocation: %+v", st.Failing)
	}
}

func TestGateRevokedMissing(t *testing.T) {
	w := newWorld(t)
	w.cluster.set("s1", w.j2)
	w.cluster.set("s2", w.k.build(t, w.c2)) // target templates, no revocation
	st, _ := Gate(context.Background(), w.gate())
	var found bool
	for _, f := range st.Failing {
		if f.Server == "s2" && f.Check == CheckRevoked && strings.Contains(f.Detail, w.k.revoked) {
			found = true
		}
	}
	if !found || hasFailure(st, "s1", "", CheckRevoked) {
		t.Errorf("failing = %+v", st.Failing)
	}
	if st.State != StatePartial {
		t.Errorf("state = %s", st.State)
	}
}

func TestGateWitnessOpen(t *testing.T) {
	w := newWorld(t)
	w.cluster.set("s1", w.j2)
	w.cluster.set("s2", w.j2)
	w.wit.open[WitnessKey{"s2", "foo.c.core"}] = true
	st, _ := Gate(context.Background(), w.gate())
	if st.State != StatePartial || !hasFailure(st, "s2", "foo.c.core", CheckWitness) || len(st.Failing) != 1 {
		t.Fatalf("%+v", st)
	}
}

func TestGateRemovedReachAllowedOnOneServer(t *testing.T) {
	w := newWorld(t)
	w.cluster.set("s1", w.j2)
	w.cluster.set("s2", w.j2)
	w.prober.allow["s2"] = true
	st, _ := Gate(context.Background(), w.gate())
	if st.State != StatePartial || !hasFailure(st, "s2", "foo.c.core", CheckRefused) || hasFailure(st, "s1", "foo.c.core", CheckRefused) {
		t.Fatalf("%+v", st.Failing)
	}
}

// A tag-only narrowing: the old tags still reach the receiver under the target template, the target tags do not. With
// every server on the target generation the target credential is refused and the gate applies.
func TestGateTagOnlyNarrowingTargetRefused(t *testing.T) {
	w := newWorld(t)
	if len(w.expect.CredentialOnly) == 0 {
		t.Fatal("no tag-only narrowing in the fixture")
	}
	w.cluster.set("s1", w.j2)
	w.cluster.set("s2", w.j2)
	st, _ := Gate(context.Background(), w.gate())
	if st.State != StateApplied || !st.InForce || len(st.Failing) != 0 {
		t.Fatalf("%+v", st)
	}
}

// A server that still holds the previous generation grants the target tags the receiver: refused_current names it.
func TestGateTagOnlyNarrowingTargetStillGranted(t *testing.T) {
	w := newWorld(t)
	w.cluster.set("s1", w.j2)
	st, _ := Gate(context.Background(), w.gate())
	if st.State != StatePartial || st.InForce {
		t.Fatalf("%+v", st)
	}
	co := w.expect.CredentialOnly[0]
	found := false
	for _, f := range st.Failing {
		if f.Check == CheckRefusedCurrent && f.Server == "s2" && f.Sender == co.Sender &&
			strings.Contains(f.Detail, co.Receiver) && strings.Contains(f.Detail, string(co.Verb)) {
			found = true
		}
		if f.Check == CheckRefusedCurrent && f.Server == "s1" {
			t.Errorf("s1 holds the target: %+v", f)
		}
	}
	if !found {
		t.Fatalf("no refused_current for s2/%s: %+v", co.Sender, st.Failing)
	}
}

func TestGateIncompleteServerSet(t *testing.T) {
	w := newWorld(t)
	w.cluster.expected = 3
	st, _ := Gate(context.Background(), w.gate())
	if st.State != StatePartial || st.InForce || !hasFailure(st, "", "", CheckServerSet) {
		t.Fatalf("%+v", st)
	}
}

func TestGateUnknownGenerationIsNull(t *testing.T) {
	w := newWorld(t)
	w.cluster.set("s1", w.j2)
	w.cluster.set("s2", "garbage")
	w.prober.allow["s2"] = true
	st, _ := Gate(context.Background(), w.gate())
	b, _ := json.Marshal(st)
	if !strings.Contains(string(b), `{"server":"s2","generation":null}`) {
		t.Errorf("json = %s", b)
	}
	if st.State != StatePartial {
		t.Errorf("state = %s", st.State)
	}
}

func TestRunApplied(t *testing.T) {
	w := newWorld(t)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	var seen []State
	in := w.run(l, set2(), "bytes-2")
	in.OnStatus = func(s Status) { seen = append(seen, s.State) }
	st, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateApplied || !st.InForce || st.Generation != 2 {
		t.Fatalf("%+v", st)
	}
	if rec, _ := l.InForce(); rec.Generation != 2 {
		t.Errorf("in force = %d", rec.Generation)
	}
	if len(seen) != 1 || seen[0] != StateApplying {
		t.Errorf("seen = %v", seen)
	}
}

func TestRunConvergeFinishesPush(t *testing.T) {
	w := newWorld(t)
	w.cluster.dropN["s2"] = 1 // the first push never reaches s2
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	st, err := Run(context.Background(), w.run(l, set2(), "bytes-2"))
	if err != nil || st.State != StateApplied {
		t.Fatalf("%+v %v", st, err)
	}
	if rec, _ := l.InForce(); rec.Generation != 2 {
		t.Errorf("in force = %d", rec.Generation)
	}
	if len(w.cluster.pushes) != 2 {
		t.Errorf("pushes = %d", len(w.cluster.pushes))
	}
}

func TestRunConvergeRollsBack(t *testing.T) {
	w := newWorld(t)
	w.cluster.failPush["s2"] = true
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	st, err := Run(context.Background(), w.run(l, set2(), "bytes-2"))
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StatePartial || st.InForce {
		t.Fatalf("%+v", st)
	}
	if g := gens(st); g["s1"] != "1" || g["s2"] != "1" {
		t.Errorf("servers = %v (want every server on the previous generation)", g)
	}
	last, err := jwt.DecodeAccountClaims(w.cluster.pushes[len(w.cluster.pushes)-1])
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := generationOfClaims(last, []Candidate{{Generation: 1, Compiled: w.c1, Signers: w.k.signers}}); !ok || g != 1 {
		t.Error("last push was not the previous generation")
	}
	if _, has := last.Revocations[w.k.revoked]; !has {
		t.Error("last push dropped the target's revocation")
	}
	if rec, _ := l.InForce(); rec.Generation != 1 {
		t.Errorf("committed generation %d", rec.Generation)
	}
}

func TestRunNotCommittedWhenCheckFails(t *testing.T) {
	w := newWorld(t)
	w.prober.allow["s1"] = true
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	st, err := Run(context.Background(), w.run(l, set2(), "bytes-2"))
	if err != nil || st.State != StatePartial || !hasFailure(st, "s1", "foo.c.core", CheckRefused) {
		t.Fatalf("%+v %v", st, err)
	}
	if rec, _ := l.InForce(); rec.Generation != 1 {
		t.Errorf("committed generation %d", rec.Generation)
	}
	if len(w.cluster.pushes) != 1 {
		t.Errorf("rolled back although every server held the target: %d pushes", len(w.cluster.pushes))
	}
}

func TestRunConcurrentSingleWinner(t *testing.T) {
	w := newWorld(t)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Run(context.Background(), w.run(l, set2(), "bytes-"+string(rune('a'+i))))
		}()
	}
	wg.Wait()
	var ok, stale int
	for _, e := range errs {
		switch {
		case e == nil:
			ok++
		case errors.Is(e, listset.ErrStaleBase):
			stale++
		default:
			t.Errorf("unexpected error %v", e)
		}
	}
	if ok != 1 || stale != 1 {
		t.Fatalf("ok=%d stale=%d", ok, stale)
	}
}

func TestRunMutatedCompiledRefusedBeforePush(t *testing.T) {
	w := newWorld(t)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.Signer = withSign(in.Signer, func(_ listset.Record, c *listset.Compiled) (string, error) {
		c.Users[0].Tags = append(c.Users[0].Tags, "allow:foo.c.sdk")
		return w.k.build(t, c, w.k.revoked), nil
	})
	_, err := Run(context.Background(), in)
	if !errors.Is(err, ErrSignedMismatch) {
		t.Fatalf("err = %v", err)
	}
	if len(w.cluster.pushes) != 0 {
		t.Error("pushed")
	}
}

func TestRunJWTNotCarryingTargetRefused(t *testing.T) {
	w := newWorld(t)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.Signer = withSign(in.Signer, func(listset.Record, *listset.Compiled) (string, error) { return w.j1, nil })
	if _, err := Run(context.Background(), in); !errors.Is(err, ErrSignedMismatch) || len(w.cluster.pushes) != 0 {
		t.Fatalf("err = %v, pushes %d", err, len(w.cluster.pushes))
	}
}

func TestRunRefusedListSet(t *testing.T) {
	w := newWorld(t)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	bad := set2()
	bad.Senders = append(bad.Senders, listset.SenderLists{Sender: "bar.c.x"})
	st, err := Run(context.Background(), w.run(l, bad, "b"))
	if err != nil || st.State != StateRefused || len(st.Refusals) == 0 || st.InForce {
		t.Fatalf("%+v %v", st, err)
	}
	if len(w.cluster.pushes) != 0 {
		t.Error("pushed")
	}
	stale := set2()
	stale.Base = 0
	if _, err := Run(context.Background(), w.run(l, stale, "b")); !errors.Is(err, listset.ErrStaleBase) {
		t.Errorf("err = %v", err)
	}
}

func TestMemLedgerCompareAndCommit(t *testing.T) {
	l := NewMemLedger(listset.Record{Generation: 3})
	if err := l.CompareAndCommit(2, listset.Record{Generation: 3}); !errors.Is(err, listset.ErrStaleBase) {
		t.Errorf("err = %v", err)
	}
	if err := l.CompareAndCommit(3, listset.Record{Generation: 4}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := l.InForce(); rec.Generation != 4 {
		t.Errorf("generation %d", rec.Generation)
	}
}

func TestStatusGolden(t *testing.T) {
	w := newWorld(t)
	w.cluster.set("s1", w.j2)
	st, _ := Gate(context.Background(), w.gate())
	got, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "status_partial.golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -run Golden -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("status differs from %s (run with -update to rewrite):\n%s", path, got)
	}
}

func TestGenerationOfSigners(t *testing.T) {
	w := newWorld(t)
	cands := []Candidate{{Generation: 2, Compiled: w.c2, RevokedKeys: []string{w.k.revoked}, Signers: w.k.signers}}
	if g, ok := GenerationOf(w.j2, cands); !ok || g != 2 {
		t.Fatalf("expected signers = %d %v", g, ok)
	}
	extraKP, _ := nkeys.CreateAccount()
	extra, _ := extraKP.PublicKey()
	if _, ok := GenerationOf(w.k.buildWith(t, w.c2, w.k.signers, []string{extra}, w.k.revoked), cands); ok {
		t.Error("a JWT with an extra signing key was read as the target")
	}
	if _, ok := GenerationOf(w.k.buildWith(t, w.c2, map[string]string{"foo.c": extra}, nil, w.k.revoked), cands); ok {
		t.Error("a JWT with another signer was read as the target")
	}
}

func TestGateRefusesTargetWithoutSigners(t *testing.T) {
	w := newWorld(t)
	w.cluster.set("s1", w.j2)
	w.cluster.set("s2", w.j2)
	in := w.gate()
	in.Signers = nil
	st, _ := Gate(context.Background(), in)
	if st.State != StatePartial || !hasFailure(st, "", "", CheckGeneration) || st.Failing[0].Detail != NoSignersDetail {
		t.Fatalf("%+v", st)
	}
	r := w.run(NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1}), set2(), "bytes-2")
	r.Signers = nil
	st, err := Run(context.Background(), r)
	if err != nil || st.State != StatePartial || !hasFailure(st, "", "", CheckGeneration) || len(w.cluster.pushes) != 0 {
		t.Fatalf("%+v %v pushes %d", st, err, len(w.cluster.pushes))
	}
}

func TestGateOldKeyNotRevoked(t *testing.T) {
	w := newWorld(t)
	w.cluster.set("s1", w.j2)
	w.cluster.set("s2", w.j2)
	// Tag-only narrowing, and the old key is not among the revoked keys.
	in := w.gate()
	in.Expect.RevokedKeys = nil
	st, _ := Gate(context.Background(), in)
	if st.State == StateApplied || !hasFailure(st, "", "foo.c.core", CheckOldKeyNotRevoked) {
		t.Fatalf("empty RevokedKeys: %+v", st)
	}
	var named bool
	for _, f := range st.Failing {
		named = named || (f.Check == CheckOldKeyNotRevoked && strings.Contains(f.Detail, w.k.revoked))
	}
	if !named {
		t.Errorf("the key is not named: %+v", st.Failing)
	}
	// Missing inventory fails closed.
	in = w.gate()
	in.OldKeys = nil
	if st, _ = Gate(context.Background(), in); st.State == StateApplied || !hasFailure(st, "", "foo.c.core", CheckOldKeyNotRevoked) {
		t.Fatalf("no inventory: %+v", st)
	}
	// With the old key revoked the gate applies.
	if st, _ = Gate(context.Background(), w.gate()); st.State != StateApplied {
		t.Fatalf("%+v", st)
	}
	// A removed sender's keys must be revoked too.
	in = w.gate()
	in.Compiled = &listset.Compiled{Range: w.c2.Range, Templates: w.c2.Templates, Users: w.c2.Users[1:]}
	in.OldKeys = map[string][]string{"foo.c.core": {"UOLD"}, "foo.c.docs": {}, "foo.c.sdk": {}}
	if st, _ = Gate(context.Background(), in); !hasFailure(st, "", "foo.c.core", CheckOldKeyNotRevoked) {
		t.Fatalf("removed sender: %+v", st.Failing)
	}
}

func TestRunPendingGeneration(t *testing.T) {
	w := newWorld(t)
	w.prober.allow["s1"] = true
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	st, err := Run(context.Background(), w.run(l, set2(), "bytes-2"))
	if err != nil || st.State != StatePartial {
		t.Fatalf("%+v %v", st, err)
	}
	pend, ok, _ := l.Pending()
	if !ok || pend.Generation != 2 {
		t.Fatalf("pending = %+v %v", pend, ok)
	}
	pushes := len(w.cluster.pushes)

	other := set2()
	other.Senders[0].Allow = []string{"foo.c.docs"}
	in := w.run(l, other, "bytes-other")
	in.Signer = withSign(in.Signer, func(listset.Record, *listset.Compiled) (string, error) {
		t.Error("signed while a generation is pending")
		return "", errors.New("signed")
	})
	if _, err := Run(context.Background(), in); !errors.Is(err, ErrPendingGeneration) {
		t.Fatalf("err = %v", err)
	}
	if len(w.cluster.pushes) != pushes {
		t.Error("pushed while a generation is pending")
	}

	delete(w.prober.allow, "s1")
	st, err = Run(context.Background(), w.run(l, set2(), "bytes-2"))
	if err != nil || st.State != StateApplied {
		t.Fatalf("%+v %v", st, err)
	}
	if rec, _ := l.InForce(); rec.Generation != 2 {
		t.Errorf("in force = %d", rec.Generation)
	}
	if _, ok, _ := l.Pending(); ok {
		t.Error("pending not cleared")
	}
}

func TestRunRollbackClearsPending(t *testing.T) {
	w := newWorld(t)
	w.cluster.failPush["s2"] = true
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	// s2 refuses the target but takes the rollback push, so every server holds the rollback's revocations.
	orig := in.Signer
	in.Signer = withSignPrevious(orig, func(pc PreviousClaims, r []string) (string, error) {
		w.cluster.failPush["s2"] = false
		return orig.(SignerFuncs).SignPrevious(pc, r)
	})
	if _, err := Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := l.Pending(); ok {
		t.Error("pending after every server was rolled back")
	}
}

func TestRunRefusesOversizedJWT(t *testing.T) {
	w := newWorld(t)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.Signer = withSign(in.Signer, func(listset.Record, *listset.Compiled) (string, error) {
		return strings.Repeat("a", policy.AccountJWTLimit+1), nil
	})
	_, err := Run(context.Background(), in)
	if !errors.Is(err, ErrAccountJWTTooLarge) || !strings.Contains(err.Error(), strconv.Itoa(policy.AccountJWTLimit+1)) || len(w.cluster.pushes) != 0 {
		t.Fatalf("err = %v pushes %d", err, len(w.cluster.pushes))
	}
}

func TestMemLedgerLockHonorsContext(t *testing.T) {
	l := NewMemLedger(listset.Record{})
	unlock, err := l.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.Lock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	unlock()
	u2, err := l.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u2()
}

// withSign returns s (a SignerFuncs) with Sign replaced by f.
func withSign(s Signer, f func(listset.Record, *listset.Compiled) (string, error)) Signer {
	sf := s.(SignerFuncs)
	sf.SignFn = f
	return sf
}

// withSignPrevious returns s (a SignerFuncs) with SignPrevious replaced by f.
func withSignPrevious(s Signer, f func(PreviousClaims, []string) (string, error)) Signer {
	sf := s.(SignerFuncs)
	sf.SignPreviousFn = f
	return sf
}
