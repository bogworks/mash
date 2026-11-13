package policyapply

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
)

// at014Prev is the previous generation of AT-014: mail is shared in foo.c, with two senders.
func at014Prev() listset.ListSet {
	return listset.ListSet{
		Range:    "foo",
		Switches: switches("foo.c", listset.Shared, listset.Closed, listset.Closed, listset.Closed),
		Senders:  []listset.SenderLists{{Sender: "foo.c.core"}, {Sender: "foo.c.docs"}},
	}
}

// at014Target adds foo.c.new, which core must not reach, and newly denies the existing foo.c.docs to core.
func at014Target() listset.ListSet {
	return listset.ListSet{
		Base:     1,
		Range:    "foo",
		Switches: switches("foo.c", listset.Shared, listset.Closed, listset.Closed, listset.Closed),
		Senders: []listset.SenderLists{
			{Sender: "foo.c.core", Deny: []string{"foo.c.docs", "foo.c.new"}},
			{Sender: "foo.c.docs"},
			{Sender: "foo.c.new"},
		},
	}
}

func TestNewOrderAT014(t *testing.T) {
	c1, c2 := compile(t, at014Prev()), compile(t, at014Target())
	e, err := NewExpect(c1, c2, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The previous owner line is shared, so it would have reached foo.c.new: Expect must hold core's removed reach.
	found := false
	for _, r := range append(append([]RemovedReach{}, e.Removed...), e.CredentialOnly...) {
		if r.Sender == "foo.c.core" && r.Receiver == "foo.c.new" && r.Verb == listset.Mail {
			found = true
		}
	}
	if !found {
		t.Fatalf("Expect misses core's reach to the new address: %+v %+v", e.Removed, e.CredentialOnly)
	}
	// The target adds a tag-driven deny line that core's old tags cannot expand, so its old credential cannot be
	// probed: the reach is credential-only and the gate probes the target credential.
	if len(e.Removed) != 0 || len(e.CredentialOnly) == 0 {
		t.Errorf("removed = %+v credential only = %+v", e.Removed, e.CredentialOnly)
	}
	o := NewOrder(c1, c2, e)
	wantNew := []NewAddress{{Address: "foo.c.new", MustRefuse: []string{"foo.c.core"}}}
	if !reflect.DeepEqual(o.NewAddresses, wantNew) {
		t.Errorf("new addresses = %+v", o.NewAddresses)
	}
	wantDenied := []Denial{{Sender: "foo.c.core", Receiver: "foo.c.docs"}}
	if !reflect.DeepEqual(o.NewlyDenied, wantDenied) {
		t.Errorf("newly denied = %+v", o.NewlyDenied)
	}
}

func TestNewOrderNoRefuser(t *testing.T) {
	c1 := compile(t, at014Prev())
	ls := at014Target()
	ls.Senders = []listset.SenderLists{{Sender: "foo.c.core"}, {Sender: "foo.c.docs"}, {Sender: "foo.c.new"}}
	c2 := compile(t, ls)
	e, err := NewExpect(c1, c2, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := NewOrder(c1, c2, e)
	want := []NewAddress{{Address: "foo.c.new", MustRefuse: []string{}}}
	if !reflect.DeepEqual(o.NewAddresses, want) || len(o.NewlyDenied) != 0 {
		t.Errorf("order = %+v", o)
	}
	first := NewOrder(nil, c2, Expect{})
	if len(first.NewAddresses) != 3 {
		t.Errorf("first generation: %+v", first)
	}
}

func gen(g uint64) *uint64 { return &g }

func TestSenderPassed(t *testing.T) {
	ok := Status{Generation: 2, Servers: []ServerGeneration{{"s1", gen(2)}, {"s2", gen(2)}}}
	if !SenderPassed(ok, "foo.c.core") {
		t.Error("clean status must pass")
	}
	if SenderPassed(Status{Generation: 2}, "foo.c.core") {
		t.Error("no servers must not pass")
	}
	behind := Status{Generation: 2, Servers: []ServerGeneration{{"s1", gen(2)}, {"s2", gen(1)}}}
	if SenderPassed(behind, "foo.c.core") {
		t.Error("a server on another generation must not pass")
	}
	unknown := Status{Generation: 2, Servers: []ServerGeneration{{"s1", gen(2)}, {"s2", nil}}}
	if SenderPassed(unknown, "foo.c.core") {
		t.Error("an unknown generation must not pass")
	}
	own := ok
	own.Failing = []Failure{{Server: "s1", Sender: "foo.c.core", Check: CheckRefused}}
	if SenderPassed(own, "foo.c.core") || !SenderPassed(own, "foo.c.docs") {
		t.Error("a sender-specific failure counts against that sender only")
	}
	wide := ok
	wide.Failing = []Failure{{Server: "s2", Check: CheckRevoked}}
	if SenderPassed(wide, "foo.c.core") || SenderPassed(wide, "foo.c.docs") {
		t.Error("a server-wide failure counts against every sender")
	}
	if !Applied(own, Denial{Sender: "foo.c.docs", Receiver: "foo.c.core"}) || Applied(own, Denial{Sender: "foo.c.core", Receiver: "foo.c.docs"}) {
		t.Error("Applied follows the sender's gate")
	}
}

// events records the order of cluster pushes and provisioning calls.
type events struct {
	mu  sync.Mutex
	log []string
}

func (e *events) add(s string) {
	e.mu.Lock()
	e.log = append(e.log, s)
	e.mu.Unlock()
}

func (e *events) get() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.log...)
}

