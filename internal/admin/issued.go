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

// ErrKeyRevoked and ErrKeyBoundElsewhere are the refusals of RecordIssuedChecked.
var (
	ErrKeyRevoked        = store.ErrKeyRevoked
	ErrKeyBoundElsewhere = store.ErrKeyBoundElsewhere
)

// issuedCASTries is how many times a read-modify-write of one record retries after losing a race.
const issuedCASTries = 8

// RecordIssued appends r to the issue record: one write under the user's public key. A second issue of the same key
// adds a revision (the bucket keeps store.IssuedHistory of them); nothing is ever removed. It fills IssuedAt (now,
// UTC) when empty, Owner from the Admin's owner when empty and Purpose (store.PurposeIssue) when empty; an unknown
// purpose is refused. The write is a compare-and-set: it keeps a revoked_at that a concurrent or earlier MarkRevoked
// set. Callers record before they hand the creds out, so a user that can connect is always in the record; an error
// means the user must not be issued.
func (a *Admin) RecordIssued(ctx context.Context, r store.Issued) error {
	return a.recordIssued(ctx, r, false)
}

// RecordIssuedChecked is RecordIssued with the issue service's conditions made part of every compare-and-set attempt:
// when the record the write would follow has revoked_at set it fails with store.ErrKeyRevoked, and when that record is
// bound to another address it fails with store.ErrKeyBoundElsewhere; in both cases nothing is written. The conditions
// are tested against the revision the write is made at, so a MarkRevoked or a second issuer's write that lands between
// a caller's earlier lookup and this write is seen, not overwritten. A caller hands a credential out only after this
// returns nil.
func (a *Admin) RecordIssuedChecked(ctx context.Context, r store.Issued) error {
	return a.recordIssued(ctx, r, true)
}

func (a *Admin) recordIssued(ctx context.Context, r store.Issued, checked bool) error {
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
		if checked && cur != nil {
			if cur.RevokedAt != "" {
				return store.Issued{}, false, store.ErrKeyRevoked
			}
			if cur.Address != r.Address {
				return store.Issued{}, false, store.ErrKeyBoundElsewhere
			}
		}
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

// IssuedByKey returns the latest issue record of publicKey. found is false when the key is not recorded or the bucket
// does not exist (nothing was ever recorded: jetstream.ErrBucketNotFound and ErrKeyNotFound, and only those, mean
// absent); any other failure (network, permission, cancelled context, JetStream) and an entry that cannot be read or
// decoded is an error.
func (a *Admin) IssuedByKey(ctx context.Context, publicKey string) (rec store.Issued, found bool, err error) {
	kv, err := a.js.KeyValue(ctx, store.IssuedBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return store.Issued{}, false, nil
	}
	if err != nil {
		return store.Issued{}, false, fmt.Errorf("mash: issue record bucket: %w", err)
	}
	e, err := kv.Get(ctx, publicKey)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return store.Issued{}, false, nil
	}
	if err != nil {
		return store.Issued{}, false, fmt.Errorf("mash: issue record %s: %w", publicKey, err)
	}
	if err := json.Unmarshal(e.Value(), &rec); err != nil {
		return store.Issued{}, false, fmt.Errorf("mash: issue record %s: unreadable entry", publicKey)
	}
	if rec.PublicKey != publicKey {
		return store.Issued{}, false, fmt.Errorf("mash: issue record %s: the entry names another key", publicKey)
	}
	rec.Purpose = rec.EffectivePurpose()
	return rec, true, nil
}

// IssuedTimes is every issue time the record keeps of one key.
type IssuedTimes struct {
	// Latest is the newest revision's issued_at; Max is the greatest issued_at over all retained revisions. They differ
	// when a re-record of the same key (a reissue that keeps the key) carries an earlier time than a revision before it,
	// as after a clock rollback.
	Latest, Max time.Time
	// Revisions is how many revisions the bucket still holds of the key.
	Revisions int
	// Truncated is true when the history is at the bucket's cap: the oldest revisions may have been dropped, so Max
	// may not cover every credential ever recorded for the key.
	Truncated bool
}

// MalformedIssuedAtError is returned by IssuedHistoryByKey for a revision whose issued_at is missing or not an RFC 3339
// time.
type MalformedIssuedAtError struct {
	Key, Value string
	Revision   uint64
}

