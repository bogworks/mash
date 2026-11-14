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
	"github.com/bogworks/mash/internal/policy"
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

// buildAt is build with the revocation time of each key given.
func (k keys) buildAt(t *testing.T, c *listset.Compiled, at map[string]int64) string {
	t.Helper()
	claims := jwt.NewAccountClaims(k.acctPub)
	for _, tpl := range c.Templates {
		sc := jwt.NewUserScope()
		sc.Key = k.signers[tpl.Group]
		sc.Role = tpl.Group
		sc.Template.Permissions = tpl.Permissions
		claims.SigningKeys.AddScopedSigner(sc)
	}
	claims.Revocations = jwt.RevocationList{}
	for key, ts := range at {
		claims.Revocations[key] = ts
	}
	tok, err := claims.Encode(k.acct)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// A retry of a pending generation keeps the revocations the earlier attempt pushed: a target that lacks one, or lowers
// its time, is refused before any push.
func TestRetryOfPendingKeepsEarlierRevocations(t *testing.T) {
	w := newWorld(t)
	extra := freshUserPub(t)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	signAt := func(at map[string]int64) func(listset.Record, *listset.Compiled) (string, error) {
		return func(_ listset.Record, c *listset.Compiled) (string, error) { return w.k.buildAt(t, c, at), nil }
	}
	final := w.k.buildAt(t, w.c2, map[string]int64{w.k.revoked: 1000, extra: 2000})
	in := w.run(l, set2(), "bytes-2")
	in.Signer = withSign(in.Signer, signAt(map[string]int64{w.k.revoked: 1000}))
	in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) {
		return map[string][]string{"": {extra}}, nil, final, errors.New("rotation interrupted")
	}
	if _, err := Run(context.Background(), in); err == nil {
		t.Fatal("first attempt succeeded")
	}
	if pj, _ := l.PendingJWT(); pj != final {
		t.Fatalf("pending JWT = %q, want the final JWT", pj)
	}
	pushes := len(w.cluster.pushes)
	for name, at := range map[string]map[string]int64{
		"lacks K":  {w.k.revoked: 1000},
		"lowers K": {w.k.revoked: 1000, extra: 1999},
	} {
		retry := w.run(l, set2(), "bytes-2")
		retry.Signer = withSign(retry.Signer, signAt(at))
		if _, err := Run(context.Background(), retry); !errors.Is(err, ErrSignedMismatch) {
			t.Fatalf("%s: err = %v", name, err)
		}
		if len(w.cluster.pushes) != pushes {
			t.Fatalf("%s: pushed before refusing", name)
		}
	}
	retry := w.run(l, set2(), "bytes-2")
	retry.Signer = withSign(retry.Signer, signAt(map[string]int64{w.k.revoked: 1000, extra: 2000}))
	if _, err := Run(context.Background(), retry); err != nil {
		t.Fatal(err)
	}
	if len(w.cluster.pushes) == pushes {
		t.Fatal("the retry with K kept did not push")
	}
	if pj, _ := l.PendingJWT(); pj != "" {
		t.Errorf("pending JWT left after the commit: %q", pj)
	}
}

// A rollback JWT that revokes a key at an earlier time than the target (or the JWT in force) is not pushed.
func TestRollbackLoweredRevocationTimeIsNotPushed(t *testing.T) {
	for _, tc := range []struct {
		name               string
		inForce, fin, prev map[string]int64
	}{
		{"target", nil, map[string]int64{"R": 2000, "X": 2000}, map[string]int64{"R": 2000, "X": 1000}},
		{"in force", map[string]int64{"R": 2000}, map[string]int64{"R": 2000}, map[string]int64{"R": 1000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			extra := freshUserPub(t)
			rename := func(m map[string]int64) map[string]int64 {
				out := map[string]int64{}
				for k, v := range m {
					if k == "R" {
						k = w.k.revoked
					} else {
						k = extra
					}
					out[k] = v
				}
				return out
			}
			w.cluster.failPush["s2"] = true
			l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.k.buildAt(t, w.c1, rename(tc.inForce))})
			final := w.k.buildAt(t, w.c2, rename(tc.fin))
			bad := w.k.buildAt(t, w.c1, rename(tc.prev))
			in := w.run(l, set2(), "bytes-2")
			in.Signer = withSign(withSignPrevious(in.Signer, func(PreviousClaims, []string) (string, error) { return bad, nil }),
				func(listset.Record, *listset.Compiled) (string, error) { return final, nil })
			in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) { return nil, nil, final, nil }
			st, err := Run(context.Background(), in)
			if err != nil || !hasFailure(st, "", "", CheckRollback) || slices.Contains(w.cluster.pushes, bad) {
				t.Fatalf("%+v %v", st, err)
			}
		})
	}
}

