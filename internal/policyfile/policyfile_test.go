package policyfile

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/policy"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// copyTo copies testdata/name into a temp dir and returns the path.
func copyTo(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func load(t *testing.T, p string) *Doc {
	t.Helper()
	d, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Fatalf("%s differs:\n%s", name, got)
	}
}

// changed returns the lines only in a (removed) and only in b (added), as multisets.
func changed(a, b string) (removed, added []string) {
	count := map[string]int{}
	for _, l := range strings.Split(a, "\n") {
		count[l]++
	}
	for _, l := range strings.Split(b, "\n") {
		if count[l] > 0 {
			count[l]--
		} else {
			added = append(added, l)
		}
	}
	for _, l := range strings.Split(a, "\n") {
		if count[l] > 0 {
			count[l]--
			removed = append(removed, l)
		}
	}
	return
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func TestRoundTripGolden(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	orig, _ := os.ReadFile(p)
	d := load(t, p)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(d.AddEndpoint("ledger", map[string]string{"zone": "internal", "team": "finance"}))
	must(d.SetLabels("support-bot", map[string]string{"team": "helpdesk", "tier": "1"}, []string{"zone"}))
	must(d.SetLabels("billing-agent", map[string]string{"zone": "internal"}, nil))
	must(d.SetLabels("web-intake", nil, []string{"internet"}))
	must(d.SetLabels("deploy-bot", map[string]string{"kind": "tool"}, nil))
	must(d.Save())
	got, _ := os.ReadFile(p)
	golden(t, "commented.after.golden", got)

	rem, add := changed(string(orig), string(got))
	eq(t, "removed", rem, []string{
		`    labels: {zone: dmz, internet: "yes"}`,
		`      zone: dmz`,
		`      team: support   # who answers`,
		`  deploy-bot: {labels: {zone: internal}}`,
	})
	eq(t, "added", add, []string{
		`    labels: {zone: dmz}`,
		`      team: helpdesk   # who answers`,
		`      tier: "1"`,
		`    labels: {zone: internal}`,
		`  deploy-bot: {labels: {zone: internal, kind: tool}}`,
		`  ledger:`,
		`    labels: {team: finance, zone: internal}`,
	})
	if _, err := policy.Parse(got); err != nil {
		t.Fatal(err)
	}
}

func TestSetLabelsRemoveLast(t *testing.T) {
	d := load(t, copyTo(t, "commented.yaml"))
	if err := d.SetLabels("deploy-bot", nil, []string{"zone"}); err != nil { // flow
		t.Fatal(err)
	}
	if err := d.SetLabels("support-bot", nil, []string{"zone", "team"}); err != nil { // block
		t.Fatal(err)
	}
	b, _ := d.Bytes()
	if strings.Contains(string(b), "labels: \n") || strings.Contains(string(b), "team: support") {
		t.Fatalf("%s", b)
	}
	f, err := policy.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Endpoints["support-bot"].Labels) != 0 || len(f.Endpoints["deploy-bot"].Labels) != 0 {
		t.Fatalf("%+v", f.Endpoints)
	}
	if !strings.Contains(string(b), "  deploy-bot: {}\n") || strings.Contains(string(b), "support-bot:\n    labels") {
		t.Fatalf("%s", b)
	}
}

