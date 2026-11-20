package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
)

// Every test runs without a real server-set check: the real one needs system account credentials.
func init() {
	checkServerSet = func(context.Context, config.Config, func(string) string, int) error { return nil }
}

func TestRetireShortServerSetChangesNothing(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	old := checkServerSet
	checkServerSet = func(context.Context, config.Config, func(string) string, int) error {
		return errors.New("only 2 of 3 servers answered the account lookup")
	}
	t.Cleanup(func() { checkServerSet = old })
	out, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes")
	if code != 1 || out != "" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if !strings.Contains(errb, "nothing was revoked; the key is still valid") || !strings.Contains(errb, "only 2 of 3") {
		t.Errorf("stderr %q", errb)
	}
	if strings.Contains(errb, "deleted o.g.a") || len(s.calls) != 0 || len(h.records()) != 1 {
		t.Errorf("changed something: %q calls %v records %v", errb, s.calls, h.records())
	}
}

func TestEndpointDeleteShortServerSetChangesNothing(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	old := checkServerSet
	checkServerSet = func(context.Context, config.Config, func(string) string, int) error {
		return errors.New("only 2 of 3 servers answered the account lookup")
	}
	t.Cleanup(func() { checkServerSet = old })
	_, errb, code := h.run("endpoint", "delete", "o.g.a", "--yes")
	if code != 1 || !strings.Contains(errb, "nothing was revoked; the key is still valid") {
		t.Fatalf("%d %q", code, errb)
	}
	if len(s.calls) != 0 || len(h.records()) != 1 {
		t.Errorf("changed something: calls %v records %v", s.calls, h.records())
	}
}

func TestRetirePartialListsWhatWasRevokedAndWhatIsPending(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	stubRevoke(t)
	failAfterPurge(t)
	out, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if out != "" || !strings.Contains(errb, "error: deleted o.g.b, but ") || !strings.HasSuffix(errb, "hint: mashctl endpoint delete o.g.b\n") {
		t.Errorf("stderr: %q", errb)
	}
}

func TestRetireFailureBeforeAnyRevokeSaysKeyStillValid(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("b")
	deprovisionIfUser = func(*admin.Admin, context.Context, sap.Address, string) error {
		return errors.New("the server holding bucket KV_lease is down")
	}
	t.Cleanup(func() { deprovisionIfUser = (*admin.Admin).DeprovisionIfUser })
	_, errb, code := h.run("endpoint", "delete", "o.g.b", "--yes")
	if code != 1 || strings.Contains(errb, "deleted o.g.b") || !strings.Contains(errb, "nothing was revoked; the key is still valid") {
		t.Fatalf("%d %q", code, errb)
	}
}
