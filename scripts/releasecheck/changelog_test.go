package main

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/adrindex"
)

const testADR = "---\nstatus: accepted; a note\ndate: 2026-11-03\ndecision-makers: x\n---\n\n# Use one way\n\n## Context and Problem Statement\n"

func testIndex() string {
	return adrindex.Generate([]adrindex.File{{Name: "0001-use-one-way.md", Content: testADR}})
}

// commitExtra commits several files at once as the candidate.
func commitExtra(t *testing.T, f *fixture, files map[string]string) {
	t.Helper()
	for n, b := range files {
		write(t, f.repo, n, b)
	}
	commitPlugin(t, f, "extra.txt", "x")
}

func TestCheck_changelogFragments(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string // "" passes
	}{
		"no changelog.d at all":       {files: nil},
		"only the README":             {files: map[string]string{"changelog.d/README.md": "convention\n"}},
		"a fragment is refused":       {files: map[string]string{"changelog.d/a.added.md": "x\n"}, want: "changelog fragments were not assembled: run go run ./scripts/changelog assemble --version 0.1.0"},
		"every fragment is named":     {files: map[string]string{"changelog.d/a.added.md": "x\n", "changelog.d/README.md": "r\n", "changelog.d/b.fixed.md": "y\n"}, want: "a.added.md, b.fixed.md"},
		"a stray file is refused too": {files: map[string]string{"changelog.d/.gitkeep": ""}, want: ".gitkeep"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if c.files != nil {
				commitExtra(t, f, c.files)
			}
			err := changelogAssembled(f.o, f.o.X)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

func TestCheck_adrIndex(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string // "" passes
	}{
		"no docs/decisions is nothing to check": {files: map[string]string{"docs/other.md": "x\n"}},
		"a directory without records":           {files: map[string]string{"docs/decisions/README.md": "# d\n"}},
		"a fresh index":                         {files: map[string]string{"docs/decisions/0001-use-one-way.md": testADR, "docs/decisions/INDEX.md": testIndex(), "docs/decisions/README.md": "# d\n"}},
		"a missing index":                       {files: map[string]string{"docs/decisions/0001-use-one-way.md": testADR}, want: "docs/decisions/INDEX.md is stale: run go run ./scripts/adrlint index --write"},
		"a stale index (a record was added)": {
			files: map[string]string{"docs/decisions/0001-use-one-way.md": testADR, "docs/decisions/0002-two.md": testADR, "docs/decisions/INDEX.md": testIndex()},
			want:  "INDEX.md is stale",
		},
		"a hand-edited index": {
			files: map[string]string{"docs/decisions/0001-use-one-way.md": testADR, "docs/decisions/INDEX.md": strings.Replace(testIndex(), "accepted", "proposed", 1)},
			want:  "INDEX.md is stale",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			commitExtra(t, f, c.files)
			err := adrIndexFresh(f.o, f.o.X)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

// The index is judged at X, not in the working tree: a fresh file on disk does not rescue a stale commit.
func TestCheck_adrIndexIsReadAtTheCandidateNotTheCheckout(t *testing.T) {
	f := newFixture(t)
	commitExtra(t, f, map[string]string{"docs/decisions/0001-use-one-way.md": testADR, "docs/decisions/INDEX.md": "stale\n"})
	write(t, f.repo, "docs/decisions/INDEX.md", testIndex()) // uncommitted
	if err := adrIndexFresh(f.o, f.o.X); err == nil {
		t.Fatal("the working tree's index was read")
	}
}

func TestCheck_refusesUnassembledFragmentsAndAStaleIndex(t *testing.T) {
	f := newFixture(t)
	commitExtra(t, f, map[string]string{"changelog.d/a.added.md": "x\n", "docs/decisions/0001-use-one-way.md": testADR})
	err := f.check()
	if err == nil {
		t.Fatal("passed")
	}
	if got := exitCode(err); got != 3 {
		t.Fatalf("exit %d, want 3: %v", got, err)
	}
	for _, w := range []string{"changelog fragments were not assembled", "docs/decisions/INDEX.md is stale"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("refusal lacks %q: %v", w, err)
		}
	}
}

func TestCheck_passesWithAssembledChangelogAndFreshIndex(t *testing.T) {
	f := newFixture(t)
	commitExtra(t, f, map[string]string{"changelog.d/README.md": "r\n", "docs/decisions/0001-use-one-way.md": testADR, "docs/decisions/INDEX.md": testIndex()})
	if err := f.check(); err != nil {
		t.Fatal(err)
	}
	out := f.o.Out.(interface{ String() string }).String()
	for _, w := range []string{"ok    the change log fragments are assembled", "ok    the ADR index is current"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}
