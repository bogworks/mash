package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/store"
)

// Typed errors of Adopt. None of them echoes the value it refused.
var (
	// ErrSubmitterInvalid is returned for a submitter that is not an nkey public key.
	ErrSubmitterInvalid = errors.New("mash: adoption: the submitter is not an nkey public key")
	// ErrSubmitterIsAccount is returned for a submitter equal to the account's own public key.
	ErrSubmitterIsAccount = errors.New("mash: adoption: the submitter is the account's own key; it must be a key mash does not hold")
	// ErrExemptInvalid is returned for an exempt key that is not an nkey user public key.
	ErrExemptInvalid = errors.New("mash: adoption: an exempt key is not an nkey user public key")
	// ErrAdoptionChanged is returned when another writer changed the marker too often for the write to land.
	ErrAdoptionChanged = errors.New("mash: adoption: the marker was changed by another writer; run the command again")
)

// The values of Adopt's action result when it writes nothing.
const (
	// AdoptUnchanged is Adopt's action when the marker already names the submitter and the same exempt set: nothing
	// is written.
	AdoptUnchanged = "unchanged"
	// AdoptExemptUpdated is Adopt's action when the marker names the submitter and only the exempt set changed: a new
	// revision is written (marker action adopt).
	AdoptExemptUpdated = "exempt-updated"
)

// adoptionCASTries is how many times Adopt retries after losing a race for the marker.
const adoptionCASTries = 8

// validateSubmitterKey is the check a submitter key passes at adoption and at a planned rotation: an nkey public key
// that is not the account's own.
func validateSubmitterKey(account, submitter string) error {
	if _, err := nkeys.FromPublicKey(submitter); err != nil {
		return ErrSubmitterInvalid
	}
	if submitter == account {
		return ErrSubmitterIsAccount
	}
	return nil
}

