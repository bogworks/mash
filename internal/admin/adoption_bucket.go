package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/store"
)

// initAdoption creates the adoption bucket, or verifies an existing one. It never alters an existing bucket.
func (a *Admin) initAdoption(ctx context.Context) error {
	_, err := a.js.KeyValue(ctx, store.AdoptionBucket)
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		if _, err := store.CreateAdoptionBucket(ctx, a.js); err != nil {
			return fmt.Errorf("mash: bucket %s: %w", store.AdoptionBucket, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("mash: bucket %s: %w", store.AdoptionBucket, err)
	}
	return store.VerifyAdoptionBucket(ctx, a.js)
}

// Typed errors of ValidateNextSubmitter, which the policy document check (internal/policydoc) uses. None of them
// echoes a key.
var (
	// ErrSubmitterInvalid is returned for a submitter that is not an nkey public key.
	ErrSubmitterInvalid = errors.New("mash: adoption: the submitter is not an nkey public key")
	// ErrSubmitterIsAccount is returned for a submitter equal to the account's own public key.
	ErrSubmitterIsAccount = errors.New("mash: adoption: the submitter is the account's own key; it must be a key mash does not hold")
	// ErrNextIsCurrent is returned for a next submitter equal to the current one.
	ErrNextIsCurrent = errors.New("mash: adoption: the next submitter is the current submitter")
	// ErrNextIsMashKey is returned for a next submitter that is a key mash holds.
	ErrNextIsMashKey = errors.New("mash: adoption: the next submitter is a key mash holds")
)

// ValidateNextSubmitter reports why next may not become the next submitter of the account whose marker is m: it must be
// an nkey public key, not the account's own key, not the current submitter and not a key mash holds: the admin user
// recorded in the marker, the exempt users and held. It never echoes a key.
func ValidateNextSubmitter(account string, m store.Marker, next string, held []string) error {
	if _, err := nkeys.FromPublicKey(next); err != nil {
		return ErrSubmitterInvalid
	}
	if next == account {
		return ErrSubmitterIsAccount
	}
	if next == m.Submitter {
		return ErrNextIsCurrent
	}
	if next == m.By || slices.Contains(m.Exempt, next) || slices.Contains(held, next) {
		return ErrNextIsMashKey
	}
	return nil
}
