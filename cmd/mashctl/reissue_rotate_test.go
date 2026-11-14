package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/mint"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// seedWith writes a creds file for name as an earlier add would have (signed by the account key, with the endpoint's
// permissions), then applies edit to the claims and signs them again with the account key; a nil edit leaves it as a
// mint made it. It provisions the directory record with the same user key.
func (h *addEnv) seedWith(name string, edit func(*jwt.UserClaims)) {
	h.t.Helper()
	data, err := os.ReadFile(h.policy)
	if err != nil {
		h.t.Fatal(err)
	}
	_, c, err := compileBytes(data, h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	m, err := mint.Mint(h.keys(), c, name, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	creds := m.Creds
	if edit != nil {
		uc, err := jwt.DecodeUserClaims(m.JWT)
		if err != nil {
			h.t.Fatal(err)
		}
		edit(uc)
		akp, _, err := h.keys().Account()
		if err != nil {
			h.t.Fatal(err)
		}
		tok, err := uc.Encode(akp)
		if err != nil {
			h.t.Fatal(err)
		}
		kp, err := jwt.ParseDecoratedNKey(m.Creds)
		if err != nil {
			h.t.Fatal(err)
		}
		seed, _ := kp.Seed()
		if creds, err = jwt.FormatUserConfig(tok, seed); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := os.WriteFile(h.creds(name), creds, 0o600); err != nil {
		h.t.Fatal(err)
	}
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := adm.Provision(context.Background(), name, m.UserPub, m.AccountPub, "", admin.Meta{}); err != nil {
		h.t.Fatal(err)
	}
}

// extraTag makes a creds file that carries a tag the account key never gives: stale, and a reissue rotates its key.
func extraTag(uc *jwt.UserClaims) { uc.Tags.Add("allow:gone.x") }

type revokeCall struct {
	users []string
	at    time.Time // the revocation time the caller passed; zero when it passed none
}

// fakeRevoke replaces the revocation seam for the test; err, when set, is what every call returns.
func fakeRevoke(t *testing.T, err error) *[]revokeCall {
	t.Helper()
	var calls []revokeCall
	prev := revokeUsers
	revokeUsers = func(_ context.Context, _ config.Config, _ func(string) string, _ int, users []string, _ io.Writer, now func() time.Time) error {
		c := revokeCall{users: slices.Clone(users)}
		if now != nil {
			c.at = now()
		}
		calls = append(calls, c)
		return err
	}
	t.Cleanup(func() { revokeUsers = prev })
	fakeKeyRevoked(t, false, nil) // a reissue that keeps its key first asks whether the key is revoked
	return &calls
}

// fakeKeyRevoked replaces the revoked-key seam; every call returns revoked, err and is recorded by public key.
func fakeKeyRevoked(t *testing.T, revoked bool, err error) *[]string {
	t.Helper()
	var calls []string
	prev := keyRevoked
	keyRevoked = func(_ context.Context, _ config.Config, _ func(string) string, _ int, pub string) (bool, error) {
		calls = append(calls, pub)
		return revoked, err
	}
	t.Cleanup(func() { keyRevoked = prev })
	return &calls
}

func TestReissueRevokedKeyRotates(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a") // add-only change, record present
	_, oldPub := h.credsClaims("a")
	calls := fakeRevoke(t, nil)
	revCalls := fakeKeyRevoked(t, true, nil) // fakeRevoke resets the seam to "not revoked"

	out, errb, code := h.run("reissue", "a")
	if code != 0 || out != "reissued a (new user key, old key revoked)\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if len(*revCalls) != 1 || (*revCalls)[0] != oldPub {
		t.Fatalf("keyRevoked calls %v", *revCalls)
	}
	_, newPub := h.credsClaims("a")
	if newPub == oldPub {
		t.Fatal("a revoked key was re-signed")
	}
	if len(*calls) != 1 || !slices.Equal((*calls)[0].users, []string{oldPub}) {
		t.Fatalf("revoke calls %+v, want one for %s", *calls, oldPub)
	}
	if recs := h.records(); len(recs) != 1 || recs[0] != "a//"+newPub {
		t.Fatalf("records %v, want user %s", recs, newPub)
	}
}

func TestReissueRefusesWhenRevocationUnknown(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	_, oldPub := h.credsClaims("a")
	before, _ := os.ReadFile(h.creds("a"))
	calls := fakeRevoke(t, nil)
	fakeKeyRevoked(t, false, errors.New("no system account credentials: x"))

	out, errb, code := h.run("reissue", "a")
	if code != 1 || out != "" || !strings.Contains(errb, "cannot tell whether the user key "+oldPub[:8]+"... is revoked: no system account credentials") || !strings.Contains(errb, "nothing was changed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(before, after) {
		t.Fatal("creds file changed")
	}
	if len(*calls) != 0 {
		t.Errorf("revoke calls %+v", *calls)
	}
	if recs := h.records(); len(recs) != 1 || recs[0] != "a//"+oldPub {
		t.Fatalf("records %v", recs)
	}
}

func TestReissueNarrowingRotatesKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag)
	_, oldPub := h.credsClaims("a")
	calls := fakeRevoke(t, nil)

	out, errb, code := h.run("reissue", "a")
	if code != 0 || out != "reissued a (new user key, old key revoked)\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	uc, newPub := h.credsClaims("a")
	if newPub == oldPub {
		t.Fatal("the user key was kept")
	}
	if slices.Contains([]string(uc.Tags), "allow:gone.x") {
		t.Errorf("tags %v", uc.Tags)
	}
	if len(*calls) != 1 || !slices.Equal((*calls)[0].users, []string{oldPub}) {
		t.Fatalf("revoke calls: %+v, want one for %s", *calls, oldPub)
	}
	if recs := h.records(); len(recs) != 1 || recs[0] != "a//"+newPub {
		t.Fatalf("records %v, want user %s", recs, newPub)
	}
	// now current: nothing to reissue, no further revocation
	if out, _, code := h.run("reissue", "--changed"); code != 0 || out != "nothing to reissue\n" {
		t.Fatalf("--changed: %d %q", code, out)
	}
	if len(*calls) != 1 {
		t.Errorf("revoke calls %+v", *calls)
	}
}