type evCluster struct {
	*fakeCluster
	ev *events
}

func (c evCluster) Push(ctx context.Context, tok string, expect []string) ([]apply.ServerAck, error) {
	c.ev.add("push")
	return c.fakeCluster.Push(ctx, tok, expect)
}

// evProber records "gate" once per run of consecutive probes.
type evProber struct {
	Prober
	ev *events
}

func (p evProber) Refused(ctx context.Context, server string, pc ProbeCred, op Op, subject string) (bool, error) {
	if l := p.ev.get(); len(l) == 0 || l[len(l)-1] != "gate" {
		p.ev.add("gate")
	}
	return p.Prober.Refused(ctx, server, pc, op, subject)
}

// evLedger records "commit".
type evLedger struct {
	*MemLedger
	ev *events
}

func (l evLedger) CompareAndCommit(base uint64, rec listset.Record) error {
	l.ev.add("commit")
	return l.MemLedger.CompareAndCommit(base, rec)
}

func (l evLedger) CompareAndCommitDetail(base uint64, rec listset.Record, d InForceDetail) error {
	l.ev.add("commit")
	return l.MemLedger.CompareAndCommitDetail(base, rec, d)
}

type recProv struct {
	ev      *events
	mu      sync.Mutex
	failing map[string]bool // "create:addr" or "issue:addr" that fail
	seen    map[string]bool // addresses any step was called for
}

func (p *recProv) fail(k string, on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failing == nil {
		p.failing = map[string]bool{}
	}
	p.failing[k] = on
}

func (p *recProv) step(kind, addr string) error {
	k := kind + ":" + addr
	p.ev.add(k)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil {
		p.seen = map[string]bool{}
	}
	p.seen[addr] = true
	if p.failing[k] {
		return errors.New("boom")
	}
	return nil
}
func (p *recProv) CreateInbox(_ context.Context, a string) error     { return p.step("create", a) }
func (p *recProv) IssueCredential(_ context.Context, a string) error { return p.step("issue", a) }

// senderWrong accepts every probe of one sender, as a server that still lets it through.
type senderWrong struct {
	Prober
	sender string
}

func (p senderWrong) Refused(ctx context.Context, server string, pc ProbeCred, op Op, subject string) (bool, error) {
	if pc.Sender == p.sender {
		return false, nil
	}
	return p.Prober.Refused(ctx, server, pc, op, subject)
}