// adoptionKV returns the adoption bucket, creating it hardened when it does not exist (the minting authority's admin
// connection is the only one that can). An existing bucket that is not as mash makes it is an error.
func (a *Admin) adoptionKV(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := a.js.KeyValue(ctx, store.AdoptionBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		if kv, err = store.CreateAdoptionBucket(ctx, a.js); err != nil {
			// Lost a race with another admin creating it: read it again.
			if kv2, err2 := a.js.KeyValue(ctx, store.AdoptionBucket); err2 == nil {
				kv, err = kv2, nil
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("mash: adoption bucket (run `mash admin init`): %w", err)
	}
	if err := store.VerifyAdoptionBucket(ctx, a.js); err != nil {
		return nil, err
	}
	return kv, nil
}

// readMarker reads the marker's latest revision. found is false when the key does not exist. A value that cannot be
// read, or that names a schema or an empty submitter this version does not know, is an error: a reader must not take
// a marker it cannot understand for no marker.
func readMarker(ctx context.Context, kv jetstream.KeyValue) (m store.Marker, rev uint64, found bool, err error) {
	e, err := kv.Get(ctx, store.AdoptionKey)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return store.Marker{}, 0, false, nil
	}
	if err != nil {
		return store.Marker{}, 0, false, fmt.Errorf("mash: adoption marker: %w", err)
	}
	if err := json.Unmarshal(e.Value(), &m); err != nil {
		return store.Marker{}, 0, false, errors.New("mash: adoption marker: unreadable value")
	}
	if m.V != store.AdoptionSchema {
		return store.Marker{}, 0, false, fmt.Errorf("mash: adoption marker: unknown schema %d (this mash reads %d)", m.V, store.AdoptionSchema)
	}
	if _, err := nkeys.FromPublicKey(m.Submitter); err != nil {
		return store.Marker{}, 0, false, errors.New("mash: adoption marker: the submitter is not an nkey public key")
	}
	return m, e.Revision(), true, nil
}

// Adoption reads the adoption marker. found is false when the account is not adopted: the bucket does not exist, or
// it holds no marker. Any other outcome is an error, never "not adopted": a bucket that is not as mash makes it, a
// read that fails and a value that cannot be understood all return an error, so a caller refuses (fail closed). It
// never creates or changes anything, and it reads the latest revision each time, so there is nothing to cache.
func (a *Admin) Adoption(ctx context.Context) (m store.Marker, found bool, err error) {
	kv, err := a.existingAdoptionKV(ctx)
	if err != nil || kv == nil {
		return store.Marker{}, false, err
	}
	m, _, found, err = readMarker(ctx, kv)
	return m, found, err
}

// existingAdoptionKV returns the adoption bucket, or nil (and no error) when it does not exist. A bucket that is not
// as mash makes it is an error. It never creates or changes anything.
func (a *Admin) existingAdoptionKV(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := a.js.KeyValue(ctx, store.AdoptionBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mash: adoption bucket: %w", err)
	}
	if err := store.VerifyAdoptionBucket(ctx, a.js); err != nil {
		return nil, err
	}
	return kv, nil
}

// normalizeExempt validates every key as an nkey user public key and returns them sorted and de-duplicated (nil for
// none). It never echoes a key.
func normalizeExempt(keys []string) ([]string, error) {
	var out []string
	for _, k := range keys {
		if !nkeys.IsValidPublicUserKey(k) {
			return nil, ErrExemptInvalid
		}
		out = append(out, k)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// Adopt registers submitter as the one key that may hand in list sets for account, over the admin connection. by is
// the public key of the admin user running the command. The first call creates the marker (action adopt). A call with
// a different submitter is the recovery rotation: it replaces the marker at the revision it read (action recover),
// clears any pending next_submitter, and the old submitter is refused from then on. A call with the submitter
// already registered and the same exempt set writes nothing and returns AdoptUnchanged; with another exempt set it
// writes a new revision (action adopt) and returns AdoptExemptUpdated. A recovery rotation also refreshes the exempt
// set. exempt is the public keys of the users the later reconcile must never touch (see store.Marker.Exempt). The read and the write are made under the account lock (see AccountLock), and the write is a compare-and-set on the marker's last
// revision, retried when another writer got there first. The bucket is created, hardened, when it does not exist.
func (a *Admin) Adopt(ctx context.Context, account, submitter, by string, exempt []string) (store.Marker, string, error) {
	if _, err := nkeys.FromPublicKey(account); err != nil {
		return store.Marker{}, "", errors.New("mash: adoption: the account key is not an nkey public key")
	}
	if err := validateSubmitterKey(account, submitter); err != nil {
		return store.Marker{}, "", err
	}
	if _, err := nkeys.FromPublicKey(by); err != nil {
		return store.Marker{}, "", errors.New("mash: adoption: the admin user key is not an nkey public key")
	}
	exempt, err := normalizeExempt(exempt)
	if err != nil {
		return store.Marker{}, "", err
	}
	kv, err := a.adoptionKV(ctx)
	if err != nil {
		return store.Marker{}, "", err
	}
	// The account lock, held through the read and the write: Adopt waits for a guarded command that is running (it
	// started first and finishes first), and one that starts after the write reads the marker and is refused. When
	// the lock stays held for the whole wait, nothing is written (ErrAccountBusy).
	lk, err := a.LockAccount(ctx)
	if err != nil {
		return store.Marker{}, "", err
	}
	defer lk.Release()
	for range adoptionCASTries {
		cur, rev, found, err := readMarker(ctx, kv)
		if err != nil {
			return store.Marker{}, "", err
		}
		sameSubmitter := found && cur.Submitter == submitter
		if sameSubmitter && slices.Equal(cur.Exempt, exempt) {
			return cur, AdoptUnchanged, nil
		}
		next := store.Marker{V: store.AdoptionSchema, Account: account, Submitter: submitter,
			AdoptedAt: time.Now().UTC().Format(time.RFC3339), By: by, Action: store.ActionAdopt, Exempt: exempt}
		result := store.ActionAdopt
		switch {
		case sameSubmitter:
			result = AdoptExemptUpdated
			next.NextSubmitter = cur.NextSubmitter // only the exempt set changes: a pending rotation stays
		case found:
			next.Action = store.ActionRecover
			result = store.ActionRecover
		}
		b, err := json.Marshal(next)
		if err != nil {
			return store.Marker{}, "", err
		}
		if err := lk.Held(ctx); err != nil { // lost the lock: write nothing
			return store.Marker{}, "", err
		}
		if found {
			_, err = kv.Update(ctx, store.AdoptionKey, b, rev)
		} else {
			_, err = kv.Create(ctx, store.AdoptionKey, b)
		}
		if err == nil {
			return next, result, nil
		}
		if errors.Is(err, jetstream.ErrKeyExists) || isWrongLastSeq(err) {
			continue
		}
		return store.Marker{}, "", fmt.Errorf("mash: adoption marker: %w", err)
	}
	return store.Marker{}, "", ErrAdoptionChanged
}

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
