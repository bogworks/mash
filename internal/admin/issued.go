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

// RecordIssued appends r to the issue record: one write under the user's public key. A second issue of the same key
// adds a revision (the bucket keeps store.IssuedHistory of them); nothing is ever removed. It fills IssuedAt (now,
// UTC) when empty and Owner from the Admin's owner when empty. Callers record before they hand the creds out, so a
// user that can connect is always in the record; an error means the user must not be issued.
func (a *Admin) RecordIssued(ctx context.Context, r store.Issued) error {
	if !nkeys.IsValidPublicUserKey(r.PublicKey) {
		return fmt.Errorf("mash: issue record: %q is not a user public key", r.PublicKey)
	}
	if r.Kind == "" {
		return errors.New("mash: issue record: no kind")
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
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	kv, err := a.issuedKV(ctx)
	if err != nil {
		return err
	}
	if _, err := kv.Put(ctx, r.PublicKey, b); err != nil {
		return fmt.Errorf("mash: issue record: %w", err)
	}
	return nil
}

// Issued reads the issue record: the latest entry of each key, sorted by key. present is false when the bucket does
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