type world14 struct {
	k      keys
	c1, c2 *listset.Compiled
	j1     string
	ev     *events
	in     RunInput
	ledger *MemLedger
	prov   *recProv
}

func newWorld14(t *testing.T) *world14 { return newWorld14For(t, at014Target()) }

// newWorld14For is the world of AT-014 with target as the submitted list set. Core's old key is revoked by the change.
func newWorld14For(t *testing.T, target listset.ListSet) *world14 {
	t.Helper()
	w := &world14{k: newKeys(t), ev: &events{}}
	w.c1, w.c2 = compile(t, at014Prev()), compile(t, target)
	w.j1 = w.k.build(t, w.c1)
	servers := []string{"s1", "s2"}
	cl := newCluster(w.j1, servers...)
	prober := fakeProber{cluster: cl, byGen: map[uint64]*listset.Compiled{1: w.c1, 2: w.c2}, allow: map[string]bool{}}
	w.ledger = NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	w.prov = &recProv{ev: w.ev}
	w.in = RunInput{
		Cluster: evCluster{cl, w.ev}, Prober: evProber{prober, w.ev}, Witnesses: witnessesFor(servers, "foo.c.core", "foo.c.docs"),
		Ledger: evLedger{w.ledger, w.ev}, Account: w.k.acctPub, ListSet: target, Received: []byte("x"),
		Previous: w.c1, Now: fixedNow, RevokedKeys: []string{w.k.revoked},
		Signers: w.k.signers, PreviousSigners: w.k.signers,
		OldKeys: map[string][]string{"foo.c.core": {w.k.revoked}, "foo.c.docs": {}},
		Signer: SignerFuncs{
			SignPreviousFn: func(_ PreviousClaims, revoked []string) (string, error) { return w.k.build(t, w.c1, revoked...), nil },
			SignFn:         func(_ listset.Record, c *listset.Compiled) (string, error) { return w.k.build(t, c, w.k.revoked), nil },
		},
	}
	return w
}

func TestRunOrderedHoldsUntilGatePasses(t *testing.T) {
	w := newWorld14(t)
	w.in.Prober = senderWrong{Prober: w.in.Prober, sender: "foo.c.core"}
	os, err := RunOrdered(context.Background(), w.in, w.prov)
	if err != nil {
		t.Fatal(err)
	}
	if os.Status.State != StatePartial {
		t.Fatalf("status = %+v", os.Status)
	}
	for _, e := range w.ev.get() {
		if strings.HasPrefix(e, "create:") || strings.HasPrefix(e, "issue:") || e == "commit" {
			t.Errorf("provisioned or committed although the gate failed for core: %v", w.ev.get())
		}
	}
	want := []HeldAddress{{Address: "foo.c.new", Reason: HeldSendersNotPassed, Blocking: []string{"foo.c.core"}}}
	if !reflect.DeepEqual(os.Held, want) || len(os.Provisioned) != 0 {
		t.Errorf("held = %+v provisioned = %v", os.Held, os.Provisioned)
	}
	if Applied(os.Status, Denial{Sender: "foo.c.core", Receiver: "foo.c.docs"}) {
		t.Error("the denial of docs to core is applied although core's gate failed")
	}
	if !Applied(os.Status, Denial{Sender: "foo.c.docs", Receiver: "foo.c.core"}) {
		t.Error("docs passed its own gate")
	}
}

