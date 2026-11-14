package reconcile

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

// fakeCluster is one or more servers that hold an account JWT and take a push.
type fakeCluster struct {
	mu     sync.Mutex
	names  []string
	jwts   map[string]string
	drop   bool // a push is acknowledged but not kept
	pushes int
}

func newCluster(tok string, names ...string) *fakeCluster {
	f := &fakeCluster{names: names, jwts: map[string]string{}}
	for _, n := range names {
		f.jwts[n] = tok
	}
	return f
}

func (f *fakeCluster) Servers(context.Context) (apply.ServerSet, error) {
	return apply.ServerSet{Names: slices.Clone(f.names), Expected: len(f.names)}, nil
}

func (f *fakeCluster) AccountJWTs(context.Context, string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for k, v := range f.jwts {
		out[k] = v
	}
	return out, nil
}

func (f *fakeCluster) Push(_ context.Context, tok string, _ []string) ([]apply.ServerAck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushes++
	var acks []apply.ServerAck
	for _, n := range f.names {
		if !f.drop {
			f.jwts[n] = tok
		}
		acks = append(acks, apply.ServerAck{Server: n, Code: 200})
	}
	return acks, nil
}

// fakeMarker records MarkRevoked; a key in unrecorded answers ErrNotRecorded.
type fakeMarker struct {
	marked     map[string]time.Time
	unrecorded map[string]bool
}

func (m *fakeMarker) MarkRevoked(_ context.Context, k string, at time.Time) error {
	if m.unrecorded[k] {
		return admin.ErrNotRecorded
	}
	m.marked[k] = at
	return nil
}

// first is an account at generation 1 with a list set in force, and the signing material of the test signer.
type first struct {
	t       *testing.T
	acct    nkeys.KeyPair
	acctPub string
	signers map[string]string
	ls      listset.ListSet
	rec     listset.Record
	jwt     string
	ledger  *policyapply.MemLedger
	cluster *fakeCluster
	marker  *fakeMarker
	signer  Signer
	now     time.Time
}

