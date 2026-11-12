package main

import (
	"bytes"
	"os"
	"path/filepath"
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

// A creds file that exists but cannot be checked refuses the retire with nothing changed; a missing one does not.
func TestRetireCredsCheckFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, path string)
		want   string
	}{
		{"mode 0644", func(t *testing.T, p string) {
			if err := os.Chmod(p, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "exists but cannot be read"},
		{"garbage", func(t *testing.T, p string) {
			if err := os.WriteFile(p, []byte("not a creds file\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "exists but its JWT cannot be decoded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFleetEnv(t, addPolicy)
			if _, errb, code := h.run("add", "c", "--zone", "red"); code != 0 {
				t.Fatal(errb)
			}
			tc.damage(t, h.creds("c"))
			before, _ := os.ReadFile(h.creds("c"))
			fiBefore, _ := os.Stat(h.creds("c"))
			recs := h.records()
			policyBefore := h.policyText()
			calls := fakeRevoke(t, nil)
			out, errb, code := h.run("retire", "c")
			if code != 1 || out != "" || !strings.Contains(errb, tc.want) || !strings.Contains(errb, "nothing was changed") {
				t.Fatalf("%d %q %q", code, out, errb)
			}
			noSecrets(t, out, errb)
			if strings.Contains(errb, "not a creds file") {
				t.Fatal("file content printed")
			}
			if len(*calls) != 0 {
				t.Fatalf("revoke calls %+v", *calls)
			}
			after, _ := os.ReadFile(h.creds("c"))
			fiAfter, _ := os.Stat(h.creds("c"))
			if !bytes.Equal(before, after) || fiBefore.Mode() != fiAfter.Mode() {
				t.Fatal("creds file changed")
			}
			if len(h.records()) != len(recs) || h.policyText() != policyBefore {
				t.Fatal("record or policy changed")
			}
		})
	}

	t.Run("missing file proceeds", func(t *testing.T) {
		h := newFleetEnv(t, addPolicy)
		if _, errb, code := h.run("add", "c", "--zone", "red"); code != 0 {
			t.Fatal(errb)
		}
		if err := os.Remove(h.creds("c")); err != nil {
			t.Fatal(err)
		}
		calls := fakeRevoke(t, nil)
		out, errb, code := h.run("retire", "c")
		if code != 0 || len(*calls) != 1 {
			t.Fatalf("%d %q %q calls %+v", code, out, errb, *calls)
		}
	})
}

// The creds check runs inside the guarded removal, under the name's lock: creds that become unreadable after the
// directory read and before the revoke still refuse the retire, with nothing revoked or removed.
func TestRetireCredsCheckRunsInsideLock(t *testing.T) {
	h := newFleetEnv(t, addPolicy)
	if _, errb, code := h.run("add", "c", "--zone", "red"); code != 0 {
		t.Fatal(errb)
	}
	recs := h.records()
	policyBefore := h.policyText()
	calls := fakeRevoke(t, nil)

	swapped := 0
	beforeRetireRevoke = func() {
		swapped++
		if err := os.Chmod(h.creds("c"), 0o644); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { beforeRetireRevoke = nil })
	out, errb, code := h.run("retire", "c")
	if swapped != 1 {
		t.Fatalf("seam ran %d times", swapped)
	}
	if code != 1 || out != "" || !strings.Contains(errb, "exists but cannot be read") || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if len(*calls) != 0 {
		t.Fatalf("revoke calls %+v", *calls)
	}
	if len(h.records()) != len(recs) || h.policyText() != policyBefore {
		t.Fatal("record or policy changed")
	}
}

// Creds read through a symlink are checked at the link's target: a future-dated target JWT refuses the retire with
// nothing changed, and a dangling link (no target) proceeds as a missing file does.
func TestRetireChecksSymlinkedCredsTarget(t *testing.T) {
	t.Run("future-dated target refuses", func(t *testing.T) {
		h := newFleetEnv(t, addPolicy)
		if _, errb, code := h.run("add", "c", "--zone", "red"); code != 0 {
			t.Fatal(errb)
		}
		target := filepath.Join(t.TempDir(), "mounted.creds")
		raw, err := os.ReadFile(h.creds("c"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(h.creds("c")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, h.creds("c")); err != nil {
			t.Fatal(err)
		}
		recs := h.records()
		policyBefore := h.policyText()
		calls := fakeRevoke(t, nil)

		prev := retireRevokeNow
		retireRevokeNow = func() time.Time { return time.Now().Add(-clockBehind) }
		out, errb, code := h.run("retire", "c")
		retireRevokeNow = prev
		if code != 1 || out != "" || !strings.Contains(errb, "claims it was issued in the future (") || !strings.Contains(errb, "nothing was changed") {
			t.Fatalf("%d %q %q", code, out, errb)
		}
		noSecrets(t, out, errb)
		if len(*calls) != 0 {
			t.Fatalf("revoke calls %+v", *calls)
		}
		if fi, err := os.Lstat(h.creds("c")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("symlink changed: %v", err)
		}
		if after, _ := os.ReadFile(target); !bytes.Equal(raw, after) {
			t.Fatal("target changed")
		}
		if len(h.records()) != len(recs) || h.policyText() != policyBefore {
			t.Fatal("record or policy changed")
		}
	})

	t.Run("dangling link proceeds", func(t *testing.T) {
		h := newFleetEnv(t, addPolicy)
		if _, errb, code := h.run("add", "c", "--zone", "red"); code != 0 {
			t.Fatal(errb)
		}
		if err := os.Remove(h.creds("c")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), h.creds("c")); err != nil {
			t.Fatal(err)
		}
		calls := fakeRevoke(t, nil)
		out, errb, code := h.run("retire", "c")
		// as for any symlinked creds, the retire completes and reports that the link was left in place
		if code != 1 || !strings.HasPrefix(out, "retired c\n") || !strings.Contains(errb, "not a regular file") || len(*calls) != 1 {
			t.Fatalf("%d %q %q calls %+v", code, out, errb, *calls)
		}
		if _, err := os.Lstat(h.creds("c")); err != nil {
			t.Error("the symlink itself was removed")
		}
	})
}