func TestAddEndpointStyles(t *testing.T) {
	// no endpoints: key at all
	p := filepath.Join(t.TempDir(), "p.yaml")
	os.WriteFile(p, []byte("default: allow\n# end"), 0o644)
	d := load(t, p)
	if err := d.AddEndpoint("a", map[string]string{"zone": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddEndpoint("b", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := d.Bytes()
	want := "default: allow\n# end\n\nendpoints:\n  a:\n    labels: {zone: x}\n  b: {}\n"
	if string(b) != want {
		t.Fatalf("%q", b)
	}
	if err := d.AddEndpoint("a", nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("%v", err)
	}
	// values that need quotes
	if err := d.SetLabels("b", map[string]string{"flag": "yes", "n": "007", "ok": "fine"}, nil); err != nil {
		t.Fatal(err)
	}
	b, _ = d.Bytes()
	f, err := policy.Parse(b)
	if err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	if got := f.Endpoints["b"].Labels; got["flag"] != "yes" || got["n"] != "007" || got["ok"] != "fine" {
		t.Fatalf("%v\n%s", got, b)
	}
	// block-style labels in the file are followed
	d2 := load(t, copyTo(t, "block.yaml"))
	if err := d2.AddEndpoint("c", map[string]string{"k": "v", "j": "w"}); err != nil {
		t.Fatal(err)
	}
	b, _ = d2.Bytes()
	if !strings.Contains(string(b), "    c:\n      labels:\n        j: w\n        k: v\n") {
		t.Fatalf("%s", b)
	}
	if _, err := policy.Parse(b); err != nil {
		t.Fatal(err)
	}
}

func TestCreateThenAdd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "policy.yaml")
	d, err := Create(p, "deny")
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "default: deny") || !strings.Contains(string(b), "docs/policy-file.md") {
		t.Fatalf("%s", b)
	}
	d.Close()
	if _, err := Create(p, "allow"); err == nil || !strings.Contains(err.Error(), "exists, not overwritten") || !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite: %v", err)
	}
	if _, err := Create(filepath.Join(t.TempDir(), "q.yaml"), "maybe"); err == nil {
		t.Fatal("bad default accepted")
	}
	d = load(t, p)
	if err := d.AddEndpoint("a", map[string]string{"zone": "z"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	if !strings.Contains(string(b), "endpoints:\n  a:\n    labels: {zone: z}\n\nrules: []") {
		t.Fatalf("%s", b)
	}
}

func TestRemoveEndpoint(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	orig, _ := os.ReadFile(p)
	d := load(t, p)
	refs, err := d.RemoveEndpoint("support-bot")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "refs", refs, []string{"tests[1] web-intake->support-bot"})
	b, _ := d.Bytes()
	rem, add := changed(string(orig), string(b))
	eq(t, "removed", rem, []string{"  support-bot:", "    labels:", "      zone: dmz", "      team: support   # who answers"})
	eq(t, "added", add, nil)
	// the test still names it, so the result is not a valid policy and cannot be saved
	if err := d.Save(); err == nil || !strings.Contains(err.Error(), "refusing to save an invalid policy") {
		t.Fatalf("%v", err)
	}
	now, _ := os.ReadFile(p)
	if string(now) != string(orig) {
		t.Fatal("file changed")
	}
	if _, err := d.RemoveEndpoint("nope"); err == nil {
		t.Fatal("removing an unknown endpoint succeeded")
	}
	// one nothing names saves
	d2 := load(t, copyTo(t, "commented.yaml"))
	if refs, err := d2.RemoveEndpoint("deploy-bot"); err != nil || len(refs) != 0 {
		t.Fatal(refs, err)
	}
	if err := d2.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestSaveKeepsMode(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	os.Chmod(p, 0o644)
	d := load(t, p)
	if err := d.AddEndpoint("z", nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", st.Mode())
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
}

func TestSaveRenameFailureKeepsOldFile(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	orig, _ := os.ReadFile(p)
	d := load(t, p)
	if err := d.AddEndpoint("z", nil); err != nil {
		t.Fatal(err)
	}
	old := rename
	rename = func(string, string) error { return errors.New("disk on fire") }
	defer func() { rename = old }()
	if err := d.Save(); err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("%v", err)
	}
	now, _ := os.ReadFile(p)
	if string(now) != string(orig) {
		t.Fatal("the old file was changed")
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.yaml")
	os.WriteFile(p, []byte("default: maybe\n"), 0o644)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("%v", err)
	}
	// a failed Load must not leave the lock held
	os.WriteFile(p, []byte("default: allow\n"), 0o644)
	d, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
}

func TestLockContention(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	d1, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(p)
	if !errors.Is(err, ErrLocked) || !strings.HasPrefix(err.Error(), "policy file is being edited by another mashctl") || !strings.Contains(err.Error(), "leftover") {
		t.Fatalf("second Load: %v", err)
	}
	d1.Close()
	d1.Close() // idempotent
	d3, err := Load(p)
	if err != nil {
		t.Fatalf("after Close: %v", err)
	}
	d3.Close()
}

func noTemps(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
}

func TestSaveRefusesChangedFile(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	d := load(t, p)
	orig, _ := os.ReadFile(p)
	hand := append(append([]byte(nil), orig...), []byte("# hand edit\n")...)
	if err := os.WriteFile(p, hand, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := d.AddEndpoint("z", nil); err != nil {
		t.Fatal(err)
	}
	err := d.Save()
	if !errors.Is(err, ErrChanged) || !strings.Contains(err.Error(), p) {
		t.Fatalf("%v", err)
	}
	if now, _ := os.ReadFile(p); string(now) != string(hand) {
		t.Fatal("the hand edit was overwritten")
	}
	noTemps(t, filepath.Dir(p))

	// same length, same mtime: only the contents differ
	p2 := copyTo(t, "commented.yaml")
	d2 := load(t, p2)
	st, _ := os.Stat(p2)
	same, _ := os.ReadFile(p2)
	same = append([]byte(nil), same...)
	i := strings.Index(string(same), "allow")
	if i < 0 {
		i = strings.Index(string(same), "deny")
	}
	same[i], same[i+1] = same[i+1], same[i]
	os.WriteFile(p2, same, 0o600)
	os.Chtimes(p2, st.ModTime(), st.ModTime())
	if err := d2.AddEndpoint("z", nil); err != nil {
		t.Fatal(err)
	}
	if err := d2.Save(); !errors.Is(err, ErrChanged) {
		t.Fatalf("same-length change: %v", err)
	}

	// a file removed since Load
	p3 := copyTo(t, "commented.yaml")
	d3 := load(t, p3)
	os.Remove(p3)
	if err := d3.Save(); !errors.Is(err, ErrChanged) {
		t.Fatalf("removed: %v", err)
	}
}

func TestSaveTwice(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	d := load(t, p)
	for _, n := range []string{"y", "z"} {
		if err := d.AddEndpoint(n, nil); err != nil {
			t.Fatal(err)
		}
		if err := d.Save(); err != nil {
			t.Fatalf("save %s: %v", n, err)
		}
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "  y:") || !strings.Contains(string(b), "  z:") {
		t.Fatalf("%s", b)
	}
}

func TestCreateDoesNotReplaceRacingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "policy.yaml")
	beforeCreatePublish = func() { os.WriteFile(p, []byte("competing\n"), 0o600) }
	defer func() { beforeCreatePublish = nil }()
	_, err := Create(p, "deny")
	if !errors.Is(err, os.ErrExist) || !strings.Contains(err.Error(), "exists, not overwritten") {
		t.Fatalf("%v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "competing\n" {
		t.Fatalf("competing file changed: %q", b)
	}
	noTemps(t, filepath.Dir(p))
}

func TestLockFileRemovedOnClose(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	d, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".lock"); err != nil {
		t.Fatalf("lock file while held: %v", err)
	}
	d.Close()
	if _, err := os.Stat(p + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("lock file after Close: %v", err)
	}
}