func TestReissueChangedRotatesPerEndpoint(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag)
	h.seed("b") // up to date: not selected, keeps its key
	_, oldB := h.credsClaims("b")
	calls := fakeRevoke(t, nil)
	out, errb, code := h.run("reissue", "--changed")
	if code != 0 || !strings.Contains(out, "reissued a (new user key, old key revoked)\n") || strings.Contains(out, "reissued b") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, pub := h.credsClaims("b"); pub != oldB {
		t.Error("b's key changed")
	}
	if len(*calls) != 1 {
		t.Errorf("revoke calls %+v", *calls)
	}
}

func TestReissueRevokeFailureLeavesCreds(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag)
	_, oldPub := h.credsClaims("a")
	before, _ := os.ReadFile(h.creds("a"))
	calls := fakeRevoke(t, errors.New("not confirmed"))

	out, errb, code := h.run("reissue", "a")
	if code != 1 || out != "" || !strings.Contains(errb, "reissue a:") || !strings.Contains(errb, "not confirmed (") || !strings.Contains(errb, "run the reissue again") || !strings.Contains(errb, "not confirmed") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(before, after) {
		t.Fatal("creds file changed")
	}
	if len(*calls) != 1 {
		t.Fatalf("calls %+v", *calls)
	}
	// the record already names a key that was never written; the old creds are still stale

	// a re-run, with the revocation working, finishes the job
	ok := fakeRevoke(t, nil)
	out, errb, code = h.run("reissue", "a")
	if code != 0 || !strings.Contains(out, "old key revoked") {
		t.Fatalf("re-run: %d %q %q", code, out, errb)
	}
	if len(*ok) != 1 || !slices.Equal((*ok)[0].users, []string{oldPub}) {
		t.Fatalf("re-run revoke calls %+v", *ok)
	}
	_, newPub := h.credsClaims("a")
	if newPub == oldPub {
		t.Error("key kept")
	}
	if recs := h.records(); len(recs) != 1 || recs[0] != "a//"+newPub {
		t.Fatalf("records %v", recs)
	}
}

func TestReissueAddOnlyKeepsKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	_, oldPub := h.credsClaims("a")
	calls := fakeRevoke(t, errors.New("must not be called"))

	out, errb, code := h.run("reissue", "a")
	if code != 0 || out != "reissued a\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, pub := h.credsClaims("a"); pub != oldPub {
		t.Error("key changed")
	}
	if recs := h.records(); len(recs) != 1 || recs[0] != "a//"+oldPub {
		t.Fatalf("records %v", recs)
	}
	if len(*calls) != 0 {
		t.Errorf("revoke calls %+v", *calls)
	}
}