// TestRunOrderedProvisionsBeforeCommit: the steps run in the order push, gate, provision, commit, and the generation
// in force is still the previous one while provisioning.
func TestRunOrderedProvisionsBeforeCommit(t *testing.T) {
	w := newWorld14(t)
	os, err := RunOrdered(context.Background(), w.in, w.prov)
	if err != nil {
		t.Fatal(err)
	}
	if os.Status.State != StateApplied || !Applied(os.Status, Denial{Sender: "foo.c.core", Receiver: "foo.c.docs"}) {
		t.Fatalf("status = %+v", os.Status)
	}
	if got, want := w.ev.get(), []string{"push", "gate", "create:foo.c.new", "issue:foo.c.new", "commit"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(os.Provisioned, []string{"foo.c.new"}) || len(os.Held) != 0 || len(os.Errors) != 0 {
		t.Errorf("ordered status = %+v", os)
	}
	if rec, _ := w.ledger.InForce(); rec.Generation != 2 {
		t.Errorf("in force = %d", rec.Generation)
	}
}

// TestRunOrderedHasNoOrderInput (H1): RunOrdered takes no Order, and provisions only addresses of the list set that Run
// accepted, however the caller shapes the input.
func TestRunOrderedHasNoOrderInput(t *testing.T) {
	if n := reflect.TypeOf(RunOrdered).NumIn(); n != 3 {
		t.Fatalf("RunOrdered takes %d parameters: it must take no Order", n)
	}
	w := newWorld14(t)
	if _, err := RunOrdered(context.Background(), w.in, w.prov); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w.prov.seen, map[string]bool{"foo.c.new": true}) {
		t.Errorf("provisioned %v: only foo.c.new is in the accepted list set", w.prov.seen)
	}
	// A failed gate for the sender that must refuse the address holds it: no caller input can empty MustRefuse.
	w = newWorld14(t)
	w.in.Prober = senderWrong{Prober: w.in.Prober, sender: "foo.c.core"}
	os, err := RunOrdered(context.Background(), w.in, w.prov)
	if err != nil || len(w.prov.seen) != 0 || len(os.Held) != 1 || os.Held[0].Address != "foo.c.new" {
		t.Errorf("failed gate: %+v %v seen=%v", os, err, w.prov.seen)
	}
}

