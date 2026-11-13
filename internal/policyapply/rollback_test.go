package policyapply

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
)

func freshUserPub(t *testing.T) string {
	t.Helper()
	kp, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	return pub
}

// A rollback after AfterPush revoked a key pushes a JWT that still revokes it, and the gate still reads it as the
// previous generation.
func TestRollbackKeepsAfterPushRevocations(t *testing.T) {
	w := newWorld(t)
	extra := freshUserPub(t)
	w.cluster.failPush["s2"] = true
	in := w.run(NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1}), set2(), "bytes-2")
	in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) {
		return map[string][]string{"": {extra}}, nil, "", nil
	}
	var got []string
	sign := in.Signer.SignPrevious
	in.Signer = withSignPrevious(in.Signer, func(prev PreviousClaims, revoked []string) (string, error) {
		got = revoked
		if prev.JWT != w.j1 {
			t.Errorf("SignPrevious got prev %q, want the JWT in force", prev.JWT)
		}
		return sign(prev, revoked)
	})
	st, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Sorted(slices.Values([]string{w.k.revoked, extra}))
	if !slices.Equal(got, want) {
		t.Errorf("SignPrevious got %v, want %v", got, want)
	}
	if st.State != StatePartial || st.InForce {
		t.Fatalf("%+v", st)
	}
	if g := gens(st); g["s1"] != "1" {
		t.Errorf("servers = %v (s1 must be on the previous generation)", g)
	}
	jwts, _ := w.cluster.AccountJWTs(context.Background(), "")
	// s2 refuses every push, so s1 is the server that took the rollback.
	for _, s := range []string{"s1"} {
		c, err := jwt.DecodeAccountClaims(jwts[s])
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range want {
			if _, has := c.Revocations[k]; !has {
				t.Errorf("%s: rolled back JWT un-revoked %s", s, k)
			}
		}
	}
}

// A SignPrevious that drops a revocation is not pushed: the servers stay split and the change is partial.
func TestRollbackDroppedRevocationIsNotPushed(t *testing.T) {
	w := newWorld(t)
	extra := freshUserPub(t)
	w.cluster.failPush["s2"] = true
	in := w.run(NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1}), set2(), "bytes-2")
	in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) {
		return map[string][]string{"": {extra}}, nil, "", nil
	}
	bad := w.k.build(t, w.c1, w.k.revoked) // lacks extra
	in.Signer = withSignPrevious(in.Signer, func(PreviousClaims, []string) (string, error) { return bad, nil })
	st, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StatePartial || st.InForce {
		t.Fatalf("%+v", st)
	}
	if slices.Contains(w.cluster.pushes, bad) {
		t.Error("the rollback that drops a revocation was pushed")
	}
	var found bool
	for _, f := range st.Failing {
		if f.Check == CheckRollback && strings.Contains(f.Detail, extra) {
			found = true
		}
	}
	if !found {
		t.Errorf("no rollback failure naming %s: %+v", extra, st.Failing)
	}
	jwts, _ := w.cluster.AccountJWTs(context.Background(), "")
	if jwts["s1"] == bad || jwts["s2"] != w.j1 {
		t.Error("the servers were not left as they were")
	}
}

func TestRollbackSignErrorAndWrongGeneration(t *testing.T) {
	type fn = func(PreviousClaims, []string) (string, error)
	cases := map[string]func(w *world) fn{
		"error": func(*world) fn {
			return func(PreviousClaims, []string) (string, error) { return "", context.Canceled }
		},
		"wrong": func(w *world) fn {
			return func(PreviousClaims, []string) (string, error) { return w.j2, nil }
		},
		"not a jwt": func(*world) fn { return func(PreviousClaims, []string) (string, error) { return "x", nil } },
	}
	for name, sp := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.cluster.failPush["s2"] = true
			in := w.run(NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1}), set2(), "bytes-2")
			in.Signer = withSignPrevious(in.Signer, sp(w))
			st, err := Run(context.Background(), in)
			if err != nil || st.State != StatePartial || !hasFailure(st, "", "", CheckRollback) {
				t.Fatalf("%+v %v", st, err)
			}
		})
	}
}

