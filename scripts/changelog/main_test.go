package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/releasenotes"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for n, c := range files {
		p := filepath.Join(root, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func cli(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, env{&out, &errb})
	return code, out.String(), errb.String()
}

func read(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const header = "# Changelog\n\nIntro.\n\n"

// opening is what assemble writes under a new section's heading.
const opening = "### Summary\n\n" + SummaryPlaceholder + "\n\n### Upgrading\n\n" + UpgradingPlaceholder + "\n\n"

const prior = "## [0.3.2] - 2026-11-02\n\n### Added\n\n- Old one.\n- Old two.\n\n### Fixed\n\n- Old fix.\n"

func TestCheck(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string // a substring of stdout; "" is a pass
	}{
		{"no directory", nil, ""},
		{"good", map[string]string{"changelog.d/a-b_c1.added.md": "Text.\n", "changelog.d/README.md": "- not a fragment\n"}, ""},
		{"multi-line text", map[string]string{"changelog.d/a.fixed.md": "First line.\nsecond line.\n"}, ""},
		{"empty", map[string]string{"changelog.d/a.added.md": " \n\n"}, "changelog.d/a.added.md: empty"},
		{"bad kind", map[string]string{"changelog.d/a.docs.md": "x\n"}, `kind "docs" must be one of`},
		{"no kind", map[string]string{"changelog.d/a.md": "x\n"}, "name must be <slug>.<kind>.md"},
		{"upper-case slug", map[string]string{"changelog.d/Abc.added.md": "x\n"}, "name must be"},
		{"dot in slug", map[string]string{"changelog.d/a.b.added.md": "x\n"}, "name must be"},
		{"not markdown", map[string]string{"changelog.d/a.added.txt": "x\n"}, "name must be"},
		{"dash bullet", map[string]string{"changelog.d/a.added.md": "- text\n"}, "must not start with a bullet"},
		{"star bullet", map[string]string{"changelog.d/a.added.md": "\n* text\n"}, "must not start with a bullet"},
		{"heading", map[string]string{"changelog.d/a.added.md": "# Added\ntext\n"}, `must not start with "#"`},
		{"a hyphenated word is fine", map[string]string{"changelog.d/a.added.md": "-x flag now works.\n"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := tree(t, tc.files)
			code, out, errs := cli("check", "--root", root)
			if tc.want == "" {
				if code != 0 || out != "" {
					t.Fatalf("want pass, got %d %q %q", code, out, errs)
				}
				return
			}
			if code != 1 || !strings.Contains(out, tc.want) {
				t.Fatalf("want exit 1 with %q, got %d %q %q", tc.want, code, out, errs)
			}
		})
	}
}

func TestCheck_listsEveryViolation(t *testing.T) {
	root := tree(t, map[string]string{"changelog.d/a.added.md": "", "changelog.d/b.nope.md": "x\n", "changelog.d/c.fixed.md": "- x\n"})
	code, out, _ := cli("check", "--root", root)
	if code != 1 || strings.Count(out, "\n") != 3 {
		t.Fatalf("%d %q", code, out)
	}
}

func TestAssemble_order_bulletsAndFragments(t *testing.T) {
	root := tree(t, map[string]string{
		"CHANGELOG.md":                header + "## [Unreleased]\n\n### Fixed\n\n- Existing fix.\n\n### Added\n\n- Existing add.\n\n- Existing add two,\n  continued.\n\n### Added\n\n- Existing add three.\n\n### Docs\n\n- A docs line.\n\n" + prior,
		"changelog.d/zeta.added.md":   "Zeta entry.\n",
		"changelog.d/alpha.added.md":  "Alpha entry,\nsecond line.\n\nthird after blank.\n",
		"changelog.d/m.security.md":   "A security note.\n",
		"changelog.d/b.changed.md":    "A change.\n",
		"changelog.d/c.removed.md":    "A removal.\n",
		"changelog.d/d.deprecated.md": "Soon gone.\n",
		"changelog.d/README.md":       "convention\n",
	})
	code, out, errs := cli("assemble", "--version", "0.4.0", "--date", "2026-11-03", "--root", root)
	if code != 0 {
		t.Fatalf("%d %q %q", code, out, errs)
	}
	want := header + "## [Unreleased]\n\n## [0.4.0] - 2026-11-03\n\n" + opening +
		"### Added\n\n- Existing add.\n- Existing add two,\n  continued.\n- Existing add three.\n- Alpha entry,\n  second line.\n\n  third after blank.\n- Zeta entry.\n\n" +
		"### Changed\n\n- A change.\n\n### Deprecated\n\n- Soon gone.\n\n### Removed\n\n- A removal.\n\n" +
		"### Fixed\n\n- Existing fix.\n\n### Security\n\n- A security note.\n\n### Docs\n\n- A docs line.\n\n" + prior
	if got := read(t, root, "CHANGELOG.md"); got != want {
		t.Fatalf("changelog:\n%s\nwant:\n%s", got, want)
	}
	left, _ := os.ReadDir(filepath.Join(root, "changelog.d"))
	if len(left) != 1 || left[0].Name() != "README.md" {
		t.Errorf("fragments left: %v", left)
	}
}

