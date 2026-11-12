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

// seedWith writes a creds file for name as an earlier add would have, with the tag edit applied to its compiled tags,
// and provisions its directory record with the same user key and the endpoint's zone.
func (h *addEnv) seedWith(name string, edit func([]string) []string) {
	h.t.Helper()
	data, err := os.ReadFile(h.policy)
	if err != nil {
		h.t.Fatal(err)
	}
	_, c, err := compileBytes(data, h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	zone := ""
	for i := range c.Endpoints {
		if c.Endpoints[i].Name == name {
			zone = c.Endpoints[i].Zone
			c.Endpoints[i].Tags = edit(slices.Clone(c.Endpoints[i].Tags))
		}
	}
	m, err := mint.Mint(h.keys(), c, name, nil, true)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(h.creds(name), m.Creds, 0o600); err != nil {
		h.t.Fatal(err)
	}
	adm, err := admin.New(h.e.Conn(h.t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := adm.Provision(context.Background(), name, m.UserPub, m.AccountPub, "", admin.Meta{Zone: zone}); err != nil {
		h.t.Fatal(err)
	}
}

func extraTag(tags []string) []string { return append(tags, "allow:gone.x") } // a tag the policy no longer gives
func noDenyNone(tags []string) []string {
	return slices.DeleteFunc(tags, func(s string) bool { return s == admin.NoDenyTag })
}

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

type signerCall struct{ pub, role string }

// fakeSignerPublished replaces the published-signer seam; every call returns published, err and is recorded.
func fakeSignerPublished(t *testing.T, published bool, err error) *[]signerCall {
	t.Helper()
	var calls []signerCall
	prev := signerPublished
	signerPublished = func(_ context.Context, _ config.Config, _ func(string) string, _ int, pub, role string) (bool, error) {
		calls = append(calls, signerCall{pub, role})
		return published, err
	}
	t.Cleanup(func() { signerPublished = prev })
	return &calls
}

// A reissue, rotating or not, refuses when the zone key that would sign the new JWT is not published, before it
// stages, revokes or writes anything.
func TestReissueRefusesUnpublishedZoneKey(t *testing.T) {
	for _, rotating := range []bool{true, false} {
		h := newAddEnv(t, addPolicy)
		if rotating {
			h.seedWith("a", extraTag)
		} else {
			h.seedWith("a", noDenyNone)
		}
		_, oldPub := h.credsClaims("a")
		before, _ := os.ReadFile(h.creds("a"))
		calls := fakeRevoke(t, nil)
		sig := fakeSignerPublished(t, false, nil)

		out, errb, code := h.run("reissue", "a")
		if code != 1 || out != "" || !strings.Contains(errb, "is not in the account JWT on every server: run `mashctl policy apply` first; nothing was changed") {
			t.Fatalf("rotating=%v: %d %q %q", rotating, code, out, errb)
		}
		noSecrets(t, out, errb)
		zk, _ := h.keys().ZoneKey("red", false)
		zpub, _ := zk.PublicKey()
		if len(*sig) != 1 || (*sig)[0] != (signerCall{zpub, "red"}) {
			t.Fatalf("signer calls %+v", *sig)
		}
		if len(*calls) != 0 {
			t.Errorf("revoke calls %+v", *calls)
		}
		if _, err := os.Stat(h.creds("a") + ".next"); !os.IsNotExist(err) {
			t.Error("a staged file was written")
		}
		if after, _ := os.ReadFile(h.creds("a")); !bytes.Equal(before, after) {
			t.Fatal("creds file changed")
		}
		if recs := h.records(); len(recs) != 1 || recs[0] != "a/red/"+oldPub {
			t.Fatalf("records %v", recs)
		}
		// not knowing is a refusal too
		fakeSignerPublished(t, false, errors.New("only 1 of 2 servers answered"))
		if _, errb, code := h.run("reissue", "a"); code != 1 || !strings.Contains(errb, "cannot tell whether the zone key") || !strings.Contains(errb, "nothing was changed") {
			t.Fatalf("unknown: %d %q", code, errb)
		}
	}
}

func TestReissueRevokedKeyRotates(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", noDenyNone) // add-only change, record present
	_, oldPub := h.credsClaims("a")
	calls := fakeRevoke(t, nil)
	revCalls := fakeKeyRevoked(t, true, nil) // fakeRevoke resets the seam to "not revoked"

	out, errb, code := h.run("reissue", "a")
	if code != 0 || out != "reissued a (tags: deny:_none; new user key, old key revoked)\n" {
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
	if recs := h.records(); len(recs) != 1 || recs[0] != "a/red/"+newPub {
		t.Fatalf("records %v, want user %s", recs, newPub)
	}
}

func TestReissueRefusesWhenRevocationUnknown(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", noDenyNone)
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
	if recs := h.records(); len(recs) != 1 || recs[0] != "a/red/"+oldPub {
		t.Fatalf("records %v", recs)
	}
}

func TestReissueNarrowingRotatesKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag)
	_, oldPub := h.credsClaims("a")
	calls := fakeRevoke(t, nil)

	out, errb, code := h.run("reissue", "a")
	if code != 0 || out != "reissued a (tags: deny:_none; new user key, old key revoked)\n" {
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
	if recs := h.records(); len(recs) != 1 || recs[0] != "a/red/"+newPub {
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
	h.seedWith("b", noDenyNone) // only a tag to add: keeps its key
	_, oldB := h.credsClaims("b")
	calls := fakeRevoke(t, nil)
	out, errb, code := h.run("reissue", "--changed")
	if code != 0 || !strings.Contains(out, "reissued a (tags: deny:_none; new user key, old key revoked)\n") || !strings.Contains(out, "reissued b (tags: deny:_none)\n") {
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
	if recs := h.records(); len(recs) != 1 || recs[0] != "a/red/"+newPub {
		t.Fatalf("records %v", recs)
	}
}

func TestReissueAddOnlyKeepsKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", noDenyNone)
	_, oldPub := h.credsClaims("a")
	calls := fakeRevoke(t, errors.New("must not be called"))

	out, errb, code := h.run("reissue", "a")
	if code != 0 || out != "reissued a (tags: deny:_none)\n" {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, pub := h.credsClaims("a"); pub != oldPub {
		t.Error("key changed")
	}
	if recs := h.records(); len(recs) != 1 || recs[0] != "a/red/"+oldPub {
		t.Fatalf("records %v", recs)
	}
	if len(*calls) != 0 {
		t.Errorf("revoke calls %+v", *calls)
	}
}

func TestReissueRefusesWithoutMatchingRecord(t *testing.T) {
	for _, narrow := range []bool{false, true} {
		edit := noDenyNone
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
			if code != 1 || out != "" || !strings.Contains(errb, "reissue a:") || !strings.Contains(errb, "needs the server") || !strings.Contains(errb, "nothing was changed") {
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

// denyPolicy is addPolicy plus a rule that gives team x a real deny tag toward zone blue.
var denyPolicy = strings.Replace(strings.Replace(addPolicy, "  b:\n    labels: {zone: blue}\n", "  b:\n    labels: {zone: blue}\n  b2:\n    labels: {zone: blue, tier: secret}\n", 1), "\ntests: []", "  - {name: red-blue, action: allow, from: {zone: red}, to: {zone: blue}}\n  - {name: no-b, action: deny, from: {team: x}, to: {tier: secret}}\n\ntests: []", 1)

// withoutRealDeny drops the endpoint's real deny tags, as if its creds file were minted before the deny rule.
func withoutRealDeny(tags []string) []string {
	return slices.DeleteFunc(tags, func(s string) bool { return strings.HasPrefix(s, "deny:") && s != admin.NoDenyTag })
}

func TestReissueAddedAllowKeepsKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", func(tags []string) []string {
		return slices.DeleteFunc(tags, func(s string) bool { return strings.HasPrefix(s, "allow:") })
	})
	_, oldPub := h.credsClaims("a")
	calls := fakeRevoke(t, errors.New("must not be called"))
	out, errb, code := h.run("reissue", "a")
	if code != 0 || strings.Contains(out, "old key revoked") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, pub := h.credsClaims("a"); pub != oldPub || len(*calls) != 0 {
		t.Errorf("key changed or revoked: %v", *calls)
	}
}

func TestReissueAddedDenyRotates(t *testing.T) {
	h := newAddEnv(t, denyPolicy)
	h.seedWith("a", withoutRealDeny)
	uc, oldPub := h.credsClaims("a")
	if slices.ContainsFunc([]string(uc.Tags), func(s string) bool { return strings.HasPrefix(s, "deny:") && s != admin.NoDenyTag }) {
		t.Fatal("seed holds a real deny tag")
	}
	calls := fakeRevoke(t, nil)
	out, errb, code := h.run("reissue", "a")
	if code != 0 || !strings.Contains(out, "new user key, old key revoked") || !strings.Contains(out, "deny:blue.") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	noSecrets(t, out, errb)
	_, newPub := h.credsClaims("a")
	if newPub == oldPub || len(*calls) != 1 || !slices.Equal((*calls)[0].users, []string{oldPub}) {
		t.Fatalf("new %s old %s calls %+v", newPub, oldPub, *calls)
	}
	if recs := h.records(); !slices.Contains(recs, "a/red/"+newPub) {
		t.Fatalf("records %v", recs)
	}
}

func TestReissueAddedNoDenyPlaceholderKeepsKey(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", noDenyNone) // only deny:_none is missing
	_, oldPub := h.credsClaims("a")
	calls := fakeRevoke(t, errors.New("must not be called"))
	if out, errb, code := h.run("reissue", "a"); code != 0 || strings.Contains(out, "old key revoked") {
		t.Fatalf("%d %q %q", code, out, errb)
	}
	if _, pub := h.credsClaims("a"); pub != oldPub || len(*calls) != 0 {
		t.Errorf("key changed or revoked: %v", *calls)
	}
}

// TestReissueRefusesCredsOfAnotherAccount: a JWT issued for another account cannot be revoked from this one.
func TestReissueRefusesCredsOfAnotherAccount(t *testing.T) {
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
	uc.IssuerAccount = otherPub
	zkp, err := h.keys().ZoneKey("red", false)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := uc.Encode(zkp)
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
	if len(*calls) != 0 || !slices.Equal(recs, h.records()) || !slices.Contains(h.records(), "a/red/"+oldPub) {
		t.Errorf("calls %+v records %v", *calls, h.records())
	}
}
