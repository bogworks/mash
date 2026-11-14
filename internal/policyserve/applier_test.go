package policyserve

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

type anyRecord struct{}

func (anyRecord) Add(context.Context, policyapply.IssueEntry) error    { return nil }
func (anyRecord) MarkRevoked(context.Context, string, time.Time) error { return nil }
func (anyRecord) ByAddress(context.Context, string) ([]policyapply.IssueEntry, error) {
	return nil, nil
}

func (anyRecord) AllMaxIssued(context.Context) ([]policyapply.IssueEntry, error) { return nil, nil }

type mintedPubs []string

func (m mintedPubs) Minted() []string { return m }

type countRevoker struct {
	n   int
	err error
}

func (r *countRevoker) Revoke(context.Context, []string) (string, error) { r.n++; return "", r.err }

type denyGuard struct{ seen []string }

func (g *denyGuard) Before(_ context.Context, what string) error {
	g.seen = append(g.seen, what)
	return errors.New("the account lock is no longer held")
}

const probeKey = "UAPROBEKEY0123456789"

func testApplier(buf *bytes.Buffer) *Applier {
	return &Applier{Record: anyRecord{}, Log: slog.New(slog.NewTextHandler(buf, nil))}
}

func TestCleanupProbesNeedsTheGuard(t *testing.T) {
	var buf bytes.Buffer
	rv := &countRevoker{}
	g := &denyGuard{}
	testApplier(&buf).cleanupProbes(context.Background(), g, rv, mintedPubs{probeKey})
	if rv.n != 0 {
		t.Fatal("the account JWT was read, signed and pushed after the guard refused")
	}
	if len(g.seen) != 1 || g.seen[0] != "revoke probes" {
		t.Errorf("%v", g.seen)
	}
	log := buf.String()
	if !strings.Contains(log, "level=ERROR") || !strings.Contains(log, "UAPROBEK") || strings.Contains(log, probeKey) {
		t.Errorf("the log must list the minted keys by short key: %s", log)
	}
}

// A failed cleanup is logged and changes nothing else: cleanupProbes has no result, so the outcome of Apply (and the
// rotation of the submitter after it) cannot depend on it.
func TestCleanupProbesFailureIsOnlyLogged(t *testing.T) {
	var buf bytes.Buffer
	rv := &countRevoker{err: errors.New("a server did not acknowledge")}
	testApplier(&buf).cleanupProbes(context.Background(), &fakeGuard{}, rv, mintedPubs{probeKey})
	log := buf.String()
	if rv.n != 1 || !strings.Contains(log, "level=ERROR") || !strings.Contains(log, "UAPROBEK") || strings.Contains(log, probeKey) {
		t.Errorf("%d: %s", rv.n, log)
	}
	buf.Reset()
	testApplier(&buf).cleanupProbes(context.Background(), &fakeGuard{}, &countRevoker{}, mintedPubs{probeKey})
	testApplier(&buf).cleanupProbes(context.Background(), &denyGuard{}, &countRevoker{}, mintedPubs{})
	if buf.Len() != 0 {
		t.Errorf("a successful or empty cleanup logs nothing: %s", buf.String())
	}
}

type fakeInForce struct {
	gen uint64
	err error
}

func (f fakeInForce) InForce() (listset.Record, error) {
	return listset.Record{Generation: f.gen}, f.err
}

func TestStatusWrittenFollowsTheLedger(t *testing.T) {
	applied := policyapply.Status{Generation: 5, State: policyapply.StateApplied}
	boom := errors.New("write status: bucket unavailable")
	tests := map[string]struct {
		st   policyapply.Status
		err  error
		l    inForceReader
		want bool
	}{
		"applied, no error":                    {applied, nil, fakeInForce{gen: 5}, true},
		"applied, error, generation in force":  {applied, boom, fakeInForce{gen: 5}, true},
		"applied, error, commit did not occur": {applied, boom, fakeInForce{gen: 4}, false},
		"applied, error, ledger unreadable":    {applied, boom, fakeInForce{gen: 5, err: boom}, false},
		"applied, error, no ledger":            {applied, boom, nil, false},
		"refused":                              {policyapply.Status{Generation: 5, State: policyapply.StateRefused}, boom, fakeInForce{gen: 4}, true},
		"no status":                            {policyapply.Status{}, boom, fakeInForce{gen: 5}, false},
	}
	for name, tc := range tests {
		if got := statusWritten(tc.st, tc.err, tc.l); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}
