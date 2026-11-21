package refgen

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
			PathOf(diffs[0]), len(diffs), strings.Join(diffs, "\n"))
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
	if err := Run(fixture, out); err != nil {
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
	if got := CheckDir(fixture, out); !reflect.DeepEqual(got, []string{"extra: notes.md", "extra: old-page.md"}) {
		t.Fatalf("CheckDir = %v", got)
	}
	if err := Run(fixture, out); err != nil {
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
	if err := Run(fixture, out); err != nil {
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

func TestCheckDirFlagsEveryExtraFile(t *testing.T) {
	out := t.TempDir()
	if err := Run(fixture, out); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(out, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"notes.txt", "sub/hand.md", "sub/deep/x.png", "sub/banner.md"} {
		body := "hand written\n"
		if f == "sub/banner.md" {
			body = banner + "\n"
		}
		if err := os.WriteFile(filepath.Join(out, filepath.FromSlash(f)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"extra: notes.txt", "extra: sub/banner.md", "extra: sub/deep/x.png", "extra: sub/hand.md"}
	if got := CheckDir(fixture, out); !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckDir = %v, want %v", got, want)
	}
	// The writer removes only the banner-carrying one.
	if err := Run(fixture, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "sub", "banner.md")); !os.IsNotExist(err) {
		t.Error("stale banner file kept")
	}
	for _, f := range []string{"notes.txt", "sub/hand.md", "sub/deep/x.png"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
			t.Errorf("%s removed: %v", f, err)
		}
	}
}

// patchedFixture copies the fixture spec to a temp dir and applies
// old -> new replacements to the named file.
func patchedFixture(t *testing.T, file string, repl ...string) string {
	t.Helper()
	dst := t.TempDir()
	ents, err := os.ReadDir(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(fixture, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if e.Name() == file {
			for i := 0; i < len(repl); i += 2 {
				if !strings.Contains(string(b), repl[i]) {
					t.Fatalf("%s lacks %q", file, repl[i])
				}
				b = []byte(strings.Replace(string(b), repl[i], repl[i+1], 1))
			}
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func TestMalformedMetadataFails(t *testing.T) {
	for _, tc := range []struct{ field, from, to string }{
		{"x-output", `"value":[
      {"case":"Done"`, `"value":{"case":"Done"},"x":[
      {"case":"Done"`},
		{"x-mcp", `"value":{"tool":"zed","why":"Same job."}`, `"value":"zed"`},
		{"x-notes", `"value":"A note."`, `"value":7`},
	} {
		dir := patchedFixture(t, "mash.json", tc.from, tc.to)
		_, err := Generate(dir)
		if err == nil {
			t.Errorf("%s: Generate accepted a malformed value", tc.field)
			continue
		}
		if !strings.Contains(err.Error(), "mash zed") || !strings.Contains(err.Error(), tc.field) {
			t.Errorf("%s: error %q names neither the command nor the field", tc.field, err)
		}
		if got := CheckDir(dir, t.TempDir()); len(got) != 1 || !strings.HasPrefix(got[0], "cannot generate: ") {
			t.Errorf("%s: CheckDir = %v", tc.field, got)
		}
	}
}

func TestMalformedRemovedFails(t *testing.T) {
	dir := patchedFixture(t, "mash.json",
		`"exitCodes":[{"code":0,"description":"It worked."}],
  "commands"`, `"exitCodes":[{"code":0,"description":"It worked."}],
  "metadata":[{"name":"x-removed","value":"nope"}],
  "commands"`)
	_, err := Generate(dir)
	if err == nil || !strings.Contains(err.Error(), "x-removed") || !strings.Contains(err.Error(), "mash") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodeSpan(t *testing.T) {
	for in, want := range map[string]string{
		"plain":  "`plain`",
		"a`b":    "``a`b``",
		"a``b`c": "```a``b`c```",
		"`lead":  "`` `lead ``",
		"trail`": "`` trail` ``",
		"`":      "`` ` ``",
	} {
		if got := code(in); got != want {
			t.Errorf("code(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEsc(t *testing.T) {
	for in, want := range map[string]string{
		"<address> & <name>":   "&lt;address&gt; &amp; &lt;name&gt;",
		"a*b*c":                `a\*b\*c`,
		"snake_case and _em_":  `snake_case and \_em\_`,
		"keep `<a> * _` spans": "keep `<a> * _` spans",
		"open ` tick":          "open \\` tick",
		`a\*b`:                 `a\\\*b`,
		`C:\dir`:               `C:\dir`,
	} {
		if got := esc(in); got != want {
			t.Errorf("esc(%q) = %q, want %q", in, got, want)
		}
	}
}

var lineStart = regexp.MustCompile(`^\\s*(#+|-|\\+|>|\\d+[.)]) [xyzwq]\\b`)

// hazards reports a line of generated prose or table text that markdown would
// read as something other than text.
func hazards(page string) []string {
	var out []string
	fenced := false
	for _, l := range strings.Split(page, "\n") {
		if strings.HasPrefix(l, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		// Strip inline code spans, which keep spec text verbatim.
		plain := l
		for {
			i := strings.Index(plain, "`")
			if i < 0 {
				break
			}
			j := strings.Index(plain[i+1:], "`")
			if j < 0 {
				break
			}
			plain = plain[:i] + plain[i+1+j+1:]
		}
		if strings.ContainsAny(plain, "<>") {
			out = append(out, "angle bracket: "+l)
		}
		if strings.Contains(plain, "a*b*c") {
			out = append(out, "bare emphasis: "+l)
		}
		if strings.Contains(plain, " & ") {
			out = append(out, "bare ampersand: "+l)
		}
		if m := lineStart.FindString(l); m != "" {
			out = append(out, "line starts with a markdown marker: "+l)
		}
	}
	return out
}

func TestSpecTextIsEscaped(t *testing.T) {
	long := strings.Repeat("word ", 14)
	dir := patchedFixture(t, "mash.json",
		`"description":"The tool."`, `"description":"# x leads <address> a*b*c & more"`,
		`"description":"Zed does a thing."`, `"description":"- x then <release base> a*b*c & `+long+`# x 1. x - x\u003e y 2) z"`,
		`"description":"A | kind."`, `"description":"A <address> a*b*c & | # x - x 1. x"`,
		`"value":"A note."`, `"value":"1. x first <address> `+long+`1. x tail + x # x"`)
	pages, err := Generate(dir)
	if err != nil {
		t.Fatal(err)
	}
	for n, p := range pages {
		for _, h := range hazards(string(p)) {
			t.Errorf("%s: %s", n, h)
		}
	}
	z := string(pages["mash-zed.md"])
	for _, want := range []string{
		"&lt;release base&gt;", `a\*b\*c`, "&amp;", "| `--kind` | `<kind>`: a, b | A &lt;address&gt; a\\*b\\*c &amp; \\| # x - x 1. x |",
		"1\\. x first &lt;address&gt;", "\\- x then",
	} {
		if !strings.Contains(z, want) {
			t.Errorf("mash-zed.md lacks %q\n%s", want, z)
		}
	}
	if !strings.Contains(string(pages["mash.md"]), "\\# x leads &lt;address&gt;") {
		t.Errorf("mash.md:\n%s", pages["mash.md"])
	}
}

// TestRealReferenceHasNoMarkdownHazards runs the hazard scan over the pages
// generated from the real spec.
func TestRealReferenceHasNoMarkdownHazards(t *testing.T) {
	pages, err := Generate(filepath.Join(repoRoot(t), "spec"))
	if err != nil {
		t.Fatal(err)
	}
	for n, p := range pages {
		for _, h := range hazards(string(p)) {
			t.Errorf("%s: %s", n, h)
		}
	}
}

func TestRunRefusesHandwrittenAndSymlink(t *testing.T) {
	pages, err := Generate(fixture)
	if err != nil {
		t.Fatal(err)
	}
	names := sortedNames(pages)
	first, last := names[0], names[len(names)-1]

	out := t.TempDir()
	mine := filepath.Join(out, filepath.FromSlash(last))
	if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("# Hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Run(fixture, out)
	if err == nil || !strings.Contains(err.Error(), mine) || !strings.Contains(err.Error(), "not generated") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(mine); string(b) != "# Hand written\n" {
		t.Error("hand-written file changed")
	}
	if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(first))); first != last && !os.IsNotExist(err) {
		t.Error("a page was written before the refusal")
	}

	out2 := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.md")
	if err := os.WriteFile(target, []byte(banner+"\n\n# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(out2, filepath.FromSlash(first))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks")
	}
	if err := Run(fixture, out2); err == nil || !strings.Contains(err.Error(), link) {
		t.Fatalf("symlink err = %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != banner+"\n\n# x\n" {
		t.Error("symlink target changed")
	}
}

func TestRunRegeneratesOverBannerPages(t *testing.T) {
	out := t.TempDir()
	if err := Run(fixture, out); err != nil {
		t.Fatal(err)
	}
	pages, _ := Generate(fixture)
	n := sortedNames(pages)[0]
	if err := os.WriteFile(filepath.Join(out, filepath.FromSlash(n)), []byte(banner+"\n\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(fixture, out); err != nil {
		t.Fatal(err)
	}
	if got := CheckDir(fixture, out); len(got) != 0 {
		t.Fatalf("CheckDir = %v", got)
	}
}

// realPages generates the pages of the real spec.
func realPages(t *testing.T) map[string]string {
	t.Helper()
	pages, err := Generate(filepath.Join(repoRoot(t), "spec"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range pages {
		out[k] = string(v)
	}
	return out
}

func TestCLIJSONDetailRendered(t *testing.T) {
	p := realPages(t)
	for f, key := range map[string]string{"mash-status.md": `"session"`, "mash-join.md": `"creds"`} {
		page := p[f]
		i := strings.Index(page, "## JSON with --detail\n")
		if i < 0 {
			t.Fatalf("%s lacks the JSON with --detail section", f)
		}
		if !strings.Contains(page[i:], "fuller shape") || !strings.Contains(page[i:], key) {
			t.Errorf("%s detail section lacks its text or example:\n%s", f, page[i:])
		}
	}
}

func TestMCPCasesHaveNoStreamOrExit(t *testing.T) {
	p := realPages(t)
	for name, page := range p {
		if !strings.HasPrefix(name, "mcp-") {
			continue
		}
		if strings.Contains(page, "Exit code:") || strings.Contains(page, "Stream:") {
			t.Errorf("%s mentions a stream or exit code:\n%s", name, page)
		}
	}
	if s := p["mcp-send.md"]; !strings.Contains(s, "The call returns this text.") {
		t.Errorf("mcp-send.md lacks the text-result statement:\n%s", s)
	}
	if !strings.Contains(p["mcp-reply.md"]+p["mcp-send.md"]+p["mcp-read.md"], "`isError` set") {
		t.Errorf("no MCP page states isError for its errors")
	}
}

func TestMCPStructuredDetailRendered(t *testing.T) {
	p := realPages(t)
	s := p["mcp-status.md"]
	i := strings.Index(s, "## Structured result with --detail\n")
	if i < 0 || !strings.Contains(s[i:], "\"session\"") {
		t.Errorf("mcp-status.md lacks the detail section:\n%s", s)
	}
	if strings.Contains(p["mcp-send.md"], "with --detail") {
		t.Errorf("mcp-send.md has a detail section though its structured_detail is null")
	}
}
