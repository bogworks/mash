package reconcile

import (
	"context"
	"errors"
	"slices"
	"strings"
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
	f.signer = f.issuerLike()
	return f
}

// issuerLike behaves as (*policyserve.Issuer).SignPrevious: a revocation is stamped now, or at the planned time
// PreviousClaims.RevokeAt gives when that is later.
func (f *first) issuerLike() Signer {
	return policyapply.SignerFuncs{SignPreviousFn: func(prev policyapply.PreviousClaims, revoked []string) (string, error) {
		c, err := jwt.DecodeAccountClaims(prev.JWT)
		if err != nil {
			return "", err
		}
		now := time.Now()
		for _, k := range revoked {
			if t, ok := prev.RevokeAt[k]; ok && t.After(now) {
				c.RevokeAt(k, t)
			} else {
				c.Revoke(k)
			}
		}
		return c.Encode(f.acct)
	}}
}

// A connected credential issued in the future (clock skew) is planned for revocation at its issue time: the signer is
// told that time, so the signed JWT carries it and the revocation runs.
func TestExecuteRevokesAtAFutureIssueTime(t *testing.T) {
	f := newFirst(t)
	k := userKey(t)
	future := time.Now().Add(time.Hour).Truncate(time.Second).UTC()
	p := f.plan(k)
	p.Revocations[0].At = future.Format(time.RFC3339)
	res, err := f.exec(p)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Status.State != policyapply.StateApplied {
		t.Fatalf("state = %v", res.Status.State)
	}
	if got := revocationsOf(t, f.inForceJWT())[k]; got != future.Unix() {
		t.Errorf("revocation at %d, want %d", got, future.Unix())
	}
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
	return Execute(context.Background(), ExecInput{Build: func(context.Context) (Plan, error) { return p, nil }, Seen: &p, Ledger: f.ledger, Cluster: f.cluster, Account: f.acctPub, Inventory: 2,
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
			_, err := Execute(context.Background(), ExecInput{Build: func(context.Context) (Plan, error) { return f.plan(k), nil }, Ledger: f.ledger, Cluster: f.cluster, Account: f.acctPub, Inventory: 2,
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

// The plan is rebuilt under the lease: a plan that differs from the one the person saw is refused before any push,
// and one that differs only in time is executed as rebuilt.
func TestExecuteRebuildsThePlanAndRefusesOneThatChanged(t *testing.T) {
	a, b := userKey(t), userKey(t)
	f := newFirst(t)
	seen := f.plan(a)
	_, err := Execute(context.Background(), ExecInput{Build: func(context.Context) (Plan, error) { return f.plan(a, b), nil }, Seen: &seen,
		Ledger: f.ledger, Cluster: f.cluster, Account: f.acctPub, Inventory: 2, Signers: f.signers, Signer: f.signer, Marker: f.marker})
	if !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("err = %v", err)
	}
	if f.cluster.pushes != 0 || f.inForceJWT() != f.jwt || len(f.marker.marked) != 0 {
		t.Error("something was changed")
	}

	// same keys, later time: executed, and the executed plan is the rebuilt one
	f = newFirst(t)
	seen = f.plan(a)
	rebuilt := f.plan(a)
	f.signer = f.signerAt(func(string) int64 { return f.now.Add(time.Minute).Unix() })
	rebuilt.Revocations[0].At = f.now.Add(time.Minute).Truncate(time.Second).Format(time.RFC3339)
	res, err := Execute(context.Background(), ExecInput{Build: func(context.Context) (Plan, error) { return rebuilt, nil }, Seen: &seen,
		Ledger: f.ledger, Cluster: f.cluster, Account: f.acctPub, Inventory: 2, Signers: f.signers, Signer: f.signer, Marker: f.marker,
		Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan.Revocations[0].At != rebuilt.Revocations[0].At {
		t.Errorf("executed plan %+v", res.Plan)
	}
}

// A server holding a revocation the ledger's JWT lacks must keep it: a signed JWT without it is refused before the push.
func TestExecuteRefusesToDropARevocationAServerHolds(t *testing.T) {
	f := newFirst(t)
	extra, k := userKey(t), userKey(t)
	c, _ := jwt.DecodeAccountClaims(f.jwt)
	c.RevokeAt(extra, time.Now())
	tok, err := c.Encode(f.acct)
	if err != nil {
		t.Fatal(err)
	}
	f.cluster.jwts["s2"] = tok
	if _, err := f.exec(f.plan(k)); !errors.Is(err, policyapply.ErrSignedMismatch) {
		t.Fatalf("err = %v", err)
	}
	if f.cluster.pushes != 0 || f.inForceJWT() != f.jwt {
		t.Error("pushed or committed")
	}
}

// The plan seen must be for the same account, source and generation as the rebuilt one, even with the same revocations;
// a preview plan is never executable.
func TestExecuteRefusesAPlanSeenForAnotherGenerationAccountOrSource(t *testing.T) {
	a := userKey(t)
	run := func(mutate func(*Plan)) (*first, error) {
		f := newFirst(t)
		seen := f.plan(a)
		mutate(&seen)
		_, err := Execute(context.Background(), ExecInput{Build: func(context.Context) (Plan, error) { return f.plan(a), nil }, Seen: &seen,
			Ledger: f.ledger, Cluster: f.cluster, Account: f.acctPub, Inventory: 2, Signers: f.signers, Signer: f.signer, Marker: f.marker})
		return f, err
	}
	for name, mutate := range map[string]func(*Plan){
		"generation": func(p *Plan) { p.Generation = 7 },
		"account":    func(p *Plan) { p.Account = "AOTHER" },
	} {
		f, err := run(mutate)
		if !errors.Is(err, ErrPlanChanged) {
			t.Errorf("%s: err = %v", name, err)
		}
		if f.cluster.pushes != 0 || len(f.marker.marked) != 0 {
			t.Errorf("%s: something was changed", name)
		}
	}
	f, err := run(func(p *Plan) { p.Source = SourceListSetFile; p.Generation = 0 })
	if err == nil || errors.Is(err, ErrPlanChanged) || !errors.Is(err, ErrNotExecutable) || !strings.Contains(err.Error(), "preview") {
		t.Errorf("preview: err = %v", err)
	}
	if f.cluster.pushes != 0 || len(f.marker.marked) != 0 {
		t.Error("preview: something was changed")
	}
}

// TestExecuteKeepsAnotherRangesHeldKey: an account whose JWT also carries another range's held scoped key still passes
// the generation checks and the gate, which count that key as kept; without it declared the exact signer count fails.
func TestExecuteKeepsAnotherRangesHeldKey(t *testing.T) {
	f := newFirst(t)
	_, other := acctKey(t)
	claims, err := jwt.DecodeAccountClaims(f.jwt)
	if err != nil {
		t.Fatal(err)
	}
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = other, "bar.c"
	claims.SigningKeys.AddScopedSigner(sc)
	if f.jwt, err = claims.Encode(f.acct); err != nil {
		t.Fatal(err)
	}
	f.ledger = policyapply.NewMemLedgerWith(f.rec, policyapply.InForceDetail{JWT: f.jwt, ListSet: &f.ls})
	f.cluster = newCluster(f.jwt, "s1", "s2")
	k := userKey(t)
	p := f.plan(k)
	in := ExecInput{Build: func(context.Context) (Plan, error) { return p, nil }, Seen: &p, Ledger: f.ledger, Cluster: f.cluster, Account: f.acctPub, Inventory: 2,
		Signers: f.signers, Signer: f.signer, Marker: f.marker, Now: func() time.Time { return f.now }}
	if _, err := Execute(context.Background(), in); err == nil {
		t.Fatal("another range's key not declared: the exact signer count should fail")
	}
	in.Held = map[string]string{"foo.c": f.signers["foo.c"], "bar.c": other}
	res, err := Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Revoked) != 1 || res.Revoked[0] != k {
		t.Errorf("revoked %v", res.Revoked)
	}
	got, err := jwt.DecodeAccountClaims(f.inForceJWT())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.SigningKeys.GetScope(other); !ok {
		t.Error("the other range's key left the committed JWT")
	}
}
