package policyapply

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/bogworks/mash/internal/listset"
)

// Provisioning markers (REQ-015).
//
// RunOrdered creates a new address's inbox only after the senders that must refuse it have passed the gate. If the
// credential step then fails, the generation stays pending and the inbox stays. A later run of the same content may
// find the gate failing and roll the target back: the previous generation is pushed again, the pending record is
// cleared, and the new inbox remains, so a sender that the target refuses would reach the new address again.
//
// To make that impossible, a ledger that implements ProvisionMarkers persists, before every CreateInbox, a marker for
// (address, generation). While a marker stands for the generation a run builds, RunOrdered refuses to roll back: the
// FenceRollback write is refused and the pending record is not cleared, so the generation stays pending and the run
// fails (ErrProvisioningMarker). A marker stands until it is cleared: after the generation commits, or after the
// Provisioner removed the inbox (InboxRemover.RemoveInbox), which is tried first when the Provisioner implements it.
// A marker of another generation is not looked at: it belongs to a generation that was committed or replaced.
//
// A ledger that does not implement ProvisionMarkers keeps the earlier behaviour.

// ProvisionMarkers is the optional interface of a Ledger that keeps provisioning markers. A persistent implementation
// writes them in the same store as the pending record, so they outlive the process. MemLedger and KVLedger may
// implement it later.
type ProvisionMarkers interface {
	// MarkProvisioning records that the inbox of address may exist for generation. It is called before CreateInbox.
	MarkProvisioning(address string, generation uint64) error
	// Provisioning returns the addresses with a marker for generation, sorted.
	Provisioning(generation uint64) ([]string, error)
	// ClearProvisioning removes the marker of address for generation; a marker that is not there is not an error.
	ClearProvisioning(address string, generation uint64) error
}

// InboxRemover is the optional interface of a Provisioner that can take a new address's inbox away again, which is what
// lets a rollback proceed while a marker stands. RemoveInbox must be idempotent and must leave the address unable to
// receive (the inbox stream and consumer removed, or fenced).
type InboxRemover interface {
	RemoveInbox(ctx context.Context, address string) error
}

// VerifyOnly is the optional interface of a Provisioner whose CreateInbox only verifies an inbox that already exists
// and creates nothing. RunOrdered writes no provisioning marker for such a Provisioner: there is no inbox of this run to
// remove, so a marker would block a rollback for nothing.
type VerifyOnly interface {
	VerifiesOnly() bool
}

// verifiesOnly reports whether prov declares that CreateInbox creates nothing.
func verifiesOnly(prov Provisioner) bool {
	v, ok := prov.(VerifyOnly)
	return ok && v.VerifiesOnly()
}

// ErrProvisioningMarker is the error of a run that refused to roll back or clear its pending generation because an
// inbox may exist that a rollback would expose to a sender the target refuses.
var ErrProvisioningMarker = errors.New("an inbox was provisioned for the pending generation; it cannot be rolled back until the inbox is removed")

// provisionGuard decides whether the generation of a run may be rolled back.
type provisionGuard struct {
	ledger  Ledger
	markers ProvisionMarkers
	remover InboxRemover
}

// release returns nil when no marker stands for the generation the run builds, after removing the inboxes the
// Provisioner can remove and clearing their markers. Otherwise it returns an error wrapping ErrProvisioningMarker that
// names the addresses.
func (g provisionGuard) release(ctx context.Context) error {
	base, err := g.ledger.InForce()
	if err != nil {
		return err
	}
	gen := base.Generation + 1
	marked, err := g.markers.Provisioning(gen)
	if err != nil {
		return fmt.Errorf("read provisioning markers: %w", err)
	}
	var left []string
	for _, a := range marked {
		if g.remover != nil && g.remover.RemoveInbox(ctx, a) == nil && g.markers.ClearProvisioning(a, gen) == nil {
			continue
		}
		left = append(left, a)
	}
	if len(left) > 0 {
		sort.Strings(left)
		return fmt.Errorf("%w: %v", ErrProvisioningMarker, left)
	}
	return nil
}

// guardLedger is the Ledger a run uses when markers are kept: ClearPending is refused while a marker stands.
type guardLedger struct {
	optForward
	g provisionGuard
}

