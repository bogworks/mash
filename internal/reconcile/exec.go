package reconcile

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policy"
	"github.com/bogworks/mash/internal/policyapply"
)

// The refusals of Execute. Each is returned before anything is pushed.
var (
	// ErrNotExecutable: the plan is a preview against a file, or its issuer check failed.
	ErrNotExecutable = errors.New("reconcile: the plan cannot be executed")
	// ErrStalePlan: the generation in force is no longer the one the plan was computed against.
	ErrStalePlan = errors.New("reconcile: the generation in force changed since the plan was computed")
	// ErrPlanChanged: the plan rebuilt under the lock revokes something other than the plan the person saw.
	ErrPlanChanged = errors.New("reconcile: the plan changed since the dry run: run it again")
	// ErrPending: a generation is pending, pushed and not applied or rolled back: finish or roll it back first.
	ErrPending = errors.New("reconcile: a generation is pending")
	// ErrNotApplied: the gate did not pass with every server on the revoking JWT. The JWT in force is unchanged; the
	// revocations may be on some servers, and running the reconcile again signs and pushes them again.
	ErrNotApplied = errors.New("reconcile: the revocations are not on every server")
)

// Signer signs the in-force account claims with more keys revoked. It is the SignPrevious half of
// policyapply.Signer: policyapply.SignerFuncs and a full Signer both satisfy it, so wiring --yes later is one call.
type Signer interface {
	SignPrevious(prev policyapply.PreviousClaims, revoked []string) (accountJWT string, err error)
}

// IssueMarker marks a revoked key in the issue record. *admin.Admin is one.
type IssueMarker interface {
	MarkRevoked(ctx context.Context, publicKey string, at time.Time) error
}

// ExecInput is what Execute needs.
type ExecInput struct {
	// Build computes the plan. Execute calls it under the ledger lease, after the generation in force is read again,
	// and executes what it returns: never a plan computed earlier. The caller holds the account lock around Execute.
	Build func(ctx context.Context) (Plan, error)
	// Seen, when set, is the plan the person saw (the dry run): Execute refuses with ErrPlanChanged when the rebuilt
	// plan's revocations differ from it by key or reason (times may differ).
	Seen   *Plan
	Ledger policyapply.DetailLedger
	// Cluster, Account and Inventory are the push and the gate of policyapply, unchanged.
	Cluster   policyapply.Cluster
	Account   string
	Inventory int
	// Signers are the list set's scoped keys by group, as for policyapply.RunInput.Signers.
	Signers map[string]string
	Signer  Signer
	Marker  IssueMarker
	// BeforeWrite, when set, is called before the push ("push") and before the commit ("commit"): the caller's account
	// lock fence (admin.AccountLock.Held). An error stops the step.
	BeforeWrite func(ctx context.Context, what string) error
	Now         func() time.Time
}

// Result is what Execute did.
type Result struct {
	// Plan is the plan that was executed (the one built under the lease); zero when Execute refused before building it.
	Plan Plan
	// Status is the gate's report of the revoking JWT.
	Status policyapply.Status
	// Revoked is the keys now revoked on every server, sorted; Marked the ones marked revoked in the issue record (an
	// unrecorded key has no record to mark).
	Revoked []string
	Marked  []string
}

