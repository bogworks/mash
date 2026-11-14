package policyapply

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"
)

// Purpose says why a credential was issued.
type Purpose string

// The purposes of an issue record entry.
const (
	PurposeIssue    Purpose = "issue"
	PurposeRotation Purpose = "rotation"
	PurposeProbe    Purpose = "probe"
)

// IssueEntry is one credential in the issue record. Its shape follows ADR 0101 (REQ-018) so that record can be
// swapped in for MemIssueRecord.
type IssueEntry struct {
	Address   string
	PublicKey string
	Purpose   Purpose
	IssuedAt  time.Time
	Expires   time.Time
	RevokedAt *time.Time
}

// IssueRecord is where every credential mash issues is written down.
type IssueRecord interface {
	// Add records an entry. A credential is recorded before it is handed out.
	Add(ctx context.Context, e IssueEntry) error
	// MarkRevoked notes that the credential with public key pub was revoked at at.
	MarkRevoked(ctx context.Context, pub string, at time.Time) error
}

// ErrUnknownKey is returned by MemIssueRecord.MarkRevoked for a key that was never added.
var ErrUnknownKey = errors.New("no issue record entry for that key")

// MemIssueRecord is an in-memory IssueRecord, safe for concurrent use.
type MemIssueRecord struct {
	mu      sync.Mutex
	entries []IssueEntry
}

// Add implements IssueRecord.
func (m *MemIssueRecord) Add(_ context.Context, e IssueEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, copyEntry(e))
	return nil
}

// MarkRevoked implements IssueRecord. It marks every entry with that key; the first revocation time is kept.
func (m *MemIssueRecord) MarkRevoked(_ context.Context, pub string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for i := range m.entries {
		if m.entries[i].PublicKey != pub {
			continue
		}
		found = true
		if m.entries[i].RevokedAt == nil {
			t := at
			m.entries[i].RevokedAt = &t
		}
	}
	if !found {
		return ErrUnknownKey
	}
	return nil
}

// Entries returns a copy of the entries in the order they were added.
func (m *MemIssueRecord) Entries() []IssueEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]IssueEntry, len(m.entries))
	for i, e := range m.entries {
		out[i] = copyEntry(e)
	}
	return out
}

func copyEntry(e IssueEntry) IssueEntry {
	if e.RevokedAt != nil {
		t := *e.RevokedAt
		e.RevokedAt = &t
	}
	return e
}

// Revoker revokes user keys on every server.
type Revoker interface {
	Revoke(ctx context.Context, pubs []string) error
}

// MintedSource is anything that minted probe credentials and can say which keys.
type MintedSource interface {
	Minted() []string
}

// RevokeProbes revokes the probe keys pubs (duplicates ignored) and, once the revocation succeeded, marks each in the
// record as revoked at now. Call it when the gate ends, pass or fail, with a context that outlives the gate's own.
// With no keys it does nothing. An error from the revoker is returned and nothing is marked.
func RevokeProbes(ctx context.Context, revoker Revoker, record IssueRecord, pubs []string, now time.Time) error {
	pubs = slices.Clone(pubs)
	sort.Strings(pubs)
	pubs = slices.Compact(pubs)
	if len(pubs) == 0 {
		return nil
	}
	if err := revoker.Revoke(ctx, pubs); err != nil {
		return err
	}
	var errs []error
	for _, p := range pubs {
		if err := record.MarkRevoked(ctx, p, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RevokeMinted is RevokeProbes for every key the sources minted. Use it in a defer around Run or Converge:
//
//	defer func() { err = errors.Join(err, RevokeMinted(ctx, rv, rec, time.Now(), prober, witnesses)) }()
func RevokeMinted(ctx context.Context, revoker Revoker, record IssueRecord, now time.Time, sources ...MintedSource) error {
	var pubs []string
	for _, s := range sources {
		if s != nil {
			pubs = append(pubs, s.Minted()...)
		}
	}
	return RevokeProbes(ctx, revoker, record, pubs, now)
}
