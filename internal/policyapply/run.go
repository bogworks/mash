package policyapply

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/jwt/v2"

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
// the push fails or some server still lacks the target it pushes the previous generation so every server is back on
// one generation, and gates again. It never rolls back when every server already holds the target but a check fails:
// that is one generation, reported partial with the failures.
//
// signPrevious builds the rollback JWT. It is called only when a rollback is needed, with in.Expect.RevokedKeys (every
// key the change revokes, sorted and unique), and must return the previous generation's account JWT with every one of
// them revoked: a revocation is never lowered or removed. Before anything is pushed Converge decodes the result and
// checks that it carries the previous generation and every key in the list. If it does not, or signPrevious fails, the
// rollback is not pushed and a rollback failure is added (fail closed: the servers stay as they are, partial, nothing
// in force). The returned Status is the final gate result; it is applied only if the target is.
func Converge(ctx context.Context, in GateInput, targetJWT string, signPrevious func(revoked []string) (accountJWT string, err error)) (Status, error) {
	st, err := Gate(ctx, in)
	if err != nil || st.State == StateApplied || onTarget(st, in.Record.Generation) {
		return st, err
	}
	if pushed(ctx, in, targetJWT) {
		if st, err = Gate(ctx, in); err != nil || st.State == StateApplied || onTarget(st, in.Record.Generation) {
			return st, err
		}
	}
	failRollback := func(detail string) (Status, error) {
		st.Failing = append(st.Failing, Failure{Check: CheckRollback, Detail: detail})
		sortFailures(st.Failing)
		return st, nil
	}
	if signPrevious == nil || in.Previous == nil {
		return failRollback("servers hold different generations and there is no previous generation to push back")
	}
	revoked := slices.Compact(slices.Sorted(slices.Values(in.Expect.RevokedKeys)))
	prevTok, err := signPrevious(slices.Clone(revoked))
	if err != nil {
		return failRollback(fmt.Sprintf("servers hold different generations and the previous generation could not be signed: %v", err))
	}
	claims, derr := jwt.DecodeAccountClaims(prevTok)
	if derr != nil {
		return failRollback(fmt.Sprintf("rollback JWT does not decode, not pushed: %v", derr))
	}
	if g, ok := generationOfClaims(claims, []Candidate{{Generation: in.Previous.Generation, Compiled: in.Previous.Compiled}}); !ok || g != in.Previous.Generation {
		return failRollback(fmt.Sprintf("rollback JWT does not carry generation %d, not pushed", in.Previous.Generation))
	}
	var missing []string
	for _, k := range revoked {
		if _, has := claims.Revocations[k]; !has {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return failRollback("rollback JWT would un-revoke keys, not pushed; missing revocations: " + strings.Join(missing, ", "))
	}
	// A server holding the rollback carries the previous generation plus extra revocations; GenerationOf needs the
	// candidate's own revocations to be a subset, so in.Previous still matches it. A failed push back shows in the gate below: servers keep the generations they hold.
	pushed(ctx, in, prevTok)
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
	// Previous is the Compiled of the generation in force (nil when none); PreviousRevoked are the keys it revokes.
	Previous        *listset.Compiled
	PreviousRevoked []string
	// SignPrevious signs the rollback: the previous generation's account JWT with every key in revoked also revoked.
	// Run calls it only when a rollback is needed, with the union of RevokedKeys and the keys AfterPush revoked
	// (sorted, unique). Converge refuses to push a result that lacks the previous generation or any of those keys.
	SignPrevious func(revoked []string) (accountJWT string, err error)
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
	// AfterPush, if set, is called after the target is pushed and before the gate. It revokes keys (REQ-014: a
	// narrowed sender's old keys, a removed sender's keys) and reports them by sender address (the key "" is for keys
	// that belong to no sender). They are added to the gate's revoked keys, and each sender's keys are merged into
	// its old-key inventory (OldKeys), so a sender listed here needs no entry there; an address present with no keys
	// has an empty inventory. A sender in notConnected never showed a connection with its new key by the deadline: the Status lists it,
	// holds the state at partial and nothing is committed. See RotationsAfterPush.
	AfterPush func(ctx context.Context) (revoked map[string][]string, notConnected []string, err error)
	// OnStatus, if set, receives the applying status before the push.
	OnStatus func(Status)
	// BeforeCommit, if set, runs after the gate reports applied (and so with no sender not connected) and before the generation is committed, still under
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
func Run(ctx context.Context, in RunInput) (Status, error) { return run(ctx, in, nil, nil) }

// run is Run; when acc is not nil it is filled with what was accepted once the Expect is derived, before any push.
// When the ledger is a StatusWriter, the Status of the run is written to it at the end (see writeStatus), while the
// run still holds the ledger lease, so a later run's status is never overwritten by an earlier run's. When adjust is
// not nil it is given the outcome under the lease, before the status is written, and returns the Status to write and
// return.
func run(ctx context.Context, in RunInput, acc *Accepted, adjust func(Status, error) Status) (Status, error) {
	unlock, err := in.Ledger.Lock(ctx)
	if err != nil {
		return Status{}, err
	}
	defer unlock()
	st, err := runLocked(ctx, in, acc)
	if adjust != nil {
		st = adjust(st, err)
	}
	return st, writeStatus(ctx, in.Ledger, st, err)
}

// writeStatus writes st to l if l is a StatusWriter and st is a status of this run: applied, partial or refused. A run
// that failed before it had a status (the zero Status), and an applied status that came with an error (the commit or a
// provisioning step failed, so it is not what is in force), write nothing. It returns err, joined with the error of
// the write if that failed.
func writeStatus(ctx context.Context, l Ledger, st Status, err error) error {
	sw, ok := l.(StatusWriter)
	if !ok || st.State == "" || (err != nil && st.State == StateApplied) {
		return err
	}
	if werr := sw.WriteStatus(ctx, st); werr != nil {
		return errors.Join(err, fmt.Errorf("write status: %w", werr))
	}
	return err
}

// runLocked is the body of a run; the caller holds the ledger lease.
func runLocked(ctx context.Context, in RunInput, acc *Accepted) (Status, error) {
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
	var notConnected []string
	if in.AfterPush != nil {
		revoked, nc, aerr := in.AfterPush(ctx)
		notConnected = slices.Compact(slices.Sorted(slices.Values(nc)))
		// Whatever AfterPush revoked stays revoked, even when it also failed: it joins the gate's revoked keys, and
		// the keys of each sender are that sender's old keys, so the gate checks them too (merged into OldKeys; a
		// copy, the caller's map is not changed).
		var all []string
		oldKeys := maps.Clone(in.OldKeys)
		if oldKeys == nil {
			oldKeys = map[string][]string{}
		}
		for addr, keys := range revoked {
			all = append(all, keys...)
			if addr != "" {
				oldKeys[addr] = slices.Compact(slices.Sorted(slices.Values(slices.Concat(oldKeys[addr], keys))))
			}
		}
		gi.OldKeys = oldKeys
		gi.Expect.RevokedKeys = slices.Compact(slices.Sorted(slices.Values(slices.Concat(gi.Expect.RevokedKeys, all))))
		if aerr != nil {
			// The target is on the servers: pending, so only a resubmission of the same content may follow.
			if perr := in.Ledger.SetPending(rec); perr != nil {
				aerr = errors.Join(aerr, perr)
			}
			return Status{}, fmt.Errorf("after push of generation %d: %w", rec.Generation, aerr)
		}
	}
	st, err := Converge(ctx, gi, tok, in.SignPrevious)
	if len(notConnected) > 0 && err == nil {
		st.NotConnected = notConnected
		for _, s := range notConnected {
			st.Failing = append(st.Failing, Failure{Sender: s, Check: CheckNotConnected,
				Detail: "no connection with the new key was seen by the deadline; the old keys were revoked"})
		}
		sortFailures(st.Failing)
		st.State, st.InForce = StatePartial, false
	}
	if err != nil || st.State != StateApplied {
		// The target was pushed. Unless every server is back on the previous generation it may be visible, so it is
		// pending: only a resubmission of the same content may follow. A not-connected run is pending too, even when
		// the servers are back on the previous generation: the old keys are already revoked, so the previous
		// generation is not a place to return to.
		var perr error
		if err == nil && len(notConnected) == 0 && backOn(st, base.Generation) {
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