func TestAssemble_emptyUnreleasedAndOnlyFragments(t *testing.T) {
	root := tree(t, map[string]string{
		"CHANGELOG.md":           header + "## [Unreleased]\n\n" + prior,
		"changelog.d/a.fixed.md": "Fix.",
	})
	if code, out, _ := cli("assemble", "--version", "0.3.3", "--date", "2026-11-03", "--root", root); code != 0 {
		t.Fatal(code, out)
	}
	want := header + "## [Unreleased]\n\n## [0.3.3] - 2026-11-03\n\n" + opening + "### Fixed\n\n- Fix.\n\n" + prior
	if got := read(t, root, "CHANGELOG.md"); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAssemble_unreleasedIsLast(t *testing.T) {
	root := tree(t, map[string]string{
		"CHANGELOG.md":           header + "## [Unreleased]\n\n### Added\n\n- One.\n",
		"changelog.d/a.fixed.md": "Fix.\n",
	})
	if code, out, _ := cli("assemble", "--version", "0.1.0", "--date", "2026-11-03", "--root", root); code != 0 {
		t.Fatal(code, out)
	}
	want := header + "## [Unreleased]\n\n## [0.1.0] - 2026-11-03\n\n" + opening + "### Added\n\n- One.\n\n### Fixed\n\n- Fix.\n"
	if got := read(t, root, "CHANGELOG.md"); got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestAssemble_refusalsWriteNothing(t *testing.T) {
	good := header + "## [Unreleased]\n\n### Added\n\n- One.\n\n" + prior
	tests := []struct {
		name    string
		cl      string
		frags   map[string]string
		version string
		date    string
		want    string
	}{
		{"version exists", good, nil, "0.3.2", "2026-11-03", "already has a section for 0.3.2"},
		{"bad version", good, nil, "0.3", "2026-11-03", "must be X.Y.Z"},
		{"v prefix", good, nil, "v0.4.0", "2026-11-03", "must be X.Y.Z"},
		{"bad date", good, nil, "0.4.0", "2026-13-01", "valid YYYY-MM-DD"},
		{"date shape", good, nil, "0.4.0", "11/03/2026", "valid YYYY-MM-DD"},
		{"no Unreleased", header + prior, nil, "0.4.0", "2026-11-03", "no \"## [Unreleased]\" heading"},
		{"unparsable Unreleased", header + "## [Unreleased]\n\nstray words\n\n" + prior, nil, "0.4.0", "2026-11-03", "cannot parse"},
		{"bullet before a subsection", header + "## [Unreleased]\n\n- x\n\n" + prior, nil, "0.4.0", "2026-11-03", "before any"},
		{"bad fragment", good, map[string]string{"changelog.d/a.added.md": "- x\n"}, "0.4.0", "2026-11-03", "must not start with a bullet"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"CHANGELOG.md": tc.cl, "changelog.d/keep.fixed.md": "Keep me.\n"}
			for k, v := range tc.frags {
				files[k] = v
			}
			root := tree(t, files)
			code, out, _ := cli("assemble", "--version", tc.version, "--date", tc.date, "--root", root)
			if code != 1 || !strings.Contains(out, tc.want) {
				t.Fatalf("want exit 1 with %q, got %d %q", tc.want, code, out)
			}
			if read(t, root, "CHANGELOG.md") != tc.cl {
				t.Errorf("the change log was written")
			}
			if _, err := os.Stat(filepath.Join(root, "changelog.d", "keep.fixed.md")); err != nil {
				t.Errorf("a fragment was removed: %v", err)
			}
		})
	}
}

