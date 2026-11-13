package policyapply

import (
	"context"
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
	in.AfterPush = func(context.Context) (map[string][]string, []string, error) {
		return map[string][]string{"": {extra}}, nil, nil
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
	in.AfterPush = func(context.Context) (map[string][]string, []string, error) {
		return map[string][]string{"": {extra}}, nil, nil
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
