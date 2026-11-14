package policyapply

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/bogworks/mash/internal/listset"
)

// MemProvisionMarkers keeps provisioning markers in memory, for a ledger whose pending record is also in memory and
// for tests. Markers do not outlive the process.
type MemProvisionMarkers struct {
	mu    sync.Mutex
	marks map[string]struct{} // generation + "\x00" + address
}

var _ ProvisionMarkers = (*MemProvisionMarkers)(nil)

// NewMemProvisionMarkers returns an empty MemProvisionMarkers.
func NewMemProvisionMarkers() *MemProvisionMarkers {
	return &MemProvisionMarkers{marks: map[string]struct{}{}}
}

func memMarkKey(address string, generation uint64) string {
	return strconv.FormatUint(generation, 10) + "\x00" + address
}

// MarkProvisioning implements ProvisionMarkers.
func (m *MemProvisionMarkers) MarkProvisioning(address string, generation uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.marks == nil {
		m.marks = map[string]struct{}{}
	}
	m.marks[memMarkKey(address, generation)] = struct{}{}
	return nil
}

// Provisioning implements ProvisionMarkers.
func (m *MemProvisionMarkers) Provisioning(generation uint64) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := strconv.FormatUint(generation, 10) + "\x00"
	var out []string
	for k := range m.marks {
		if a, ok := strings.CutPrefix(k, prefix); ok {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ClearProvisioning implements ProvisionMarkers.
func (m *MemProvisionMarkers) ClearProvisioning(address string, generation uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.marks, memMarkKey(address, generation))
	return nil
}

// markerMethods adds the three ProvisionMarkers methods to a ledger wrapper.
type markerMethods struct{ m ProvisionMarkers }

func (x markerMethods) MarkProvisioning(a string, g uint64) error { return x.m.MarkProvisioning(a, g) }
func (x markerMethods) Provisioning(g uint64) ([]string, error)   { return x.m.Provisioning(g) }
func (x markerMethods) ClearProvisioning(a string, g uint64) error {
	return x.m.ClearProvisioning(a, g)
}

type markedLedger struct {
	optForward
	markerMethods
}

type markedDetail struct {
	markedLedger
	dl DetailLedger
}

// CommitRollback forwards RollbackLedger, so a ledger wrapped for markers still records a confirmed rollback.
func (l markedDetail) CommitRollback(jwt string) error {
	return commitRollback(l.markedLedger.Ledger, jwt)
}

func (l markedDetail) CompareAndCommitDetail(base uint64, rec listset.Record, d InForceDetail) error {
	return l.dl.CompareAndCommitDetail(base, rec, d)
}
func (l markedDetail) InForceJWT() (string, error)                    { return l.dl.InForceJWT() }
func (l markedDetail) InForceListSet() (listset.ListSet, bool, error) { return l.dl.InForceListSet() }

type markedStatus struct {
	markedLedger
	sw StatusWriter
}

func (l markedStatus) WriteStatus(ctx context.Context, st Status) error {
	return l.sw.WriteStatus(ctx, st)
}

type markedBoth struct {
	markedDetail
	sw StatusWriter
}

func (l markedBoth) WriteStatus(ctx context.Context, st Status) error {
	return l.sw.WriteStatus(ctx, st)
}

// WithProvisionMarkers returns l with the ProvisionMarkers methods backed by m, so the provisioning-marker guard of
// RunOrdered is active. The DetailLedger, RollbackLedger, PendingJWTLedger, CommitSizeChecker and StatusWriter
// interfaces of l are kept when l has them. Other optional interfaces of l (StatusReader, RefusalWriter) are not forwarded; callers that need them use the ledger directly.
func WithProvisionMarkers(l Ledger, m ProvisionMarkers) Ledger {
	ml := markedLedger{optForward{l}, markerMethods{m}}
	dl, hasDetail := l.(DetailLedger)
	sw, hasStatus := l.(StatusWriter)
	switch {
	case hasDetail && hasStatus:
		return markedBoth{markedDetail{ml, dl}, sw}
	case hasDetail:
		return markedDetail{ml, dl}
	case hasStatus:
		return markedStatus{ml, sw}
	}
	return ml
}
