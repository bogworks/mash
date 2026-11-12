package policyapply

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policy"
)

// ErrSignedMismatch is returned by Run when what was signed is not what Accept compiled: the Compiled changed, or the
// JWT does not carry it. Nothing is pushed.
var ErrSignedMismatch = errors.New("what was signed is not the compiled output that was accepted")

// ErrPendingGeneration is returned by Run when a generation is pending and the submission is not that same content.
// A pending generation is one whose JWT was pushed but did not pass the gate and was not rolled back, so it may still
// be visible on servers. The operator resolves it by resubmitting the same list set: Run pushes and gates again and,
// when applied, commits it and clears the pending record. Rolling back is Converge's work, which clears the record when
// every server is back on the previous generation; this change offers no other way out.
var ErrPendingGeneration = errors.New("a generation is pending: resubmit the same list set")

// ErrAccountJWTTooLarge is returned by Run when the signed account JWT exceeds policy.AccountJWTLimit. Nothing is
// pushed.
var ErrAccountJWTTooLarge = errors.New("the signed account JWT is over the size limit")

// Ledger holds the record in force. It changes only after the gate passes.
type Ledger interface {
	// Lock takes the exclusive run lock, or fails when ctx is done first. Run holds it across reading the record in
	// force, Accept, sign, push, gate and commit, and re-reads the record in force AFTER taking it. A persistent
	// implementation MUST be a cross-process lock (a lease in the shared store): a process-local lock lets runs of
	// separate processes interleave pushes.
	Lock(ctx context.Context) (unlock func(), err error)
	InForce() (listset.Record, error)
	// CompareAndCommit replaces the record in force with rec if the generation in force is base, atomically, and
	// otherwise fails with an error matching listset.ErrStaleBase.
	CompareAndCommit(base uint64, rec listset.Record) error
	// Pending returns the pending generation: pushed, not applied, not rolled back. ok is false when none.
	Pending() (rec listset.Record, ok bool, err error)
	// SetPending records rec as pending, replacing any other.
	SetPending(rec listset.Record) error
	// ClearPending removes the pending record.
	ClearPending() error
}

// MemLedger is an in-memory Ledger, safe for concurrent use. It is process-local: see the package comment.
type MemLedger struct {
	mu      sync.Mutex // guards rec, pending, hasPend
	run     chan struct{}
	rec     listset.Record
	pending listset.Record
	hasPend bool
}

// NewMemLedger returns a MemLedger with rec in force; the zero Record means nothing is in force.
func NewMemLedger(rec listset.Record) *MemLedger {
	return &MemLedger{rec: rec, run: make(chan struct{}, 1)}
}

// InForce returns the record in force.
func (l *MemLedger) InForce() (listset.Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec, nil
}

// CompareAndCommit implements Ledger.
func (l *MemLedger) CompareAndCommit(base uint64, rec listset.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rec.Generation != base {
		return &listset.StaleBaseError{Stated: base, InForce: l.rec.Generation}
	}
	l.rec = rec
	return nil
}