// The final JWT from AfterPush and the rollback JWT are held to the size limit as the target is.
func TestFinalAndRollbackJWTSizeLimit(t *testing.T) {
	w := newWorld(t)
	big := strings.Repeat("a", policy.AccountJWTLimit+1)
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) { return nil, nil, big, nil }
	if _, err := Run(context.Background(), in); !errors.Is(err, ErrAccountJWTTooLarge) || slices.Contains(w.cluster.pushes, big) {
		t.Fatalf("final: err = %v", err)
	}
	if g, _ := l.InForce(); g.Generation != 1 {
		t.Fatalf("committed generation %d", g.Generation)
	}
	if _, ok, _ := l.Pending(); !ok {
		t.Error("not pending")
	}

	w = newWorld(t)
	w.cluster.failPush["s2"] = true
	in = w.run(NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1}), set2(), "bytes-2")
	in.Signer = withSignPrevious(in.Signer, func(PreviousClaims, []string) (string, error) { return big, nil })
	st, err := Run(context.Background(), in)
	if err != nil || !hasFailure(st, "", "", CheckRollback) || slices.Contains(w.cluster.pushes, big) {
		t.Fatalf("rollback: %+v %v", st, err)
	}
}

// A server that keeps the previous generation because the rollback push failed lacks the rollback's revocations: the
// rollback is not confirmed, nothing is committed, the generation stays pending and the failure names the server.
func TestRollbackNotConfirmedWhenServerLacksRevocations(t *testing.T) {
	w := newWorld(t)
	extra := freshUserPub(t)
	w.cluster.failPush["s2"] = true
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) {
		return map[string][]string{"": {extra}}, nil, w.k.build(t, w.c2, w.k.revoked, extra), nil
	}
	st, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if g := gens(st); g["s1"] != "1" || g["s2"] != "1" {
		t.Fatalf("servers = %v", g)
	}
	if !hasFailure(st, "s2", "", CheckRollback) || hasFailure(st, "s1", "", CheckRollback) {
		t.Fatalf("failures = %+v", st.Failing)
	}
	if st.State == StateApplied || st.InForce {
		t.Fatalf("state = %+v", st)
	}
	if _, ok, _ := l.Pending(); !ok {
		t.Fatal("pending cleared")
	}
	if stored, _ := l.InForceJWT(); stored != w.j1 {
		t.Fatal("the rollback JWT was committed as in force")
	}
}

// After a confirmed rollback the JWT in force is the rollback JWT, which may carry revocations the earlier JWT in
// force lacked: the next submission, signed from it, passes the server-revocation check.
func TestRollbackBecomesJWTInForce(t *testing.T) {
	w := newWorld(t)
	extra := freshUserPub(t)
	w.cluster.failPush["s2"] = true
	l := NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) {
		return map[string][]string{"": {extra}}, nil, w.k.build(t, w.c2, w.k.revoked, extra), nil
	}
	// s2 refuses the target but takes the rollback push.
	orig := in.Signer
	in.Signer = withSignPrevious(orig, func(pc PreviousClaims, r []string) (string, error) {
		w.cluster.failPush["s2"] = false
		return orig.(SignerFuncs).SignPrevious(pc, r)
	})
	st, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if g := gens(st); g["s1"] != "1" || g["s2"] != "1" {
		t.Fatalf("servers = %v", g)
	}
	if _, ok, _ := l.Pending(); ok {
		t.Fatal("pending left after the rollback")
	}
	stored, _ := l.InForceJWT()
	if last := w.cluster.pushes[len(w.cluster.pushes)-1]; stored != last || stored == w.j1 {
		t.Fatalf("stored JWT in force is not the rollback JWT")
	}
	if rec, _ := l.InForce(); rec.Generation != 1 {
		t.Errorf("record in force changed: %+v", rec)
	}
	claims, err := jwt.DecodeAccountClaims(stored)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := claims.Revocations[extra]; !has {
		t.Fatal("the stored JWT lacks the revocation the rollback carries")
	}
	for n, held := range w.cluster.jwts {
		hc, err := jwt.DecodeAccountClaims(held)
		if err != nil {
			t.Fatal(err)
		}
		for k := range claims.Revocations {
			if _, has := hc.Revocations[k]; !has {
				t.Fatalf("server %s lacks the revocation of %s", n, k)
			}
		}
	}
	// The next submission builds from the stored JWT in force.
	w.cluster.failPush["s2"] = false
	next := w.run(l, set2(), "bytes-2")
	next.Signer = withSign(next.Signer, func(_ listset.Record, c *listset.Compiled) (string, error) {
		cur, _ := l.InForceJWT()
		pc, err := jwt.DecodeAccountClaims(cur)
		if err != nil {
			return "", err
		}
		var keys []string
		for k := range pc.Revocations {
			keys = append(keys, k)
		}
		return w.k.build(t, c, keys...), nil
	})
	st, err = Run(context.Background(), next)
	if err != nil {
		t.Fatalf("next submission: %v", err)
	}
	if st.State != StateApplied {
		t.Fatalf("next submission: %+v", st)
	}
}
