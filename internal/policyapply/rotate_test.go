package policyapply

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
)

type rotIssuer struct{ ev *events }

func (i rotIssuer) Issue(_ context.Context, address, pub, _ string, _ []string) (string, error) {
	i.ev.add("issue:" + address)
	return "jwt-" + pub, nil
}

// recEvents wraps a record and logs each Add.
type recEvents struct {
	IssueRecord
	ev *events
}

func (r recEvents) Add(ctx context.Context, e IssueEntry) error {
	r.ev.add("record:" + string(e.Purpose))
	return r.IssueRecord.Add(ctx, e)
}

type rotDeliverer struct {
	ev  *events
	err error
	on  func()
}

func (d rotDeliverer) Deliver(_ context.Context, address, _ string) error {
	d.ev.add("deliver:" + address)
	if d.on != nil {
		d.on()
	}
	return d.err
}

// rotConns shows a connection once ok is set.
type rotConns struct {
	ev *events
	ok *atomic.Bool
}

func (c rotConns) Connected(context.Context, string) (bool, error) {
	if c.ok.Load() {
		c.ev.add("connected-seen")
		return true, nil
	}
	return false, nil
}

type rotRevoker struct {
	ev   *events
	hook func(pubs []string) error
}

func (r rotRevoker) Revoke(_ context.Context, pubs []string) error {
	r.ev.add("revoke:" + strings.Join(pubs, ","))
	if r.hook != nil {
		return r.hook(pubs)
	}
	return nil
}

func userPub(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	p, _ := kp.PublicKey()
	return p
}

type rotEnv struct {
	t       *testing.T
	ev      *events
	rec     *MemIssueRecord
	in      RotateInput
	ok      *atomic.Bool
	old     []string
	probe   string
	address string
}

// newRotEnv has two old credentials (issue) and one probe for foo.c.core, and one credential for foo.c.docs.
func newRotEnv(t *testing.T, deadline time.Duration) *rotEnv {
	t.Helper()
	e := &rotEnv{t: t, ev: &events{}, rec: &MemIssueRecord{}, ok: &atomic.Bool{}, address: "foo.c.core"}
	ctx := context.Background()
	e.old = []string{userPub(t), userPub(t)}
	e.probe = userPub(t)
	add := func(addr, pub string, p Purpose) {
		if err := e.rec.Add(ctx, IssueEntry{Address: addr, PublicKey: pub, Purpose: p, IssuedAt: fixedNow()}); err != nil {
			t.Fatal(err)
		}
	}
	add(e.address, e.old[0], PurposeIssue)
	add(e.address, e.old[1], PurposeRotation)
	add(e.address, e.probe, PurposeProbe)
	add("foo.c.docs", userPub(t), PurposeIssue)
	e.in = RotateInput{
		Address: e.address, Name: "core", Tags: []string{"allow:c.sdk"}, NewPub: userPub(t), Generation: 2,
		Issuer: rotIssuer{e.ev}, Deliverer: rotDeliverer{ev: e.ev}, Conns: rotConns{e.ev, e.ok},
		Record: recEvents{e.rec, e.ev}, Revoker: rotRevoker{ev: e.ev},
		Deadline: time.Now().Add(deadline), Poll: 5 * time.Millisecond,
	}
	return e
}

func (e *rotEnv) revokedKeys() []string {
	var out []string
	for _, en := range e.rec.Entries() {
		if en.RevokedAt != nil {
			out = append(out, en.PublicKey)
		}
	}
	slices.Sort(out)
	return out
}