func TestAssemble_nothingToRelease(t *testing.T) {
	cl := header + "## [Unreleased]\n\n" + prior
	root := tree(t, map[string]string{"CHANGELOG.md": cl})
	code, out, _ := cli("assemble", "--version", "0.4.0", "--date", "2026-11-03", "--root", root)
	if code != 1 || !strings.Contains(out, "nothing to release") || read(t, root, "CHANGELOG.md") != cl {
		t.Fatalf("%d %q", code, out)
	}
}

func TestAssemble_dryRunPrintsTheSectionOnly(t *testing.T) {
	cl := header + "## [Unreleased]\n\n### Added\n\n- One.\n\n" + prior
	root := tree(t, map[string]string{"CHANGELOG.md": cl, "changelog.d/a.fixed.md": "Fix.\n"})
	code, out, _ := cli("assemble", "--version", "0.4.0", "--date", "2026-11-03", "--dry-run", "--root", root)
	want := "## [0.4.0] - 2026-11-03\n\n" + opening + "### Added\n\n- One.\n\n### Fixed\n\n- Fix.\n\n"
	if code != 0 || out != want {
		t.Fatalf("%d %q", code, out)
	}
	if read(t, root, "CHANGELOG.md") != cl {
		t.Error("dry run wrote the change log")
	}
	if _, err := os.Stat(filepath.Join(root, "changelog.d", "a.fixed.md")); err != nil {
		t.Error("dry run removed a fragment")
	}
}

func TestAssemble_customPaths(t *testing.T) {
	root := tree(t, map[string]string{"docs/CL.md": header + "## [Unreleased]\n\n", "frag/a.added.md": "x\n"})
	if code, out, _ := cli("assemble", "--version", "1.0.0", "--date", "2026-11-03", "--root", root, "--dir", "frag", "--changelog", "docs/CL.md"); code != 0 {
		t.Fatal(code, out)
	}
	if !strings.Contains(read(t, root, "docs/CL.md"), "## [1.0.0] - 2026-11-03\n\n"+opening+"### Added\n\n- x\n") {
		t.Error("not assembled")
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}, {"assemble"}, {"assemble", "--version", "1.0.0"}, {"check", "extra"}, {"section"}, {"check-notes"}, {"check-notes", "--version", "1.0.0"}} {
		if code, _, _ := cli(args...); code != 2 {
			t.Errorf("%v: %d, want 2", args, code)
		}
	}
	if code, _, _ := cli("assemble", "--version", "1.0.0", "--date", "2026-11-03", "--root", t.TempDir()); code != 2 {
		t.Errorf("missing change log: want 2")
	}
}

// TestRepositoryChangelogAssembles proves the real CHANGELOG.md parses: assembling a fragment-less release on a copy
// folds the Unreleased entries into a section that keeps them all.
func TestRepositoryChangelogAssembles(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, err := assemble(string(b), "9.9.9", "2026-01-01", nil)
	if err != nil {
		// Unreleased may be empty on a release branch: that is the only acceptable refusal.
		if !strings.Contains(err.Error(), "nothing to release") {
			t.Fatal(err)
		}
		return
	}
	if !strings.HasPrefix(section, "## [9.9.9] - 2026-01-01\n\n### Summary\n\nREPLACE THIS PARAGRAPH:") {
		t.Fatalf("section starts %q", section[:60])
	}
}

func TestRepositoryFragmentsPass(t *testing.T) {
	if code, out, errs := cli("check", "--root", filepath.Join("..", "..")); code != 0 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
}

