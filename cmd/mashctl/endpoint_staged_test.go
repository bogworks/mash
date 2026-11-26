package main

import (
	"errors"
	"os"
	"strings"
	"testing"

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
		return []string{"endpoint", "create", a.String()}
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
	if code != 0 || so != "Created endpoint "+a.String()+"\nCredentials: "+h.creds(a)+"\nNext: MASH_CONFIG_DIR="+shellQuote(h.dir)+" MASH_ENDPOINT="+a.String()+" claude\n" || !gone(t, h.creds(a)+".pending") {
		t.Fatalf("matching record: %d %q %q", code, so, se)
	}

	// reissued to another key since
	b := ad("s2")
	if _, se, code := h.run(args(b)...); code != 0 {
		t.Fatalf("create %q", se)
	}
	oldCreds, _ := os.ReadFile(h.creds(b))
	oldSeed := credsSeed(t, h.creds(b))
	if _, se, code := h.run("endpoint", "reissue", b.String()); code != 0 {
		t.Fatalf("reissue %q", se)
	}
	if err := os.WriteFile(h.creds(b), oldCreds, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.creds(b)+".pending", oldSeed, 0o600); err != nil {
		t.Fatal(err)
	}
	so, se, code = h.run(args(b)...)
	if code != 1 || so != "" || se != "error: "+b.String()+" already exists\n" {
		t.Fatalf("reissued record: %d %q %q", code, so, se)
	}
	if !gone(t, h.creds(b)+".pending") {
		t.Error("pending file left after the address was found taken")
	}

	// deleted since: the address is burned
	c := ad("s3")
	if _, se, code := h.run(args(c)...); code != 0 {
		t.Fatalf("create %q", se)
	}
	oldCreds, _ = os.ReadFile(h.creds(c))
	oldSeed = credsSeed(t, h.creds(c))
	if _, se, code := h.run("endpoint", "delete", c.String(), "--yes"); code != 0 {
		t.Fatalf("delete %q", se)
	}
	if err := os.WriteFile(h.creds(c), oldCreds, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.creds(c)+".pending", oldSeed, 0o600); err != nil {
		t.Fatal(err)
	}
	so, se, code = h.run(args(c)...)
	if code != 1 || so != "" || se != "error: "+c.String()+" was deleted and its address cannot be used again\n" {
		t.Fatalf("burned: %d %q %q", code, so, se)
	}
	if !gone(t, h.creds(c)+".pending") {
		t.Error("pending file left after the address was found deleted")
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
	args := []string{"endpoint", "create", a.String()}
	fakeKeyRevoked(t, func(p string) (bool, error) { return p == pub, nil })
	before := epSnapshot(t, h, h.dir)
	so, se, code := h.run(args...)
	if code != 1 || so != "" || se != "error: the staged key "+pending+" was revoked\nhint: rm "+pending+"\n" {
		t.Fatalf("revoked: %d %q %q", code, so, se)
	}
	if epSnapshot(t, h, h.dir) != before {
		t.Error("a refusal changed state")
	}
	fakeKeyRevoked(t, func(string) (bool, error) { return false, errors.New("no answer") })
	so, se, code = h.run(args...)
	if code != 1 || !strings.Contains(se, "cannot tell whether the staged key") || epSnapshot(t, h, h.dir) != before {
		t.Fatalf("unknown: %d %q %q", code, so, se)
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
	args := []string{"endpoint", "reissue", a.String()}
	fakeKeyRevoked(t, func(p string) (bool, error) { return p == pub, nil })
	before := epSnapshot(t, h, h.dir)
	so, se, code := h.run(args...)
	if code != 1 || !strings.Contains(se, "hint: rm "+pending) {
		t.Fatalf("revoked: %d %q %q", code, so, se)
	}
	fakeKeyRevoked(t, func(string) (bool, error) { return false, errors.New("no answer") })
	so, se, code = h.run(args...)
	if code != 1 || !strings.Contains(se, "cannot tell whether the staged key") {
		t.Fatalf("unknown: %d %q %q", code, so, se)
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
		return []string{"endpoint", "create", a.String()}
	}
	_, _, seed := epKey(t, t.TempDir(), "p")

	taken := ad("t1")
	otherPub, _, _ := epKey(t, t.TempDir(), "o")
	if _, err := enrollCreateFor(h, taken, otherPub); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.creds(taken)+".pending", seed, 0o600); err != nil {
		t.Fatal(err)
	}
	so, se, code := h.run(args(taken)...)
	if code != 1 || se != "error: "+taken.String()+" already exists\n" || !gone(t, h.creds(taken)+".pending") {
		t.Fatalf("taken: %d %q %q", code, so, se)
	}

	burned := ad("t2")
	h.seed(burned)
	if _, se, code := h.run("endpoint", "delete", burned.String(), "--yes"); code != 0 {
		t.Fatalf("delete %q", se)
	}
	if err := os.WriteFile(h.creds(burned)+".pending", seed, 0o600); err != nil {
		t.Fatal(err)
	}
	so, se, code = h.run(args(burned)...)
	if code != 1 || se != "error: "+burned.String()+" was deleted and its address cannot be used again\n" || !gone(t, h.creds(burned)+".pending") {
		t.Fatalf("burned: %d %q %q", code, so, se)
	}
}

func TestEndpointStagedSeedMustBePrivate(t *testing.T) {
	h := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	a, b := ad("exposed"), ad("exposed2")
	h.seed(b)
	_, _, seed := epKey(t, t.TempDir(), "p")
	for _, p := range []string{h.creds(a) + ".pending", h.creds(b) + ".pending"} {
		if err := os.WriteFile(p, seed, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := epSnapshot(t, h, h.dir)
	for _, args := range [][]string{
		{"endpoint", "create", a.String()},
		{"endpoint", "reissue", b.String()},
	} {
		_, se, code := h.run(args...)
		if code == 0 || !strings.Contains(se, "exposes the seed") {
			t.Fatalf("%v: %d %q", args, code, se)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("a key was revoked for an exposed staged seed: %v", *calls)
	}
	if epSnapshot(t, h, h.dir) != before {
		t.Error("state changed")
	}
}

func TestEpOwnedByCaller(t *testing.T) {
	p := t.TempDir() + "/f"
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !epOwnedByCaller(fi) {
		t.Error("a file this process made is not owned by the caller")
	}
}

func TestEndpointShortcutNeedsASeedInTheCreds(t *testing.T) {
	h := newFleetEnv(t)
	fakeRevoke(t, nil)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	a := ad("noseed")
	// a finished create: the record names the key and the creds file holds the JWT and the seed
	if _, se, code := h.run("endpoint", "create", a.String()); code != 0 {
		t.Fatalf("create: %q", se)
	}
	seed := credsSeed(t, h.creds(a))
	pending := h.creds(a) + ".pending"
	if err := os.WriteFile(pending, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	// the creds file loses its seed: only the JWT is left
	raw, err := os.ReadFile(h.creds(a))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.creds(a), []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	so, se, code := h.run("endpoint", "create", a.String())
	if code != 1 || so != "" || se != "error: "+h.creds(a)+" already exists\n" {
		t.Fatalf("%d %q %q", code, so, se)
	}
	if _, err := os.Stat(pending); err != nil {
		t.Errorf("the staged seed was removed: %v", err)
	}
}
