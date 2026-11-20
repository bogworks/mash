package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const fixture = "testdata/spec"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// TestReferenceIsCurrent fails when docs/reference differs from what the
// spec generates, in either direction.
func TestReferenceIsCurrent(t *testing.T) {
	root := repoRoot(t)
	diffs := CheckDir(filepath.Join(root, "spec"), filepath.Join(root, "docs", "reference"))
	if len(diffs) > 0 {
		t.Fatalf("docs/reference is out of date with spec/ (first difference: %s; %d in all).\n"+
			"Regenerate with: go run ./scripts/refgen\n%s",
			pathOf(diffs[0]), len(diffs), strings.Join(diffs, "\n"))
	}
}

func fixturePages(t *testing.T) map[string]string {
	t.Helper()
	pages, err := Generate(fixture)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range pages {
		out[k] = string(v)
	}
	return out
}

func TestFixturePages(t *testing.T) {
	p := fixturePages(t)
	var names []string
	for n := range p {
		names = append(names, n)
	}
	sort.Strings(names)
	want := []string{
		"README.md", "coming-in-0-6-0.md", "glossary.md", "mash-alpha.md",
		"mash-zed.md", "mash.md", "mashctl-grp-leaf.md", "mashctl-grp.md",
		"mashctl.md", "mcp-server.md", "mcp-zed.md", "mcp.md", "rules.md",
		"setup.md",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("pages = %v, want %v", names, want)
	}
	for n, c := range p {
		if !strings.HasPrefix(c, banner+"\n") {
			t.Errorf("%s: no banner", n)
		}
		if !strings.HasSuffix(c, "\n") || strings.HasSuffix(c, "\n\n") || strings.Contains(c, "\r") {
			t.Errorf("%s: bad line ending", n)
		}
	}
}

func TestOutputCasesVerbatim(t *testing.T) {
	z := fixturePages(t)["mash-zed.md"]
	for _, want := range []string{
		"### Done\n\nStream: stdout. Exit code: 0.\n\n````text\n  two  spaces\nand ```fences``` kept\n````\n",
		"### Bad\n\nStream: stderr. Exit code: 2.\n\n```text\nerror: no\n```\n",
		"```json\n{\n  \"b\": 1,\n  \"a\": []\n}\n```\n",
		"[`zed`](mcp-zed.md)",
		"A note.", "Replaces old.", "mash zed <item> [options]",
		"| `--kind` | `<kind>`: a, b | A \\| kind. |",
	} {
		if !strings.Contains(z, want) {
			t.Errorf("mash-zed.md lacks %q\n%s", want, z)
		}
	}
	if a := fixturePages(t)["mash-alpha.md"]; !strings.Contains(a, "None. Not for agents.") {
		t.Errorf("alpha lacks the no-MCP reason:\n%s", a)
	}
}

func TestComingSeparated(t *testing.T) {
	p := fixturePages(t)
	if _, ok := p["mash-future.md"]; ok {
		t.Error("a next-release command got its own page")
	}
	c := p["coming-in-0-6-0.md"]
	for _, want := range []string{"`mash future`", "Comes later.", "A thing changes. Later."} {
		if !strings.Contains(c, want) {
			t.Errorf("coming page lacks %q", want)
		}
	}
	if strings.Contains(p["mash.md"], "[`mash future`]") {
		t.Error("index links a next-release command")
	}
	if strings.Contains(c, "Zed does a thing") {
		t.Error("coming page holds a current command")
	}
}

func TestGlossarySorted(t *testing.T) {
	g := fixturePages(t)["glossary.md"]
	if strings.Index(g, "Apple") > strings.Index(g, "zebra") {
		t.Error("glossary not sorted")
	}
}

func TestDeterministic(t *testing.T) {
	a, _ := Generate(fixture)
	b, _ := Generate(fixture)
	for k := range a {
		if !bytes.Equal(a[k], b[k]) {
			t.Errorf("%s differs between runs", k)
		}
	}
}

func TestNoLongProseLines(t *testing.T) {
	root := repoRoot(t)
	pages, err := Generate(filepath.Join(root, "spec"))
	if err != nil {
		t.Fatal(err)
	}
	for n, c := range pages {
		fenced := false
		for _, l := range strings.Split(string(c), "\n") {
			if strings.HasPrefix(l, "```") {
				fenced = !fenced
				continue
			}
			if !fenced && len(l) > 80 && !strings.HasPrefix(l, "|") {
				t.Errorf("%s: line over 80 columns: %.60s...", n, l)
			}
		}
	}
}

func TestRunRemovesOnlyStaleGenerated(t *testing.T) {
	out := t.TempDir()
	if err := run(fixture, out); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(out, "old-page.md")
	mine := filepath.Join(out, "notes.md")
	if err := os.WriteFile(stale, []byte(banner+"\n\n# Old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("# Hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CheckDir(fixture, out); len(got) != 1 || got[0] != "extra: old-page.md" {
		t.Fatalf("CheckDir = %v", got)
	}
	if err := run(fixture, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale generated page kept")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Error("hand-written file removed")
	}
}

func TestCheckDirBothDirections(t *testing.T) {
	out := t.TempDir()
	if err := run(fixture, out); err != nil {
		t.Fatal(err)
	}
	if got := CheckDir(fixture, out); len(got) != 0 {
		t.Fatalf("fresh reference reported %v", got)
	}
	if err := os.Remove(filepath.Join(out, "glossary.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "rules.md"), []byte(banner+"\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "zz.md"), []byte(banner+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"missing: glossary.md", "changed: rules.md", "extra: zz.md"}
	if got := CheckDir(fixture, out); !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckDir = %v, want %v", got, want)
	}
}
