package policyapply

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// IssuedAdmin is the part of *admin.Admin that AdminIssueRecord uses: the issue record's operations. A fake
// stands in for it in unit tests.
type IssuedAdmin interface {
	// RecordIssuedChecked writes r with the conditions tested inside the write's own compare-and-set: it fails with
	// store.ErrKeyRevoked for a revoked key, store.ErrKeyBoundElsewhere for a key of another address and
	// store.ErrKeyPurposeChanged for a revision that moves the key between the sender and probe classes.
	RecordIssuedChecked(ctx context.Context, r store.Issued) error
	MarkRevoked(ctx context.Context, publicKey string, at time.Time) error
	Issued(ctx context.Context) (recs []store.Issued, present bool, err error)
	// IssuedHistoryByKey reads every retained revision of one key's record (ADR 0115): an *admin.IncompleteHistoryError
	// when the retained history cannot be shown complete.
	IssuedHistoryByKey(ctx context.Context, publicKey string) (t admin.IssuedTimes, found bool, err error)
}

var _ IssuedAdmin = (*admin.Admin)(nil)

// AdminIssueRecord is the IssueRecord of the live path: it keeps the entries in mash's issue record (the mash-issued
// bucket) through the admin connection that issues users.
//
// Mapping. An entry's Address, PublicKey, Tags, Generation and Purpose are stored as they are, its Kind is
// store.IssuedEndpoint and its Owner is the owner part of the address (the middle part of a three-part address, else
// empty, which the admin fills with its own owner). IssuedAt is stored as RFC 3339 in UTC. Expires is NOT stored:
// the record has no expiry, so a probe's expiry lives in its JWT only and entries read back with a zero Expires.
//
// Add refuses a revoked key (ErrRevokedKey) and a key recorded for another address, or a revision that moves an active key between the sender and probe classes (ErrDuplicateKey). A reissue of an
// active key for the same address adds a revision of its record (ADR 0101). The refusals are made inside the write's
// own compare-and-set (admin.RecordIssuedChecked), at the revision the write is made at, so a revocation or another
// writer that lands after an earlier read is seen and nothing is overwritten.
type AdminIssueRecord struct {
	admin IssuedAdmin
}

// NewAdminIssueRecord returns an AdminIssueRecord over a (an *admin.Admin or a fake).
func NewAdminIssueRecord(a IssuedAdmin) *AdminIssueRecord { return &AdminIssueRecord{admin: a} }

var _ IssueRecord = (*AdminIssueRecord)(nil)

// Add implements IssueRecord.
func (r *AdminIssueRecord) Add(ctx context.Context, e IssueEntry) error {
	if e.Generation > math.MaxInt {
		return fmt.Errorf("issue record: generation %d does not fit the record", e.Generation)
	}
	s := store.Issued{
		PublicKey:  e.PublicKey,
		Address:    e.Address,
		Owner:      ownerOf(e.Address),
		Kind:       store.IssuedEndpoint,
		Tags:       append([]string{}, e.Tags...),
		Generation: int(e.Generation),
		Purpose:    string(e.Purpose),
	}
	if !e.IssuedAt.IsZero() {
		s.IssuedAt = e.IssuedAt.UTC().Format(time.RFC3339)
	}
	err := r.admin.RecordIssuedChecked(ctx, s)
	switch {
	case errors.Is(err, store.ErrKeyRevoked):
		return ErrRevokedKey
	case errors.Is(err, store.ErrKeyBoundElsewhere), errors.Is(err, store.ErrKeyPurposeChanged):
		return ErrDuplicateKey
	}
	return err
}

// MarkRevoked implements IssueRecord. A key the record does not hold is ErrUnknownKey.
func (r *AdminIssueRecord) MarkRevoked(ctx context.Context, pub string, at time.Time) error {
	err := r.admin.MarkRevoked(ctx, pub, at)
	if errors.Is(err, admin.ErrNotRecorded) {
		return ErrUnknownKey
	}
	return err
}