func TestRotateHappyPath(t *testing.T) {
	e := newRotEnv(t, 5*time.Second)
	e.in.Deliverer = rotDeliverer{ev: e.ev, on: func() {
		time.AfterFunc(40*time.Millisecond, func() { e.ok.Store(true) })
	}}
	res, err := Rotate(context.Background(), e.in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Connected || res.DeliverError != "" {
		t.Fatalf("result = %+v", res)
	}
	want := slices.Sorted(slices.Values(e.old))
	if !slices.Equal(res.Revoked, want) {
		t.Errorf("revoked %v, want %v", res.Revoked, want)
	}
	log := e.ev.get()
	idx := func(prefix string) int {
		return slices.IndexFunc(log, func(s string) bool { return strings.HasPrefix(s, prefix) })
	}
	ordered := idx("issue:") >= 0 && idx("issue:") < idx("record:rotation") && idx("record:rotation") < idx("deliver:") &&
		idx("deliver:") < idx("connected-seen") && idx("connected-seen") < idx("revoke:")
	if !ordered {
		t.Errorf("events out of order: %v", log)
	}
	if n := strings.Count(strings.Join(log, " "), "revoke:"); n != 1 {
		t.Errorf("%d revoke calls, want one: %v", n, log)
	}
	// The new key is recorded as a rotation with the target tags and generation, and is not revoked; the probe and
	// another address are untouched.
	got := e.rec.Entries()
	var found bool
	for _, en := range got {
		switch {
		case en.PublicKey == e.in.NewPub:
			found = true
			if en.Purpose != PurposeRotation || en.RevokedAt != nil || en.Generation != 2 || !slices.Equal(en.Tags, []string{"allow:c.sdk"}) {
				t.Errorf("new entry = %+v", en)
			}
		case en.PublicKey == e.probe || en.Address == "foo.c.docs":
			if en.RevokedAt != nil {
				t.Errorf("entry %+v was revoked", en)
			}
		}
	}
	if !found {
		t.Error("new key not in the record")
	}
	if !slices.Equal(e.revokedKeys(), want) {
		t.Errorf("record revoked %v, want %v", e.revokedKeys(), want)
	}
}

func TestRotateNeverConnectsRevokesAtDeadline(t *testing.T) {
	for name, derr := range map[string]error{"failed delivery": errors.New("disk full"), "false report": nil} {
		t.Run(name, func(t *testing.T) {
			e := newRotEnv(t, 150*time.Millisecond)
			e.in.Deliverer = rotDeliverer{ev: e.ev, err: derr}
			start := time.Now()
			res, err := Rotate(context.Background(), e.in)
			if err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d < 150*time.Millisecond {
				t.Errorf("returned after %v, before the deadline", d)
			}
			if res.Connected {
				t.Error("reported connected")
			}
			if (res.DeliverError != "") != (derr != nil) {
				t.Errorf("DeliverError = %q", res.DeliverError)
			}
			if !slices.Equal(res.Revoked, slices.Sorted(slices.Values(e.old))) {
				t.Errorf("revoked %v", res.Revoked)
			}
			if slices.Contains(e.ev.get(), "connected-seen") {
				t.Error("a connection was seen")
			}
		})
	}
}

func TestRotateRefusesRevokedAndKnownKeys(t *testing.T) {
	e := newRotEnv(t, time.Second)
	ctx := context.Background()
	if err := e.rec.MarkRevoked(ctx, e.old[0], fixedNow()); err != nil {
		t.Fatal(err)
	}
	if err := e.rec.Add(ctx, IssueEntry{Address: e.address, PublicKey: e.old[0], Purpose: PurposeIssue}); !errors.Is(err, ErrRevokedKey) {
		t.Errorf("Add of a revoked key: %v", err)
	}
	if err := e.rec.Add(ctx, IssueEntry{Address: "foo.c.sdk", PublicKey: e.old[1], Purpose: PurposeIssue}); !errors.Is(err, ErrDuplicateKey) {
		t.Errorf("Add of a duplicate key: %v", err)
	}
	e.in.NewPub = e.old[0]
	if _, err := Rotate(ctx, e.in); !errors.Is(err, ErrRevokedKey) {
		t.Errorf("Rotate to a revoked key: %v", err)
	}
	e.in.NewPub = e.old[1]
	if _, err := Rotate(ctx, e.in); !errors.Is(err, ErrDuplicateKey) {
		t.Errorf("Rotate to a known key: %v", err)
	}
	e.in.NewPub = "not-a-key"
	if _, err := Rotate(ctx, e.in); err == nil {
		t.Error("Rotate to a malformed key")
	}
	if log := e.ev.get(); len(log) != 0 {
		t.Errorf("a refused rotation did something: %v", log)
	}
}

func TestRotateRevokeFailureLeavesRecordUnmarked(t *testing.T) {
	e := newRotEnv(t, time.Second)
	e.ok.Store(true)
	e.in.Revoker = rotRevoker{ev: e.ev, hook: func([]string) error { return errors.New("push refused") }}
	if _, err := Rotate(context.Background(), e.in); err == nil {
		t.Fatal("no error")
	}
	if got := e.revokedKeys(); len(got) != 0 {
		t.Errorf("marked revoked without a revocation: %v", got)
	}
}

func TestRemoveSenderRevokesEveryKey(t *testing.T) {
	e := newRotEnv(t, time.Second)
	got, err := RemoveSender(context.Background(), e.address, e.rec, rotRevoker{ev: e.ev}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Sorted(slices.Values(e.old))
	if !slices.Equal(got, want) || !slices.Equal(e.revokedKeys(), want) {
		t.Errorf("revoked %v, record %v, want %v", got, e.revokedKeys(), want)
	}
	// A second call has nothing left to revoke.
	n := len(e.ev.get())
	if got, err := RemoveSender(context.Background(), e.address, e.rec, rotRevoker{ev: e.ev}, fixedNow); err != nil || len(got) != 0 || len(e.ev.get()) != n {
		t.Errorf("second removal: %v %v", got, err)
	}
}

func TestGroupUserIssuer(t *testing.T) {
	k := newKeys(t)
	sk, _ := nkeys.CreateAccount()
	signer := signerFunc(func(group string, uc *jwt.UserClaims) (string, error) {
		if group != "foo.c" {
			t.Errorf("group = %q", group)
		}
		return uc.Encode(sk)
	})
	pub := userPub(t)
	tags := []string{"allow:c.sdk", "deny-none"}
	tok, err := GroupUserIssuer{Signer: signer, Account: k.acctPub}.Issue(context.Background(), "foo.c.core", pub, "core", tags)
	if err != nil {
		t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	if uc.Subject != pub || uc.Name != "core" || !slices.Equal([]string(uc.Tags), tags) || uc.IssuerAccount != k.acctPub {
		t.Errorf("claims = %+v", uc)
	}
	if len(uc.Pub.Allow)+len(uc.Sub.Allow)+len(uc.Pub.Deny)+len(uc.Sub.Deny) != 0 {
		t.Error("the credential carries permissions")
	}
	if _, err := (GroupUserIssuer{Signer: signer, Account: k.acctPub}).Issue(context.Background(), "foo.c.core", "SUAAAA", "core", tags); err == nil {
		t.Error("issued to a malformed key")
	}
	if _, err := (GroupUserIssuer{Signer: signer, Account: k.acctPub}).Issue(context.Background(), "foo.c.core", pub, "core", []string{"Allow:C.sdk"}); err == nil {
		t.Error("issued with non-canonical tags")
	}
}

type signerFunc func(group string, uc *jwt.UserClaims) (string, error)

func (f signerFunc) SignUser(group string, uc *jwt.UserClaims) (string, error) { return f(group, uc) }

// rotRun runs set1 -> set2 (core's tags narrow) with the rotation of foo.c.core as AfterPush on the fake world.
func rotRun(t *testing.T, connect bool, mod ...func(*RunInput)) (Status, *world, *Ledger0, *rotEnv) {
	t.Helper()
	w := newWorld(t)
	e := newRotEnv(t, 200*time.Millisecond)
	e.ok.Store(connect)
	e.in.Revoker = rotRevoker{ev: e.ev, hook: func(pubs []string) error {
		// The fake push: every server now holds the target with these keys revoked too.
		tok := w.k.build(t, w.c2, append([]string{w.k.revoked}, pubs...)...)
		for _, s := range w.servers {
			w.cluster.set(s, tok)
		}
		return nil
	}}
	plan := &RotationPlan{
		Narrowed:   []NarrowedSender{{Address: e.address, Name: "core", Tags: e.in.Tags, NewPub: e.in.NewPub}},
		Generation: 2, Issuer: e.in.Issuer, Deliverer: e.in.Deliverer, Conns: e.in.Conns, Record: e.in.Record,
		Revoker: e.in.Revoker, Deadline: e.in.Deadline, Poll: e.in.Poll,
	}
	led := NewMemLedger(listset.Record{Generation: 1})
	in := w.run(led, set2(), "x")
	in.AfterPush = RotationsAfterPush(plan)
	// The inventory comes from the issue record through AfterPush: the caller passes none for core.
	in.OldKeys = map[string][]string{"foo.c.docs": {}, "foo.c.sdk": {}}
	for _, m := range mod {
		m(&in)
	}
	st, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return st, w, &Ledger0{led}, e
}

// Ledger0 reads the generation in force.
type Ledger0 struct{ l *MemLedger }

func (l *Ledger0) gen() uint64 { r, _ := l.l.InForce(); return r.Generation }

func TestRunRotationConnectedIsApplied(t *testing.T) {
	st, _, led, e := rotRun(t, true)
	if st.State != StateApplied || len(st.NotConnected) != 0 || len(st.Failing) != 0 {
		t.Fatalf("status = %+v", st)
	}
	if led.gen() != 2 {
		t.Errorf("generation in force = %d", led.gen())
	}
	if !SenderPassed(st, "foo.c.core") {
		t.Error("core did not pass")
	}
	if !slices.Equal(e.revokedKeys(), slices.Sorted(slices.Values(e.old))) {
		t.Errorf("revoked %v", e.revokedKeys())
	}
}

func TestRunRotationNotConnectedIsPartialAndNotCommitted(t *testing.T) {
	st, _, led, e := rotRun(t, false)
	if st.State != StatePartial || st.InForce {
		t.Fatalf("state = %s in force = %v", st.State, st.InForce)
	}
	if !slices.Equal(st.NotConnected, []string{"foo.c.core"}) {
		t.Errorf("not connected = %v", st.NotConnected)
	}
	if !slices.ContainsFunc(st.Failing, func(f Failure) bool { return f.Sender == "foo.c.core" && f.Check == CheckNotConnected }) {
		t.Errorf("failing = %+v", st.Failing)
	}
	if led.gen() != 1 {
		t.Errorf("generation in force = %d: something was committed", led.gen())
	}
	if SenderPassed(st, "foo.c.core") {
		t.Error("a not-connected sender passed")
	}
	if !SenderPassed(st, "foo.c.sdk") {
		t.Error("an unrelated sender did not pass")
	}
	// The old keys were revoked anyway and the servers hold the revocations.
	if !slices.Equal(e.revokedKeys(), slices.Sorted(slices.Values(e.old))) {
		t.Errorf("revoked %v", e.revokedKeys())
	}
	b, err := st.MarshalJSON()
	if err != nil || !strings.Contains(string(b), `"not_connected":["foo.c.core"]`) {
		t.Errorf("json = %s %v", b, err)
	}
}

func TestRotationsAfterPushLeavingAndOrder(t *testing.T) {
	e := newRotEnv(t, time.Second)
	e.ok.Store(true)
	plan := &RotationPlan{
		Narrowed: []NarrowedSender{{Address: e.address, Name: "core", Tags: e.in.Tags, NewPub: e.in.NewPub}},
		Leaving:  []string{"foo.c.docs"}, Generation: 2, Issuer: e.in.Issuer, Deliverer: e.in.Deliverer, Conns: e.in.Conns,
		Record: e.in.Record, Revoker: e.in.Revoker, Deadline: e.in.Deadline, Poll: e.in.Poll,
	}
	revoked, nc, err := RotationsAfterPush(plan)(context.Background())
	if err != nil || len(nc) != 0 {
		t.Fatalf("%v %v", nc, err)
	}
	var n int
	for _, keys := range revoked {
		if !slices.IsSorted(keys) {
			t.Errorf("revoked not sorted: %v", keys)
		}
		n += len(keys)
	}
	if n != 3 || len(revoked[e.address]) == 0 || len(revoked["foo.c.docs"]) == 0 {
		t.Errorf("revoked = %v", revoked)
	}
	if len(plan.Results) != 1 || !plan.Results[0].Connected {
		t.Errorf("results = %+v", plan.Results)
	}
}

// The old-key inventory of a rotated sender comes from the issue record: with no OldKeys entry for it, the gate still
// applies, and it fails when the rotation revoked nothing for a sender that needs its old keys revoked.
func TestRunRotationDerivesOldKeys(t *testing.T) {
	st, _, led, _ := rotRun(t, true)
	if st.State != StateApplied || led.gen() != 2 {
		t.Fatalf("%+v", st)
	}
	st, _, _, _ = rotRun(t, true, func(in *RunInput) {
		in.OldKeys = map[string][]string{"foo.c.docs": {}, "foo.c.sdk": {}, "foo.c.core": {freshUserPub(t)}}
	})
	if st.State != StatePartial || !hasFailure(st, "", "foo.c.core", CheckOldKeyNotRevoked) {
		t.Fatalf("a caller's old key that nothing revoked was accepted: %+v", st)
	}
}

// A not-connected run leaves the target pending: a different submission is refused, the same content is not, and
// BeforeCommit does not run.
func TestRunRotationNotConnectedIsPending(t *testing.T) {
	var ran bool
	var led *MemLedger
	st, _, _, _ := rotRun(t, false, func(in *RunInput) {
		led = in.Ledger.(*MemLedger)
		in.BeforeCommit = func(context.Context, Accepted, Status) error { ran = true; return nil }
	})
	if st.State != StatePartial || len(st.NotConnected) == 0 {
		t.Fatalf("%+v", st)
	}
	if ran {
		t.Error("BeforeCommit ran with a sender not connected")
	}
	pend, ok, _ := led.Pending()
	if !ok || pend.Generation != 2 {
		t.Fatalf("pending = %+v %v", pend, ok)
	}
}