// Execute revokes the keys of the plan in.Build returns under the lease. It holds the ledger lease, signs the in-force account claims with the keys
// also revoked (Signer.SignPrevious), checks that the result is the generation in force with every earlier revocation
// kept and each new one at its planned time, pushes it to every server and gates it with policyapply.Converge (the
// same push and gate a list set uses, with no previous generation: there is no rollback of a revocation), commits it
// as the JWT in force with the record and list set unchanged (so the next list-set run keeps the revocations,
// policyapply.KeepsRevocations), and only then marks each key revoked in the issue record.
//
// The account lock (admin.Admin.LockAccount) is the caller's, taken before the plan is recomputed; pass its Held as
// BeforeWrite. Execute never signs anything itself and never reads a key.
func Execute(ctx context.Context, in ExecInput) (Result, error) {
	if in.Seen != nil && in.Seen.Source != SourceInForce {
		return Result{}, fmt.Errorf("%w: the plan seen is a %q preview, not the list set in force: a preview is never executed", ErrNotExecutable, in.Seen.Source)
	}
	if in.Build == nil || in.Ledger == nil || in.Cluster == nil || in.Signer == nil || in.Marker == nil || in.Signers == nil {
		return Result{}, errors.New("reconcile: Execute needs a plan builder, a ledger, a cluster, signers, a signer and an issue-record marker")
	}
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	fence := func(what string) error {
		if in.BeforeWrite == nil {
			return nil
		}
		return in.BeforeWrite(ctx, what)
	}
	unlock, err := in.Ledger.Lock(ctx)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	// Read everything again under the lease: the plan was computed before it.
	rec, err := in.Ledger.InForce()
	if err != nil {
		return Result{}, err
	}
	if _, pending, err := in.Ledger.Pending(); err != nil {
		return Result{}, err
	} else if pending {
		return Result{}, ErrPending
	}
	// Rebuild the plan now, under the account lock and the lease and right before signing: what is revoked is what is
	// true now, not what was true at the dry run.
	p, err := in.Build(ctx)
	if err != nil {
		return Result{}, err
	}
	res0 := Result{Plan: p}
	if p.Source != SourceInForce || len(p.IssuerProblems) > 0 {
		return res0, ErrNotExecutable
	}
	if rec.Generation == 0 || rec.Generation != p.Generation {
		return res0, fmt.Errorf("%w: plan for generation %d, in force %d", ErrStalePlan, p.Generation, rec.Generation)
	}
	if in.Seen != nil && !sameRevocations(*in.Seen, p) {
		return res0, ErrPlanChanged
	}
	if len(p.Revocations) == 0 {
		return res0, nil
	}
	prevJWT, err := in.Ledger.InForceJWT()
	if err != nil {
		return res0, err
	}
	if prevJWT == "" {
		return res0, errors.New("reconcile: the ledger stores no account JWT for the generation in force, so its revocations cannot be kept")
	}
	ls, ok, err := in.Ledger.InForceListSet()
	if err != nil {
		return res0, err
	}
	if !ok {
		return res0, policyapply.ErrNoInForceListSet
	}
	compiled, err := listset.Compile(ls)
	if err != nil {
		return res0, fmt.Errorf("compile the list set in force (generation %d): %w", rec.Generation, err)
	}
	keys := make([]string, 0, len(p.Revocations))
	at := map[string]time.Time{}
	for _, r := range p.Revocations {
		t, err := time.Parse(time.RFC3339, r.At)
		if err != nil {
			return res0, fmt.Errorf("reconcile: the plan's revocation time of %s is not a time", r.Key)
		}
		keys = append(keys, r.Key)
		at[r.Key] = t
	}
	slices.Sort(keys)

	if g, ok := policyapply.GenerationOf(prevJWT, []policyapply.Candidate{{Generation: rec.Generation, Compiled: compiled, Signers: in.Signers}}); !ok || g != rec.Generation {
		return res0, fmt.Errorf("reconcile: the JWT in force does not carry generation %d with the expected signers", rec.Generation)
	}
	serverJWTs, err := policyapply.ServerRevocationJWTs(ctx, in.Cluster, in.Account, in.Inventory)
	if err != nil {
		return res0, err
	}
	tok, err := in.Signer.SignPrevious(policyapply.PreviousClaims{JWT: prevJWT}, slices.Clone(keys))
	if err != nil {
		return res0, fmt.Errorf("reconcile: sign: %w", err)
	}
	if err := checkSigned(tok, prevJWT, serverJWTs, keys, at, rec, compiled, in.Signers); err != nil {
		return res0, err
	}
	if cc, ok := in.Ledger.(policyapply.CommitSizeChecker); ok {
		if err := cc.CheckCommitSize(rec, tok, ls); err != nil {
			return res0, err
		}
	}

	expect, err := policyapply.NewExpect(nil, compiled, keys)
	if err != nil {
		return res0, err
	}
	// Each key carries its issue time, which only the plan knows for a connected one: the gate then requires the
	// revocation on every server to be at or after it.
	old := make([]policyapply.OldKey, 0, len(keys))
	for _, r := range p.Revocations {
		old = append(old, policyapply.OldKey{PublicKey: r.Key, IssuedAt: at[r.Key]})
	}
	gi := policyapply.GateInput{
		Cluster: in.Cluster, Account: in.Account, Inventory: in.Inventory,
		Record: rec, Compiled: compiled, PreviousJWT: prevJWT,
		Expect: expect, Signers: in.Signers, TargetJWT: tok, Now: now,
		OldKeys: map[string][]policyapply.OldKey{"reconcile": old},
	}
	if err := fence(policyapply.FencePush); err != nil {
		return res0, &policyapply.FenceError{What: policyapply.FencePush, Err: err}
	}
	st, err := policyapply.Converge(ctx, gi, tok, nil)
	res := Result{Plan: p, Status: st}
	if err != nil {
		return res, err
	}
	if st.State != policyapply.StateApplied {
		return res, fmt.Errorf("%w: %s", ErrNotApplied, failing(st))
	}
	if err := fence(policyapply.FenceCommit); err != nil {
		return res, &policyapply.FenceError{What: policyapply.FenceCommit, Err: err}
	}
	if err := in.Ledger.CompareAndCommitDetail(rec.Generation, rec, policyapply.InForceDetail{JWT: tok, ListSet: &ls}); err != nil {
		return res, fmt.Errorf("reconcile: the revocations are on every server but could not be committed as the JWT in force (run the reconcile again to commit them): %w", err)
	}
	res.Revoked = keys
	var errs []error
	for _, k := range keys {
		switch err := in.Marker.MarkRevoked(ctx, k, at[k]); {
		case err == nil:
			res.Marked = append(res.Marked, k)
		case errors.Is(err, admin.ErrNotRecorded): // an untracked key has no record to mark
		default:
			errs = append(errs, fmt.Errorf("mark %s revoked in the issue record: %w", k, err))
		}
	}
	return res, errors.Join(errs...)
}