func TestRunOrderedNoRefuserIsProvisioned(t *testing.T) {
	ls := at014Target()
	ls.Senders = []listset.SenderLists{{Sender: "foo.c.core"}, {Sender: "foo.c.docs"}, {Sender: "foo.c.new"}}
	w := newWorld14For(t, ls)
	os, err := RunOrdered(context.Background(), w.in, w.prov)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := w.ev.get(), []string{"push", "create:foo.c.new", "issue:foo.c.new", "commit"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(os.Provisioned, []string{"foo.c.new"}) {
		t.Errorf("provisioned = %v", os.Provisioned)
	}
}

// failingStatus is a ledger whose status write fails.
type failingStatus struct{ *MemLedger }

var errStatusDown = errors.New("status store down")

func (failingStatus) WriteStatus(context.Context, Status) error { return errStatusDown }

// A status write that fails on the provisioning-failure path is returned, not hidden behind the provisioning error.
func TestRunOrderedProvisionFailureStatusWriteError(t *testing.T) {
	w := newWorld14(t)
	w.in.Ledger = failingStatus{w.ledger}
	w.prov.fail("create:foo.c.new", true)
	if _, err := RunOrdered(context.Background(), w.in, w.prov); !errors.Is(err, errStatusDown) {
		t.Fatalf("err = %v, want the status write error", err)
	}
}

// TestRunOrderedProvisionFailureResumes (H2): a failed provisioning step commits nothing and leaves the generation
// pending; resubmitting the same content runs the steps again and commits.
func TestRunOrderedProvisionFailureResumes(t *testing.T) {
	for _, step := range []string{"create:foo.c.new", "issue:foo.c.new"} {
		t.Run(step, func(t *testing.T) {
			w := newWorld14(t)
			w.prov.fail(step, true)
			os, err := RunOrdered(context.Background(), w.in, w.prov)
			if err != nil {
				t.Fatal(err)
			}
			if got := w.ev.get(); got[len(got)-1] == "commit" {
				t.Errorf("committed although provisioning failed: %v", got)
			}
			if rec, _ := w.ledger.InForce(); rec.Generation != 1 {
				t.Errorf("in force = %d, want 1", rec.Generation)
			}
			if pend, ok, _ := w.ledger.Pending(); !ok || pend.Generation != 2 {
				t.Errorf("pending = %+v %v", pend, ok)
			}
			wantStep := StepCreateInbox
			if strings.HasPrefix(step, "issue") {
				wantStep = StepIssueCredential
			}
			if len(os.Errors) != 1 || os.Errors[0].Address != "foo.c.new" || os.Errors[0].Step != wantStep || len(os.Provisioned) != 0 {
				t.Errorf("ordered status = %+v", os)
			}
			if os.Status.State != StatePartial || os.Status.InForce || !hasFailure(os.Status, "", "", CheckProvision) {
				t.Errorf("status = %+v", os.Status)
			}
			// A different submission on the same base is refused while pending.
			other := w.in
			other.ListSet.Senders = []listset.SenderLists{{Sender: "foo.c.core"}, {Sender: "foo.c.docs"}, {Sender: "foo.c.new"}}
			if _, err := RunOrdered(context.Background(), other, w.prov); !errors.Is(err, ErrPendingGeneration) {
				t.Errorf("different content while pending: %v", err)
			}

			// The same content resumes.
			w.prov.fail(step, false)
			w.ev.mu.Lock()
			w.ev.log = nil
			w.ev.mu.Unlock()
			os, err = RunOrdered(context.Background(), w.in, w.prov)
			if err != nil {
				t.Fatal(err)
			}
			if os.Status.State != StateApplied || !os.Status.InForce || !reflect.DeepEqual(os.Provisioned, []string{"foo.c.new"}) || len(os.Errors) != 0 {
				t.Fatalf("resumed: %+v", os)
			}
			if got, want := w.ev.get(), []string{"push", "gate", "create:foo.c.new", "issue:foo.c.new", "commit"}; !reflect.DeepEqual(got, want) {
				t.Errorf("events = %v, want %v", got, want)
			}
			if rec, _ := w.ledger.InForce(); rec.Generation != 2 {
				t.Errorf("in force = %d, want 2", rec.Generation)
			}
			if _, ok, _ := w.ledger.Pending(); ok {
				t.Error("pending not cleared")
			}
		})
	}
}

func TestRunOrderedRefusedHoldsWithReason(t *testing.T) {
	w := newWorld14(t)
	w.in.ListSet.Base = 7 // a stale base is an error: nothing is provisioned
	if _, err := RunOrdered(context.Background(), w.in, w.prov); err == nil {
		t.Fatal("want a stale base error")
	}
	w = newWorld14(t)
	w.in.ListSet.Range = "" // fails validation: a refused status and no error
	os, err := RunOrdered(context.Background(), w.in, w.prov)
	if err != nil || os.Status.State != StateRefused || len(os.Provisioned) != 0 {
		t.Fatalf("refused: %+v %v", os, err)
	}
	// The gate never ran: the address is held for the refusal, and no sender is named as blocking.
	want := []HeldAddress{{Address: "foo.c.new", Reason: HeldListSetRefused}}
	if !reflect.DeepEqual(os.Held, want) {
		t.Errorf("held = %+v, want %+v", os.Held, want)
	}
	if len(w.ev.get()) != 0 {
		t.Errorf("events = %v", w.ev.get())
	}
}

func TestOrderedStatusJSON(t *testing.T) {
	b, err := json.Marshal(OrderedStatus{Held: []HeldAddress{{Address: "b", Reason: HeldListSetRefused}, {Address: "a", Reason: HeldSendersNotPassed, Blocking: []string{"y", "x"}}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"provisioned":[]`, `"errors":[]`, `"held":[{"address":"a","reason":"senders not passed","blocking":["x","y"]},{"address":"b","reason":"list set refused","blocking":[]}]`, `"status":{`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON lacks %s: %s", want, s)
		}
	}
}
