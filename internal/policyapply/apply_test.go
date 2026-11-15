package policyapply

import (
	"context"
	"errors"
	"slices"
	"strings"
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
		IssueRecord: rec, ProvisionMarkers: NewMemProvisionMarkers(),
		Ledger: l, Cluster: w.cluster, Prober: w.prober, Witnesses: w.wit, Signer: in.Signer,
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
	// The JWT in force is the final one AfterPush returns (it may be built a second after the target).
	if inner := deps.AfterPush; inner != nil {
		deps.AfterPush = func(ctx context.Context) (map[string][]string, []string, string, error) {
			rev, nc, final, err := inner(ctx)
			if final != "" {
				signed = final
			}
			return rev, nc, final, err
		}
	}
	deps.Signer = withSign(deps.Signer, func(rec listset.Record, c *listset.Compiled) (string, error) {
		tok := w.k.build(t, c, w.k.revoked)
		if signed == "" {
			signed = tok
		}
		return tok, nil
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
		"a revoked credential without an issue time": func(t *testing.T, w *world, d *ApplyDeps) {
			rec := &MemIssueRecord{}
			at := time.Unix(1500, 0)
			if err := rec.Add(context.Background(), IssueEntry{Address: "foo.c.core", PublicKey: w.k.revoked, Purpose: PurposeIssue, RevokedAt: &at}); err != nil {
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

// With the record, OldKeys carries each credential key with its issue time, an already revoked key included: the gate
// must see that a server's revocation of it is not older than the credential.
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
	core := got["foo.c.core"]
	if len(core) != 2 {
		t.Fatalf("core = %+v", core)
	}
	for _, k := range core {
		switch {
		case k.PublicKey == w.k.revoked && k.IssuedAt.Equal(time.Unix(1000, 0)):
		case k.PublicKey == "UREV" && k.IssuedAt.Equal(at):
		default:
			t.Errorf("unexpected core key %+v", k)
		}
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
	deps.ProvisionMarkers = nil
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

// ownMarkersLedger is a MemLedger that keeps its provisioning markers itself.
type ownMarkersLedger struct {
	*MemLedger
	*MemProvisionMarkers
}

// A retry with no Provisioner still honours a marker a crashed run left: the rollback is refused and the generation
// stays pending, whether the markers come from deps or from the ledger itself.
func TestApplyWithoutProvisionerRefusesRollbackWhileMarkerStands(t *testing.T) {
	for name, setup := range map[string]func(deps *ApplyDeps, l *MemLedger, pm *MemProvisionMarkers){
		"deps markers": func(d *ApplyDeps, _ *MemLedger, pm *MemProvisionMarkers) { d.ProvisionMarkers = pm },
		"ledger markers": func(d *ApplyDeps, l *MemLedger, pm *MemProvisionMarkers) {
			d.ProvisionMarkers, d.Ledger = nil, ownMarkersLedger{l, pm}
		},
	} {
		t.Run(name, func(t *testing.T) {
			w, l, deps := applyWorld(t)
			pm := NewMemProvisionMarkers()
			if err := pm.MarkProvisioning("foo.c.new", 2); err != nil {
				t.Fatal(err)
			}
			setup(&deps, l, pm)
			w.cluster.dropN["s2"] = dropTargetPushes // the gate fails and the run would roll back
			_, err := Apply(context.Background(), applyReq(), deps)
			if !errors.Is(err, ErrProvisioningMarker) {
				t.Fatalf("err = %v, want ErrProvisioningMarker", err)
			}
			if pend, ok, _ := l.Pending(); !ok || pend.Generation != 2 {
				t.Errorf("pending = %+v %v: the generation must stay pending", pend, ok)
			}
			if got, _ := pm.Provisioning(2); !slices.Equal(got, []string{"foo.c.new"}) {
				t.Errorf("markers = %v, want the marker to stand", got)
			}
		})
	}
}

// With markers kept and none standing, a failed gate still rolls back and clears the pending record.
func TestApplyWithoutProvisionerRollsBackWithNoMarker(t *testing.T) {
	w, l, deps := applyWorld(t)
	deps.ProvisionMarkers = NewMemProvisionMarkers()
	w.cluster.dropN["s2"] = dropTargetPushes // the rollback push reaches s2, so the rollback is confirmed
	if _, err := Apply(context.Background(), applyReq(), deps); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := l.Pending(); ok {
		t.Error("pending stayed after a rollback")
	}
}

// A retry after an earlier revocation that was too early: the old key is already marked revoked in the record, but a
// credential for it was issued with a time after that revocation. The key stays in the inventory with its issue time,
// so the gate sees the revocation on the servers is older than the credential and the change is not applied.
func TestApplyRevokedFutureDatedOldKeyFailsTheGate(t *testing.T) {
	w, l, deps := applyWorld(t)
	ctx := context.Background()
	const future = time.Hour
	issued := time.Now().Add(future)
	rec := &MemIssueRecord{}
	if err := rec.Add(ctx, IssueEntry{Address: "foo.c.core", PublicKey: w.k.revoked, Purpose: PurposeIssue, IssuedAt: issued}); err != nil {
		t.Fatal(err)
	}
	if err := rec.MarkRevoked(ctx, w.k.revoked, time.Now()); err != nil {
		t.Fatal(err)
	}
	deps.IssueRecord = rec
	st, err := Apply(ctx, applyReq(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if st.State == StateApplied || st.InForce {
		t.Fatalf("applied although the revocation is older than the credential: %+v", st)
	}
	var named bool
	for _, f := range st.Failing {
		named = named || (f.Check == CheckRevoked && strings.Contains(f.Detail, "older than the credential"))
	}
	if !named {
		t.Errorf("the early revocation is not named: %+v", st.Failing)
	}
	if got, _ := l.InForce(); got.Generation != 1 {
		t.Errorf("in force = %d", got.Generation)
	}
}

// Without a Provisioner Apply still needs provisioning markers: a retry on the bare ledger could otherwise roll back
// after a crashed run left an inbox.
func TestApplyRefusesNoProvisionerWithoutMarkers(t *testing.T) {
	_, l, deps := applyWorld(t)
	deps.ProvisionMarkers = nil
	signed := false
	deps.Signer = withSign(deps.Signer, func(listset.Record, *listset.Compiled) (string, error) {
		signed = true
		return "", errors.New("signed")
	})
	if _, err := Apply(context.Background(), applyReq(), deps); !errors.Is(err, ErrNoProvisionMarkers) {
		t.Fatalf("err = %v, want ErrNoProvisionMarkers", err)
	}
	if signed {
		t.Error("signed before the refusal")
	}
	if rec, _ := l.InForce(); rec.Generation != 1 {
		t.Errorf("in force = %d, want 1", rec.Generation)
	}
}

// A list set with the content in force is refused as unchanged before anything is signed or pushed; the record in
// force keeps its generation and nothing becomes pending (ADR 0119).
func TestRunSameContentRefusedAsUnchanged(t *testing.T) {
	w := newWorld(t)
	set := set1()
	l := NewMemLedgerWith(w.rec1(), InForceDetail{JWT: w.j1, ListSet: &set})
	same := set1()
	same.Base = 1
	in := w.run(l, same, "bytes-same")
	signed := false
	in.Signer = withSign(in.Signer, func(listset.Record, *listset.Compiled) (string, error) {
		signed = true
		return "", errors.New("signed")
	})
	st, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateRefused || st.InForce || len(st.Refusals) != 1 {
		t.Fatalf("%+v", st)
	}
	want := listset.Refusal{Stage: StageUnchanged, Reason: "same content as generation 1 in force; nothing to apply"}
	if st.Refusals[0] != want {
		t.Errorf("refusal = %+v, want %+v", st.Refusals[0], want)
	}
	if signed || len(w.cluster.pushes) != 0 {
		t.Errorf("signed %v, pushes %d", signed, len(w.cluster.pushes))
	}
	if rec, _ := l.InForce(); rec != w.rec1() {
		t.Errorf("in force = %+v", rec)
	}
	if _, has, _ := l.Pending(); has {
		t.Error("a generation is pending")
	}
	if got, _ := l.InForceJWT(); got != w.j1 {
		t.Error("the stored JWT changed")
	}
}

// A submission that starts or completes a planned submitter rotation applies as a new generation even when its
// content is the content in force, because the rotation completes only after a successful apply (ADR 0119, 0112).
func TestRunRotationStepAppliesSameContent(t *testing.T) {
	w := newWorld(t)
	set := set1()
	l := NewMemLedgerWith(w.rec1(), InForceDetail{JWT: w.j1, ListSet: &set})
	same := set1()
	same.Base = 1
	in := w.run(l, same, "bytes-same")
	in.RotationStep = true
	st, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateApplied || !st.InForce || st.Generation != 2 || len(st.Refusals) != 0 {
		t.Fatalf("%+v", st)
	}
	if rec, _ := l.InForce(); rec.Generation != 2 {
		t.Errorf("in force = %d, want 2", rec.Generation)
	}
}

// TestApplyPreviousSignedInTheOtherPolicyForm: the generation in force was signed with the other form of the policy
// line than the default (ADR 0129); Apply compiles it in that form, so the previous digests to what was signed and
// there is no ErrPreviousMismatch.
func TestApplyPreviousSignedInTheOtherPolicyForm(t *testing.T) {
	for name, form := range map[string]listset.PolicyForm{"agents": listset.PolicyFormAgents, "mash": listset.PolicyFormMash} {
		t.Run(name, func(t *testing.T) {
			w, l, deps := applyWorld(t)
			set := set1()
			c1, err := listset.CompileForm(set, form)
			if err != nil {
				t.Fatal(err)
			}
			if form != listset.DefaultPolicyForm && listset.DigestCompiled(c1) == listset.DigestCompiled(w.c1) {
				t.Fatal("the two forms digest alike")
			}
			j1 := w.k.build(t, c1)
			w.cluster = newCluster(j1, w.servers...)
			w.prober = fakeProber{cluster: w.cluster, byGen: map[uint64]*listset.Compiled{1: c1, 2: w.c2}, allow: map[string]bool{}}
			l = NewMemLedgerWith(listset.Record{Generation: 1, Signed: listset.DigestCompiled(c1)}, InForceDetail{JWT: j1, ListSet: &set})
			deps.Ledger, deps.Cluster, deps.Prober = l, w.cluster, w.prober
			deps.Signer = SignerFuncs{
				SignPreviousFn: func(_ PreviousClaims, revoked []string) (string, error) { return w.k.build(t, c1, revoked...), nil },
				SignFn: func(_ listset.Record, c *listset.Compiled) (string, error) {
					return w.k.build(t, c, w.k.revoked), nil
				},
			}
			st, err := Apply(context.Background(), applyReq(), deps)
			if errors.Is(err, ErrPreviousMismatch) {
				t.Fatalf("previous mismatch: %v", err)
			}
			if err != nil || st.State != StateApplied {
				t.Fatalf("%+v %v", st, err)
			}
		})
	}
}