func TestReissueRefusesWithoutMatchingRecord(t *testing.T) {
	for _, narrow := range []bool{false, true} {
		var edit func(*jwt.UserClaims)
		if narrow {
			edit = extraTag
		}
		t.Run(map[bool]string{false: "add-only", true: "narrowing"}[narrow], func(t *testing.T) {
			// no record: a leftover creds file of a retired name
			h := newAddEnv(t, addPolicy)
			h.seedWith("a", edit)
			before, _ := os.ReadFile(h.creds("a"))
			calls := fakeRevoke(t, nil)
			adm, err := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
			if err != nil {
				t.Fatal(err)
			}
			if err := adm.Deprovision(context.Background(), "a"); err != nil {
				t.Fatal(err)
			}
			out, errb, code := h.run("reissue", "a")
			if code != 1 || out != "" || !strings.Contains(errb, "a has no directory record (retired or never added): add it again; a leftover creds file is not reissued") {
				t.Fatalf("no record: %d %q %q", code, out, errb)
			}
			noSecrets(t, out, errb)
			if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(before, after) {
				t.Fatal("creds file changed")
			}
			if len(*calls) != 0 || len(h.records()) != 0 {
				t.Errorf("calls %+v records %v", *calls, h.records())
			}
		})
		t.Run(map[bool]string{false: "add-only user differs", true: "narrowing user differs"}[narrow], func(t *testing.T) {
			// the record names another user key than the creds file's seed
			h := newAddEnv(t, addPolicy)
			h.seedWith("a", edit)
			before, _ := os.ReadFile(h.creds("a"))
			calls := fakeRevoke(t, nil)
			_, other := h.e.Mint(t, "other")
			adm, err := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
			if err != nil {
				t.Fatal(err)
			}
			_, oldPub := h.credsClaims("a")
			if _, err := adm.SetUser(context.Background(), "a", h.accountPub(), oldPub, other); err != nil {
				t.Fatal(err)
			}
			recs := h.records()
			out, errb, code := h.run("reissue", "a")
			if code != 1 || out != "" || !strings.Contains(errb, "names user key "+other[:8]+"...") || !strings.Contains(errb, "not reissued") {
				t.Fatalf("%d %q %q", code, out, errb)
			}
			noSecrets(t, out, errb)
			if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(before, after) {
				t.Fatal("creds file changed")
			}
			if len(*calls) != 0 || !slices.Equal(recs, h.records()) {
				t.Errorf("calls %+v records %v", *calls, h.records())
			}
		})
	}
}

func (h *addEnv) accountPub() string {
	h.t.Helper()
	akp, pub, err := h.keys().Account()
	if err != nil {
		h.t.Fatal(err)
	}
	akp.Wipe()
	return pub
}

func TestReissueRotationNeedsServer(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(h *addEnv)
	}{
		{"unreachable server", func(h *addEnv) { h.env["MASH_URL"] = "nats://127.0.0.1:1" }},
		{"no admin creds", func(h *addEnv) { delete(h.env, "MASH_ADMIN_CREDS") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAddEnv(t, addPolicy)
			h.seedWith("a", extraTag)
			before, _ := os.ReadFile(h.creds("a"))
			calls := fakeRevoke(t, nil)
			tc.edit(h)
			out, errb, code := h.run("reissue", "a")
			// The adoption guard asks the server first (REQ-016): with a server configured and no way to ask, it refuses.
			if code != 1 || out != "" || !strings.Contains(errb, "reissue is refused: cannot tell whether this account is adopted") || !strings.Contains(errb, "nothing was changed") {
				t.Fatalf("%d %q %q", code, out, errb)
			}
			if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(before, after) {
				t.Fatal("creds file changed")
			}
			if len(*calls) != 0 {
				t.Errorf("revoke calls %+v", *calls)
			}
		})
	}
}

// TestReissueRefusesCredsOfAnotherAccount: a JWT issued for another account cannot be revoked from this one, whether
// it names that account in issuer_account (a signing key's JWT) or is signed directly by the other account's key (no
// issuer_account: the issuer is the account).
func TestReissueRefusesCredsOfAnotherAccount(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "issuer_account", true: "signed directly"}[direct], func(t *testing.T) {
			reissueRefusesCredsOfAnotherAccount(t, direct)
		})
	}
}

func reissueRefusesCredsOfAnotherAccount(t *testing.T, direct bool) {
	h := newAddEnv(t, addPolicy)
	h.seed("a")
	raw, err := os.ReadFile(h.creds("a"))
	if err != nil {
		t.Fatal(err)
	}
	kp, err := jwt.ParseDecoratedNKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	uc, oldPub := h.credsClaims("a")
	other, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	otherPub, _ := other.PublicKey()
	signer := other
	if !direct {
		uc.IssuerAccount = otherPub
		akp, _, err := h.keys().Account()
		if err != nil {
			t.Fatal(err)
		}
		signer = akp
	}
	tok, err := uc.Encode(signer)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := jwt.FormatUserConfig(tok, seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.creds("a"), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	recs := h.records()
	calls := fakeRevoke(t, nil)

	out, errb, code := h.run("reissue", "a")
	want := "a's creds file was issued for another account (" + otherPub[:8] + "...); mashctl can only revoke in this account, so it cannot cut that JWT off: revoke it where it was issued, then retire and add a again"
	if code != 1 || out != "" || !strings.Contains(errb, want) {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(creds, after) {
		t.Error("creds file changed")
	}
	if len(*calls) != 0 || !slices.Equal(recs, h.records()) || !slices.Contains(h.records(), "a//"+oldPub) {
		t.Errorf("calls %+v records %v", *calls, h.records())
	}
}
