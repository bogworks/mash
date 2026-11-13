package policyapply

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bogworks/mash/internal/listset"
)

// ErrBadGeneration is returned by Apply when the request's Generation is not its BaseGeneration plus one, or its
// ListSet.Base is not its BaseGeneration. Nothing is pushed.
var ErrBadGeneration = errors.New("the generation is not the base generation plus one")

// ErrNoInForceListSet is returned by Apply when a generation is in force but the ledger holds no list set for it, so
// the previous Compiled cannot be built. That is a ledger written before list sets were stored; Apply never guesses.
var ErrNoInForceListSet = errors.New("the ledger holds no list set for the generation in force")

// ApplyRequest is one submission as the intake has decoded it.
type ApplyRequest struct {
	// ListSet is the decoded document.
	ListSet listset.ListSet
	// Received is the exact bytes of the document ListSet was decoded from (never empty).
	Received []byte
	// Generation is the generation the document states for itself; it must be BaseGeneration+1.
	Generation uint64
	// BaseGeneration is the generation the document states it is based on; it must equal ListSet.Base and the
	// generation in force.
	BaseGeneration uint64
}

// ApplyDeps is what Apply needs besides the request. Every field but Provisioner, AfterPush and Now is required.
type ApplyDeps struct {
	// Ledger holds the record in force. To apply on top of a generation it must be a DetailLedger, so the previous
	// Compiled and the JWT for a rollback can be read.
	Ledger Ledger
	// Cluster pushes and reads the account JWT on the servers.
	Cluster Cluster
	// Prober probes the servers for the gate.
	Prober Prober
	// Witnesses are the connections the gate observes through.
	Witnesses Witnesses
	// Signer signs the target and the rollback.
	Signer Signer
	// Account is the account's public key.
	Account string
	// Inventory is the number of senders' credentials expected (see RunInput.Inventory).
	Inventory int
	// Signers are the expected scoped signing keys of the target and, because the groups do not change between
	// generations unless the signer set does, also PreviousSigners. A caller whose signer set changed with this
	// generation must call Run itself.
	Signers map[string]string
	// ServerLimits are the servers' limits. Apply does not use them: the intake checks sizes with listset.SizeCheck
	// before it calls Apply, and Run refuses a JWT over policy.AccountJWTLimit.
	ServerLimits []listset.ServerLimits
	// Provisioner, if set, provisions new addresses inside the run (RunOrdered); if nil, Run is used.
	Provisioner Provisioner
	// AfterPush is RunInput.AfterPush.
	AfterPush func(ctx context.Context) (revoked map[string][]string, notConnected []string, err error)
	// Now is the clock for the Status; nil means time.Now.
	Now func() time.Time
}

// Apply is Run for the intake: it takes what the document states and reads the rest from the ledger.
//
// It first checks, before anything else is pushed, that req.Generation is req.BaseGeneration+1 and that
// req.ListSet.Base is req.BaseGeneration (ErrBadGeneration), and that req.BaseGeneration is the generation in force
// in the ledger (*listset.StaleBaseError, matching listset.ErrStaleBase). It then builds RunInput: Previous is the
// compiled list set of the generation in force, read from the ledger (DetailLedger.InForceListSet; none when nothing
// is in force; ErrNoInForceListSet when something is and the ledger holds no list set), and PreviousSigners is
// deps.Signers. The reads happen before Run takes the ledger's lock; if the generation in force changes in between,
// Run's own check under the lock refuses the stale base before anything is pushed. The rollback JWT is the one the
// ledger stored (see Run). With deps.Provisioner set it calls RunOrdered, and the returned Status is the one of that
// run. Apply does not set RunInput.RevokedKeys, PreviousRevoked or OldKeys: keys a narrowing revokes are reported by
// deps.AfterPush.
func Apply(ctx context.Context, req ApplyRequest, deps ApplyDeps) (Status, error) {
	if req.Generation != req.BaseGeneration+1 || req.ListSet.Base != req.BaseGeneration {
		return Status{}, fmt.Errorf("%w: generation %d, base %d, list set base %d", ErrBadGeneration, req.Generation, req.BaseGeneration, req.ListSet.Base)
	}
	cur, err := deps.Ledger.InForce()
	if err != nil {
		return Status{}, err
	}
	if cur.Generation != req.BaseGeneration {
		return Status{}, &listset.StaleBaseError{Stated: req.BaseGeneration, InForce: cur.Generation}
	}
	var prev *listset.Compiled
	if cur.Generation > 0 {
		dl, ok := deps.Ledger.(DetailLedger)
		if !ok {
			return Status{}, fmt.Errorf("%w: the ledger keeps none", ErrNoInForceListSet)
		}
		ls, ok, err := dl.InForceListSet()
		if err != nil {
			return Status{}, err
		}
		if !ok {
			return Status{}, fmt.Errorf("%w: generation %d", ErrNoInForceListSet, cur.Generation)
		}
		if prev, err = listset.Compile(ls); err != nil {
			return Status{}, fmt.Errorf("compile the list set in force (generation %d): %w", cur.Generation, err)
		}
	}
	in := RunInput{
		Cluster: deps.Cluster, Prober: deps.Prober, Witnesses: deps.Witnesses, Ledger: deps.Ledger,
		Account: deps.Account, Inventory: deps.Inventory,
		ListSet: req.ListSet, Received: req.Received,
		Previous: prev, Signer: deps.Signer,
		Signers: deps.Signers, PreviousSigners: deps.Signers,
		AfterPush: deps.AfterPush, Now: deps.Now,
	}
	if deps.Provisioner != nil {
		os, err := RunOrdered(ctx, in, deps.Provisioner)
		return os.Status, err
	}
	return Run(ctx, in)
}
