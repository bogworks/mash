package policyview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

func sw(group string, mail, prompt, status, hb listset.Switch) []listset.SwitchEntry {
	return []listset.SwitchEntry{
		{Group: group, Verb: listset.Mail, Switch: mail},
		{Group: group, Verb: listset.Prompt, Switch: prompt},
		{Group: group, Verb: listset.Status, Switch: status},
		{Group: group, Verb: listset.Heartbeat, Switch: hb},
	}
}

// testSet: r.o.a reaches r.p.x by allow and is denied r.o.c; r.p.x reaches r.o.a and r.o.b.
func testSet() listset.ListSet {
	return listset.ListSet{
		Range:    "r",
		Switches: append(sw("r.o", listset.Shared, listset.Closed, listset.Closed, listset.Shared), sw("r.p", listset.Closed, listset.Closed, listset.Closed, listset.Closed)...),
		Senders: []listset.SenderLists{
			{Sender: "r.o.a", Allow: []string{"r.p.x"}, Deny: []string{"r.o.c"}},
			{Sender: "r.o.b"},
			{Sender: "r.o.c", Allow: []string{"r.o.a"}},
			{Sender: "r.p.x", Allow: []string{"r.o.a", "r.o.b"}},
		},
	}
}

type fakeStore struct {
	mu     sync.Mutex
	ls     *listset.ListSet
	status *policyapply.Status
	err    error
}

func (f *fakeStore) InForceListSet() (listset.ListSet, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return listset.ListSet{}, false, f.err
	}
	if f.ls == nil {
		return listset.ListSet{}, false, nil
	}
	return *f.ls, true, nil
}

func (f *fakeStore) LatestStatus(context.Context) (policyapply.Status, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status == nil {
		return policyapply.Status{}, false, nil
	}
	return *f.status, true, nil
}

type caller struct {
	id   sap.Identity
	addr string
}

type rig struct {
	nc      *nats.Conn
	store   *fakeStore
	users   map[string]string // user public key -> address
	now     time.Time
	callers map[string]caller
	stop    func() error
}