func TestLeftoverLockFileIsReused(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	// a file left by a holder that died: no flock on it
	if err := os.WriteFile(p+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Load(p)
	if err != nil {
		t.Fatalf("Load with leftover lock file: %v", err)
	}
	d.Close()
	if _, err := os.Stat(p + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("leftover not removed on Close: %v", err)
	}
}

func TestSecondLoadWaitsForClose(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	d1, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Load: %v", err)
	}
	// the refused Load must not have removed the first holder's file
	if _, err := os.Stat(p + ".lock"); err != nil {
		t.Fatalf("holder's lock file gone: %v", err)
	}
	d1.Close()
	d2, err := Load(p)
	if err != nil {
		t.Fatalf("after Close: %v", err)
	}
	d2.Close()
}

func TestAcquireRetriesWhenLockFileUnlinkedBeforeFlock(t *testing.T) {
	p := copyTo(t, "commented.yaml")
	n := 0
	afterLockOpen = func() {
		if n++; n == 1 {
			os.Remove(p + ".lock") // another process closed and removed it between our open and flock
		}
	}
	t.Cleanup(func() { afterLockOpen = nil })
	d, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("opens %d, want 2", n)
	}
	if !sameLockFile(d.lock, p+".lock") {
		t.Error("held descriptor is not the file at the path")
	}
	d.Close()
	// every attempt unlinked: bounded, then ErrLocked
	afterLockOpen = func() { os.Remove(p + ".lock") }
	if _, err := Load(p); !errors.Is(err, ErrLocked) {
		t.Fatalf("endless unlink: %v", err)
	}
}

func TestSameLockFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.lock")
	f, _ := os.Create(path)
	defer f.Close()
	if !sameLockFile(f, path) {
		t.Error("same file reported different")
	}
	os.Remove(path)
	if sameLockFile(f, path) {
		t.Error("removed path reported same")
	}
	os.WriteFile(path, nil, 0o600)
	if sameLockFile(f, path) {
		t.Error("replaced path reported same")
	}
}

func TestAllowEmptySurvivesAnEdit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "policy.yaml")
	src := "default: allow\nendpoints:\n  a:\n    labels: {zone: red}\nrules:\n  - {name: later, action: deny, from: {zone: blue}, to: {}, allow_empty: true}\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	d := load(t, p)
	if err := d.SetLabels("a", map[string]string{"team": "x"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.AddEndpoint("b", map[string]string{"zone": "red"}); err != nil {
		t.Fatal(err)
	}
	b, _ := d.Bytes()
	if !strings.Contains(string(b), "allow_empty: true") {
		t.Fatalf("%s", b)
	}
	f, err := policy.Parse(b)
	if err != nil || !f.Rules[0].AllowEmpty {
		t.Fatalf("%v %+v", err, f)
	}
}
