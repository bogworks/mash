package policyapply

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/listset"
)

// pendingRevokeWorld is a first run whose AfterPush pushed a revocation of K to s1 only and then failed. With lose the
// attempted JWT is not returned either.
func pendingRevokeWorld(t *testing.T, lose bool) (w *world, l *MemLedger, k string) {
	t.Helper()
	w = newWorld(t)
	k = freshUserPub(t)
	withK := w.k.build(t, w.c2, w.k.revoked, k)
	l = NewMemLedgerWith(listset.Record{Generation: 1}, InForceDetail{JWT: w.j1})
	in := w.run(l, set2(), "bytes-2")
	in.AfterPush = func(context.Context) (map[string][]string, []string, string, error) {
		w.cluster.set("s1", withK) // the interrupted revocation reached s1 only
		if lose {
			return nil, nil, "", errors.New("interrupted")
		}
		return nil, nil, withK, errors.New("interrupted")
	}
	if _, err := Run(context.Background(), in); err == nil {
		t.Fatal("first run: want an error")
	}
	if _, has, _ := l.Pending(); !has {
		t.Fatal("first run: not pending")
	}
	return w, l, k
}

func hasRevocation(t *testing.T, tok, key string) bool {
	t.Helper()
	c, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	_, has := c.Revocations[key]
	return has
}

// retryRun is the retry of the pending content; keep says whether its target revokes k.
func retryRun(w *world, l Ledger, k string, keep bool) RunInput {
	in := w.run(l, set2(), "bytes-2")
	if keep {
		in.RevokedKeys = []string{w.k.revoked, k}
		in.Signer = SignerFuncs{
			SignPreviousFn: func(_ PreviousClaims, revoked []string) (string, error) { return w.k.build(w.t, w.c1, revoked...), nil },
			SignFn: func(_ listset.Record, c *listset.Compiled) (string, error) {
				return w.k.build(w.t, c, w.k.revoked, k), nil
			},
		}
	}
	return in
}

// The attempted final JWT is stored as the pending JWT, so a retry whose target lacks K is refused before any push.
func TestPendingKeepsAttemptedFinalJWT(t *testing.T) {
	w, l, k := pendingRevokeWorld(t, false)
	got, err := l.PendingJWT()
	if err != nil || !hasRevocation(t, got, k) {
		t.Fatalf("pending JWT = %q, %v; want the attempted one with the revocation", got, err)
	}
	before := len(w.cluster.pushes)
	_, err = Run(context.Background(), retryRun(w, l, k, false))
	if !errors.Is(err, ErrSignedMismatch) {
		t.Fatalf("err = %v", err)
	}
	if len(w.cluster.pushes) != before {
		t.Error("a push was made")
	}
}

// The attempted JWT is lost too: the JWT s1 holds still revokes K, so the retry is refused before any push.
func TestPendingRetryKeepsRevocationsOnServers(t *testing.T) {
	w, l, k := pendingRevokeWorld(t, true)
	before := len(w.cluster.pushes)
	_, err := Run(context.Background(), retryRun(w, l, k, false))
	if !errors.Is(err, ErrSignedMismatch) || !strings.Contains(err.Error(), "a server holds") {
		t.Fatalf("err = %v", err)
	}
	if len(w.cluster.pushes) != before {
		t.Error("a push was made")
	}
}

type failingJWTs struct {
	*fakeCluster
	err error
}

func (f failingJWTs) AccountJWTs(context.Context, string) (map[string]string, error) {
	return nil, f.err
}

type shortRead struct{ *fakeCluster }

func (s shortRead) AccountJWTs(ctx context.Context, a string) (map[string]string, error) {
	m, _ := s.fakeCluster.AccountJWTs(ctx, a)
	delete(m, "s2")
	return m, nil
}

// A failed server read, or a server missing from it, refuses the retry before any push.
func TestPendingRetryFailsClosedOnServerRead(t *testing.T) {
	for name, wrap := range map[string]func(*fakeCluster) Cluster{
		"read fails": func(f *fakeCluster) Cluster { return failingJWTs{f, errors.New("no answer")} },
		"missing":    func(f *fakeCluster) Cluster { return shortRead{f} },
	} {
		t.Run(name, func(t *testing.T) {
			w, l, k := pendingRevokeWorld(t, true)
			before := len(w.cluster.pushes)
			in := retryRun(w, l, k, true)
			in.Cluster = wrap(w.cluster)
			_, err := Run(context.Background(), in)
			if !errors.Is(err, ErrSignedMismatch) {
				t.Fatalf("err = %v", err)
			}
			if len(w.cluster.pushes) != before {
				t.Error("a push was made")
			}
		})
	}
}

// A retry that keeps K proceeds.
func TestPendingRetryThatKeepsRevocationProceeds(t *testing.T) {
	w, l, k := pendingRevokeWorld(t, true)
	before := len(w.cluster.pushes)
	if _, err := Run(context.Background(), retryRun(w, l, k, true)); err != nil {
		t.Fatal(err)
	}
	if len(w.cluster.pushes) == before {
		t.Error("no push was made")
	}
	if got, _ := l.InForceJWT(); !hasRevocation(t, got, k) {
		t.Error("the committed JWT lacks the revocation")
	}
}

type failingRevoker struct{ tok string }

func (f failingRevoker) Revoke(context.Context, []string) (string, error) {
	return f.tok, errors.New("push failed")
}

// lockedRevoker records the JWT a failed revocation attempted.
func TestLockedRevokerRecordsAttemptedJWT(t *testing.T) {
	var mu sync.Mutex
	var last string
	var refused error
	rv := lockedRevoker{mu: &mu, r: failingRevoker{"attempted"}, last: &last, refused: &refused}
	if _, err := rv.Revoke(context.Background(), []string{"K"}); err == nil {
		t.Fatal("want an error")
	}
	if last != "attempted" {
		t.Errorf("last = %q", last)
	}
}