func newCaller(t *testing.T, account, addr string) caller {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	id, err := sap.NewIdentity(seed, account)
	if err != nil {
		t.Fatal(err)
	}
	return caller{id: id, addr: addr}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	e := testenv.Start(t)
	ls := testSet()
	r := &rig{
		nc:      e.Conn(t, e.AdminCreds),
		store:   &fakeStore{ls: &ls, status: &policyapply.Status{Generation: 3, State: policyapply.StateApplied, InForce: true}},
		users:   map[string]string{},
		callers: map[string]caller{},
		now:     time.Now(),
	}
	for _, a := range []string{"r.o.a", "r.o.b", "r.o.c", "r.p.x", "q.o.z"} {
		c := newCaller(t, e.Account, a)
		r.callers[a] = c
		r.users[c.id.User] = a
	}
	stop, err := Mount(r.nc, Options{
		Range: "r", Store: r.store, Now: func() time.Time { return r.now },
		ResolveAddress: func(_ context.Context, pub string) (string, bool, error) {
			a, ok := r.users[pub]
			return a, ok, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.stop = stop
	t.Cleanup(func() { _ = stop() })
	return r
}

func (r *rig) signed(t *testing.T, c caller, payload []byte) *nats.Msg {
	t.Helper()
	h, err := c.id.Sign(Subject("r"), payload, "x", r.now, "")
	if err != nil {
		t.Fatal(err)
	}
	m := nats.NewMsg(Subject("r"))
	m.Data = payload
	m.Header.Set(sap.HeaderName, h.Encode())
	return m
}

func (r *rig) do(t *testing.T, m *nats.Msg) []byte {
	t.Helper()
	resp, err := r.nc.RequestMsg(m, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return resp.Data
}

func (r *rig) ask(t *testing.T, as string, req Request) []byte {
	t.Helper()
	b, _ := json.Marshal(req)
	return r.do(t, r.signed(t, r.callers[as], b))
}

func errorOf(b []byte) string {
	var e ErrorReply
	_ = json.Unmarshal(b, &e)
	return e.Error
}

func TestSubject(t *testing.T) {
	if Subject("r") != "agents.policy.r" {
		t.Fatal(Subject("r"))
	}
}

func TestShowAnswersOnlyTheCallersOwnReach(t *testing.T) {
	r := newRig(t)
	raw := r.ask(t, "r.o.a", Request{Op: OpShow})
	var v ShowReply
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if v.Describes != "list_set" || v.Sender != "r.o.a" || v.Generation != 3 || v.State != "applied" || !v.InForce || v.Notice != "" {
		t.Fatalf("header: %+v", v)
	}
	got := map[listset.Verb][]string{}
	for _, vr := range v.Reach {
		got[vr.Verb] = vr.Receivers
	}
	if strings.Join(got[listset.Mail], ",") != "r.o.a,r.o.b,r.p.x" {
		t.Fatalf("mail reach %v", got[listset.Mail])
	}
	if v.RawClientLimit != listset.RawClientLimit || len(v.OpenSubjects.Publish) == 0 {
		t.Fatalf("limit/open: %+v", v)
	}
	if !contains(v.OpenSubjects.Publish, "agents.policy.r") {
		t.Fatalf("open subjects lack the responder: %v", v.OpenSubjects.Publish)
	}
	// The answer never names another sender: r.o.c is denied to the caller, and nobody else's lists appear.
	for _, other := range []string{"r.o.c"} {
		if strings.Contains(string(raw), other) {
			t.Errorf("answer to r.o.a names %s: %s", other, raw)
		}
	}
	for _, key := range []string{`"allow"`, `"deny"`, `"senders"`, `"switches"`} {
		if strings.Contains(string(raw), key) {
			t.Errorf("answer carries %s: %s", key, raw)
		}
	}
	// r.p.x's answer holds its own reach (r.o.a, r.o.b) and not r.o.c, which only r.o.a's lists mention.
	raw = r.ask(t, "r.p.x", Request{Op: OpShow})
	if strings.Contains(string(raw), "r.o.c") || !strings.Contains(string(raw), `"r.p.x"`) {
		t.Fatalf("r.p.x answer: %s", raw)
	}
	if strings.Contains(string(raw), `"r.o.a"`) == false {
		t.Fatalf("r.p.x reaches r.o.a by its own allow list: %s", raw)
	}
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func TestShowStableJSONAndNonNilSlices(t *testing.T) {
	r := newRig(t)
	raw := r.ask(t, "r.o.b", Request{Op: OpShow})
	for _, want := range []string{`"describes":"list_set"`, `"generation":3`, `"state":"applied"`, `"in_force":true`, `"sender":"r.o.b"`, `"reach":[`, `"open_subjects":{"publish":[`, `"raw_client_limit":`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("answer lacks %s: %s", want, raw)
		}
	}
	if strings.Contains(string(raw), "null") {
		t.Errorf("answer has null: %s", raw)
	}
}

func TestShowPartialSaysSo(t *testing.T) {
	r := newRig(t)
	r.store.status = &policyapply.Status{Generation: 4, State: policyapply.StatePartial}
	var v ShowReply
	if err := json.Unmarshal(r.ask(t, "r.o.a", Request{Op: OpShow}), &v); err != nil {
		t.Fatal(err)
	}
	if v.State != "partial" || v.InForce || !strings.Contains(v.Notice, "partial") {
		t.Fatalf("%+v", v)
	}
}

func TestExplainReceiver(t *testing.T) {
	r := newRig(t)
	var e ExplainReply
	raw := r.ask(t, "r.o.a", Request{Op: OpExplain, Receiver: "r.o.c"})
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	if e.Sender != "r.o.a" || e.Receiver != "r.o.c" || e.Generation != 3 || e.State != "applied" || len(e.Verbs) != 4 {
		t.Fatalf("%+v", e)
	}
	if e.Verbs[0].Verb != listset.Mail || e.Verbs[0].Allowed || e.Verbs[0].Reason != listset.ReasonDenyTag {
		t.Fatalf("mail verb: %+v", e.Verbs[0])
	}
	// The explanation is of the caller's own entry: asking about another sender is not possible, only a receiver.
	if msg := errorOf(r.ask(t, "r.o.a", Request{Op: OpExplain, Receiver: "r.o"})); msg == "" {
		t.Error("an invalid receiver was answered")
	}
	if msg := errorOf(r.ask(t, "r.o.a", Request{Op: OpExplain, Receiver: "q.o.z"})); msg == "" {
		t.Error("a receiver outside the range was answered")
	}
}

func TestUnsignedClaimedAndForgedAreRefused(t *testing.T) {
	r := newRig(t)
	body := []byte(`{"op":"show"}`)
	c := r.callers["r.o.a"]

	// absent
	m := nats.NewMsg(Subject("r"))
	m.Data = body
	if got := errorOf(r.do(t, m)); got != ErrSignatureRequired {
		t.Errorf("unsigned: %q", got)
	}
	// claimed: a header with no signature
	m = nats.NewMsg(Subject("r"))
	m.Data = body
	m.Header.Set(sap.HeaderName, sap.SenderHeader{V: 1, Account: c.id.Account, User: c.id.User}.Encode())
	if got := errorOf(r.do(t, m)); got != ErrSignatureRequired {
		t.Errorf("claimed: %q", got)
	}
	// forged: a good header over another payload
	m = r.signed(t, c, body)
	m.Data = []byte(`{"op":"show","x":1}`)
	if got := errorOf(r.do(t, m)); got != ErrSignatureRequired {
		t.Errorf("forged: %q", got)
	}
	// signed for another subject
	h, _ := c.id.Sign("agents.policy.other", body, "x", r.now, "")
	m = nats.NewMsg(Subject("r"))
	m.Data = body
	m.Header.Set(sap.HeaderName, h.Encode())
	if got := errorOf(r.do(t, m)); got != ErrSignatureRequired {
		t.Errorf("wrong subject: %q", got)
	}
	// stale
	h, _ = c.id.Sign(Subject("r"), body, "x", r.now.Add(-time.Hour), "")
	m = nats.NewMsg(Subject("r"))
	m.Data = body
	m.Header.Set(sap.HeaderName, h.Encode())
	if got := errorOf(r.do(t, m)); got != ErrSignatureRequired {
		t.Errorf("stale: %q", got)
	}
	// The refusals say nothing else.
	m = nats.NewMsg(Subject("r"))
	m.Data = body
	if string(r.do(t, m)) != `{"error":"signature required"}` {
		t.Error("the refusal carries more than the error")
	}
}

func TestReplayedRequestIsRefused(t *testing.T) {
	r := newRig(t)
	m := r.signed(t, r.callers["r.o.a"], []byte(`{"op":"show"}`))
	if got := errorOf(r.do(t, m)); got != "" {
		t.Fatalf("first request: %q", got)
	}
	if got := errorOf(r.do(t, m)); got != ErrSignatureRequired {
		t.Fatalf("replay: %q", got)
	}
}

func TestKeyWithNoAddressIsRefused(t *testing.T) {
	r := newRig(t)
	stranger := newCaller(t, "ACC", "")
	b := []byte(`{"op":"show"}`)
	if got := errorOf(r.do(t, r.signed(t, stranger, b))); !strings.Contains(got, "names no endpoint") {
		t.Errorf("unknown key: %q", got)
	}
	// A key that maps to an address outside the range.
	if got := errorOf(r.ask(t, "q.o.z", Request{Op: OpShow})); !strings.Contains(got, "not an endpoint of range") {
		t.Errorf("outside the range: %q", got)
	}
	// An address is never taken from the body.
	b = []byte(`{"op":"show","sender":"r.o.a"}`)
	var v ShowReply
	if err := json.Unmarshal(r.do(t, r.signed(t, r.callers["r.o.b"], b)), &v); err != nil || v.Sender != "r.o.b" {
		t.Errorf("sender = %q (%v)", v.Sender, err)
	}
}

func TestNothingInForce(t *testing.T) {
	r := newRig(t)
	r.store.ls = nil
	var v ShowReply
	raw := r.ask(t, "r.o.a", Request{Op: OpShow})
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.InForce || v.Notice != "no list set in force: owner line only" || v.Sender != "r.o.a" || v.Reach == nil {
		t.Fatalf("%s", raw)
	}
	if msg := errorOf(r.ask(t, "r.o.a", Request{Op: OpExplain, Receiver: "r.o.b"})); !strings.Contains(msg, "no list set in force") {
		t.Errorf("explain with nothing in force: %q", msg)
	}
}

func TestUnknownOpAndStoreError(t *testing.T) {
	r := newRig(t)
	if msg := errorOf(r.ask(t, "r.o.a", Request{Op: "dump"})); !strings.Contains(msg, "unknown op") {
		t.Errorf("unknown op: %q", msg)
	}
	r.store.err = errors.New("ledger down")
	if msg := errorOf(r.ask(t, "r.o.a", Request{Op: OpShow})); !strings.Contains(msg, "ledger down") {
		t.Errorf("store error: %q", msg)
	}
}

func TestMountRefusesIncompleteOptions(t *testing.T) {
	e := testenv.Start(t)
	nc := e.Conn(t, e.AdminCreds)
	res := func(context.Context, string) (string, bool, error) { return "", false, nil }
	for name, o := range map[string]Options{
		"no range":    {Store: &fakeStore{}, ResolveAddress: res},
		"no store":    {Range: "r", ResolveAddress: res},
		"no resolver": {Range: "r", Store: &fakeStore{}},
	} {
		if _, err := Mount(nc, o); err == nil {
			t.Errorf("%s: mounted", name)
		}
	}
}

// DirectoryResolver reads the directory: a new-form record resolves by its user key to agent.owner.name; an unknown
// key resolves to none.
func TestDirectoryResolver(t *testing.T) {
	e := testenv.Start(t)
	ctx := context.Background()
	adm, err := admin.New(e.Conn(t, e.AdminCreds), e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := adm.Init(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := sap.New("r", "o", "a")
	if err != nil {
		t.Fatal(err)
	}
	_, user := e.Mint(t, id.Flat())
	if _, err := adm.ProvisionIdentity(ctx, id, nil, user, e.Account, "", admin.Meta{}); err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(e.Conn(t, e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	resolve := DirectoryResolver(kv, 0)
	if a, ok, err := resolve(ctx, user); err != nil || !ok || a != "r.o.a" {
		t.Fatalf("new form: %q %v %v", a, ok, err)
	}
	if _, ok, err := resolve(ctx, "UNKNOWN"); err != nil || ok {
		t.Fatalf("unknown: ok=%v err=%v", ok, err)
	}
}
