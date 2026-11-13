package policyapply

import (
	"context"
	"errors"
	"testing"

	"github.com/bogworks/mash/internal/listset"
)

// applyWorld is newWorld with generation 1 in force in a MemLedger that holds its JWT and list set.
func applyWorld(t *testing.T) (*world, *MemLedger, ApplyDeps) {
	t.Helper()
	w := newWorld(t)
	set := set1()
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1, ListSet: &set})
	in := w.run(l, set2(), "bytes-2")
	return w, l, ApplyDeps{
		Ledger: l, Cluster: w.cluster, Prober: w.prober, Witnesses: w.wit, Signer: in.Signer,
		Account: w.k.acctPub, Signers: w.k.signers, Now: fixedNow,
		AfterPush: func(context.Context) (map[string][]string, []string, error) {
			return map[string][]string{"foo.c.core": {w.k.revoked}}, nil, nil
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
	deps.Ledger = NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
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
	in := w.run(NewMemLedger(listset.Record{Generation: 1}), set2(), "bytes-2")
	in.Signer = withSignPrevious(in.Signer, func(PreviousClaims, []string) (string, error) {
		t.Error("SignPrevious called with nothing stored")
		return "", errors.New("called")
	})
	st, err := Run(context.Background(), in)
	if err != nil || st.State != StatePartial || !hasFailure(st, "", "", CheckRollback) {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestRunWithoutSigner(t *testing.T) {
	w := newWorld(t)
	in := w.run(NewMemLedger(listset.Record{Generation: 1}), set2(), "bytes-2")
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
