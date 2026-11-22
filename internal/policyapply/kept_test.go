package policyapply

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
)

const foreignGroup = "bar.x"

// foreignScoped adds a fresh scoped signing key for group to the account JWT tok (re-signed by the account key) and
// returns the new token and the key.
func foreignScoped(t *testing.T, k keys, tok, group string) (string, string) {
	t.Helper()
	kp, _ := nkeys.CreateAccount()
	pub, _ := kp.PublicKey()
	return addScoped(t, k, tok, group, pub), pub
}

func addScoped(t *testing.T, k keys, tok, group, pub string) string {
	t.Helper()
	claims, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		t.Fatal(err)
	}
	sc := jwt.NewUserScope()
	sc.Key, sc.Role = pub, group
	claims.SigningKeys.AddScopedSigner(sc)
	out, err := claims.Encode(k.acct)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGenerationOfKeptSigners(t *testing.T) {
	k := newKeys(t)
	c := compile(t, set1())
	base := k.build(t, c)
	tok, kept := foreignScoped(t, k, base, foreignGroup)
	other, otherPub := foreignScoped(t, k, base, foreignGroup)
	plainKP, _ := nkeys.CreateAccount()
	plain, _ := plainKP.PublicKey()
	plainTok := k.buildWith(t, c, k.signers, []string{plain})
	wrongRole, wrongPub := foreignScoped(t, k, base, "bar.other")
	keptSet := map[string]string{foreignGroup: kept}
	for name, tc := range map[string]struct {
		tok  string
		kept map[string]string
		want bool
	}{
		"kept key present":               {tok, keptSet, true},
		"kept key not declared":          {tok, nil, false},
		"a foreign key outside the set":  {other, keptSet, false},
		"declared kept key missing":      {base, keptSet, false},
		"kept key under another role":    {wrongRole, map[string]string{foreignGroup: wrongPub}, false},
		"kept key present only plain":    {plainTok, map[string]string{foreignGroup: plain}, false},
		"kept key of this range's group": {tok, map[string]string{"foo.c": kept}, false},
		"another key, declared instead":  {other, map[string]string{foreignGroup: otherPub}, true},
	} {
		cands := []Candidate{{Generation: 1, Compiled: c, Signers: k.signers, Kept: tc.kept}}
		if _, ok := GenerationOf(tc.tok, cands); ok != tc.want {
			t.Errorf("%s: read = %v, want %v", name, ok, tc.want)
		}
	}
}

// keptWorld is newWorld in a multi-range account: every JWT also carries another range's scoped key.
func keptWorld(t *testing.T) (*world, map[string]string) {
	w := newWorld(t)
	var pub string
	w.j1, pub = foreignScoped(t, w.k, w.j1, foreignGroup)
	kept := map[string]string{foreignGroup: pub}
	w.j2 = addScoped(t, w.k, w.j2, foreignGroup, pub)
	w.cluster = newCluster(w.j1, w.servers...)
	w.prober.cluster = w.cluster
	w.previous.Kept = kept
	return w, kept
}

func keptRun(t *testing.T, w *world, kept map[string]string) RunInput {
	in := w.run(NewMemLedgerWith(w.rec1(), InForceDetail{JWT: w.j1}), set2(), "bytes-2")
	in.KeptSigners = kept
	in.Signer = SignerFuncs{
		SignFn: func(_ listset.Record, c *listset.Compiled) (string, error) {
			return addScoped(t, w.k, w.k.build(t, c, w.k.revoked), foreignGroup, kept[foreignGroup]), nil
		},
		SignPreviousFn: func(_ PreviousClaims, revoked []string) (string, error) {
			return addScoped(t, w.k, w.k.build(t, w.c1, revoked...), foreignGroup, kept[foreignGroup]), nil
		},
	}
	return in
}

func TestRunMultiRangeAccountKeepsOtherRangesSigners(t *testing.T) {
	w, kept := keptWorld(t)
	st, err := Run(context.Background(), keptRun(t, w, kept))
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateApplied || !st.InForce {
		t.Fatalf("%+v", st)
	}
	// Without KeptSigners the same account is never read as the target.
	w, kept = keptWorld(t)
	in := keptRun(t, w, kept)
	in.KeptSigners = nil
	if st, _ := Run(context.Background(), in); st.State == StateApplied {
		t.Fatalf("applied without the kept signers: %+v", st)
	}
}

