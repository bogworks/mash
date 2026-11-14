package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/bogworks/mash/internal/admin"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/mint"
	"github.com/nats-io/jwt/v2"
)

// stagedClaims reads the staged file of name's creds file.
func (h *addEnv) stagedClaims(name string) (*jwt.UserClaims, string) {
	h.t.Helper()
	raw, err := os.ReadFile(h.creds(name) + ".next")
	if err != nil {
		h.t.Fatal(err)
	}
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		h.t.Fatal(err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		h.t.Fatal(err)
	}
	return uc, uc.Subject
}

func (h *addEnv) noStaged(name string) {
	h.t.Helper()
	if _, err := os.Lstat(h.creds(name) + ".next"); !errors.Is(err, os.ErrNotExist) {
		h.t.Fatalf("staged file still there: %v", err)
	}
}

// A failed creds write after the revoke and the record update leaves the staged seed; the same reissue finishes it.
func TestReissueResumesAfterFailedWrite(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag) // narrowing: rotates
	_, oldPub := h.credsClaims("a")
	before, _ := os.ReadFile(h.creds("a"))
	calls := fakeRevoke(t, nil)

	prev := writeCreds
	writeCreds = func(string, []byte) error { return errors.New("disk full") }
	out, errb, code := h.run("reissue", "a")
	writeCreds = prev
	if code != 1 || out != "" || !strings.Contains(errb, "revoked") || !strings.Contains(errb, ".next") || !strings.Contains(errb, "mashctl reissue a again") || strings.Contains(errb, "retire") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(before, after) {
		t.Fatal("creds file changed")
	}
	fi, err := os.Stat(h.creds("a") + ".next")
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("staged file: %v %v", fi, err)
	}
	_, stagedPub := h.stagedClaims("a")
	if stagedPub == oldPub {
		t.Fatal("staged key is the old key")
	}
	if recs := h.records(); len(recs) != 1 || recs[0] != "a//"+stagedPub {
		t.Fatalf("records %v, want user %s", recs, stagedPub)
	}

	out, errb, code = h.run("reissue", "a")
	if code != 0 || !strings.HasPrefix(out, "reissued a (") || !strings.Contains(out, "new user key, old key revoked") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if _, pub := h.credsClaims("a"); pub != stagedPub {
		t.Fatalf("creds key %s, want staged %s", pub, stagedPub)
	}
	h.noStaged("a")
	if recs := h.records(); len(recs) != 1 || recs[0] != "a//"+stagedPub {
		t.Fatalf("records %v", recs)
	}
	if len(*calls) != 2 || !slices.Equal((*calls)[1].users, []string{oldPub}) {
		t.Fatalf("revoke calls %+v, want two for %s", *calls, oldPub)
	}
}

// A revoke that fails after staging leaves the record alone; the re-run reuses the staged key.
func TestReissueResumesAfterFailedRevoke(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag)
	_, oldPub := h.credsClaims("a")
	fakeRevoke(t, errors.New("sys down"))

	out, errb, code := h.run("reissue", "a")
	if code != 1 || !strings.Contains(errb, "revocation was not confirmed") || strings.Contains(errb, "retire") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	_, stagedPub := h.stagedClaims("a")
	if recs := h.records(); len(recs) != 1 || recs[0] != "a//"+oldPub {
		t.Fatalf("records %v", recs)
	}
	if _, pub := h.credsClaims("a"); pub != oldPub {
		t.Fatal("creds file changed")
	}

	calls := fakeRevoke(t, nil)
	out, errb, code = h.run("reissue", "a")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, pub := h.credsClaims("a"); pub != stagedPub {
		t.Fatalf("creds key %s, want the same staged key %s", pub, stagedPub)
	}
	h.noStaged("a")
	if len(*calls) != 1 || !slices.Equal((*calls)[0].users, []string{oldPub}) {
		t.Fatalf("revoke calls %+v", *calls)
	}
}

