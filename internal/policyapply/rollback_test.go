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
		return map[string][]string{"": {extra}}, nil, w.k.build(t, w.c2, w.k.revoked, extra), nil
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
		return map[string][]string{"": {extra}}, nil, w.k.build(t, w.c2, w.k.revoked, extra), nil
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

// AfterPush that revokes keys but returns no final JWT is not committed: tok would lack the revocations.
func TestRevokedKeysWithoutFinalJWTIsNotCommitted(t *testing.T) {
	w := newWorld(t)
	extra := freshUserPub(t)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.AfterPush = afterPushRevoking(w, extra, "")
	_, err := Run(context.Background(), in)
	if !errors.Is(err, ErrSignedMismatch) || !strings.Contains(err.Error(), "no final account JWT") {
		t.Fatalf("err = %v", err)
	}
	if rec, _ := l.InForce(); rec.Generation != 1 {
		t.Errorf("committed generation %d", rec.Generation)
	}
	if got, _ := l.InForceJWT(); got != w.j1 {
		t.Error("the stored JWT changed")
	}
	if _, has, _ := l.Pending(); !has {
		t.Error("not pending")
	}
}

// revokedAt re-encodes tok with the revocation of key at unix time at.
func revokedAt(t *testing.T, k keys, tok, key string, at int64) string {
	t.Helper()
	c, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Revocations == nil {
		c.Revocations = jwt.RevocationList{}
	}
	c.Revocations[key] = at
	out, err := c.Encode(k.acct)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A target JWT must keep every revocation of the JWT in force, each at a time no earlier: it is refused before any
// push otherwise (a revocation is never lowered or removed).
func TestRunKeepsInForceRevocations(t *testing.T) {
	const at = 2_000_000_000
	for _, tc := range []struct {
		name string
		at   int64 // the time of the target's revocation; 0 means not revoked
		ok   bool
	}{
		{"missing", 0, false},
		{"lowered", at - 1, false},
		{"same time", at, true},
		{"later", at + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			old := freshUserPub(t)
			j1 := revokedAt(t, w.k, w.j1, old, at)
			l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: j1})
			in := w.run(l, set2(), "bytes-2")
			in.Signer = withSign(in.Signer, func(_ listset.Record, c *listset.Compiled) (string, error) {
				tok := w.k.build(t, c, w.k.revoked)
				if tc.at != 0 {
					tok = revokedAt(t, w.k, tok, old, tc.at)
				}
				return tok, nil
			})
			pushes := len(w.cluster.pushes)
			st, err := Run(context.Background(), in)
			if tc.ok {
				if err != nil || st.State != StateApplied {
					t.Fatalf("%+v %v", st, err)
				}
				return
			}
			if !errors.Is(err, ErrSignedMismatch) || !strings.Contains(err.Error(), old) {
				t.Fatalf("err = %v, want ErrSignedMismatch naming %s", err, old)
			}
			if len(w.cluster.pushes) != pushes {
				t.Errorf("pushed %d -> %d", pushes, len(w.cluster.pushes))
			}
		})
	}
}

// The final JWT after AfterPush is held to the same rule; a ledger with a generation in force and no stored JWT fails closed.
func TestRunKeepsInForceRevocationsFinalAndFailClosed(t *testing.T) {
	w := newWorld(t)
	old := freshUserPub(t)
	j1 := revokedAt(t, w.k, w.j1, old, 2_000_000_000)
	in := w.run(NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: j1}), set2(), "bytes-2")
	in.Signer = withSign(in.Signer, func(_ listset.Record, c *listset.Compiled) (string, error) {
		return revokedAt(t, w.k, w.k.build(t, c, w.k.revoked), old, 2_000_000_000), nil
	})
	in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) {
		return nil, nil, w.k.build(t, w.c2, w.k.revoked), nil // drops old
	}
	if _, err := Run(context.Background(), in); !errors.Is(err, ErrSignedMismatch) {
		t.Fatalf("final JWT: err = %v", err)
	}

	w = newWorld(t)
	in = w.run(NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{}), set2(), "bytes-2")
	pushes := len(w.cluster.pushes)
	if _, err := Run(context.Background(), in); !errors.Is(err, ErrSignedMismatch) {
		t.Fatalf("no stored JWT: err = %v", err)
	}
	if len(w.cluster.pushes) != pushes {
		t.Error("pushed with no stored JWT")
	}
}
