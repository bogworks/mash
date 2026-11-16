package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
)

// The refusal text is pinned: tests, docs and the CLI face of the governed path all name this one command.
func TestListSetRefusalTextIsPinned(t *testing.T) {
	const addWant = "a list set is in force (generation 3): add would mint an owner-wide credential the list set does not govern; " +
		"get a governed one with: mashctl credential issue --address OWNER.NAME --endpoint cc.OWNER.NAME --public-key <the endpoint's public key>; nothing was changed"
	if got := listSetRefusal("add", 3); got != addWant {
		t.Fatalf("got\n%s\nwant\n%s", got, addWant)
	}
	if got, want := listSetRefusal("reissue", 12), strings.Replace(strings.Replace(addWant, "add would", "reissue would", 1), "generation 3", "generation 12", 1); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// snapshotTree is every file under dir with its content, by relative path.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func standInListSetGuard(t *testing.T, gen uint64, readErr error) {
	t.Helper()
	listSetGuard = func(_ context.Context, _ *admin.Admin, _, verb string) error {
		if readErr != nil {
			return errors.New("cannot tell whether a list set is in force (" + readErr.Error() + "); " + verb + " changed nothing")
		}
		return errors.New(listSetRefusal(verb, gen))
	}
	t.Cleanup(func() { listSetGuard = listSetGuardCheck })
}

// With no policy bucket the real check finds no list set in force, so add and reissue work as before.
func TestListSetGuardPassesWhenNoListSetIsInForce(t *testing.T) {
	h := newAddEnv(t)
	if out, errb, code := h.run("add", "x"); code != 0 {
		t.Fatalf("add: %d %s %s", code, out, errb)
	}
	// reissue passes the guard too: this server has no system account, so it stops later, on a different refusal.
	if _, errb, _ := h.run("reissue", "x"); strings.Contains(errb, "list set") {
		t.Fatalf("reissue refused for a list set: %s", errb)
	}
}

// While a list set is in force, add and reissue refuse before any key, record or file is made, and change nothing.
func TestAddAndReissueRefuseWhileAListSetIsInForce(t *testing.T) {
	h := newAddEnv(t)
	if out, errb, code := h.run("add", "x"); code != 0 {
		t.Fatalf("add x: %d %s %s", code, out, errb)
	}
	keysBefore, dirBefore, recsBefore := snapshotTree(t, h.env["MASH_KEYS_DIR"]), snapshotTree(t, h.dir), h.records()
	standInListSetGuard(t, 4, nil)

	out, errb, code := h.run("add", "y")
	want := "a list set is in force (generation 4): add would mint an owner-wide credential the list set does not govern; " +
		"get a governed one with: " + governedCredentialCommand + "; nothing was changed"
	if code != 1 || !strings.Contains(errb, want) || strings.Contains(out, "added") {
		t.Fatalf("add: %d\n%s\n%s", code, out, errb)
	}
	if _, err := os.Lstat(h.creds("y")); err == nil {
		t.Fatal("a refused add wrote a creds file")
	}
	for _, args := range [][]string{{"reissue", "x"}, {"reissue", "--all", "--yes"}} {
		out, errb, code = h.run(args...)
		wantR := strings.Replace(want, "add would", "reissue would", 1)
		if code != 1 || !strings.Contains(errb, wantR) || strings.Contains(out, "reissued") {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
	}
	if got := snapshotTree(t, h.env["MASH_KEYS_DIR"]); !sameFiles(got, keysBefore) {
		t.Fatal("a refused command changed the key store")
	}
	if got := snapshotTree(t, h.dir); !sameFiles(got, dirBefore) {
		t.Fatalf("a refused command changed the config directory: %v", got)
	}
	if got := h.records(); !slices.Equal(got, recsBefore) {
		t.Fatalf("directory records %v, were %v", got, recsBefore)
	}
}

// A ledger that cannot be read refuses too: the guard fails closed, with an error that says nothing was changed.
func TestAddAndReissueRefuseWhenTheListSetCannotBeRead(t *testing.T) {
	h := newAddEnv(t)
	if _, _, code := h.run("add", "x"); code != 0 {
		t.Fatal("add x")
	}
	dirBefore, recsBefore := snapshotTree(t, h.dir), h.records()
	standInListSetGuard(t, 0, errors.New("ledger unreadable"))
	for _, args := range [][]string{{"add", "y"}, {"reissue", "x"}} {
		out, errb, code := h.run(args...)
		if code != 1 || !strings.Contains(errb, "cannot tell whether a list set is in force") || !strings.Contains(errb, "changed nothing") || strings.Contains(out, "added") {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errb)
		}
	}
	if !sameFiles(snapshotTree(t, h.dir), dirBefore) || !slices.Equal(h.records(), recsBefore) {
		t.Fatal("a refused command changed something")
	}
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
