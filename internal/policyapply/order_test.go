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

type recProv struct {
	ev      *events
	failing map[string]string // "create:addr" or "issue:addr" that fail
}

func (p *recProv) step(kind, addr string) error {
	k := kind + ":" + addr
	p.ev.add(k)
	if _, bad := p.failing[k]; bad {
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
	order  Order
	ev     *events
	in     RunInput
	ledger *MemLedger
}

func newWorld14(t *testing.T) *world14 {
	t.Helper()
	w := &world14{k: newKeys(t), ev: &events{}}
	w.c1, w.c2 = compile(t, at014Prev()), compile(t, at014Target())
	w.j1 = w.k.build(t, w.c1)
	e, err := NewExpect(w.c1, w.c2, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.order = NewOrder(w.c1, w.c2, e)
	servers := []string{"s1", "s2"}
	cl := newCluster(w.j1, servers...)
	prober := fakeProber{cluster: cl, byGen: map[uint64]*listset.Compiled{1: w.c1, 2: w.c2}, allow: map[string]bool{}}
	w.ledger = NewMemLedger(listset.Record{Generation: 1})
	w.in = RunInput{
		Cluster: evCluster{cl, w.ev}, Prober: prober, Witnesses: witnessesFor(servers, "foo.c.core", "foo.c.docs"),
		Ledger: w.ledger, Account: w.k.acctPub, ListSet: at014Target(), Received: []byte("x"),
		Previous: w.c1, PreviousJWT: w.j1, Now: fixedNow,
		Sign: func(_ listset.Record, c *listset.Compiled) (string, error) { return w.k.build(t, c), nil },
	}
	return w
}

func TestRunOrderedHoldsUntilGatePasses(t *testing.T) {
	w := newWorld14(t)
	w.in.Prober = senderWrong{Prober: w.in.Prober, sender: "foo.c.core"}
	prov := &recProv{ev: w.ev}
	os, err := RunOrdered(context.Background(), w.in, w.order, prov)
	if err != nil {
		t.Fatal(err)
	}
	if os.Status.State != StatePartial {
		t.Fatalf("status = %+v", os.Status)
	}
	for _, e := range w.ev.get() {
		if strings.HasPrefix(e, "create:") || strings.HasPrefix(e, "issue:") {
			t.Errorf("provisioned although the gate failed for core: %v", w.ev.get())
		}
	}
	want := []HeldAddress{{Address: "foo.c.new", Blocking: []string{"foo.c.core"}}}
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

func TestRunOrderedProvisionsAfterPush(t *testing.T) {
	w := newWorld14(t)
	prov := &recProv{ev: w.ev}
	os, err := RunOrdered(context.Background(), w.in, w.order, prov)
	if err != nil {
		t.Fatal(err)
	}
	if os.Status.State != StateApplied || !Applied(os.Status, Denial{Sender: "foo.c.core", Receiver: "foo.c.docs"}) {
		t.Fatalf("status = %+v", os.Status)
	}
	if got, want := w.ev.get(), []string{"push", "create:foo.c.new", "issue:foo.c.new"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(os.Provisioned, []string{"foo.c.new"}) || len(os.Held) != 0 || len(os.Errors) != 0 {
		t.Errorf("ordered status = %+v", os)
	}
}

func TestRunOrderedNoRefuserIsProvisioned(t *testing.T) {
	w := newWorld14(t)
	order := Order{NewAddresses: []NewAddress{{Address: "foo.c.other", MustRefuse: nil}}}
	prov := &recProv{ev: w.ev}
	os, err := RunOrdered(context.Background(), w.in, order, prov)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := w.ev.get(), []string{"push", "create:foo.c.other", "issue:foo.c.other"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(os.Provisioned, []string{"foo.c.other"}) {
		t.Errorf("provisioned = %v", os.Provisioned)
	}
}

func TestRunOrderedStopsAtFirstError(t *testing.T) {
	w := newWorld14(t)
	order := Order{NewAddresses: []NewAddress{{Address: "foo.c.a"}, {Address: "foo.c.b"}}}
	prov := &recProv{ev: w.ev, failing: map[string]string{"create:foo.c.a": "x"}}
	os, err := RunOrdered(context.Background(), w.in, order, prov)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := w.ev.get(), []string{"push", "create:foo.c.a", "create:foo.c.b", "issue:foo.c.b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	if len(os.Errors) != 1 || os.Errors[0].Step != StepCreateInbox || os.Errors[0].Address != "foo.c.a" {
		t.Errorf("errors = %+v", os.Errors)
	}
	if !reflect.DeepEqual(os.Provisioned, []string{"foo.c.b"}) {
		t.Errorf("provisioned = %v", os.Provisioned)
	}
}

func TestRunOrderedRefusedHoldsEverything(t *testing.T) {
	w := newWorld14(t)
	w.in.ListSet.Base = 7 // a stale base is an error: nothing is provisioned
	prov := &recProv{ev: w.ev}
	order := Order{NewAddresses: []NewAddress{{Address: "foo.c.a"}}}
	if _, err := RunOrdered(context.Background(), w.in, order, prov); err == nil {
		t.Fatal("want a stale base error")
	}
	w = newWorld14(t)
	w.in.ListSet.Range = "" // fails validation: a refused status and no error
	os, err := RunOrdered(context.Background(), w.in, order, &recProv{ev: w.ev})
	if err != nil || os.Status.State != StateRefused || len(os.Provisioned) != 0 || len(os.Held) != 1 {
		t.Errorf("refused: %+v %v", os, err)
	}
	if len(w.ev.get()) != 0 {
		t.Errorf("events = %v", w.ev.get())
	}
}

func TestOrderedStatusJSON(t *testing.T) {
	b, err := json.Marshal(OrderedStatus{Held: []HeldAddress{{Address: "b"}, {Address: "a", Blocking: []string{"y", "x"}}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"provisioned":[]`, `"errors":[]`, `"held":[{"address":"a","blocking":["x","y"]},{"address":"b","blocking":[]}]`, `"status":{`} {
		if !strings.Contains(s, want) {
			t.Errorf("JSON lacks %s: %s", want, s)
		}
	}
}