func newFirst(t *testing.T) *first {
	t.Helper()
	kp, pub := acctKey(t)
	_, scoped := acctKey(t)
	f := &first{t: t, acct: kp, acctPub: pub, signers: map[string]string{"foo.c": scoped}, ls: testSet(), now: time.Now().UTC()}
	rec, c, err := listset.Accept(listset.Record{}, f.ls, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	f.rec = rec
	claims := jwt.NewAccountClaims(pub)
	for _, tpl := range c.Templates {
		sc := jwt.NewUserScope()
		sc.Key, sc.Role = scoped, tpl.Group
		sc.Template.Permissions = tpl.Permissions
		claims.SigningKeys.AddScopedSigner(sc)
	}
	if f.jwt, err = claims.Encode(kp); err != nil {
		t.Fatal(err)
	}
	f.ledger = policyapply.NewMemLedgerWith(rec, policyapply.InForceDetail{JWT: f.jwt, ListSet: &f.ls})
	f.cluster = newCluster(f.jwt, "s1", "s2")
	f.marker = &fakeMarker{marked: map[string]time.Time{}, unrecorded: map[string]bool{}}
	f.signer = f.signerAt(func(string) int64 { return time.Now().Unix() })
	return f
}

// signerAt is the test signer: it re-signs the previous claims with the keys also revoked at the time at gives for each.
func (f *first) signerAt(at func(key string) int64) Signer {
	return policyapply.SignerFuncs{SignPreviousFn: func(prev policyapply.PreviousClaims, revoked []string) (string, error) {
		c, err := jwt.DecodeAccountClaims(prev.JWT)
		if err != nil {
			return "", err
		}
		for _, k := range revoked {
			c.RevokeAt(k, time.Unix(at(k), 0))
		}
		return c.Encode(f.acct)
	}}
}

func (f *first) plan(keys ...string) Plan {
	p := Plan{Account: f.acctPub, Source: SourceInForce, Generation: 1, SigningKeys: []SigningKey{}, Findings: []Finding{}, IssuerProblems: []admin.IssuerProblem{}}
	for _, k := range keys {
		p.Revocations = append(p.Revocations, Revocation{Key: k, Reason: ReasonUntracked, At: f.now.Truncate(time.Second).Format(time.RFC3339)})
	}
	if p.Revocations == nil {
		p.Revocations = []Revocation{}
	}
	return p
}

func (f *first) exec(p Plan) (Result, error) {
	return Execute(context.Background(), ExecInput{Plan: p, Ledger: f.ledger, Cluster: f.cluster, Account: f.acctPub, Inventory: 2,
		Signers: f.signers, Signer: f.signer, Marker: f.marker, Now: func() time.Time { return f.now }})
}

func (f *first) inForceJWT() string {
	f.t.Helper()
	tok, err := f.ledger.InForceJWT()
	if err != nil {
		f.t.Fatal(err)
	}
	return tok
}

func revocationsOf(t *testing.T, tok string) map[string]int64 {
	t.Helper()
	c, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	return c.Revocations
}

func TestExecuteRevokesPushesCommitsAndMarks(t *testing.T) {
	f := newFirst(t)
	recorded, untracked := userKey(t), userKey(t)
	f.marker.unrecorded[untracked] = true
	res, err := f.exec(f.plan(recorded, untracked))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status.State != policyapply.StateApplied || !slices.Equal(res.Revoked, slices.Sorted(slices.Values([]string{recorded, untracked}))) {
		t.Fatalf("result %+v", res)
	}
	// every server holds the revoking JWT, and it is the JWT in force, so the next list-set run keeps the revocations
	for n, tok := range f.cluster.jwts {
		if _, ok := revocationsOf(t, tok)[recorded]; !ok {
			t.Errorf("server %s does not revoke the key", n)
		}
	}
	if got := f.inForceJWT(); got != f.cluster.jwts["s1"] || got == f.jwt {
		t.Error("the revoking JWT is not the one in force")
	}
	if err := policyapply.KeepsRevocations(f.inForceJWT(), true, f.cluster.jwts["s2"]); err != nil {
		t.Error(err)
	}
	if rec, _ := f.ledger.InForce(); rec != f.rec {
		t.Errorf("the record in force changed: %+v", rec)
	}
	if ls, ok, _ := f.ledger.InForceListSet(); !ok || ls.Range != "foo" {
		t.Errorf("the list set in force changed: %v %v", ls, ok)
	}
	// only the recorded key has a record to mark
	if len(f.marker.marked) != 1 || f.marker.marked[recorded].IsZero() || !slices.Equal(res.Marked, []string{recorded}) {
		t.Errorf("marked %v result %v", f.marker.marked, res.Marked)
	}
}

func TestExecuteKeepsEarlierRevocations(t *testing.T) {
	f := newFirst(t)
	a, b := userKey(t), userKey(t)
	if _, err := f.exec(f.plan(a)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.exec(f.plan(b)); err != nil {
		t.Fatal(err)
	}
	r := revocationsOf(t, f.inForceJWT())
	if _, ok := r[a]; !ok {
		t.Error("the first revocation was lost")
	}
	if _, ok := r[b]; !ok {
		t.Error("the second revocation is missing")
	}
}

func TestExecuteRefusesWhatItCannotRun(t *testing.T) {
	k := userKey(t)
	for name, tc := range map[string]struct {
		mutate func(f *first, p *Plan)
		want   error
	}{
		"a preview from a file": {func(_ *first, p *Plan) { p.Source = SourceListSetFile }, ErrNotExecutable},
		"an issuer problem": {func(_ *first, p *Plan) {
			p.IssuerProblems = []admin.IssuerProblem{{Role: "admin", User: "U", Issuer: "A"}}
		}, ErrNotExecutable},
		"a stale generation": {func(_ *first, p *Plan) { p.Generation = 2 }, ErrStalePlan},
		"a pending generation": {func(f *first, _ *Plan) {
			if err := f.ledger.SetPending(listset.Record{Generation: 2}); err != nil {
				f.t.Fatal(err)
			}
		}, ErrPending},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFirst(t)
			p := f.plan(k)
			tc.mutate(f, &p)
			if _, err := f.exec(p); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if f.cluster.pushes != 0 || f.inForceJWT() != f.jwt || len(f.marker.marked) != 0 {
				t.Error("something was changed")
			}
		})
	}
}

func TestExecuteWithNothingToDoDoesNothing(t *testing.T) {
	f := newFirst(t)
	res, err := f.exec(f.plan())
	if err != nil || len(res.Revoked) != 0 || f.cluster.pushes != 0 {
		t.Errorf("%+v %v pushes %d", res, err, f.cluster.pushes)
	}
}

