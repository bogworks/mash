package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/sap"
)

// credsSeed is the seed in a creds file.
func credsSeed(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := jwt.ParseDecoratedUserNKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := kp.Seed()
	return seed
}

func refusalCode(t *testing.T, so string) string {
	t.Helper()
	d := epDocOf(t, so)
	if d.Refusal == nil {
		t.Fatalf("no refusal in %q", so)
	}
	return d.Refusal.Code
}

func gone(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Lstat(p)
	return os.IsNotExist(err)
}

// The creds file holds the staged key, but the directory record has moved on: the earlier run did not finish this
// create, so it is not reported as created.
func TestEndpointCreateMakeKeyShortcutNeedsMatchingRecord(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	args := func(a sap.Address) []string {
		return []string{"endpoint", "create", a.String(), "--make-key", "--json"}
	}

	// the record names the staged key: the run only missed the removal of the pending file
	a := ad("s1")
	if _, se, code := h.run(args(a)...); code != 0 {
		t.Fatalf("create %q", se)
	}
	if err := os.WriteFile(h.creds(a)+".pending", credsSeed(t, h.creds(a)), 0o600); err != nil {
		t.Fatal(err)
	}
	so, se, code := h.run(args(a)...)
	if code != 0 || epDocOf(t, so).Outcome != "created" || !gone(t, h.creds(a)+".pending") {
		t.Fatalf("matching record: %d %q %q", code, so, se)
	}

	// reissued to another key since
	b := ad("s2")
	if _, se, code := h.run(args(b)...); code != 0 {
		t.Fatalf("create %q", se)
	}
	oldCreds, _ := os.ReadFile(h.creds(b))
	oldSeed := credsSeed(t, h.creds(b))
	if _, se, code := h.run("endpoint", "reissue", b.String(), "--make-key"); code != 0 {
		t.Fatalf("reissue %q", se)
	}
	if err := os.WriteFile(h.creds(b), oldCreds, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.creds(b)+".pending", oldSeed, 0o600); err != nil {
		t.Fatal(err)
	}
	so, _, code = h.run(args(b)...)
	if code != 1 || refusalCode(t, so) != "address_taken" || epDocOf(t, so).Outcome == "created" {
		t.Fatalf("reissued record: %d %q", code, so)
	}
	if !gone(t, h.creds(b)+".pending") {
		t.Error("pending file left after address_taken")
	}

	// deleted since: the address is burned
	c := ad("s3")
	if _, se, code := h.run(args(c)...); code != 0 {
		t.Fatalf("create %q", se)
	}
	oldCreds, _ = os.ReadFile(h.creds(c))
	oldSeed = credsSeed(t, h.creds(c))
	if _, se, code := h.run("endpoint", "delete", c.String()); code != 0 {
		t.Fatalf("delete %q", se)
	}
	if err := os.WriteFile(h.creds(c), oldCreds, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.creds(c)+".pending", oldSeed, 0o600); err != nil {
		t.Fatal(err)
	}
	so, _, code = h.run(args(c)...)
	if code != 1 || refusalCode(t, so) != "key_retired" {
		t.Fatalf("burned: %d %q", code, so)
	}
	if !gone(t, h.creds(c)+".pending") {
		t.Error("pending file left after key_retired")
	}
}

func TestEndpointCreateStagedKeyRevoked(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("r1")
	pub, _, seed := epKey(t, t.TempDir(), "p")
	pending := h.creds(a) + ".pending"
	if err := os.WriteFile(pending, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"endpoint", "create", a.String(), "--make-key", "--json"}
	fakeKeyRevoked(t, func(p string) (bool, error) { return p == pub, nil })
	before := epSnapshot(t, h, h.dir)
	so, _, code := h.run(args...)
	if code != 1 || refusalCode(t, so) != "staged_key_revoked" || !strings.Contains(epDocOf(t, so).Refusal.Detail, "was revoked; remove "+pending+" and run the command again") {
		t.Fatalf("revoked: %d %q", code, so)
	}
	if epSnapshot(t, h, h.dir) != before {
		t.Error("a refusal changed state")
	}
	fakeKeyRevoked(t, func(string) (bool, error) { return false, errors.New("no answer") })
	so, _, code = h.run(args...)
	if code != 6 || refusalCode(t, so) != "revocation_unknown" || epSnapshot(t, h, h.dir) != before {
		t.Fatalf("unknown: %d %q", code, so)
	}
}

