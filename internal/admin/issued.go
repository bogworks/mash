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

// issuedKV returns the issue record's bucket. A deployment from before the record has no bucket until `mash admin
// init` is run again; the minting authority creates it here, over the same admin connection that issues the user, so
// issuing never has to be refused for want of it. The bucket is made hardened (store.CreateIssuedBucket), and an
// existing one that is not as mash makes it is an error: a record that can be purged is not a record.
func (a *Admin) issuedKV(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := a.js.KeyValue(ctx, store.IssuedBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		if kv, err = store.CreateIssuedBucket(ctx, a.js); err != nil {
			// Lost a race with another admin creating it: read it again.
			if kv2, err2 := a.js.KeyValue(ctx, store.IssuedBucket); err2 == nil {
				kv, err = kv2, nil
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("mash: issue record bucket (run `mash admin init`): %w", err)
	}
	if err := store.VerifyIssuedBucket(ctx, a.js); err != nil {
		return nil, err
	}
	return kv, nil
}

// ErrNotRecorded is returned by MarkRevoked for a key the issue record does not hold: mash never issued it (or the
// record's bucket does not exist). A caller that revokes a key mash may not have issued treats it as no finding.
var ErrNotRecorded = errors.New("mash: issue record: key not recorded")

// issuedCASTries is how many times a read-modify-write of one record retries after losing a race.
const issuedCASTries = 8

// RecordIssued appends r to the issue record: one write under the user's public key. A second issue of the same key
// adds a revision (the bucket keeps store.IssuedHistory of them); nothing is ever removed. It fills IssuedAt (now,
// UTC) when empty, Owner from the Admin's owner when empty and Purpose (store.PurposeIssue) when empty; an unknown
// purpose is refused. The write is a compare-and-set: it keeps a revoked_at that a concurrent or earlier MarkRevoked
// set. Callers record before they hand the creds out, so a user that can connect is always in the record; an error
// means the user must not be issued.
func (a *Admin) RecordIssued(ctx context.Context, r store.Issued) error {
	if !nkeys.IsValidPublicUserKey(r.PublicKey) {
		return fmt.Errorf("mash: issue record: %q is not a user public key", r.PublicKey)
	}
	if r.Kind == "" {
		return errors.New("mash: issue record: no kind")
	}
	if r.Purpose == "" {
		r.Purpose = store.PurposeIssue
	}
	if !store.ValidPurpose(r.Purpose) {
		return fmt.Errorf("mash: issue record: unknown purpose %q (issue, rotation or probe)", r.Purpose)
	}
	if r.Owner == "" {
		r.Owner = a.owner
	}
	if r.IssuedAt == "" {
		r.IssuedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if r.Tags == nil {
		r.Tags = []string{}
	}
	r.Tags = slices.Clone(r.Tags)
	kv, err := a.issuedKV(ctx)
	if err != nil {
		return err
	}
	return casIssued(ctx, kv, r.PublicKey, func(cur *store.Issued) (store.Issued, bool, error) {
		next := r
		if cur != nil && cur.RevokedAt != "" {
			next.RevokedAt = cur.RevokedAt
		}
		return next, true, nil
	})
}

// MarkRevoked sets revoked_at (RFC 3339, UTC) on the record of publicKey if it is unset: it reads the record and
// writes it back with a revision check, retrying when it loses a race, so a concurrent RecordIssued loses no field.
// It never clears or moves a revoked_at already set, so it is idempotent. A key the record does not hold returns
// ErrNotRecorded. It is called after a revocation succeeded; the revocation does not depend on it.
func (a *Admin) MarkRevoked(ctx context.Context, publicKey string, at time.Time) error {
	if !nkeys.IsValidPublicUserKey(publicKey) {
		return fmt.Errorf("mash: issue record: %q is not a user public key", publicKey)
	}
	kv, err := a.js.KeyValue(ctx, store.IssuedBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return ErrNotRecorded
	}
	if err != nil {
		return fmt.Errorf("mash: issue record bucket: %w", err)
	}
	stamp := at.UTC().Format(time.RFC3339)
	return casIssued(ctx, kv, publicKey, func(cur *store.Issued) (store.Issued, bool, error) {
		if cur == nil {
			return store.Issued{}, false, ErrNotRecorded
		}
		if cur.RevokedAt != "" {
			return *cur, false, nil
		}
		next := *cur
		next.RevokedAt = stamp
		return next, true, nil
	})
}

// casIssued is a read-modify-write of one record: change gets the current entry (nil when there is none) and returns
// the entry to write and whether to write it. The write is Create or Update at the revision read, retried when
// another writer got there first.
func casIssued(ctx context.Context, kv jetstream.KeyValue, key string, change func(cur *store.Issued) (store.Issued, bool, error)) error {
	for range issuedCASTries {
		var cur *store.Issued
		var rev uint64
		e, err := kv.Get(ctx, key)
		switch {
		case errors.Is(err, jetstream.ErrKeyNotFound):
		case err != nil:
			return fmt.Errorf("mash: issue record %s: %w", key, err)
		default:
			var c store.Issued
			if err := json.Unmarshal(e.Value(), &c); err != nil {
				return fmt.Errorf("mash: issue record %s: unreadable entry", key)
			}
			cur, rev = &c, e.Revision()
		}
		next, write, err := change(cur)
		if err != nil {
			return err
		}
		if !write {
			return nil
		}
		b, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if cur == nil {
			_, err = kv.Create(ctx, key, b)
		} else {
			_, err = kv.Update(ctx, key, b, rev)
		}
		if err == nil {
			return nil
		}
		if errors.Is(err, jetstream.ErrKeyExists) || isWrongLastSeq(err) {
			continue
		}
		return fmt.Errorf("mash: issue record: %w", err)
	}
	return fmt.Errorf("mash: issue record %s: changed by another writer too often; try again", key)
}

// isWrongLastSeq reports whether err is the stream's refusal of a write made at a stale revision.
func isWrongLastSeq(err error) bool {
	var apiErr *jetstream.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence
}

// Issued reads the issue record: the latest entry of each key, sorted by key; a record with no purpose reads as store.PurposeIssue. present is false when the bucket does
// not exist (nothing was ever recorded). An entry that cannot be read or decoded is an error: a check must not pass
// over a record it cannot read.
func (a *Admin) Issued(ctx context.Context) (recs []store.Issued, present bool, err error) {
	kv, err := a.js.KeyValue(ctx, store.IssuedBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("mash: issue record bucket: %w", err)
	}
	keys, err := kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return []store.Issued{}, true, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("mash: issue record bucket: %w", err)
	}
	slices.Sort(keys)
	recs = make([]store.Issued, 0, len(keys))
	for _, k := range keys {
		e, err := kv.Get(ctx, k)
		if err != nil {
			return nil, true, fmt.Errorf("mash: issue record %s: %w", k, err)
		}
		var r store.Issued
		if err := json.Unmarshal(e.Value(), &r); err != nil {
			return nil, true, fmt.Errorf("mash: issue record %s: unreadable entry", k)
		}
		if r.PublicKey != k {
			return nil, true, fmt.Errorf("mash: issue record %s: the entry names another key", k)
		}
		r.Purpose = r.EffectivePurpose() // a record from before the field is an ordinary issue
		recs = append(recs, r)
	}
	return recs, true, nil
}

// initIssued creates the issue record's bucket, or verifies an existing one. It never alters an existing bucket.
func (a *Admin) initIssued(ctx context.Context) error {
	_, err := a.js.KeyValue(ctx, store.IssuedBucket)
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		if _, err := store.CreateIssuedBucket(ctx, a.js); err != nil {
			return fmt.Errorf("mash: bucket %s: %w", store.IssuedBucket, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("mash: bucket %s: %w", store.IssuedBucket, err)
	}
	return store.VerifyIssuedBucket(ctx, a.js)
}