func (l guardLedger) ClearPending() error {
	if err := l.g.release(context.Background()); err != nil {
		return err
	}
	return l.Ledger.ClearPending()
}

// optForward forwards, for a ledger wrapper, the optional interfaces Run looks for that need no other interface:
// PendingJWTLedger and CommitSizeChecker. When the wrapped ledger lacks one, the method does what Run does for a ledger
// without it (SetPending, no stored pending JWT, no size check), so claiming the interface changes nothing.
type optForward struct{ Ledger }

func (l optForward) SetPendingJWT(rec listset.Record, jwt string) error {
	if pl, ok := l.Ledger.(PendingJWTLedger); ok {
		return pl.SetPendingJWT(rec, jwt)
	}
	return l.Ledger.SetPending(rec)
}

func (l optForward) PendingJWT() (string, error) {
	if pl, ok := l.Ledger.(PendingJWTLedger); ok {
		return pl.PendingJWT()
	}
	return "", nil
}

func (l optForward) CheckCommitSize(rec listset.Record, jwt string, ls listset.ListSet) error {
	if cs, ok := l.Ledger.(CommitSizeChecker); ok {
		return cs.CheckCommitSize(rec, jwt, ls)
	}
	return nil
}

// commitRollback is RollbackLedger.CommitRollback of l, or ClearPending when l is not a RollbackLedger, which is what
// Run does for such a ledger.
func commitRollback(l Ledger, jwt string) error {
	if rl, ok := l.(RollbackLedger); ok && jwt != "" {
		return rl.CommitRollback(jwt)
	}
	return l.ClearPending()
}

// guardDetail, guardStatus and guardBoth keep the optional interfaces of the ledger a run was given, so wrapping does
// not change what Run does with it. A guardDetail is a RollbackLedger: its CommitRollback is refused while a marker
// stands, like ClearPending.
type guardDetail struct {
	guardLedger
	dl DetailLedger
}

func (l guardDetail) CommitRollback(jwt string) error {
	if err := l.g.release(context.Background()); err != nil {
		return err
	}
	return commitRollback(l.guardLedger.Ledger, jwt)
}

func (l guardDetail) CompareAndCommitDetail(base uint64, rec listset.Record, d InForceDetail) error {
	return l.dl.CompareAndCommitDetail(base, rec, d)
}
func (l guardDetail) InForceJWT() (string, error)                    { return l.dl.InForceJWT() }
func (l guardDetail) InForceListSet() (listset.ListSet, bool, error) { return l.dl.InForceListSet() }

type guardStatus struct {
	guardLedger
	sw StatusWriter
}

func (l guardStatus) WriteStatus(ctx context.Context, st Status) error {
	return l.sw.WriteStatus(ctx, st)
}

type guardBoth struct {
	guardDetail
	sw StatusWriter
}

func (l guardBoth) WriteStatus(ctx context.Context, st Status) error {
	return l.sw.WriteStatus(ctx, st)
}

// withProvisionGuard returns in with the rollback refusal installed when the ledger keeps markers, and those markers;
// otherwise in unchanged and nil. The BeforeWrite hook of the caller still runs first.
func withProvisionGuard(in RunInput, prov Provisioner) (RunInput, ProvisionMarkers) {
	pm, ok := in.Ledger.(ProvisionMarkers)
	if !ok {
		return in, nil
	}
	remover, _ := prov.(InboxRemover)
	g := provisionGuard{ledger: in.Ledger, markers: pm, remover: remover}
	gl := guardLedger{optForward{in.Ledger}, g}
	dl, hasDetail := in.Ledger.(DetailLedger)
	sw, hasStatus := in.Ledger.(StatusWriter)
	switch {
	case hasDetail && hasStatus:
		in.Ledger = guardBoth{guardDetail{gl, dl}, sw}
	case hasDetail:
		in.Ledger = guardDetail{gl, dl}
	case hasStatus:
		in.Ledger = guardStatus{gl, sw}
	default:
		in.Ledger = gl
	}
	user := in.BeforeWrite
	in.BeforeWrite = func(ctx context.Context, what string) error {
		if user != nil {
			if err := user(ctx, what); err != nil {
				return err
			}
		}
		if what == FenceRollback {
			return g.release(ctx)
		}
		return nil
	}
	return in, pm
}