// Lock implements Ledger. It is exclusive within this process only.
func (l *MemLedger) Lock(ctx context.Context) (unlock func(), err error) {
	select {
	case l.run <- struct{}{}:
		return func() { <-l.run }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Pending implements Ledger.
func (l *MemLedger) Pending() (listset.Record, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pending, l.hasPend, nil
}

// SetPending implements Ledger.
func (l *MemLedger) SetPending(rec listset.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending, l.hasPend = rec, true
	return nil
}

// ClearPending implements Ledger.
func (l *MemLedger) ClearPending() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending, l.hasPend = listset.Record{}, false
	return nil
}

// Converge gates the target and, when servers do not all hold it, finishes the push: it pushes targetJWT again. If
// the push fails or some server still lacks the target it pushes previousJWT so every server is back on one
// generation, and gates again. It never rolls back when every server already holds the target but a check fails: that
// is one generation, reported partial with the failures. previousJWT is the caller's rollback JWT; it must keep the
// target's revoked keys revoked, or the rollback un-revokes them. The returned Status is the final gate result; it is
// applied only if the target is.
func Converge(ctx context.Context, in GateInput, targetJWT, previousJWT string) (Status, error) {
	st, err := Gate(ctx, in)
	if err != nil || st.State == StateApplied || onTarget(st, in.Record.Generation) {
		return st, err
	}
	if pushed(ctx, in, targetJWT) {
		if st, err = Gate(ctx, in); err != nil || st.State == StateApplied || onTarget(st, in.Record.Generation) {
			return st, err
		}
	}
	if previousJWT == "" || in.Previous == nil {
		st.Failing = append(st.Failing, Failure{Check: CheckRollback, Detail: "servers hold different generations and there is no previous generation to push back"})
		sortFailures(st.Failing)
		return st, nil
	}
	// A failed push back shows in the gate below: servers keep the generations they hold.
	pushed(ctx, in, previousJWT)
	return Gate(ctx, in)
}

// onTarget says every server holds the target, so there is nothing to converge.
func onTarget(st Status, gen uint64) bool {
	for _, s := range st.Servers {
		if s.Generation == nil || *s.Generation != gen {
			return false
		}
	}
	return len(st.Servers) > 0
}

// backOn says every server holds generation gen (the previous one): the push is rolled back everywhere.
func backOn(st Status, gen uint64) bool { return onTarget(st, gen) }

// pushed sends tok to every server and reports whether the push was acknowledged by all of them without error.
func pushed(ctx context.Context, in GateInput, tok string) bool {
	set, err := in.Cluster.Servers(ctx)
	if err != nil {
		return false
	}
	acks, err := in.Cluster.Push(ctx, tok, slices.Clone(set.Names))
	if err != nil {
		return false
	}
	got := map[string]bool{}
	for _, a := range acks {
		if !a.OK() {
			return false
		}
		got[a.Server] = true
	}
	for _, n := range set.Names {
		if !got[n] {
			return false
		}
	}
	return true
}

// RunInput is one submission.
type RunInput struct {
	Cluster   Cluster
	Prober    Prober
	Witnesses Witnesses
	Ledger    Ledger
	Account   string
	Inventory int

	// ListSet and Received are the submission and the exact bytes it was decoded from.
	ListSet  listset.ListSet
	Received []byte
	// Previous is the Compiled of the generation in force (nil when none); PreviousJWT is the JWT to push back
	// (see Converge); PreviousRevoked are the keys it revokes.
	Previous        *listset.Compiled
	PreviousJWT     string
	PreviousRevoked []string
	// RevokedKeys are the user public keys this change revokes.
	RevokedKeys []string
	// Signers are the expected scoped signing keys of the target, and PreviousSigners of the previous generation
	// (see Candidate.Signers). Signers is required: Run refuses a target without it.
	Signers         map[string]string
	PreviousSigners map[string]string
	// OldKeys are the public keys of each sender's credentials issued before this change (see GateInput.OldKeys).
	OldKeys map[string][]string
	// Sign builds the account JWT for the record and a copy of the Compiled. Signing is the caller's.
	Sign func(rec listset.Record, c *listset.Compiled) (accountJWT string, err error)
	Now  func() time.Time
	// OnStatus, if set, receives the applying status before the push.
	OnStatus func(Status)
	// BeforeCommit, if set, runs after the gate reports applied and before the generation is committed, still under
	// the ledger's lock. It gets what Run accepted and froze. If it fails, nothing is committed and the generation is
	// pending (a resubmission of the same content runs it again), and Run returns the error. RunOrdered uses it to
	// provision new addresses.
	BeforeCommit func(ctx context.Context, acc Accepted, st Status) error
}

// Accepted is what Run accepted and froze for one submission: the record, the Compiled that was signed and gated, and
// the Expect derived from it. It is derived inside Run from the submission; a caller never supplies it. Compiled is
// shared with Run and must be treated as read-only.
type Accepted struct {
	Record   listset.Record
	Compiled *listset.Compiled
	Expect   Expect
}

// Run accepts, signs, pushes, converges and gates a list set, and commits the generation only if it is applied.
//
// Run holds the ledger's lock throughout and reads the record in force after taking it. If the target was pushed and
// the final state is not applied, the target is recorded as pending unless every server is back on the previous
// generation. While one is pending Run refuses any submission with other content with ErrPendingGeneration, before
// signing or pushing; resubmitting the same content pushes and gates again and, when applied, commits and clears it.
// A signed JWT over policy.AccountJWTLimit is refused with ErrAccountJWTTooLarge before any push. A target without
// Signers is refused as a partial Status with a generation failure.
//
// A list set that fails validation comes back as a refused Status and a nil error. A stale base, a signing error or a
// mismatch between the signed JWT and the accepted output (ErrSignedMismatch, before any push) come back as errors.
// When the final state is not applied nothing is committed and the Status says partial with the failures. If
// RunInput.BeforeCommit fails after the gate applied, nothing is committed, the generation is pending, and its error
// is returned. The Compiled
// is deep-copied before Sign; Sign gets its own copy, and both the copy and the frozen original must still digest to
// Record.Signed, and the JWT must decode to the target generation, before anything is pushed.
func Run(ctx context.Context, in RunInput) (Status, error) { return run(ctx, in, nil) }

// run is Run; when acc is not nil it is filled with what was accepted once the Expect is derived, before any push.
func run(ctx context.Context, in RunInput, acc *Accepted) (Status, error) {
	unlock, err := in.Ledger.Lock(ctx)
	if err != nil {
		return Status{}, err
	}
	defer unlock()
	base, err := in.Ledger.InForce()
	if err != nil {
		return Status{}, err
	}
	rec, compiled, err := listset.Accept(base, in.ListSet, in.Received)
	if err != nil {
		var ve *listset.ValidationError
		var ce *listset.CheckError
		st := newStatus(listset.Record{}, StateRefused, in.Now)
		switch {
		case errors.As(err, &ve):
			st.Refusals = refusals("validate", ve.Problems)
		case errors.As(err, &ce):
			st.Refusals = refusals("check", ce.Problems)
		default:
			return Status{}, err
		}
		return st, nil
	}

	pend, hasPend, err := in.Ledger.Pending()
	if err != nil {
		return Status{}, err
	}
	if hasPend && pend.Generation != base.Generation+1 { // left over from a generation that has since been committed
		if err := in.Ledger.ClearPending(); err != nil {
			return Status{}, err
		}
		hasPend = false
	}
	if hasPend && pend.Content != rec.Content {
		return Status{}, fmt.Errorf("%w: generation %d is pending, not this content", ErrPendingGeneration, pend.Generation)
	}
	if in.Signers == nil {
		st := newStatus(rec, StatePartial, in.Now)
		st.Failing = []Failure{{Check: CheckGeneration, Detail: NoSignersDetail}}
		return st, nil
	}

	frozen := cloneCompiled(compiled)
	signCopy := cloneCompiled(frozen)
	tok, err := in.Sign(rec, signCopy)
	if err != nil {
		return Status{}, fmt.Errorf("sign generation %d: %w", rec.Generation, err)
	}
	if len(tok) > policy.AccountJWTLimit {
		return Status{}, fmt.Errorf("%w: %d bytes, limit %d", ErrAccountJWTTooLarge, len(tok), policy.AccountJWTLimit)
	}
	if listset.DigestCompiled(signCopy) != rec.Signed || listset.DigestCompiled(frozen) != rec.Signed {
		return Status{}, ErrSignedMismatch
	}
	expect, err := NewExpect(in.Previous, frozen, in.RevokedKeys)
	if err != nil {
		return Status{}, err
	}
	if acc != nil {
		*acc = Accepted{Record: rec, Compiled: frozen, Expect: expect}
	}
	if g, ok := GenerationOf(tok, []Candidate{{Generation: rec.Generation, Compiled: frozen, RevokedKeys: expect.RevokedKeys, Signers: in.Signers}}); !ok || g != rec.Generation {
		return Status{}, fmt.Errorf("%w: the account JWT does not carry generation %d", ErrSignedMismatch, rec.Generation)
	}

	gi := GateInput{
		Cluster: in.Cluster, Prober: in.Prober, Witnesses: in.Witnesses, Account: in.Account, Inventory: in.Inventory,
		Record: rec, Compiled: frozen, Expect: expect, Signers: in.Signers, OldKeys: in.OldKeys, Now: in.Now,
	}
	if in.Previous != nil {
		gi.Previous = &Candidate{Generation: base.Generation, Compiled: in.Previous, RevokedKeys: in.PreviousRevoked, Signers: in.PreviousSigners}
	}
	if in.OnStatus != nil {
		in.OnStatus(newStatus(rec, StateApplying, in.Now))
	}
	pushed(ctx, gi, tok)
	st, err := Converge(ctx, gi, tok, in.PreviousJWT)
	if err != nil || st.State != StateApplied {
		// The target was pushed. Unless every server is back on the previous generation it may be visible, so it is
		// pending: only a resubmission of the same content may follow.
		var perr error
		if err == nil && backOn(st, base.Generation) {
			perr = in.Ledger.ClearPending()
		} else {
			perr = in.Ledger.SetPending(rec)
		}
		if err == nil {
			err = perr
		}
		return st, err
	}
	if in.BeforeCommit != nil {
		if err := in.BeforeCommit(ctx, Accepted{Record: rec, Compiled: frozen, Expect: expect}, st); err != nil {
			// The target is on the servers and not committed: pending, so only the same content may follow.
			if perr := in.Ledger.SetPending(rec); perr != nil {
				err = errors.Join(err, perr)
			}
			return st, fmt.Errorf("before committing generation %d: %w", rec.Generation, err)
		}
	}
	if err := in.Ledger.CompareAndCommit(base.Generation, rec); err != nil {
		return st, fmt.Errorf("commit generation %d: %w", rec.Generation, err)
	}
	if err := in.Ledger.ClearPending(); err != nil {
		return st, fmt.Errorf("clear pending after committing generation %d: %w", rec.Generation, err)
	}
	return st, nil
}

func refusals(stage string, ps []listset.Problem) []listset.Refusal {
	out := make([]listset.Refusal, 0, len(ps))
	for _, p := range ps {
		out = append(out, listset.Refusal{Stage: stage, Entry: p.Entry, Reason: p.Reason})
	}
	return out
}

// cloneCompiled deep-copies c. A User's Permissions must be empty (Check refuses otherwise), so copying it by value
// shares nothing.
func cloneCompiled(c *listset.Compiled) *listset.Compiled {
	out := &listset.Compiled{Range: c.Range}
	for _, t := range c.Templates {
		p := t.Permissions
		p.Pub.Allow, p.Pub.Deny = slices.Clone(p.Pub.Allow), slices.Clone(p.Pub.Deny)
		p.Sub.Allow, p.Sub.Deny = slices.Clone(p.Sub.Allow), slices.Clone(p.Sub.Deny)
		if p.Resp != nil {
			r := *p.Resp
			p.Resp = &r
		}
		out.Templates = append(out.Templates, listset.Template{Group: t.Group, Permissions: p})
	}
	for _, u := range c.Users {
		u.Tags = slices.Clone(u.Tags)
		out.Users = append(out.Users, u)
	}
	return out
}
