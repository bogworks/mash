package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// treeSnapshot is every path under dir with its mode and, for a file, its bytes.
func treeSnapshot(t *testing.T, dir string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := p + " " + info.Mode().String()
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			line += " " + string(b)
		}
		paths = append(paths, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return strings.Join(paths, "\n")
}

// A command line that is wrong attempts nothing: exit 2, one error line, one hint, and the folder is as it was.
func TestSetupCommandsWithAWrongCommandLineChangeNothing(t *testing.T) {
	dir := chdirTemp(t)
	cases := [][]string{
		{"key", "create", "--out"},
		{"key", "create", "extra"},
		{"key", "create", "--no-such-flag"},
		{"join"},
		{"join", "a.join", "b.join"},
		{"join", "a.join", "--no-such-flag"},
		{"join", "a.join", "--key"},
	}
	before := treeSnapshot(t, dir)
	for _, args := range cases {
		rc, out, errb := kcRun(t, args...)
		lines := strings.Split(strings.TrimSuffix(errb, "\n"), "\n")
		if rc != 2 || out != "" || !strings.HasPrefix(lines[0], "error: ") || (len(lines) != 2 || !strings.HasPrefix(lines[1], "hint: mash ")) && !strings.HasPrefix(lines[0], "error: unknown flag ") {
			t.Errorf("%v: rc %d, stdout %q, stderr %q", args, rc, out, errb)
		}
		if after := treeSnapshot(t, dir); after != before {
			t.Errorf("%v changed the folder:\n%s\n---\n%s", args, before, after)
		}
	}
}