func TestAssemble_writesThePlaceholdersTheGatesRefuse(t *testing.T) {
	root := tree(t, map[string]string{"CHANGELOG.md": header + "## [Unreleased]\n\n" + prior, "changelog.d/a.fixed.md": "Fix.\n"})
	if code, out, _ := cli("assemble", "--version", "0.4.0", "--date", "2026-11-03", "--root", root); code != 0 {
		t.Fatal(code, out)
	}
	got := read(t, root, "CHANGELOG.md")
	if !strings.Contains(got, "## [0.4.0] - 2026-11-03\n\n### Summary\n\nREPLACE THIS PARAGRAPH: ") || !strings.Contains(got, "### Upgrading\n\nREPLACE THIS: say what an operator must do after upgrading, or write \"Nothing to do.\"\n\n### Fixed") {
		t.Fatalf("placeholders missing:\n%s", got)
	}
	// The fresh section fails the notes check on the marker; once replaced with real prose it passes.
	if code, out, _ := cli("section", "--version", "0.4.0", "--root", root); code != 0 || releasenotes.NoPlaceholder(out) == nil {
		t.Fatalf("the placeholder section was not refused: %d %q", code, out)
	}
	words := strings.TrimSpace(strings.Repeat("word ", 45))
	fixed := strings.Replace(got, SummaryPlaceholder, words+".", 1)
	fixed = strings.Replace(fixed, UpgradingPlaceholder, "Nothing to do.", 1)
	_, body, _ := strings.Cut(fixed, "## [0.4.0] - 2026-11-03\n")
	if err := releasenotes.Opening(body); err != nil {
		t.Fatal(err)
	}
	if err := releasenotes.NoPlaceholder(body); err != nil {
		t.Fatal(err)
	}
}

func TestSection(t *testing.T) {
	cl := header + "## [Unreleased]\n\n## [0.4.0] - 2026-11-03\n\n### Summary\n\nProse.\n\n### Added\n\n- One.\n\n" + prior
	root := tree(t, map[string]string{"CHANGELOG.md": cl})
	code, out, _ := cli("section", "--version", "0.4.0", "--root", root)
	if code != 0 || out != "### Summary\n\nProse.\n\n### Added\n\n- One.\n" {
		t.Fatalf("%d %q", code, out)
	}
	if code, out, _ = cli("section", "--version", "0.3.2", "--root", root); code != 0 || !strings.HasPrefix(out, "### Added\n\n- Old one.") || strings.Contains(out, "0.4.0") {
		t.Fatalf("last section: %d %q", code, out)
	}
	// 0.4 is not 0.4.0, and 0.4.00 is not 0.4.0.
	for _, v := range []string{"0.5.0", "0.4.1", "4.0"} {
		code, out, errs := cli("section", "--version", v, "--root", root)
		if code != 1 || out != "" || (!strings.Contains(errs, "no section") && !strings.Contains(errs, "must be X.Y.Z")) {
			t.Errorf("%s: %d %q %q", v, code, out, errs)
		}
	}
	if code, _, _ := cli("section", "--version", "0.4.0", "--root", t.TempDir()); code != 2 {
		t.Errorf("a missing change log: want 2")
	}
}

func TestCheckNotes(t *testing.T) {
	prose := strings.TrimSpace(strings.Repeat("word ", 45)) + "."
	good := "### Summary\n\n" + prose + "\n\n### Upgrading\n\nNothing to do.\n\n### Added\n\n- One.\n\nFull changes: [v0.3.3...v0.4.1](https://github.com/o/r/compare/v0.3.3...v0.4.1)\n"
	tests := []struct {
		name, text string
		args       []string
		want       string // "" passes
	}{
		{"good", good, nil, ""},
		{"good with the link", good, []string{"--version", "0.4.1", "--prev", "0.3.3"}, ""},
		{"wrong previous", good, []string{"--version", "0.4.1", "--prev", "0.3.2"}, "no link to the full changes"},
		{"link required with a version", strings.Replace(good, "compare", "x", 2), []string{"--version", "0.4.1"}, "no link to the full changes"},
		{"commit list", "### Summary\n\n" + prose + "\n\n### Upgrading\n\nNothing to do.\n\n- fix it (a1b2c3d)\n", nil, "commit hash"},
		{"placeholder", strings.Replace(good, prose, "REPLACE THIS PARAGRAPH: "+prose, 1), nil, "REPLACE THIS"},
		{"no summary", "- fix it\n", nil, "start with a list"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := tree(t, map[string]string{"notes.md": tc.text})
			code, out, errs := cli(append([]string{"check-notes", filepath.Join(root, "notes.md")}, tc.args...)...)
			if tc.want == "" {
				if code != 0 || out != "" {
					t.Fatalf("%d %q %q", code, out, errs)
				}
				return
			}
			if code != 1 || !strings.Contains(out, tc.want) {
				t.Fatalf("want exit 1 with %q, got %d %q %q", tc.want, code, out, errs)
			}
		})
	}
	if code, _, _ := cli("check-notes", filepath.Join(t.TempDir(), "none.md")); code != 2 {
		t.Errorf("a missing file: want 2")
	}
}
