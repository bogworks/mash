package policyapply

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/bogworks/mash/internal/listset"
)

// ErrSignedMismatch is returned by Run when what was signed is not what Accept compiled: the Compiled changed, or the
// JWT does not carry it. Nothing is pushed.
var ErrSignedMismatch = errors.New("what was signed is not the compiled output that was accepted")

// Ledger holds the record in force. It changes only after the gate passes.
type Ledger interface {
	InForce() (listset.Record, error)
	// CompareAndCommit replaces the record in force with rec if the generation in force is base, atomically, and
	// otherwise fails with an error matching listset.ErrStaleBase.
	CompareAndCommit(base uint64, rec listset.Record) error
}

// MemLedger is an in-memory Ledger, safe for concurrent use. It is process-local: see the package comment.
type MemLedger struct {
	mu  sync.Mutex // guards rec
	run sync.Mutex // serialises Run
	rec listset.Record
}

// NewMemLedger returns a MemLedger with rec in force; the zero Record means nothing is in force.
func NewMemLedger(rec listset.Record) *MemLedger { return &MemLedger{rec: rec} }

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

// Lock makes Run exclusive on this ledger until the returned function is called, so two runs do not push over each
// other: the second reads the record the first committed and is refused as stale. Run uses it when the Ledger has it.
func (l *MemLedger) Lock() (unlock func()) {
	l.run.Lock()
	return l.run.Unlock
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
	// Sign builds the account JWT for the record and a copy of the Compiled. Signing is the caller's.
	Sign func(rec listset.Record, c *listset.Compiled) (accountJWT string, err error)
	Now  func() time.Time
	// OnStatus, if set, receives the applying status before the push.
	OnStatus func(Status)
}

// Run accepts, signs, pushes, converges and gates a list set, and commits the generation only if it is applied.
//
// A list set that fails validation comes back as a refused Status and a nil error. A stale base, a signing error or a
// mismatch between the signed JWT and the accepted output (ErrSignedMismatch, before any push) come back as errors.
// When the final state is not applied nothing is committed and the Status says partial with the failures. The Compiled
// is deep-copied before Sign; Sign gets its own copy, and both the copy and the frozen original must still digest to
// Record.Signed, and the JWT must decode to the target generation, before anything is pushed.
func Run(ctx context.Context, in RunInput) (Status, error) {
	if unlock, ok := in.Ledger.(interface{ Lock() func() }); ok {
		defer unlock.Lock()()
	}
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

	frozen := cloneCompiled(compiled)
	signCopy := cloneCompiled(frozen)
	tok, err := in.Sign(rec, signCopy)
	if err != nil {
		return Status{}, fmt.Errorf("sign generation %d: %w", rec.Generation, err)
	}
	if listset.DigestCompiled(signCopy) != rec.Signed || listset.DigestCompiled(frozen) != rec.Signed {
		return Status{}, ErrSignedMismatch
	}
	expect, err := NewExpect(in.Previous, frozen, in.RevokedKeys)
	if err != nil {
		return Status{}, err
	}
	if g, ok := GenerationOf(tok, []Candidate{{rec.Generation, frozen, expect.RevokedKeys}}); !ok || g != rec.Generation {
		return Status{}, fmt.Errorf("%w: the account JWT does not carry generation %d", ErrSignedMismatch, rec.Generation)
	}

	gi := GateInput{
		Cluster: in.Cluster, Prober: in.Prober, Witnesses: in.Witnesses, Account: in.Account, Inventory: in.Inventory,
		Record: rec, Compiled: frozen, Expect: expect, Now: in.Now,
	}
	if in.Previous != nil {
		gi.Previous = &Candidate{Generation: base.Generation, Compiled: in.Previous, RevokedKeys: in.PreviousRevoked}
	}
	if in.OnStatus != nil {
		in.OnStatus(newStatus(rec, StateApplying, in.Now))
	}
	pushed(ctx, gi, tok)
	st, err := Converge(ctx, gi, tok, in.PreviousJWT)
	if err != nil || st.State != StateApplied {
		return st, err
	}
	if err := in.Ledger.CompareAndCommit(base.Generation, rec); err != nil {
		return st, fmt.Errorf("commit generation %d: %w", rec.Generation, err)
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
