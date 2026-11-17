package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/apply"
)

// A revoke that failed after the update was pushed may be in force on some servers: the text must not say that
// nothing happened. The directory record is unchanged either way.
func TestRetireRevokeAfterPushSaysMayBeInForce(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	s.err = fmt.Errorf("revoke: %w: the update was not acknowledged: one server; running the revoke again is safe", apply.ErrNotConfirmed)
	_, errb, code := h.run("endpoint", "delete", "o.g.a")
	if code != 1 {
		t.Fatalf("%d %q", code, errb)
	}
	if !strings.Contains(errb, "the revocation may already be in force on some servers; the directory record was not changed") {
		t.Errorf("no in-force text: %q", errb)
	}
	if strings.Contains(errb, "nothing was changed") || strings.Contains(errb, "the key is still valid") {
		t.Errorf("implies nothing happened: %q", errb)
	}
	if len(h.records()) != 1 {
		t.Error("the record changed")
	}
}

// A revoke that failed before any push revoked nothing.
func TestRetireRevokeBeforePushSaysNothingRevoked(t *testing.T) {
	h := newFleetEnv(t)
	h.provisionDeclared("a")
	s := stubRevoke(t)
	s.err = errors.New("refusing: only 2 of 3 servers answered the account lookup")
	_, errb, code := h.run("endpoint", "delete", "o.g.a")
	if code != 1 {
		t.Fatalf("%d %q", code, errb)
	}
	if !strings.Contains(errb, "nothing was revoked; the key is still valid") || !strings.Contains(errb, "the directory record was not changed") {
		t.Errorf("text: %q", errb)
	}
	if strings.Contains(errb, "nothing was changed") {
		t.Errorf("old text: %q", errb)
	}
}