func TestRunMultiRangeRollbackKeepsOtherRangesSigners(t *testing.T) {
	w, kept := keptWorld(t)
	w.cluster.failPush["s2"] = true
	st, err := Run(context.Background(), keptRun(t, w, kept))
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StatePartial || gens(st)["s1"] != "1" {
		t.Fatalf("%+v", st)
	}
	jwts, _ := w.cluster.AccountJWTs(context.Background(), "")
	claims, err := jwt.DecodeAccountClaims(jwts["s1"])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := claims.SigningKeys.GetScope(kept[foreignGroup]); !ok {
		t.Error("the rollback JWT dropped the kept signer")
	}
}

func TestRunMultiRangeRollbackDroppingKeptSignerIsNotPushed(t *testing.T) {
	w, kept := keptWorld(t)
	w.cluster.failPush["s2"] = true
	in := keptRun(t, w, kept)
	bad := w.k.build(t, w.c1, w.k.revoked)
	in.Signer = withSignPrevious(in.Signer, func(PreviousClaims, []string) (string, error) { return bad, nil })
	st, err := Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range st.Failing {
		found = found || f.Check == CheckRollback
	}
	if !found || st.State != StatePartial {
		t.Fatalf("%+v", st)
	}
	for _, p := range w.cluster.pushes {
		if p == bad {
			t.Error("a rollback without the kept signer was pushed")
		}
	}
}

// hookLedger runs onLock when the run takes the lock, before it reads anything under it.
type hookLedger struct {
	*MemLedger
	onLock func()
}

func (h hookLedger) Lock(ctx context.Context) (func(), error) {
	if h.onLock != nil {
		h.onLock()
	}
	return h.MemLedger.Lock(ctx)
}

func TestApplyReadsInventoryUnderTheLock(t *testing.T) {
	_, l, deps := applyWorld(t)
	rec := deps.IssueRecord.(*MemIssueRecord)
	late := freshUserPub(t)
	deps.Ledger = hookLedger{MemLedger: l, onLock: func() {
		// Apply's early read is done; a credential of the narrowed sender is recorded before the gate.
		if err := rec.Add(context.Background(), IssueEntry{Address: "foo.c.core", PublicKey: late, Purpose: PurposeIssue, IssuedAt: time.Unix(2000, 0)}); err != nil {
			t.Error(err)
		}
	}}
	st, err := Apply(context.Background(), applyReq(), deps)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range st.Failing {
		found = found || (f.Check == CheckOldKeyNotRevoked && strings.Contains(f.Detail, late))
	}
	if !found || st.State == StateApplied {
		t.Fatalf("the gate did not see the credential recorded after the early read: %+v", st)
	}
}

// A plain signing key that is mash's own (Plain) is not a key beyond the expected ones; another plain key still is.
func TestGenerationOfPlainSigners(t *testing.T) {
	k := newKeys(t)
	c := compile(t, set1())
	ownKP, _ := nkeys.CreateAccount()
	own, _ := ownKP.PublicKey()
	otherKP, _ := nkeys.CreateAccount()
	other, _ := otherKP.PublicKey()
	withOwn := k.buildWith(t, c, k.signers, []string{own})
	withBoth := k.buildWith(t, c, k.signers, []string{own, other})
	without := k.build(t, c)
	for name, tc := range map[string]struct {
		tok   string
		plain []string
		want  bool
	}{
		"own plain key present":     {withOwn, []string{own}, true},
		"own plain key absent":      {without, []string{own}, true},
		"plain key not declared":    {withOwn, nil, false},
		"another plain key as well": {withBoth, []string{own}, false},
		"declared key is the other": {withOwn, []string{other}, false},
	} {
		cands := []Candidate{{Generation: 1, Compiled: c, Signers: k.signers, Plain: tc.plain}}
		if _, ok := GenerationOf(tc.tok, cands); ok != tc.want {
			t.Errorf("%s: read = %v, want %v", name, ok, tc.want)
		}
	}
}

// The configured signing key is required: a JWT missing it, or carrying it scoped, is not the generation.
func TestGenerationOfRequiresConfiguredSigner(t *testing.T) {
	k := newKeys(t)
	c := compile(t, set1())
	ownKP, _ := nkeys.CreateAccount()
	own, _ := ownKP.PublicKey()
	plain := k.buildWith(t, c, k.signers, []string{own})
	missing := k.build(t, c)
	scopedTok := addScoped(t, k, missing, foreignGroup, own)
	for name, tc := range map[string]struct {
		tok  string
		want bool
	}{
		"missing":        {missing, false},
		"present scoped": {scopedTok, false},
		"present plain":  {plain, true},
	} {
		cands := []Candidate{{Generation: 1, Compiled: c, Signers: k.signers, Plain: []string{own}, RequiredPlain: []string{own}}}
		if _, ok := GenerationOf(tc.tok, cands); ok != tc.want {
			t.Errorf("%s: read = %v, want %v", name, ok, tc.want)
		}
	}
}
