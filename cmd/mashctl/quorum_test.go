package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/store"
)

// A retire whose revocation reached the quorum but not every server exits 0, says so, and names the pending servers on
// one plain line a script can find; the endpoint is gone.
func TestRetireWithPendingServersExitsZeroAndReportsThem(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	old := revokeUsers
	t.Cleanup(func() { revokeUsers = old })
	revokeUsers = func(ctx context.Context, _ config.Config, _ func(string) string, _ int, users []string, out io.Writer, _ func() time.Time) error {
		pending := []string{"n3"}
		if rep, ok := ctx.Value(revokeReportKey{}).(*revokeReport); ok {
			rep.Quorum, rep.Declared, rep.Acked, rep.Pending = 2, 3, 2, pending
		}
		fmt.Fprintf(out, "revoked user key %s...: 2 server(s) acknowledged\n", users[0][:8])
		fmt.Fprintln(out, pendingLine(pending))
		return nil
	}
	out, errb, code := h.run("retire", "a")
	if code != 0 {
		t.Fatalf("exit %d: %q %q", code, out, errb)
	}
	for _, want := range []string{"retired a\n", "pending_servers: n3 (", "will converge when they rejoin", "2 of 3 servers hold the revocation (write quorum 2)"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if len(h.records()) != 0 {
		t.Error("the record is still there")
	}
}

// With every server holding the revocation there is no pending line.
func TestRetireWithNoPendingServersSaysNothingOfThem(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	stubRevoke(t)
	out, errb, code := h.run("retire", "a")
	if code != 0 || strings.Contains(out, "pending_servers") || strings.Contains(out, "write quorum") {
		t.Fatalf("exit %d: %q %q", code, out, errb)
	}
}

// Below the quorum the revocation refuses, so nothing is removed and the exit is 1, with the message that says how many
// answered of how many and what the quorum is.
func TestRetireBelowTheQuorumRefusesAndRemovesNothing(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	s.err = errors.New("revoke: refusing: 1 of 3 servers answered and the write quorum is 2: a server may be down: bring it back, or set write_quorum (nothing was revoked)")
	out, errb, code := h.run("retire", "a")
	if code != 1 || strings.Contains(out, "retired") || !strings.Contains(errb, "1 of 3 servers answered and the write quorum is 2") || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("exit %d: %q %q", code, out, errb)
	}
	if len(h.records()) != 1 {
		t.Error("retire removed the record though the revocation was refused")
	}
}

func TestPendingLineNamesTheServers(t *testing.T) {
	got := pendingLine([]string{"n2", "a server that did not answer"})
	if !strings.HasPrefix(got, "pending_servers: n2, a server that did not answer (") || !strings.Contains(got, "converge when they rejoin") || !strings.Contains(got, "revocation_pending") {
		t.Fatal(got)
	}
}

func TestWithDeclaredFallsBackToTheConfig(t *testing.T) {
	if got := withDeclared(config.Config{Servers: 3}, 0); got != 3 {
		t.Fatal(got)
	}
	if got := withDeclared(config.Config{Servers: 3}, 5); got != 5 {
		t.Fatal(got)
	}
}

// The findings of a report: one per key an answering server lacks, one for the servers that did not answer, none once
// every server holds every revocation.
func TestRevocationProblems(t *testing.T) {
	if p := revocationProblems(apply.RevocationReport{Declared: 3, Answered: 3}); len(p) != 0 {
		t.Fatalf("converged report: %+v", p)
	}
	if p := revocationProblems(apply.RevocationReport{Declared: 3, Answered: 2, Silent: 1, Keys: 0}); len(p) != 0 {
		t.Fatalf("a silent server and nothing revoked: %+v", p)
	}
	p := revocationProblems(apply.RevocationReport{Declared: 3, Answered: 3, Keys: 1, Pending: []apply.PendingRevocation{{Key: "UABCDEFGHIJKLMNOP", Lacking: 1}}})
	if len(p) != 1 || p[0].What != "revocation_pending" || p[0].Item != "UABCDEFG" || !strings.Contains(p[0].Error, "1 of 3 answering servers do not list it") {
		t.Fatalf("%+v", p)
	}
	p = revocationProblems(apply.RevocationReport{Declared: 3, Answered: 2, Silent: 1, Keys: 2})
	if len(p) != 1 || p[0].What != "revocation_pending" || p[0].Item != "servers" || !strings.Contains(p[0].Error, "1 of 3 servers did not answer") || !strings.Contains(p[0].Error, "2 recorded revocation(s)") {
		t.Fatalf("%+v", p)
	}
}

