package endpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSpoolFolderUnwritable(t *testing.T) {
	old := DirAccessible
	t.Cleanup(func() { DirAccessible = old })
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, dir  string
		accessible bool
		want       bool
	}{
		{"existing writable dir", root, true, false},
		{"missing dir under a writable parent", filepath.Join(root, "a", "b", "c"), true, false},
		{"file where a parent should be", filepath.Join(root, "file", "b", "c"), true, true},
		{"dangling symlink in the path", filepath.Join(root, "dangling", "b"), true, true},
		{"symlink to a directory", filepath.Join(root, "link", "b"), true, false},
		{"not writable by the system", filepath.Join(root, "a", "b"), false, true},
	} {
		DirAccessible = func(string) bool { return c.accessible }
		if got := SpoolFolderUnwritable(c.dir); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
