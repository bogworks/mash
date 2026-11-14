package policyintake

import (
	"context"
	"errors"
	"fmt"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// AdminMarker is the MarkerStore over the admin connection: it reads the marker with its revision and writes the
// planned rotation with a compare-and-set on the revision the document was verified under.
type AdminMarker struct{ Admin *admin.Admin }

// Current reads the marker and its revision.
func (m AdminMarker) Current(ctx context.Context) (store.Marker, uint64, bool, error) {
	return m.Admin.AdoptionRev(ctx)
}

// SetNext records the pending next submitter.
func (m AdminMarker) SetNext(ctx context.Context, next string, expectedRev, ownRev uint64, held []string) (uint64, error) {
	_, rev, err := m.Admin.SetNextSubmitter(ctx, next, expectedRev, ownRev, held)
	return rev, moved(err)
}

// Promote makes the pending next submitter the submitter.
func (m AdminMarker) Promote(ctx context.Context, next string, expectedRev, ownRev uint64) (uint64, error) {
	_, rev, err := m.Admin.PromoteSubmitter(ctx, next, expectedRev, ownRev)
	return rev, moved(err)
}

// moved marks the errors no retry can cure with ErrMarkerMoved.
func moved(err error) error {
	for _, e := range []error{admin.ErrMarkerMoved, admin.ErrNotAdopted, admin.ErrNextMismatch, admin.ErrNextPending, admin.ErrNextIsCurrent,
		admin.ErrNextIsMashKey, admin.ErrSubmitterInvalid, admin.ErrSubmitterIsAccount} {
		if errors.Is(err, e) {
			return fmt.Errorf("%w: %w", ErrMarkerMoved, err)
		}
	}
	return err
}

// AdminLock is the AccountLock over the admin connection: the lock a recovery adopt takes.
type AdminLock struct{ Admin *admin.Admin }

// Lock takes the account lock, waiting as the admin connection is set to wait.
func (l AdminLock) Lock(ctx context.Context) (Release, func(context.Context) error, error) {
	lk, err := l.Admin.LockAccount(ctx)
	if err != nil {
		return nil, nil, err
	}
	return lk.Release, lk.Held, nil
}
