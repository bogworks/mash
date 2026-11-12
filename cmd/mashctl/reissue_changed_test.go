package main

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/mint"
	"github.com/nats-io/jwt/v2"
)

// A creds JWT that matches the policy but whose user key the account JWT revokes is selected and rotated.
func TestReissueChangedSelectsRevokedKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a") // matches the policy
	_, oldPub := h.credsClaims("a")
	calls := fakeRevoke(t, nil)
	revCalls := fakeKeyRevoked(t, true, nil)

	out, errb, code := h.run("reissue", "--changed")
	if code != 0 || !strings.Contains(out, "reissued a (new user key, old key revoked)\n") || strings.Contains(out, "nothing to reissue") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if len(*revCalls) == 0 || (*revCalls)[0] != oldPub {
		t.Fatalf("keyRevoked calls %v", *revCalls)
	}
	if _, pub := h.credsClaims("a"); pub == oldPub {
		t.Fatal("a revoked key was kept")
	}
	if len(*calls) != 1 || !slices.Equal((*calls)[0].users, []string{oldPub}) {
		t.Fatalf("revoke calls %+v", *calls)
	}
}

// A staged file selects the endpoint even though the creds JWT matches the policy (the policy was reverted after an
// interrupted rotation): the reissue finishes the rotation.
func TestReissueChangedFinishesStagedRotation(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag)
	raw, _ := os.ReadFile(h.creds("a"))
	kp, err := jwt.ParseDecoratedNKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	oldSeed, _ := kp.Seed()
	oldSeed = append([]byte(nil), oldSeed...)
	oldPub, _ := kp.PublicKey()
	calls := fakeRevoke(t, nil)

	prev := writeCreds
	writeCreds = func(string, []byte) error { return errors.New("disk full") }
	_, _, code := h.run("reissue", "a")
	writeCreds = prev
	if code != 1 {
		t.Fatalf("interrupted reissue exited %d", code)
	}
	_, stagedPub := h.stagedClaims("a")

	// the policy is back to what the old JWT carries: put a matching JWT for the old key in the creds file
	data, _ := os.ReadFile(h.policy)
	_, c, err := compileBytes(data, h.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	m, err := mint.Mint(h.keys(), c, "a", oldSeed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.creds("a"), m.Creds, 0o600); err != nil {
		t.Fatal(err)
	}
	if recs := h.records(); len(recs) != 1 || recs[0] != "a//"+stagedPub {
		t.Fatalf("records %v", recs)
	}

	out, errb, code := h.run("reissue", "--changed")
	if code != 0 || !strings.Contains(out, "reissued a (") || strings.Contains(out, "nothing to reissue") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if _, pub := h.credsClaims("a"); pub != stagedPub {
		t.Fatalf("creds key %s, want staged %s", pub, stagedPub)
	}
	h.noStaged("a")
	if len(*calls) != 2 || !slices.Equal((*calls)[1].users, []string{oldPub}) {
		t.Fatalf("revoke calls %+v", *calls)
	}
}

// When the revocation cannot be read, --changed says so and exits 1, but still reissues the other endpoints.
func TestReissueChangedRevocationUnknown(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a")               // matches: needs the revocation check
	h.seedWith("b", extraTag) // stale: selected without it
	_, oldA := h.credsClaims("a")
	fakeRevoke(t, nil)
	fakeKeyRevoked(t, false, errors.New("no system account credentials: x"))

	out, errb, code := h.run("reissue", "--changed")
	if code != 1 || !strings.Contains(errb, "mashctl: reissue a: cannot tell whether its user key is revoked: no system account credentials") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if strings.Contains(out, "nothing to reissue") || !strings.Contains(out, "reissued b (") {
		t.Fatalf("other endpoint not processed: %q", out)
	}
	if _, pub := h.credsClaims("a"); pub != oldA {
		t.Error("a changed")
	}

	// with nothing else selected the error alone is the result, never "nothing to reissue"
	h2 := newAddEnv(t, addPolicy)
	h2.seed("a")
	fakeKeyRevoked(t, false, errors.New("x"))
	if out, errb, code := h2.run("reissue", "--changed"); code != 1 || out != "" || !strings.Contains(errb, "cannot tell whether its user key is revoked") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
}

// Nothing differs, no key is revoked and nothing is staged: nothing to reissue.
func TestReissueChangedNothingToDo(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	calls := fakeRevoke(t, nil)
	revCalls := fakeKeyRevoked(t, false, nil)
	out, errb, code := h.run("reissue", "--changed")
	if code != 0 || out != "nothing to reissue\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if len(*revCalls) != 1 || len(*calls) != 0 {
		t.Fatalf("keyRevoked %v, revoke %+v", *revCalls, *calls)
	}
}