// afterPushRevoking is an AfterPush that revokes extra by pushing final (the account JWT with the revocation) to every
// server and reporting it.
func afterPushRevoking(w *world, extra, final string) func(context.Context) (map[string][]string, []string, string, error) {
	return func(ctx context.Context) (map[string][]string, []string, string, error) {
		_, _ = w.cluster.Push(ctx, final, w.servers)
		return map[string][]string{"": {extra}}, nil, final, nil
	}
}

// The JWT stored at commit is the one AfterPush left on the servers, with its revocation, not the target as first signed.
func TestCommitStoresFinalJWTWithRevocations(t *testing.T) {
	w := newWorld(t)
	extra := freshUserPub(t)
	final := w.k.build(t, w.c2, w.k.revoked, extra)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.AfterPush = afterPushRevoking(w, extra, final)
	st, err := Run(context.Background(), in)
	if err != nil || st.State != StateApplied {
		t.Fatalf("%+v %v", st, err)
	}
	got, err := l.InForceJWT()
	if err != nil || got != final {
		t.Fatalf("stored JWT = %q, %v; want the final JWT", got, err)
	}
	c, err := jwt.DecodeAccountClaims(got)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := c.Revocations[extra]; !has {
		t.Error("the stored JWT does not revoke the key AfterPush revoked")
	}
}

// A later rollback is built on the stored final JWT, so PreviousClaims.JWT carries the revocation.
func TestLaterRollbackBaseCarriesRevocation(t *testing.T) {
	w := newWorld(t)
	extra := freshUserPub(t)
	final := w.k.build(t, w.c2, w.k.revoked, extra)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.AfterPush = afterPushRevoking(w, extra, final)
	if _, err := Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	prev, err := l.InForceJWT()
	if err != nil {
		t.Fatal(err)
	}
	c, err := jwt.DecodeAccountClaims(prev)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := c.Revocations[extra]; !has {
		t.Fatalf("PreviousClaims.JWT would lack the revocation of %s", extra)
	}
}

// Converge's rollback keeps every revocation of the final JWT, even one the run's list does not name.
func TestRollbackKeepsFinalJWTRevocations(t *testing.T) {
	w := newWorld(t)
	extra := freshUserPub(t)
	final := w.k.build(t, w.c2, w.k.revoked, extra)
	w.cluster.failPush["s2"] = true
	in := w.run(NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1}), set2(), "bytes-2")
	in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) {
		return nil, nil, final, nil
	}
	var got []string
	sign := in.Signer.SignPrevious
	in.Signer = withSignPrevious(in.Signer, func(prev PreviousClaims, revoked []string) (string, error) {
		got = revoked
		return sign(prev, revoked)
	})
	if _, err := Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, extra) {
		t.Errorf("SignPrevious revoked %v, want it to include %s", got, extra)
	}
	// A rollback that drops that revocation is refused.
	w2 := newWorld(t)
	w2.cluster.failPush["s2"] = true
	final2 := w2.k.build(t, w2.c2, w2.k.revoked, extra)
	in = w2.run(NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w2.j1}), set2(), "bytes-2")
	in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) {
		return nil, nil, final2, nil
	}
	bad := w2.k.build(t, w2.c1, w2.k.revoked)
	in.Signer = withSignPrevious(in.Signer, func(PreviousClaims, []string) (string, error) { return bad, nil })
	st, err := Run(context.Background(), in)
	if err != nil || !hasFailure(st, "", "", CheckRollback) || slices.Contains(w2.cluster.pushes, bad) {
		t.Fatalf("%+v %v", st, err)
	}
}

// A Revoker whose JWT lacks the revocation is not committed: the generation is pending.
func TestFinalJWTMissingRevocationIsNotCommitted(t *testing.T) {
	w := newWorld(t)
	extra := freshUserPub(t)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.AfterPush = afterPushRevoking(w, extra, w.k.build(t, w.c2, w.k.revoked)) // lacks extra
	if _, err := Run(context.Background(), in); !errors.Is(err, ErrSignedMismatch) {
		t.Fatalf("err = %v", err)
	}
	if rec, _ := l.InForce(); rec.Generation != 1 {
		t.Errorf("committed generation %d", rec.Generation)
	}
	if _, has, _ := l.Pending(); !has {
		t.Error("not pending")
	}
}
