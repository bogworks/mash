package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"
)

// clockBehind is how far behind the creds' issue time the revoker's clock is set in these tests.
const clockBehind = time.Hour

// A creds JWT dated after the revocation time is refused by retire, with nothing revoked or removed; the same
// creds with the clock right retire normally.
func TestRetireRefusesFutureIssuedCreds(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	if _, errb, code := h.run("add", "c", "--zone", "red"); code != 0 {
		t.Fatal(errb)
	}
	before, _ := os.ReadFile(h.creds("c"))
	recs := h.records()
	policyBefore := h.policyText()
	calls := fakeRevoke(t, nil)

	prev := retireRevokeNow
	retireRevokeNow = func() time.Time { return time.Now().Add(-clockBehind) } // the JWT was minted "an hour from now"
	out, errb, code := h.run("retire", "c")
	retireRevokeNow = prev
	if code != 1 || out != "" || !strings.Contains(errb, "c's current creds JWT claims it was issued in the future (") || !strings.Contains(errb, "fix this machine's clock") || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if len(*calls) != 0 {
		t.Fatalf("revoke calls %+v", *calls)
	}
	if after, _ := os.ReadFile(h.creds("c")); !bytes.Equal(before, after) {
		t.Fatal("creds file changed")
	}
	if len(h.records()) != len(recs) || h.policyText() != policyBefore {
		t.Fatal("record or policy changed")
	}

	// Control: a JWT issued in the past is revoked as before.
	out, errb, code = h.run("retire", "c")
	if code != 0 || !strings.Contains(out, "revoked in the account JWT") || len(*calls) != 1 {
		t.Fatalf("%d %q %q calls %+v", code, out, errb, *calls)
	}
}

func TestRefuseFutureIssued(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	at := func(d time.Duration) *jwt.UserClaims {
		uc := jwt.NewUserClaims("UKEY")
		uc.IssuedAt = now.Add(d).Unix()
		return uc
	}
	if err := refuseFutureIssued("a", at(0), now); err != nil {
		t.Errorf("equal iat refused: %v", err)
	}
	if err := refuseFutureIssued("a", at(-clockBehind), now); err != nil {
		t.Errorf("past iat refused: %v", err)
	}
	if err := refuseFutureIssued("a", nil, now); err != nil {
		t.Errorf("no JWT refused: %v", err)
	}
	if err := refuseFutureIssued("a", at(time.Second), now); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("future iat: %v", err)
	}
}
