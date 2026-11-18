package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func snapHome(t *testing.T) Home {
	t.Helper()
	d := t.TempDir()
	for _, sub := range []string{"state", "tmp"} {
		if err := os.Mkdir(filepath.Join(d, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return Home{Dir: d}
}

func filesDiffer(t *testing.T, h Home, exclude []string, change func()) (changed bool, bad []string) {
	t.Helper()
	b, bad1 := treeHashes([]Home{h}, exclude)
	change()
	a, bad2 := treeHashes([]Home{h}, exclude)
	return Snapshot{Hash: b}.Diff(Snapshot{Hash: a}) != nil, append(bad1, bad2...)
}

func TestTreeHashesNoChangePasses(t *testing.T) {
	h := snapHome(t)
	os.WriteFile(filepath.Join(h.Dir, "state", "x"), []byte("1"), 0o600)
	if changed, bad := filesDiffer(t, h, nil, func() {}); changed || len(bad) != 0 {
		t.Fatalf("no change: changed %v bad %v", changed, bad)
	}
}

func TestTreeHashesUnreadableFileFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	h := snapHome(t)
	p := filepath.Join(h.Dir, "secret")
	os.WriteFile(p, []byte("1"), 0o600)
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o600) })
	hashes, bad := treeHashes([]Home{h}, nil)
	if len(bad) != 1 || !strings.Contains(bad[0], p) {
		t.Fatalf("want one failed read naming %s, got %v", p, bad)
	}
	if len(hashes) != 0 {
		t.Fatalf("an unreadable tree got a hash value: %v", hashes)
	}
}

func TestTreeHashesFormerExclusionsAreCompared(t *testing.T) {
	for _, rel := range []string{"state/x", "tmp/y", "state/sub/z"} {
		h := snapHome(t)
		os.MkdirAll(filepath.Join(h.Dir, filepath.Dir(rel)), 0o700)
		changed, bad := filesDiffer(t, h, nil, func() { os.WriteFile(filepath.Join(h.Dir, rel), []byte("new"), 0o600) })
		if !changed || len(bad) != 0 {
			t.Errorf("a new file %s was not seen as a change (changed %v, bad %v)", rel, changed, bad)
		}
	}
}

func TestTreeHashesExactExclusionOnly(t *testing.T) {
	h := snapHome(t)
	ex := []string{"state/connection.json"}
	if changed, _ := filesDiffer(t, h, ex, func() { os.WriteFile(filepath.Join(h.Dir, "state", "connection.json"), []byte("a"), 0o600) }); changed {
		t.Error("the excluded path counted as a change")
	}
	if changed, _ := filesDiffer(t, h, ex, func() { os.WriteFile(filepath.Join(h.Dir, "state", "other"), []byte("a"), 0o600) }); !changed {
		t.Error("another file under state was not seen as a change")
	}
}