func (e *MalformedIssuedAtError) Error() string {
	return fmt.Sprintf("mash: issue record %s: revision %d has a missing or malformed issued_at (%q)", e.Key, e.Revision, e.Value)
}

// issuedStatusFault is a test seam: a non-nil error it returns replaces the bucket status read in IssuedHistoryByKey.
var issuedStatusFault func() error

// IssuedHistoryByKey reads every retained revision of publicKey's issue record (up to the bucket's history cap,
// store.IssuedHistory) and returns the issue times they hold. found is false when the key is not recorded or the bucket
// does not exist (jetstream.ErrBucketNotFound and ErrKeyNotFound, and only those, mean absent). Any other failure, a
// revision that cannot be decoded or names another key, a revision that is not a plain write, and a revision whose
// issued_at is missing or not a time (*MalformedIssuedAtError) is an error: a check must not pass over a revision it
// cannot read.
func (a *Admin) IssuedHistoryByKey(ctx context.Context, publicKey string) (t IssuedTimes, found bool, err error) {
	kv, err := a.js.KeyValue(ctx, store.IssuedBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return IssuedTimes{}, false, nil
	}
	if err != nil {
		return IssuedTimes{}, false, fmt.Errorf("mash: issue record bucket: %w", err)
	}
	hist, err := kv.History(ctx, publicKey)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return IssuedTimes{}, false, nil
	}
	if err != nil {
		return IssuedTimes{}, false, fmt.Errorf("mash: issue record %s: %w", publicKey, err)
	}
	if len(hist) == 0 {
		return IssuedTimes{}, false, nil
	}
	// The bucket's own history cap decides whether the oldest revisions may have been dropped, so a status that cannot
	// be read is an error: assuming the current cap could pass a key whose older, future-dated record was evicted.
	st, err := kv.Status(ctx)
	if err == nil && issuedStatusFault != nil {
		err = issuedStatusFault()
	}
	if err != nil {
		return IssuedTimes{}, false, fmt.Errorf("mash: issue record bucket status: %w", err)
	}
	limit := store.IssuedHistory
	if st.History() > 0 && int(st.History()) < limit {
		limit = int(st.History()) // a bucket from before `mash admin init` raised it keeps fewer
	}
	for i, e := range hist {
		if e.Operation() != jetstream.KeyValuePut {
			return IssuedTimes{}, false, fmt.Errorf("mash: issue record %s: revision %d is not a plain write", publicKey, e.Revision())
		}
		var rec store.Issued
		if err := json.Unmarshal(e.Value(), &rec); err != nil {
			return IssuedTimes{}, false, fmt.Errorf("mash: issue record %s: unreadable entry", publicKey)
		}
		if rec.PublicKey != publicKey {
			return IssuedTimes{}, false, fmt.Errorf("mash: issue record %s: the entry names another key", publicKey)
		}
		at, perr := time.Parse(time.RFC3339, rec.IssuedAt)
		if perr != nil {
			return IssuedTimes{}, false, &MalformedIssuedAtError{Key: publicKey, Value: rec.IssuedAt, Revision: e.Revision()}
		}
		at = at.UTC()
		if at.After(t.Max) {
			t.Max = at
		}
		if i == len(hist)-1 {
			t.Latest = at
		}
	}
	t.Revisions = len(hist)
	t.Truncated = len(hist) >= limit
	return t, true, nil
}

// initIssued creates the issue record's bucket, or verifies an existing one. An existing bucket is altered in one way
// only: a per-key history below store.IssuedHistory (the first version kept 8) is raised to it, a stream update that
// loses no record. Everything else about it is only verified.
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
	if err := store.VerifyIssuedBucket(ctx, a.js); err != nil {
		return err
	}
	s, err := a.js.Stream(ctx, store.IssuedStreamName)
	if err != nil {
		return fmt.Errorf("mash: bucket %s: %w", store.IssuedBucket, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return fmt.Errorf("mash: bucket %s: %w", store.IssuedBucket, err)
	}
	if info.Config.MaxMsgsPerSubject >= store.IssuedHistory {
		return nil
	}
	sc := info.Config
	sc.MaxMsgsPerSubject = store.IssuedHistory
	if _, err := a.js.UpdateStream(ctx, sc); err != nil {
		return fmt.Errorf("mash: bucket %s: raising the history to %d: %w", store.IssuedBucket, store.IssuedHistory, err)
	}
	return nil
}
