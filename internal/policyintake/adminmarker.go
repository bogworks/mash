package policyintake

import (
	"context"
	"errors"
	"fmt"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// AdminMarker is the MarkerStore over the admin connection: it reads the marker and writes the planned rotation with
// a compare-and-set on the marker's last revision.
type AdminMarker struct{ Admin *admin.Admin }

// Current reads the marker.
func (m AdminMarker) Current(ctx context.Context) (store.Marker, bool, error) {
	return m.Admin.Adoption(ctx)
}

// SetNext records the pending next submitter.
func (m AdminMarker) SetNext(ctx context.Context, next string) error {
	_, err := m.Admin.SetNextSubmitter(ctx, next)
	return moved(err)
}

// Promote makes the pending next submitter the submitter.
func (m AdminMarker) Promote(ctx context.Context, next string) error {
	_, err := m.Admin.PromoteSubmitter(ctx, next)
	return moved(err)
}

// moved marks the errors no retry can cure with ErrMarkerMoved.
func moved(err error) error {
	for _, e := range []error{admin.ErrNotAdopted, admin.ErrNextMismatch, admin.ErrNextPending, admin.ErrNextIsCurrent,
		admin.ErrNextIsMashKey, admin.ErrSubmitterInvalid, admin.ErrSubmitterIsAccount} {
		if errors.Is(err, e) {
			return fmt.Errorf("%w: %w", ErrMarkerMoved, err)
		}
	}
	return err
}