// The signed JWT is checked before anything is pushed.
func TestExecuteRefusesABadSignedJWTBeforeThePush(t *testing.T) {
	k := userKey(t)
	for name, signer := range map[string]func(f *first) Signer{
		"a revocation older than the credential": func(f *first) Signer {
			return f.signerAt(func(string) int64 { return f.now.Add(-time.Hour).Unix() })
		},
		"a signer that fails": func(*first) Signer {
			return policyapply.SignerFuncs{SignPreviousFn: func(policyapply.PreviousClaims, []string) (string, error) { return "", errors.New("no key") }}
		},
		"a signer that does not revoke": func(f *first) Signer {
			return policyapply.SignerFuncs{SignPreviousFn: func(prev policyapply.PreviousClaims, _ []string) (string, error) { return prev.JWT, nil }}
		},
		"a signer that drops the signers": func(f *first) Signer {
			return policyapply.SignerFuncs{SignPreviousFn: func(prev policyapply.PreviousClaims, rev []string) (string, error) {
				c := jwt.NewAccountClaims(f.acctPub)
				for _, r := range rev {
					c.RevokeAt(r, time.Now())
				}
				return c.Encode(f.acct)
			}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFirst(t)
			f.signer = signer(f)
			p := f.plan(k)
			p.Revocations[0].At = f.now.Format(time.RFC3339)
			if _, err := f.exec(p); err == nil {
				t.Fatal("no error")
			}
			if f.cluster.pushes != 0 || f.inForceJWT() != f.jwt {
				t.Error("something was pushed or committed")
			}
		})
	}
}

// A signer that lowers or removes a revocation already in force is refused (policyapply.KeepsRevocations).
func TestExecuteRefusesALoweredRevocation(t *testing.T) {
	f := newFirst(t)
	old, k := userKey(t), userKey(t)
	c, _ := jwt.DecodeAccountClaims(f.jwt)
	c.RevokeAt(old, time.Now())
	tok, _ := c.Encode(f.acct)
	f.jwt = tok
	f.ledger = policyapply.NewMemLedgerWith(f.rec, policyapply.InForceDetail{JWT: tok, ListSet: &f.ls})
	f.cluster = newCluster(tok, "s1", "s2")
	f.signer = policyapply.SignerFuncs{SignPreviousFn: func(prev policyapply.PreviousClaims, rev []string) (string, error) {
		c, _ := jwt.DecodeAccountClaims(prev.JWT)
		delete(c.Revocations, old)
		for _, r := range rev {
			c.RevokeAt(r, time.Now())
		}
		return c.Encode(f.acct)
	}}
	if _, err := f.exec(f.plan(k)); !errors.Is(err, policyapply.ErrSignedMismatch) {
		t.Fatalf("err = %v", err)
	}
	if f.cluster.pushes != 0 {
		t.Error("pushed")
	}
}

// When a server does not hold the revoking JWT the gate does not pass: nothing is committed or marked.
func TestExecuteCommitsNothingWhenTheGateFails(t *testing.T) {
	f := newFirst(t)
	f.cluster.drop = true
	k := userKey(t)
	res, err := f.exec(f.plan(k))
	if !errors.Is(err, ErrNotApplied) {
		t.Fatalf("err = %v", err)
	}
	if res.Status.State == policyapply.StateApplied || f.inForceJWT() != f.jwt || len(f.marker.marked) != 0 || len(res.Revoked) != 0 {
		t.Errorf("committed or marked: %+v", res)
	}
}

func TestExecuteStopsAtTheFence(t *testing.T) {
	f := newFirst(t)
	k := userKey(t)
	fenced := errors.New("the account lock was lost")
	for _, at := range []string{policyapply.FencePush, policyapply.FenceCommit} {
		t.Run(at, func(t *testing.T) {
			f := newFirst(t)
			_, err := Execute(context.Background(), ExecInput{Plan: f.plan(k), Ledger: f.ledger, Cluster: f.cluster, Account: f.acctPub, Inventory: 2,
				Signers: f.signers, Signer: f.signer, Marker: f.marker,
				BeforeWrite: func(_ context.Context, what string) error {
					if what == at {
						return fenced
					}
					return nil
				}})
			if !errors.Is(err, fenced) {
				t.Fatalf("err = %v", err)
			}
			if f.inForceJWT() != f.jwt || len(f.marker.marked) != 0 {
				t.Error("committed or marked after the fence refused")
			}
			if at == policyapply.FencePush && f.cluster.pushes != 0 {
				t.Error("pushed after the fence refused")
			}
		})
	}
	_ = f
}