// checkSigned is what Execute requires of the signed JWT before it pushes it.
func checkSigned(tok, prev string, serverJWTs []string, keys []string, at map[string]time.Time, rec listset.Record, c *listset.Compiled, signers map[string]string) error {
	if len(tok) > policy.AccountJWTLimit {
		return policyapply.ErrAccountJWTTooLarge
	}
	claims, err := jwt.DecodeAccountClaims(tok)
	if err != nil {
		return fmt.Errorf("reconcile: the signed JWT does not decode: %w", err)
	}
	if g, ok := policyapply.GenerationOf(tok, []policyapply.Candidate{{Generation: rec.Generation, Compiled: c, RevokedKeys: keys, Signers: signers}}); !ok || g != rec.Generation {
		return fmt.Errorf("reconcile: the signed JWT does not carry generation %d with the expected signers and every key revoked, not pushed", rec.Generation)
	}
	if err := policyapply.KeepsRevocations(prev, true, tok); err != nil {
		return fmt.Errorf("reconcile: %w, not pushed", err)
	}
	for _, sj := range serverJWTs {
		if err := policyapply.KeepsRevocations(sj, true, tok); err != nil {
			return fmt.Errorf("reconcile: %w (the revocations a server holds), not pushed", err)
		}
	}
	for _, k := range keys {
		if got, has := claims.Revocations[k]; !has || got < at[k].Unix() {
			return fmt.Errorf("reconcile: the signed JWT revokes %s too early (or not at all): a revocation covers only credentials issued at or before it, not pushed", k)
		}
	}
	return nil
}

func failing(st policyapply.Status) string {
	if len(st.Failing) == 0 {
		return "no server reported a failure but not every server holds the JWT"
	}
	f := st.Failing[0]
	return fmt.Sprintf("%s: %s (%d failures)", f.Check, f.Detail, len(st.Failing))
}

// sameRevocations is true when both plans are for the same account, source and generation and revoke the same keys
// for the same reasons; the times may differ.
func sameRevocations(a, b Plan) bool {
	if a.Account != b.Account || a.Source != b.Source || a.Generation != b.Generation {
		return false
	}
	set := func(p Plan) []string {
		out := make([]string, 0, len(p.Revocations))
		for _, r := range p.Revocations {
			out = append(out, r.Key+"\x00"+r.Reason)
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(set(a), set(b))
}