func TestEndpointReissueStagedKeyRevoked(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("r2")
	h.seed(a)
	pub, _, seed := epKey(t, t.TempDir(), "p")
	pending := h.creds(a) + ".pending"
	if err := os.WriteFile(pending, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"endpoint", "reissue", a.String(), "--make-key", "--json"}
	fakeKeyRevoked(t, func(p string) (bool, error) { return p == pub, nil })
	before := epSnapshot(t, h, h.dir)
	so, _, code := h.run(args...)
	if code != 1 || refusalCode(t, so) != "staged_key_revoked" {
		t.Fatalf("revoked: %d %q", code, so)
	}
	fakeKeyRevoked(t, func(string) (bool, error) { return false, errors.New("no answer") })
	so, _, code = h.run(args...)
	if code != 6 || refusalCode(t, so) != "revocation_unknown" {
		t.Fatalf("unknown: %d %q", code, so)
	}
	if epSnapshot(t, h, h.dir) != before {
		t.Error("a refusal changed state")
	}
}

// A staged key that enroll refuses for the address can never be used for it: the pending file is removed.
func TestEndpointCreateRefusalRemovesPending(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	args := func(a sap.Address) []string {
		return []string{"endpoint", "create", a.String(), "--make-key", "--json"}
	}
	_, _, seed := epKey(t, t.TempDir(), "p")

	taken := ad("t1")
	otherPub, _, _ := epKey(t, t.TempDir(), "o")
	if _, err := enrollCreate(h, taken, otherPub); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.creds(taken)+".pending", seed, 0o600); err != nil {
		t.Fatal(err)
	}
	so, _, code := h.run(args(taken)...)
	if code != 1 || refusalCode(t, so) != "address_taken" || !gone(t, h.creds(taken)+".pending") {
		t.Fatalf("taken: %d %q", code, so)
	}

	burned := ad("t2")
	h.seed(burned)
	if _, se, code := h.run("endpoint", "delete", burned.String()); code != 0 {
		t.Fatalf("delete %q", se)
	}
	if err := os.WriteFile(h.creds(burned)+".pending", seed, 0o600); err != nil {
		t.Fatal(err)
	}
	so, _, code = h.run(args(burned)...)
	if code != 1 || refusalCode(t, so) != "key_retired" || !gone(t, h.creds(burned)+".pending") {
		t.Fatalf("burned: %d %q", code, so)
	}
}

func TestEndpointBulkPartialNamesChanged(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a, b := ad("a"), ad("b")
	h.seed(a)
	h.seed(b)
	if err := os.Mkdir(h.creds(b)+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	prev := epLockWait
	epLockWait = 20 * time.Millisecond
	t.Cleanup(func() { epLockWait = prev })
	so, _, code := h.run("endpoint", "reissue", "--all", "--make-key", "--yes")
	if code != 5 || !strings.Contains(so, "rerun with --changed, not --all") {
		t.Fatalf("text: %d %q", code, so)
	}
	so, _, code = h.run("endpoint", "reissue", "--all", "--make-key", "--yes", "--json")
	d := bulkDoc(t, so)
	found := false
	for _, s := range d.NextSteps {
		if s.Detail == "mashctl endpoint reissue --changed --make-key --yes" {
			found = true
		}
		if strings.Contains(s.Detail, "--all") {
			t.Errorf("next step names --all: %+v", s)
		}
	}
	if code != 5 || !found {
		t.Fatalf("json: %d %q", code, so)
	}
	if !strings.Contains(endpointReissueUsage, "--changed") || strings.Contains(strings.ToLower(endpointReissueUsage), "rerun with --all") {
		t.Error("usage")
	}
}

func TestEndpointDeleteReportsZeroAcknowledged(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	a := ad("z1")
	h.seed(a)
	so, _, code := h.run("endpoint", "delete", a.String(), "--no-revoke", "--json")
	var m map[string]any
	if err := json.Unmarshal([]byte(so), &m); err != nil || code != 0 || m["outcome"] != "deleted" {
		t.Fatalf("%d %q", code, so)
	}
	if v, ok := m["servers_acknowledged"]; !ok || v.(float64) != 0 {
		t.Errorf("servers_acknowledged %v in %q", v, so)
	}
	b := ad("z2")
	h.seed(b)
	so, _, code = h.run("endpoint", "delete", b.String(), "--no-revoke")
	if code != 0 || !strings.Contains(so, "servers acknowledged the revocation: 0") {
		t.Fatalf("text %d %q", code, so)
	}
}
