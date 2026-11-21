package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lint(t *testing.T, files map[string]string, path string) []string {
	t.Helper()
	root := t.TempDir()
	for f, body := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Lint(root, path, []byte(files[path]))
}

func TestLint(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string // substrings, one per expected problem; nil means clean
	}{
		{"clean", map[string]string{"a.md": "# A\n\ntext [b](b.md) and [web](https://x.y/z) and [top](#top)\n\n```go\nfunc f() {}\n```\n", "b.md": "# B\n"}, nil},
		{"unclosed fence", map[string]string{"a.md": "# A\n\n```go\ncode\n"}, []string{"a.md:3: code fence"}},
		{"tilde fence needs a tilde", map[string]string{"a.md": "~~~\ncode\n```\n"}, []string{"a.md:1: code fence"}},
		{"longer closing fence closes", map[string]string{"a.md": "```\ncode\n`````\n\ntext\n"}, nil},
		{"shorter closing fence does not", map[string]string{"a.md": "`````\ncode\n```\n"}, []string{"never closed"}},
		{"link in a fence is not a link", map[string]string{"a.md": "```\n[x](nope.md)\n```\n"}, nil},
		{"link in inline code is not a link", map[string]string{"a.md": "use `[x](nope.md)` here\n"}, nil},
		{"broken relative link", map[string]string{"a.md": "see [x](nope.md)\n"}, []string{`a.md:1: link "nope.md"`}},
		{"anchor and query ignored, file checked", map[string]string{"a.md": "[x](b.md#sec) [y](b.md?plain=1)\n", "b.md": "x\n"}, nil},
		{"folder link ok", map[string]string{"a.md": "[d](docs/)\n", "docs/x.md": "x\n"}, nil},
		{"root relative link", map[string]string{"docs/a.md": "[r](/README.md)\n", "README.md": "x\n"}, nil},
		{"image", map[string]string{"a.md": "![i](img/none.png)\n"}, []string{"img/none.png"}},
		{"mailto and scheme-less host ignored", map[string]string{"a.md": "[m](mailto:a@b.c) [h](//cdn.x/y)\n"}, nil},
		{"NUL byte", map[string]string{"a.md": "a\x00b\n"}, []string{"NUL"}},
		{"tab is not a problem", map[string]string{"a.md": "a\tb\n- c\n\t- d\n"}, nil},
		{"CRLF files are fine", map[string]string{"a.md": "# A\r\n\r\n```\r\nx\r\n```\r\n"}, nil},
		{"link with a title", map[string]string{"a.md": "[x](b.md \"the b\")\n", "b.md": "x\n"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := lint(t, tc.files, "a.md")
			if tc.name == "root relative link" {
				got = lint(t, tc.files, "docs/a.md")
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d problems %q, want %d %q", len(got), got, len(tc.want), tc.want)
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i], w) {
					t.Errorf("problem %d = %q, want it to contain %q", i, got[i], w)
				}
			}
		})
	}
}

func TestRun(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(root, "ok.md"), []byte("# ok\n"), 0o644))
	must(os.MkdirAll(filepath.Join(root, "testdata"), 0o755))
	must(os.WriteFile(filepath.Join(root, "testdata", "bad.md"), []byte("```\nnever closed\n"), 0o644))
	var out, errw bytes.Buffer
	if rc := run([]string{"--root", root}, &out, &errw); rc != 0 {
		t.Fatalf("rc=%d out=%q err=%q", rc, out.String(), errw.String())
	}
	must(os.WriteFile(filepath.Join(root, "bad.md"), []byte("[x](none.md)\n"), 0o644))
	out.Reset()
	errw.Reset()
	if rc := run([]string{"--root", root}, &out, &errw); rc != 1 || !strings.Contains(out.String(), "bad.md:1:") {
		t.Fatalf("rc=%d out=%q", rc, out.String())
	}
	if rc := run([]string{"--bogus"}, &out, &errw); rc != 2 {
		t.Fatalf("unknown argument rc=%d", rc)
	}
}