// A staged file for another name or account is refused and nothing changes.
func TestReissueRefusesBadStagedFile(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag)
	_, oldPub := h.credsClaims("a")
	before, _ := os.ReadFile(h.creds("a"))
	calls := fakeRevoke(t, nil)

	data, _ := os.ReadFile(h.policy)
	_, c, err := compileBytes(data, h.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	m, err := mint.Mint(h.keys(), c, "b", nil) // a staged file carrying name b
	if err != nil {
		t.Fatal(err)
	}
	sp := h.creds("a") + ".next"
	if err := os.WriteFile(sp, m.Creds, 0o600); err != nil {
		t.Fatal(err)
	}
	out, errb, code := h.run("reissue", "a")
	if code != 1 || !strings.Contains(errb, "another name") || !strings.Contains(errb, "remove the stale staged file "+sp+" and run again") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(before, after) {
		t.Fatal("creds file changed")
	}
	if recs := h.records(); len(recs) != 1 || recs[0] != "a//"+oldPub {
		t.Fatalf("records %v", recs)
	}
	if len(*calls) != 0 {
		t.Fatalf("revoke calls %+v", *calls)
	}

	// A loose staged file is refused too.
	if err := os.Chmod(sp, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errb, code := h.run("reissue", "a"); code != 1 || !strings.Contains(errb, "remove the stale staged file") {
		t.Fatalf("%d %q", code, errb)
	}
}

// A normal rotation leaves no staged file.
func TestReissueRotationLeavesNoStagedFile(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag)
	fakeRevoke(t, nil)
	out, errb, code := h.run("reissue", "a")
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	h.noStaged("a")
}

// An old JWT that claims to be issued in the future is refused on a rotation, and on the revoked-key path, with
// nothing staged, revoked or changed; one issued in the past proceeds.
func TestReissueRefusesFutureIssuedOldJWT(t *testing.T) {
	for _, tc := range []struct {
		name    string
		edit    func(*jwt.UserClaims)
		revoked bool
	}{
		{"narrowing", extraTag, false},
		{"revoked key", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAddEnv(t, addPolicy)
			h.seedWith("a", tc.edit)
			_, oldPub := h.credsClaims("a")
			before, _ := os.ReadFile(h.creds("a"))
			recs := h.records()
			calls := fakeRevoke(t, nil)
			fakeKeyRevoked(t, tc.revoked, nil)

			prev := reissueNow
			reissueNow = func() time.Time { return time.Now().Add(-time.Hour) } // the JWT was minted "an hour from now"
			out, errb, code := h.run("reissue", "a")
			reissueNow = prev
			if code != 1 || out != "" || !strings.Contains(errb, "a's current creds JWT claims it was issued in the future (") || !strings.Contains(errb, "fix this machine's clock") {
				t.Fatalf("%d %q %q", code, out, errb)
			}
			noSecrets(t, out, errb)
			if len(*calls) != 0 {
				t.Fatalf("revoke calls %+v", *calls)
			}
			h.noStaged("a")
			if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(before, after) {
				t.Fatal("creds file changed")
			}
			if !slices.Equal(recs, h.records()) {
				t.Fatalf("records %v, want %v", h.records(), recs)
			}

			// Issued in the past (the real clock): proceeds.
			out, errb, code = h.run("reissue", "a")
			if code != 0 {
				t.Fatalf("%d %q %q", code, out, errb)
			}
			if _, pub := h.credsClaims("a"); pub == oldPub || len(*calls) != 1 {
				t.Fatalf("key %s, revoke calls %+v", pub, *calls)
			}
		})
	}
}

// A name lock that is no longer held before staging stops the rotation: nothing staged, no revoke call.
func TestReissueRefusesToStageWithoutLock(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag)
	before, _ := os.ReadFile(h.creds("a"))
	recs := h.records()
	calls := fakeRevoke(t, nil)

	checks := 0
	prev := nameLockCheck
	nameLockCheck = func(context.Context, admin.NameLock) error {
		checks++
		return errors.New("the name lock for a expired")
	}
	out, errb, code := h.run("reissue", "a")
	nameLockCheck = prev
	if code != 1 || out != "" || !strings.Contains(errb, "the name lock for a expired") || checks != 1 {
		t.Fatalf("%d %q %q checks %d", code, out, errb, checks)
	}
	noSecrets(t, out, errb)
	if len(*calls) != 0 {
		t.Fatalf("revoke calls %+v", *calls)
	}
	h.noStaged("a")
	if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(before, after) {
		t.Fatal("creds file changed")
	}
	if !slices.Equal(recs, h.records()) {
		t.Fatalf("records %v", h.records())
	}
}
