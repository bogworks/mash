package policyapply

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/listset"
)

// applyWorld is newWorld with generation 1 in force in a MemLedger that holds its JWT and list set.
func applyWorld(t *testing.T) (*world, *MemLedger, ApplyDeps) {
	t.Helper()
	w := newWorld(t)
	set := set1()
	l := NewMemLedgerWith(w.rec1(), InForceDetail{JWT: w.j1, ListSet: &set})
	in := w.run(l, set2(), "bytes-2")
	rec := &MemIssueRecord{}
	if err := rec.Add(context.Background(), IssueEntry{Address: "foo.c.core", PublicKey: w.k.revoked, Purpose: PurposeIssue, IssuedAt: time.Unix(1000, 0)}); err != nil {
		t.Fatal(err)
	}
	return w, l, ApplyDeps{
		IssueRecord: rec,
		Ledger:      l, Cluster: w.cluster, Prober: w.prober, Witnesses: w.wit, Signer: in.Signer,
		Account: w.k.acctPub, Signers: w.k.signers, Now: fixedNow,
		AfterPush: func(context.Context) (map[string][]string, []string, string, error) {
			return map[string][]string{"foo.c.core": {w.k.revoked}}, nil, w.k.build(t, w.c2, w.k.revoked), nil
		},
	}
}

func applyReq() ApplyRequest {
	return ApplyRequest{ListSet: set2(), Received: []byte("bytes-2"), Generation: 2, BaseGeneration: 1}
}

