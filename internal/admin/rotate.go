package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

// Typed errors of the planned rotation. None of them echoes a key.
var (
	// ErrNotAdopted is returned when a rotation is asked of an account that has no adoption marker.
	ErrNotAdopted = errors.New("mash: adoption: the account is not adopted")
	// ErrNextIsCurrent is returned for a next submitter equal to the current one.
	ErrNextIsCurrent = errors.New("mash: adoption: the next submitter is the current submitter")
	// ErrNextIsMashKey is returned for a next submitter that is a key mash holds: the admin user that adopted the
	// account or one of the exempt users.
	ErrNextIsMashKey = errors.New("mash: adoption: the next submitter is a key mash holds")
	// ErrNextPending is returned when a next submitter is already pending and a different one is asked for.
	ErrNextPending = errors.New("mash: adoption: a different next submitter is already pending")
	// ErrNextMismatch is returned by a promotion when the marker's pending next submitter is not the key asked for
	// (it was changed by a recovery adopt, or never set).
	ErrNextMismatch = errors.New("mash: adoption: the marker's pending next submitter is not the key to promote")
	// ErrMarkerMoved is returned by a rotation write when the marker's revision is not the one the caller verified
	// its document under (a recovery adopt or another writer replaced the marker in between). Nothing is written.
	ErrMarkerMoved = errors.New("mash: adoption: the marker changed since it was read")
)

// ValidateNextSubmitter reports why next may not become the next submitter of the account whose marker is m: it must be
// an nkey public key, not the account's own key, not the current submitter and not a key mash holds (the admin user
// recorded in the marker and the exempt users). It never echoes a key.
func ValidateNextSubmitter(account string, m store.Marker, next string) error {
	if err := validateSubmitterKey(account, next); err != nil {
		return err
	}
	if next == m.Submitter {
		return ErrNextIsCurrent
	}
	if next == m.By || slices.Contains(m.Exempt, next) {
		return ErrNextIsMashKey
	}
	return nil
}

// rotateMarker rewrites the marker through change, which returns the new marker or the marker unchanged (done true:
// nothing is written). The write is bound to the marker revision the caller read and verified under: expectedRev. If
// the stored marker is at another revision the call fails with ErrMarkerMoved and writes nothing, except that a
// marker at ownRev (the revision of the caller's own earlier write of the same change, 0 for none) is accepted, so
// that a retry after a lost answer finds its end state and writes nothing. It returns the marker and its revision
// (the new one after a write).
func (a *Admin) rotateMarker(ctx context.Context, expectedRev, ownRev uint64, change func(cur store.Marker) (next store.Marker, done bool, err error)) (store.Marker, uint64, error) {
	kv, err := a.existingAdoptionKV(ctx)
	if err != nil {
		return store.Marker{}, 0, err
	}
	if kv == nil {
		return store.Marker{}, 0, ErrNotAdopted
	}
	cur, rev, found, err := readMarker(ctx, kv)
	if err != nil {
		return store.Marker{}, 0, err
	}
	if !found {
		return store.Marker{}, 0, ErrNotAdopted
	}
	if rev != expectedRev && (ownRev == 0 || rev != ownRev) {
		return store.Marker{}, 0, ErrMarkerMoved
	}
	next, done, err := change(cur)
	if err != nil {
		return store.Marker{}, 0, err
	}
	if done {
		return next, rev, nil
	}
	next.AdoptedAt = time.Now().UTC().Format(time.RFC3339)
	next.Action = store.ActionRotate
	b, err := json.Marshal(next)
	if err != nil {
		return store.Marker{}, 0, err
	}
	newRev, err := kv.Update(ctx, store.AdoptionKey, b, rev)
	if err == nil {
		return next, newRev, nil
	}
	if errors.Is(err, jetstream.ErrKeyExists) || isWrongLastSeq(err) {
		return store.Marker{}, 0, ErrMarkerMoved
	}
	return store.Marker{}, 0, fmt.Errorf("mash: adoption marker: %w", err)
}

// SetNextSubmitter records next as the pending next submitter (action rotate): the planned rotation's first half. The
// current submitter stays in force until PromoteSubmitter, but is the only key refused from then on by a reader that
// expects the next one. The write is a compare-and-set on expectedRev, the marker revision the caller verified its
// document under: a marker at any other revision is ErrMarkerMoved and nothing is written (a recovery adopt decides
// then). Setting the key that is already pending writes nothing, but only when the marker is at expectedRev or at
// ownRev, the revision of the caller's own earlier write (0 for none). It returns the marker and its revision. Every
// revision stays in the bucket's history as the audit trail. The marker's other fields, the exempt set among them,
// are kept.
func (a *Admin) SetNextSubmitter(ctx context.Context, next string, expectedRev, ownRev uint64) (store.Marker, uint64, error) {
	return a.rotateMarker(ctx, expectedRev, ownRev, func(cur store.Marker) (store.Marker, bool, error) {
		if err := ValidateNextSubmitter(cur.Account, cur, next); err != nil {
			return store.Marker{}, false, err
		}
		if cur.NextSubmitter == next {
			return cur, true, nil
		}
		if cur.NextSubmitter != "" {
			return store.Marker{}, false, ErrNextPending
		}
		out := cur
		out.NextSubmitter = next
		return out, false, nil
	})
}

// PromoteSubmitter makes next, which must be the marker's pending next submitter, the submitter and clears the
// pending key (action rotate): the planned rotation's second half. It is bound to expectedRev and ownRev as
// SetNextSubmitter is. A marker that already names next as the submitter with none pending writes nothing, so a retry
// after a lost answer is safe. A marker whose pending key is anything else (a recovery adopt replaced it) is
// ErrNextMismatch and is not changed.
func (a *Admin) PromoteSubmitter(ctx context.Context, next string, expectedRev, ownRev uint64) (store.Marker, uint64, error) {
	return a.rotateMarker(ctx, expectedRev, ownRev, func(cur store.Marker) (store.Marker, bool, error) {
		if cur.NextSubmitter == "" && cur.Submitter == next {
			return cur, true, nil
		}
		if cur.NextSubmitter != next || next == "" {
			return store.Marker{}, false, ErrNextMismatch
		}
		out := cur
		out.Submitter = next
		out.NextSubmitter = ""
		return out, false, nil
	})
}