// ByAddress implements IssueRecord. The entries come in the order they were issued (by IssuedAt, then key).
func (r *AdminIssueRecord) ByAddress(ctx context.Context, address string) ([]IssueEntry, error) {
	all, err := r.All(ctx)
	if err != nil {
		return nil, err
	}
	var out []IssueEntry
	for _, e := range all {
		if e.Address == address {
			out = append(out, e)
		}
	}
	return out, nil
}

// All returns every entry of the record, in the order of ByAddress.
func (r *AdminIssueRecord) All(ctx context.Context) ([]IssueEntry, error) {
	recs, _, err := r.admin.Issued(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]IssueEntry, 0, len(recs))
	for _, s := range recs {
		e, err := entryOf(s)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].IssuedAt.Equal(out[j].IssuedAt) {
			return out[i].IssuedAt.Before(out[j].IssuedAt)
		}
		return out[i].PublicKey < out[j].PublicKey
	})
	return out, nil
}

// AllMaxIssued is All for the old-key inventory: for each issue or rotation key that is not revoked, IssuedAt is the
// greatest issue time over every retained revision of the key, not the latest revision's (ADR 0115). A revocation
// refuses a user JWT only when it is at or after the JWT's issue time, so it must be at or after the latest-dated one,
// a future-dated one included. A key whose retained history cannot be shown complete (*admin.IncompleteHistoryError),
// that is at the history cap (admin.IssuedTimes.Truncated, so an older, later-dated revision may have been dropped),
// has none, or has a revision without a readable time, is refused with ErrIncompleteInventory. Revoked keys and
// probes are returned as All returns them: they are not inventory.
func (r *AdminIssueRecord) AllMaxIssued(ctx context.Context) ([]IssueEntry, error) {
	all, err := r.All(ctx)
	if err != nil {
		return nil, err
	}
	for i, e := range all {
		if !revocable(e) || e.RevokedAt != nil {
			continue
		}
		t, found, err := r.admin.IssuedHistoryByKey(ctx, e.PublicKey)
		switch {
		case err != nil:
			return nil, fmt.Errorf("%w: the issue history of %s of %s: %w", ErrIncompleteInventory, e.PublicKey, e.Address, err)
		case !found:
			return nil, fmt.Errorf("%w: the issue record has no history of %s of %s", ErrIncompleteInventory, e.PublicKey, e.Address)
		case t.Truncated:
			return nil, fmt.Errorf("%w: the issue history of %s of %s is at the bucket's cap, so an older issue time may be dropped", ErrIncompleteInventory, e.PublicKey, e.Address)
		}
		if t.Max.After(e.IssuedAt) {
			all[i].IssuedAt = t.Max
		}
	}
	return all, nil
}

func entryOf(s store.Issued) (IssueEntry, error) {
	if s.Generation < 0 {
		return IssueEntry{}, fmt.Errorf("issue record %s: negative generation", s.PublicKey)
	}
	e := IssueEntry{
		Address: s.Address, PublicKey: s.PublicKey, Purpose: Purpose(s.EffectivePurpose()),
		Generation: uint64(s.Generation),
	}
	if len(s.Tags) > 0 {
		e.Tags = append([]string{}, s.Tags...)
	}
	if s.IssuedAt != "" {
		t, err := time.Parse(time.RFC3339, s.IssuedAt)
		if err != nil {
			return IssueEntry{}, fmt.Errorf("issue record %s: issued_at: %w", s.PublicKey, err)
		}
		e.IssuedAt = t.UTC()
	}
	if s.RevokedAt != "" {
		t, err := time.Parse(time.RFC3339, s.RevokedAt)
		if err != nil {
			return IssueEntry{}, fmt.Errorf("issue record %s: revoked_at: %w", s.PublicKey, err)
		}
		t = t.UTC()
		e.RevokedAt = &t
	}
	return e, nil
}

// ownerOf is the owner part of a three-part address (agent.owner.name), or "" for any other.
func ownerOf(address string) string {
	p := strings.Split(address, ".")
	if len(p) == 3 {
		return p[1]
	}
	return ""
}