func TestApplyApplied(t *testing.T) {
	w, l, deps := applyWorld(t)
	deps.Inventory = 0
	var signed string
	deps.Signer = withSign(deps.Signer, func(rec listset.Record, c *listset.Compiled) (string, error) {
		signed = w.k.build(t, c, w.k.revoked)
		return signed, nil
	})
	st, err := Apply(context.Background(), applyReq(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateApplied || !st.InForce || st.Generation != 2 {
		t.Fatalf("%+v", st)
	}
	if rec, _ := l.InForce(); rec.Generation != 2 {
		t.Errorf("in force = %d", rec.Generation)
	}
	if got, _ := l.InForceJWT(); got == "" || got != signed {
		t.Errorf("in-force JWT = %q, want the pushed %q", got, signed)
	}
	if ls, ok, _ := l.InForceListSet(); !ok || ls.Base != 1 {
		t.Errorf("in-force list set = %+v %v", ls, ok)
	}
}

func TestApplyStaleBaseRefusedBeforePush(t *testing.T) {
	w, _, deps := applyWorld(t)
	signed := false
	deps.Signer = withSign(deps.Signer, func(listset.Record, *listset.Compiled) (string, error) {
		signed = true
		return "", errors.New("signed")
	})
	req := applyReq()
	req.BaseGeneration, req.Generation, req.ListSet.Base = 0, 1, 0
	if _, err := Apply(context.Background(), req, deps); !errors.Is(err, listset.ErrStaleBase) {
		t.Fatalf("err = %v", err)
	}
	// A stale base is refused first, even with bad generation arithmetic.
	req = applyReq()
	req.BaseGeneration, req.Generation, req.ListSet.Base = 0, 5, 7
	if _, err := Apply(context.Background(), req, deps); !errors.Is(err, listset.ErrStaleBase) || errors.Is(err, ErrBadGeneration) {
		t.Fatalf("err = %v", err)
	}
	req = applyReq()
	req.Generation = 3
	if _, err := Apply(context.Background(), req, deps); !errors.Is(err, ErrBadGeneration) {
		t.Fatalf("err = %v", err)
	}
	req = applyReq()
	req.ListSet.Base = 0
	if _, err := Apply(context.Background(), req, deps); !errors.Is(err, ErrBadGeneration) {
		t.Fatalf("err = %v", err)
	}
	if signed || len(w.cluster.pushes) != 0 {
		t.Errorf("signed %v, pushes %d", signed, len(w.cluster.pushes))
	}
}

func TestApplyNoListSetInForce(t *testing.T) {
	w, _, deps := applyWorld(t)
	deps.Ledger = NewMemLedgerWith(w.rec1(), InForceDetail{JWT: w.j1})
	if _, err := Apply(context.Background(), applyReq(), deps); !errors.Is(err, ErrNoInForceListSet) {
		t.Fatalf("err = %v", err)
	}
	if len(w.cluster.pushes) != 0 {
		t.Error("pushed")
	}
}

func TestApplyFirstGeneration(t *testing.T) {
	w, _, deps := applyWorld(t)
	l := NewMemLedger(listset.Record{})
	deps.Ledger = l
	deps.AfterPush = nil
	deps.Signer = withSign(deps.Signer, func(_ listset.Record, c *listset.Compiled) (string, error) { return w.k.build(t, c), nil })
	req := ApplyRequest{ListSet: set1(), Received: []byte("bytes-1"), Generation: 1}
	st, err := Apply(context.Background(), req, deps)
	if err != nil || st.Generation != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	if rec, _ := l.InForce(); rec.Generation != 1 {
		t.Errorf("in force = %d", rec.Generation)
	}
	if got, _ := l.InForceJWT(); got == "" {
		t.Error("no in-force JWT stored")
	}
}

// A rollback gets the JWT the ledger stored for the generation in force, and a ledger with none cannot roll back.
func TestApplyRollbackUsesStoredPreviousJWT(t *testing.T) {
	w, _, deps := applyWorld(t)
	w.cluster.failPush["s2"] = true
	var got []PreviousClaims
	deps.Signer = withSignPrevious(deps.Signer, func(prev PreviousClaims, revoked []string) (string, error) {
		got = append(got, prev)
		return w.k.build(t, w.c1, revoked...), nil
	})
	st, err := Apply(context.Background(), applyReq(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StatePartial || st.InForce {
		t.Fatalf("%+v", st)
	}
	if len(got) != 1 || got[0].JWT != w.j1 {
		t.Fatalf("SignPrevious got %+v, want the stored JWT %q", got, w.j1)
	}
}

func TestRollbackWithoutStoredJWTFailsClosed(t *testing.T) {
	w := newWorld(t)
	w.cluster.failPush["s2"] = true
	in := w.run(NewMemLedger(w.rec1()), set2(), "bytes-2")
	in.Signer = withSignPrevious(in.Signer, func(PreviousClaims, []string) (string, error) {
		t.Error("SignPrevious called with nothing stored")
		return "", errors.New("called")
	})
	pushes := len(w.cluster.pushes)
	// A generation in force with no stored JWT: its revocations cannot be kept, so the run is refused before any push.
	if _, err := Run(context.Background(), in); !errors.Is(err, ErrSignedMismatch) {
		t.Fatalf("err = %v", err)
	}
	if len(w.cluster.pushes) != pushes {
		t.Errorf("pushes %d -> %d", pushes, len(w.cluster.pushes))
	}
}

func TestRunWithoutSigner(t *testing.T) {
	w := newWorld(t)
	in := w.run(NewMemLedger(w.rec1()), set2(), "bytes-2")
	in.Signer = nil
	if _, err := Run(context.Background(), in); !errors.Is(err, ErrNoSigner) {
		t.Fatalf("err = %v", err)
	}
}

func TestSignerFuncs(t *testing.T) {
	var s Signer = SignerFuncs{
		SignFn: func(rec listset.Record, _ *listset.Compiled) (string, error) { return "t", nil },
		SignPreviousFn: func(prev PreviousClaims, revoked []string) (string, error) {
			return prev.JWT + ":" + revoked[0], nil
		},
	}
	if tok, err := s.Sign(listset.Record{}, nil); err != nil || tok != "t" {
		t.Errorf("Sign = %q %v", tok, err)
	}
	if tok, err := s.SignPrevious(PreviousClaims{JWT: "p"}, []string{"k"}); err != nil || tok != "p:k" {
		t.Errorf("SignPrevious = %q %v", tok, err)
	}
	empty := SignerFuncs{}
	if _, err := empty.Sign(listset.Record{}, nil); err == nil {
		t.Error("nil SignFn did not fail")
	}
	if _, err := empty.SignPrevious(PreviousClaims{}, nil); err == nil {
		t.Error("nil SignPreviousFn did not fail")
	}
}

// statusLedger is a MemLedger that also holds a latest status.
type statusLedger struct {
	*MemLedger
	st *Status
}

func (l statusLedger) LatestStatus(context.Context) (Status, bool, error) {
	if l.st == nil {
		return Status{}, false, nil
	}
	return *l.st, true, nil
}

// A replay of the document in force is refused like any stale base and writes nothing.
func TestApplyReplayIsStaleBase(t *testing.T) {
	w, l, deps := applyWorld(t)
	deps.Signer = withSign(deps.Signer, func(_ listset.Record, c *listset.Compiled) (string, error) {
		return w.k.build(t, c, w.k.revoked), nil
	})
	if st, err := Apply(context.Background(), applyReq(), deps); err != nil || st.State != StateApplied {
		t.Fatalf("%+v %v", st, err)
	}
	pushes := len(w.cluster.pushes)
	signed := false
	deps.Signer = withSign(deps.Signer, func(listset.Record, *listset.Compiled) (string, error) {
		signed = true
		return "", errors.New("signed")
	})
	stored := Status{Generation: 3, State: StateRefused, UpdatedAt: "stored"}
	sl := statusLedger{MemLedger: l, st: &stored}
	deps.Ledger = sl
	// The same bytes again: the base is now stale.
	st, err := Apply(context.Background(), applyReq(), deps)
	var sb *listset.StaleBaseError
	if !errors.Is(err, listset.ErrStaleBase) || !errors.As(err, &sb) {
		t.Fatalf("err = %v", err)
	}
	if st.State != "" || signed || len(w.cluster.pushes) != pushes {
		t.Errorf("status %+v, signed %v, pushes %d -> %d", st, signed, pushes, len(w.cluster.pushes))
	}
	if stored.UpdatedAt != "stored" || stored.Generation != 3 {
		t.Errorf("status written: %+v", stored)
	}
}

// Apply refuses, before pushing anything, an old-key inventory that is missing or incomplete (ADR 0113, ADR 0114).
func TestApplyRefusesIncompleteInventory(t *testing.T) {
	cases := map[string]func(t *testing.T, w *world, deps *ApplyDeps){
		"no record for a credential-only sender": func(_ *testing.T, _ *world, d *ApplyDeps) { d.IssueRecord = nil },
		"record holds none for the sender":       func(_ *testing.T, _ *world, d *ApplyDeps) { d.IssueRecord = &MemIssueRecord{} },
		"a credential without an issue time": func(t *testing.T, w *world, d *ApplyDeps) {
			rec := &MemIssueRecord{}
			if err := rec.Add(context.Background(), IssueEntry{Address: "foo.c.core", PublicKey: w.k.revoked, Purpose: PurposeIssue}); err != nil {
				t.Fatal(err)
			}
			d.IssueRecord = rec
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			w, l, deps := applyWorld(t)
			mod(t, w, &deps)
			pushed := false
			deps.Signer = withSign(deps.Signer, func(listset.Record, *listset.Compiled) (string, error) {
				pushed = true
				return "", errors.New("signed")
			})
			if _, err := Apply(context.Background(), applyReq(), deps); !errors.Is(err, ErrIncompleteInventory) {
				t.Fatalf("err = %v", err)
			}
			if pushed {
				t.Error("something was signed before the refusal")
			}
			if rec, _ := l.InForce(); rec.Generation != 1 {
				t.Errorf("in force = %d", rec.Generation)
			}
		})
	}
}

// With the record, OldKeys carries each unrevoked key with its issue time; an already revoked key is not inventory.
func TestOldKeysFromRecord(t *testing.T) {
	w, _, deps := applyWorld(t)
	rec := deps.IssueRecord.(*MemIssueRecord)
	ctx := context.Background()
	at := time.Unix(2000, 0)
	for _, e := range []IssueEntry{
		{Address: "foo.c.core", PublicKey: "UREV", Purpose: PurposeRotation, IssuedAt: at},
		{Address: "foo.c.core", PublicKey: "UPROBE", Purpose: PurposeProbe},
		{Address: "foo.c.docs", PublicKey: "UDOCS", Purpose: PurposeIssue, IssuedAt: at},
		{Address: "admin", PublicKey: "UADM", Purpose: PurposeIssue},
	} {
		if err := rec.Add(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := rec.MarkRevoked(ctx, "UREV", at); err != nil {
		t.Fatal(err)
	}
	prev, _ := listset.Compile(set1())
	got, err := oldKeysFromRecord(ctx, rec, prev, set2())
	if err != nil {
		t.Fatal(err)
	}
	if k := got["foo.c.core"]; len(k) != 1 || k[0].PublicKey != w.k.revoked || !k[0].IssuedAt.Equal(time.Unix(1000, 0)) {
		t.Errorf("core = %+v", k)
	}
	if k := got["foo.c.docs"]; len(k) != 1 || k[0].PublicKey != "UDOCS" {
		t.Errorf("docs = %+v", k)
	}
	if _, has := got["admin"]; has {
		t.Error("a record entry of no sender is inventory")
	}
}

// A Provisioner without provisioning markers is refused before anything is signed or pushed: the guard of RunOrdered
// only works on a ledger that keeps markers, and Apply's plain ledger does not.
func TestApplyRefusesProvisionerWithoutMarkers(t *testing.T) {
	_, l, deps := applyWorld(t)
	ev := &events{}
	deps.Provisioner = &recProv{ev: ev}
	signed := false
	deps.Signer = withSign(deps.Signer, func(listset.Record, *listset.Compiled) (string, error) {
		signed = true
		return "", errors.New("signed")
	})
	if _, err := Apply(context.Background(), applyReq(), deps); !errors.Is(err, ErrNoProvisionMarkers) {
		t.Fatalf("err = %v, want ErrNoProvisionMarkers", err)
	}
	if signed || len(ev.get()) != 0 {
		t.Errorf("something ran before the refusal: signed %v, events %v", signed, ev.get())
	}
	if rec, _ := l.InForce(); rec.Generation != 1 {
		t.Errorf("in force = %d", rec.Generation)
	}
}

// With deps.ProvisionMarkers set, Apply's run writes a marker before CreateInbox, and clears it on commit.
func TestApplyWithProvisionMarkersMarksBeforeCreateInbox(t *testing.T) {
	_, l, deps := applyWorld(t)
	m := NewMemProvisionMarkers()
	deps.ProvisionMarkers = m
	req := applyReq()
	req.ListSet.Senders = append(req.ListSet.Senders, listset.SenderLists{Sender: "foo.c.new"})
	var atCreate []string
	deps.Provisioner = &markSpyProv{create: func(string) { atCreate, _ = m.Provisioning(2) }}
	st, err := Apply(context.Background(), req, deps)
	if err != nil {
		t.Fatalf("apply: %v (%+v)", err, st)
	}
	if rec, _ := l.InForce(); rec.Generation != 2 {
		t.Fatalf("in force = %d, status %+v", rec.Generation, st)
	}
	if !slices.Equal(atCreate, []string{"foo.c.new"}) {
		t.Errorf("markers when CreateInbox ran = %v", atCreate)
	}
	if left, _ := m.Provisioning(2); len(left) != 0 {
		t.Errorf("markers after commit = %v", left)
	}
}

type markSpyProv struct{ create func(string) }

func (p *markSpyProv) CreateInbox(_ context.Context, a string) error { p.create(a); return nil }
func (p *markSpyProv) IssueCredential(context.Context, string) error { return nil }

// OldKeys carries the greatest issue time of a key's revisions, and an incomplete history refuses the inventory.
func TestOldKeysFromAdminRecordUsesMaxIssued(t *testing.T) {
	w, _, _ := applyWorld(t)
	ctx := context.Background()
	f := &fakeIssued{}
	r := NewAdminIssueRecord(f)
	future := time.Unix(4_000_000_000, 0)
	for _, at := range []time.Time{future, time.Unix(1000, 0)} {
		if err := r.Add(ctx, IssueEntry{Address: "foo.c.core", PublicKey: w.k.revoked, Purpose: PurposeIssue, IssuedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	prev, _ := listset.Compile(set1())
	got, err := oldKeysFromRecord(ctx, r, prev, set2())
	if err != nil {
		t.Fatal(err)
	}
	if k := got["foo.c.core"]; len(k) != 1 || !k[0].IssuedAt.Equal(future) {
		t.Errorf("core = %+v, want the future issue time", k)
	}
	f.histErr = map[string]error{w.k.revoked: &admin.IncompleteHistoryError{Key: w.k.revoked}}
	if _, err := oldKeysFromRecord(ctx, r, prev, set2()); !errors.Is(err, ErrIncompleteInventory) {
		t.Errorf("incomplete history: err = %v", err)
	}
}
