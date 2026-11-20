package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

// revokedTotal is how many keys the fake revocation was asked to revoke.
func revokedTotal(calls *[]revokeCall) (n int) {
	for _, c := range *calls {
		n += len(c.users)
	}
	return n
}

func revokedKey(calls *[]revokeCall, key string) bool {
	for _, c := range *calls {
		for _, u := range c.users {
			if u == key {
				return true
			}
		}
	}
	return false
}

// checkRetried is the state after the identical command finished a run that stopped after the creds were written.
func checkRetried(t *testing.T, h *fleetEnv, calls *[]revokeCall, a sap.Address, installed, old string, code int, se string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("rerun %d %q", code, se)
	}
	rec, _ := h.record(a)
	if got := credsPub(t, h, a); got != rec.User || got != installed {
		t.Errorf("creds %s record %s installed %s", got, rec.User, installed)
	}
	if revokedKey(calls, installed) {
		t.Error("the key the first run installed was revoked")
	}
	if n := revokedTotal(calls); n != 1 || !revokedKey(calls, old) {
		t.Errorf("revocations %+v, want the original key once", *calls)
	}
	noPending(t, h)
}

func TestEndpointReissueMakeKeyKilledAfterPublish(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	a := ad("rt1")
	if _, se, code := h.run("endpoint", "create", a.String()); code != 0 {
		t.Fatalf("create %q", se)
	}
	old := credsPub(t, h, a)
	args := []string{"endpoint", "reissue", a.String()}
	if code := killAt(h, &epBeforeVerify, args...); code != -1 {
		t.Fatalf("not stopped: %d", code)
	}
	installed := credsPub(t, h, a)
	if installed == old {
		t.Fatal("the creds were not published before the kill point")
	}
	if _, err := os.Lstat(h.creds(a) + ".pending"); err != nil {
		t.Fatalf("the pending file was not kept: %v", err)
	}
	_, se, code := h.run(args...)
	checkRetried(t, h, calls, a, installed, old, code, se)
}

func TestEndpointReissueMakeKeyReadbackFailsThenRetry(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	a := ad("rt2")
	if _, se, code := h.run("endpoint", "create", a.String()); code != 0 {
		t.Fatalf("create %q", se)
	}
	old := credsPub(t, h, a)
	args := []string{"endpoint", "reissue", a.String()}
	epVerifyFail = func() error { return errors.New("timeout") }
	_, se, code := h.run(args...)
	epVerifyFail = nil
	if code != 1 || !strings.Contains(se, "could not be confirmed") || !strings.HasSuffix(se, "\nhint: mashctl endpoint reissue "+a.String()+"\n") {
		t.Fatalf("%d %q", code, se)
	}
	installed := credsPub(t, h, a)
	if installed == old {
		t.Fatal("the creds were not published")
	}
	if _, err := os.Lstat(h.creds(a) + ".pending"); err != nil {
		t.Fatalf("the pending file was not kept: %v", err)
	}
	_, se, code = h.run(args...)
	checkRetried(t, h, calls, a, installed, old, code, se)
}