func stubPending(t *testing.T, rep apply.RevocationReport, err error) *int {
	t.Helper()
	calls := new(int)
	old := auditPendingRevocations
	t.Cleanup(func() { auditPendingRevocations = old })
	auditPendingRevocations = func(_ context.Context, _ config.Config, _ func(string) string, _ int, keys []string) (apply.RevocationReport, error) {
		*calls++
		return rep, err
	}
	return calls
}

// audit reports a revocation that has not reached every server as a finding until it has; --strict fails on it and a
// converged state is clean again. Nothing is stored: the finding is derived on each run.
func TestAuditReportsAPendingRevocationUntilEveryServerHoldsIt(t *testing.T) {
	f := newAuditEnv(t)
	kv := f.retiredKV(t)
	f.putMarker(t, kv, "claude", "team1", "gone", "UGONEUSERKEYAAAA", store.RetiredFinal)
	sys := map[string]string{"MASH_SYS_CREDS": "/x/sys.creds", "MASH_SERVERS": "3", "MASH_REPLICAS": "1"}
	run := func(args ...string) (retiredOut, string, int) {
		t.Helper()
		out, errb, code := ctlRunWith(t, f.e, sys, append([]string{"audit", "--creds-dir", f.credsDir, "--json"}, args...)...)
		var r retiredOut
		if code == 3 || json.Unmarshal([]byte(out), &r) != nil {
			t.Fatalf("audit: %d %q\n%s", code, errb, out)
		}
		return r, errb, code
	}

	calls := stubPending(t, apply.RevocationReport{Declared: 3, Answered: 3, Keys: 1, Pending: []apply.PendingRevocation{{Key: "UGONEUSERKEYAAAA", Lacking: 1}}}, nil)
	r, errb, code := run("--strict")
	if *calls != 1 || code != 1 || len(r.Problems) != 1 || r.Problems[0].What != "revocation_pending" || r.Problems[0].Item != "UGONEUSE" || !strings.Contains(errb, "--strict") {
		t.Fatalf("pending: calls %d exit %d %+v %q", *calls, code, r.Problems, errb)
	}
	// without --strict the finding is reported and the exit is 0
	if _, _, code := run(); code != 0 {
		t.Fatalf("exit %d without --strict", code)
	}

	// every server holds it: the finding is gone by itself
	stubPending(t, apply.RevocationReport{Declared: 3, Answered: 3, Keys: 1}, nil)
	if r, _, code := run("--strict"); code != 0 || len(r.Problems) != 0 {
		t.Fatalf("converged: exit %d %+v", code, r.Problems)
	}

	// a check that cannot be made is a note, not a finding
	stubPending(t, apply.RevocationReport{}, errors.New("no server answered"))
	out, _, code := ctlRunWith(t, f.e, sys, "audit", "--creds-dir", f.credsDir, "--strict")
	if code != 0 || !strings.Contains(out, "revocations were not compared with the servers: no server answered") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// With no system credentials, or no revoked key, audit asks the servers nothing.
func TestAuditAsksNothingWithoutCredentialsOrRevokedKeys(t *testing.T) {
	f := newAuditEnv(t)
	calls := stubPending(t, apply.RevocationReport{Declared: 3, Answered: 3, Keys: 1, Pending: []apply.PendingRevocation{{Key: "UX", Lacking: 1}}}, nil)
	// no revoked key at all
	if _, _, code := ctlRunWith(t, f.e, map[string]string{"MASH_SYS_CREDS": "/x/sys.creds"}, "audit", "--creds-dir", f.credsDir, "--strict"); code != 0 || *calls != 0 {
		t.Fatalf("no revoked key: exit %d calls %d", code, *calls)
	}
	// a retired key but no system credentials
	f.putMarker(t, f.retiredKV(t), "claude", "team1", "gone", "UGONEUSERKEYAAAA", store.RetiredFinal)
	if _, _, code := ctlRunWith(t, f.e, nil, "audit", "--creds-dir", f.credsDir, "--strict"); code != 0 || *calls != 0 {
		t.Fatalf("no system credentials: exit %d calls %d", code, *calls)
	}
}
