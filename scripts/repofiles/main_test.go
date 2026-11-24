package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo makes a git checkout in a temp dir holding files (path to content), all added to the index.
func repo(t *testing.T, files map[string][]byte) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	root := t.TempDir()
	for f, body := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

func check(t *testing.T, root string) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	rc := run([]string{"--root", root}, &buf)
	return rc, buf.String()
}

func TestRepofiles(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 300*1024)
	png := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0, 0, 0)
	tests := []struct {
		name  string
		files map[string][]byte
		rc    int
		want  string
	}{
		{"clean", map[string][]byte{"README.md": []byte("hi\n"), "a.go": []byte("package a\n")}, 0, ""},
		{"large video", map[string][]byte{"docs/x.mp4": big}, 1, "docs/x.mp4"},
		{"elf", map[string][]byte{"bin/tool": []byte("\x7fELF\x02\x01\x01")}, 1, "bin/tool: executable format (ELF)"},
		{"small png with NUL", map[string][]byte{"docs/a.png": png}, 0, ""},
		{"NUL in txt", map[string][]byte{"notes.txt": []byte("a\x00b")}, 1, "notes.txt: binary content"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rc, out := check(t, repo(t, tc.files))
			if rc != tc.rc {
				t.Fatalf("rc = %d, want %d; output:\n%s", rc, tc.rc, out)
			}
			if tc.want == "" && out != "" {
				t.Errorf("want no output, got:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}
		})
	}
}

func TestRepofiles_usageAndNotCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	var buf bytes.Buffer
	if rc := run(nil, &buf); rc != 2 {
		t.Errorf("no args: rc = %d, want 2", rc)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	if rc, out := check(t, t.TempDir()); rc != 2 {
		t.Errorf("not a checkout: rc = %d, want 2; output:\n%s", rc, out)
	}
}
