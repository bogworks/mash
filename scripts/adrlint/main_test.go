package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodADR = `---
status: accepted
date: 2026-10-31
decision-makers: Someone (owner)
---

# Use one way to do the thing

## Context and Problem Statement

Why we need to decide. See [the guide](guide.md) and [the site](https://example.com/x) and [top](#top).

## Decision Drivers

* A driver.

## Considered Options

* One.
* Two.

## Decision Outcome

Chosen option: "One", because.

### Consequences

* Good, because.

### Confirmation

A test confirms it.

## Pros and Cons of the Options

### One

* Good, because.

## More Information

* Nothing more.
`

const goodReadme = "# Decisions\n\n- [0001](0001-use-one-way.md): one way\n"

// tree writes files (relative name -> content) under a fresh dir and returns it.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// next is the content of a correct NEXT file when the next free number is n.
func next(n int) string { return fmt.Sprintf("%04d\n", n) }

func good() map[string]string {
	return map[string]string{"README.md": goodReadme, "0001-use-one-way.md": goodADR, "guide.md": "guide\n", "NEXT": next(2)}
}

func with(m map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == "\x00" {
			delete(out, kv[i])
		} else {
			out[kv[i]] = kv[i+1]
		}
	}
	return out
}

func edit(s, old, repl string) string {
	if !strings.Contains(s, old) {
		panic("test bug: " + old)
	}
	return strings.Replace(s, old, repl, 1)
}

func runCLI(t *testing.T, g gitFunc, envs map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	e := env{&out, &errb, func(k string) string { return envs[k] }, g}
	return run(args, e), out.String(), errb.String()
}

func TestDocs(t *testing.T) {
	adr := "0001-use-one-way.md"
	tests := []struct {
		name  string
		files map[string]string
		want  []string // substrings, each must appear on stdout; none means a pass
	}{
		{"good", good(), nil},
		{"a: no front matter", with(good(), adr, strings.Join(strings.Split(goodADR, "\n")[5:], "\n")), []string{adr + ":1: front matter"}},
		{"a: missing key", with(good(), adr, edit(goodADR, "decision-makers: Someone (owner)\n", "")), []string{"has no decision-makers key"}},
		{"a: bad status", with(good(), adr, edit(goodADR, "status: accepted", "status: maybe")), []string{adr + ":2: status \"maybe\""}},
		{"a: superseded status ok", with(good(), "NEXT", next(3), adr, edit(goodADR, "status: accepted", "status: Superseded by ADR-0002"), "0002-new.md", goodADR, "README.md", goodReadme+"- [x](0002-new.md)\n"), nil},
		{"a: superseded by a missing ADR", with(good(), adr, edit(goodADR, "status: accepted", "status: superseded by ADR-9999")), []string{adr + ":2:", "ADR-9999 has no file 9999-*.md"}},
		{"a: superseded by an unlisted ADR is fine", with(good(), "NEXT", next(3), adr, edit(goodADR, "status: accepted", "status: superseded by ADR-0002"), "0002-new.md", goodADR), nil},
		{"a: bad superseded", with(good(), adr, edit(goodADR, "status: accepted", "status: superseded by 2")), []string{"status \"superseded by 2\""}},
		{"a: status with a note", with(good(), "NEXT", next(3), adr, edit(goodADR, "status: accepted", "status: accepted; amended by ADR-0002 (the read tool acks)"), "0002-new.md", goodADR, "README.md", goodReadme+"- [x](0002-new.md)\n"), nil},
		{"a: status with a note and no ADR", with(good(), adr, edit(goodADR, "status: accepted", "status: accepted; the clause is moved")), nil},
		{"a: status note names a missing ADR", with(good(), adr, edit(goodADR, "status: accepted", "status: accepted; amended by ADR-9999 (x)")), []string{adr + ":2:", "ADR-9999 has no file 9999-*.md"}},
		{"a: status note names an unlisted ADR is fine", with(good(), "NEXT", next(3), adr, edit(goodADR, "status: accepted", "status: accepted; amended by ADR-0002"), "0002-new.md", goodADR), nil},
		{"a: note after a bad keyword", with(good(), adr, edit(goodADR, "status: accepted", "status: maybe; accepted")), []string{`status "maybe; accepted"`}},
		{"d: front matter is not checked", with(good(), adr, edit(goodADR, "decision-makers: Someone (owner)", "decision-makers: A lead, B")), nil},
		{"d: body occurrence of lead still fails", with(good(), adr, edit(goodADR, "A driver.", "The lead decides.")), []string{`forbidden vocabulary "lead"`}},
		{"a: bad date", with(good(), adr, edit(goodADR, "2026-10-31", "2026-13-40")), []string{adr + ":3: date"}},
		{"a: empty decision-makers", with(good(), adr, edit(goodADR, "decision-makers: Someone (owner)", "decision-makers:")), []string{"decision-makers is empty"}},
		{"b: missing section", with(good(), adr, edit(goodADR, "## Decision Drivers", "## Drivers")), []string{`missing section "## Decision Drivers"`}},
		{"b: out of order", with(good(), adr, edit(edit(goodADR, "## Decision Drivers", "## XX"), "## Considered Options", "## Decision Drivers")+"\n## Considered Options\n"), []string{"out of order"}},
		{"b: no consequences", with(good(), adr, edit(goodADR, "### Consequences", "### Results")), []string{`no "### Consequences"`}},
		{"b: confirmation outside outcome", with(good(), adr, edit(edit(goodADR, "### Confirmation\n\nA test confirms it.\n", ""), "### One\n", "### Confirmation\n")), []string{`no "### Confirmation"`}},
		{"b: title not first", with(good(), adr, edit(goodADR, "# Use one way to do the thing", "## Use one way")), []string{"first heading"}},
		{"c: an ADR not listed in README is accepted", with(good(), "README.md", "# Decisions\n"), nil},
		{"c: an unlisted new ADR is accepted", with(good(), "NEXT", next(3), "0002-new.md", goodADR), nil},
		{"c: README link dead", with(good(), "README.md", goodReadme+"- [0002](0002-gone.md)\n"), []string{"README.md:4: link to 0002-gone.md does not resolve"}},
		{"c: ADR link dead", with(good(), adr, edit(goodADR, "guide.md", "nope.md")), []string{`link "nope.md" does not resolve`}},
		{"c: duplicate number", with(good(), "0001-other.md", goodADR, "README.md", goodReadme+"- [x](0001-other.md)\n"), []string{"number 0001 is used by more than one"}},
		{"n: NEXT missing", with(good(), "NEXT", "\x00"), []string{"NEXT:1: NEXT is missing", "bump NEXT in the same PR"}},
		{"n: NEXT stale (the highest number, not one more)", with(good(), "NEXT", next(1)), []string{"NEXT:1:", "NEXT holds 0001", "next free number is 0002", "take the number NEXT names and bump NEXT in the same PR"}},
		{"n: NEXT too high", with(good(), "NEXT", next(3)), []string{"NEXT holds 0003", "next free number is 0002"}},
		{"n: NEXT malformed, no newline", with(good(), "NEXT", "0002"), []string{"NEXT must be exactly 4 digits and a newline"}},
		{"n: NEXT malformed, three digits", with(good(), "NEXT", "002\n"), []string{"NEXT must be exactly 4 digits and a newline"}},
		{"n: NEXT malformed, two lines", with(good(), "NEXT", "0002\n\n"), []string{"NEXT must be exactly 4 digits and a newline"}},
		{"n: NEXT malformed, text", with(good(), "NEXT", "next\n"), []string{"NEXT must be exactly 4 digits and a newline"}},
		{"n: NEXT with a gap in the numbers", with(good(), "0005-five.md", goodADR, "NEXT", next(6)), nil},
		{"n: NEXT after a gap must still be highest + 1", with(good(), "0005-five.md", goodADR, "NEXT", next(2)), []string{"NEXT holds 0002", "next free number is 0006"}},
		{"n: no ADRs, NEXT is 0001", map[string]string{"README.md": "# Decisions\n", "NEXT": next(1)}, nil},
		{"n: no ADRs, NEXT not 0001", map[string]string{"README.md": "# Decisions\n", "NEXT": next(2)}, []string{"next free number is 0001"}},
		{"n: two PRs both take 0002 and bump NEXT alike, refused as a duplicate", with(good(), "0002-first.md", goodADR, "0002-second.md", goodADR, "NEXT", next(3)), []string{"number 0002 is used by more than one"}},
		{"c: gap allowed", with(good(), "NEXT", next(4), "0003-three.md", goodADR, "README.md", goodReadme+"- [x](0003-three.md)\n"), nil},
		{"a: extra front matter key ok", with(good(), adr, edit(goodADR, "status: accepted", "status: accepted\nrecorded: 2026-10-31")), nil},
		{"b: heading with parenthetical suffix", with(good(), adr, edit(goodADR, "## Considered Options", "## Considered Options (reconstructed 2026-10-31; not recorded at the time)")), nil},
		{"b: heading with another word after the name", with(good(), adr, edit(goodADR, "## Considered Options", "## Considered Options Extra")), []string{`missing section "## Considered Options"`}},
		{"c: bad file name", with(good(), "2-Bad.md", goodADR), []string{"file name must be NNNN-kebab-title.md"}},
		{"d: vocabulary", with(good(), adr, edit(goodADR, "A driver.", "A Lane and the lead, herd, herds, herdr, orchestrator.")), []string{`forbidden vocabulary "Lane"`, `"lead"`, `"herds"`, `"herdr"`, `"orchestrator"`}},
		{"d: escape on the same line", with(good(), adr, edit(goodADR, "A driver.", "A lane. <!-- adrlint:allow vocabulary -->")), nil},
		{"d: escape on another line does not count", with(good(), adr, edit(goodADR, "A driver.", "<!-- adrlint:allow vocabulary -->\nA lane.")), []string{`forbidden vocabulary "lane"`}},
		{"d: whole word only", with(good(), adr, edit(goodADR, "A driver.", "leading planes lanes_x")), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tree(t, tc.files)
			code, out, errs := runCLI(t, nil, nil, "docs", dir)
			if len(tc.want) == 0 {
				if code != 0 || out != "" {
					t.Fatalf("want pass, got %d %q %q", code, out, errs)
				}
				return
			}
			if code != 1 {
				t.Fatalf("want exit 1, got %d %q %q", code, out, errs)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
		})
	}
}

func TestDocs_reportsEveryViolation(t *testing.T) {
	dir := tree(t, with(good(), "0001-use-one-way.md", edit(edit(goodADR, "status: accepted", "status: nope"), "A driver.", "a lane"), "README.md", "# x\n"))
	_, out, _ := runCLI(t, nil, nil, "docs", dir)
	for _, w := range []string{"status \"nope\"", "forbidden vocabulary"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in\n%s", w, out)
		}
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"docs", "a", "b"}, {"docs", "--nope"}, {"pr"}, {"pr", "--files", filepath.Join(t.TempDir(), "absent")}, {"docs", filepath.Join(t.TempDir(), "absent")}} {
		if code, _, _ := runCLI(t, nil, nil, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

// fakeGit serves a base tree: files maps a repo-relative path to its content at the base.
func fakeGit(files map[string]string) gitFunc {
	return func(args ...string) ([]byte, error) {
		switch args[0] {
		case "rev-parse":
			if strings.HasPrefix(args[len(args)-1], "good^") {
				return []byte("abc\n"), nil
			}
			return nil, errors.New("unknown revision")
		case "show":
			_, p, _ := strings.Cut(args[1], ":./")
			if c, ok := files[p]; ok {
				return []byte(c), nil
			}
			return nil, errors.New("not in base")
		case "ls-tree":
			var out []string
			for p := range files {
				out = append(out, p)
			}
			return []byte(strings.Join(out, "\n")), nil
		}
		return nil, errors.New("unexpected git " + args[0])
	}
}

func TestImmutability(t *testing.T) {
	// The tree is under a relative path so the base path (docs/decisions/...) is stable: run from a temp cwd.
	superseded := edit(goodADR, "status: accepted", "status: superseded by ADR-0002") + "\nSuperseded by [ADR 0002](0002-new.md)\n"
	draft := edit(goodADR, "status: accepted", "status: proposed")
	tests := []struct {
		name string
		base string // the ADR at base ("" for absent)
		cur  string // the ADR in the working tree
		want string // violation substring; "" is a pass
	}{
		{"unchanged", goodADR, goodADR, ""},
		{"supersede", goodADR, superseded, ""},
		{"supersede link to a missing ADR", goodADR, edit(superseded, "(0002-new.md)", "(0009-nope.md)"), "does not exist"},
		{"supersede link to a non-ADR file", goodADR, edit(superseded, "(0002-new.md)", "(guide.md)"), "must point to an ADR file"},
		{"supersede link changed again", superseded, edit(superseded, "(0002-new.md)", "(0003-newer.md)"), ""},
		{"body edit", goodADR, edit(goodADR, "Chosen option: \"One\"", "Chosen option: \"Two\""), "immutable"},
		{"front matter edit", goodADR, edit(goodADR, "decision-makers: Someone (owner)", "decision-makers: Other"), "immutable"},
		{"date edit", goodADR, edit(goodADR, "2026-10-31", "2026-11-01"), "immutable"},
		{"status to rejected", goodADR, edit(goodADR, "status: accepted", "status: rejected"), "status may only change"},
		{"status to deprecated", goodADR, edit(goodADR, "status: accepted", "status: deprecated"), ""},
		{"note-only change while accepted", goodADR, edit(goodADR, "status: accepted", "status: accepted; reviewed"), "status line may not change"},
		{"supersede link with a trailing period", goodADR, edit(superseded, "(0002-new.md)", "(0002-new.md)."), ""},
		{"supersede link with trailing text", goodADR, edit(superseded, "(0002-new.md)", "(0002-new.md) and more"), "immutable"},
		{"link line while status stays accepted", goodADR, goodADR + "\nSuperseded by [ADR 0002](0002-new.md)\n", "needs the status"},
		{"other front matter change with supersede", goodADR, edit(superseded, "decision-makers: Someone (owner)", "decision-makers: Other"), "immutable"},
		{"appended text", goodADR, goodADR + "\nAn addition.\n", "immutable"},
		{"removed text", goodADR, strings.TrimSuffix(goodADR, "* Nothing more.\n"), "removed"},
		{"new file", "", goodADR, ""},
		{"not accepted at base", draft, edit(draft, "A driver.", "Another driver."), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wd, _ := os.Getwd()
			root := t.TempDir()
			dir := filepath.Join(root, "docs", "decisions")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for n, c := range with(good(), "0001-use-one-way.md", tc.cur) {
				os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644)
			}
			os.WriteFile(filepath.Join(dir, "0002-new.md"), []byte("x"), 0o644)
			os.WriteFile(filepath.Join(dir, "0003-newer.md"), []byte("x"), 0o644)
			if err := os.Chdir(root); err != nil {
				t.Fatal(err)
			}
			defer os.Chdir(wd)
			base := map[string]string{}
			if tc.base != "" {
				base["docs/decisions/0001-use-one-way.md"] = tc.base
			}
			vs, err := lintImmutable("docs/decisions", "docs/decisions", "good", fakeGit(base))
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(vs, "\n")
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestImmutability_deletedAcceptedADR(t *testing.T) {
	wd, _ := os.Getwd()
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "decisions")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# x\n"), 0o644)
	os.Chdir(root)
	defer os.Chdir(wd)
	vs, err := lintImmutable("docs/decisions", "docs/decisions", "good", fakeGit(map[string]string{"docs/decisions/0001-use-one-way.md": goodADR}))
	if err != nil || len(vs) != 1 || !strings.Contains(vs[0], "deleted") {
		t.Fatalf("got %v %v", vs, err)
	}
}

func TestDocs_baseFlag(t *testing.T) {
	wd, _ := os.Getwd()
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "decisions")
	os.MkdirAll(dir, 0o755)
	for n, c := range with(good(), "0001-use-one-way.md", edit(goodADR, "Chosen option", "Changed option")) {
		os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644)
	}
	os.Chdir(root)
	defer os.Chdir(wd)
	g := fakeGit(map[string]string{"docs/decisions/0001-use-one-way.md": goodADR})
	if code, out, _ := runCLI(t, g, nil, "docs", "--base", "good", "docs/decisions"); code != 1 || !strings.Contains(out, "immutable") {
		t.Errorf("--base good: %d %q", code, out)
	}
	if code, out, _ := runCLI(t, g, nil, "docs", "docs/decisions", "--base", "good"); code != 1 {
		t.Errorf("flag after dir: %d %q", code, out)
	}
	if code, _, _ := runCLI(t, g, nil, "docs", "--base", "bad", "docs/decisions"); code != 2 {
		t.Errorf("--base unknown ref: %d, want 2", code)
	}
	if code, _, _ := runCLI(t, g, nil, "docs", "docs/decisions"); code != 0 {
		t.Errorf("without --base: %d, want 0", code)
	}
}

func TestPR(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		files   string
		labels  string
		readme  string // "" is goodReadme
		want    string // "" is a pass
		wantErr int    // expected exit when not 0/1
	}{
		{name: "ADR named", body: "Summary\n\nDecision: ADR-0001\n", files: "internal/sap/x.go\ndocs/decisions/0001-use-one-way.md\n"},
		{name: "key is case-insensitive", body: "decision: adr-0001", files: "docs/decisions/0001-use-one-way.md\n"},
		{name: "ADR named but the PR does not touch it", body: "Decision: ADR-0001", files: "internal/cli/x.go\n", want: "must add or change"},
		{name: "ADR named, another record changed", body: "Decision: ADR-0001", files: "docs/decisions/0002-other.md\n", want: "must add or change"},
		{name: "ADR named, same name elsewhere", body: "Decision: ADR-0001", files: "other/0001-use-one-way.md\n", want: "must add or change"},
		{name: "none on a change to a record", body: "Decision: none", files: "docs/decisions/0001-use-one-way.md\n", want: "decision-confirmed"},
		{name: "none on a change to the README", body: "Decision: none", files: "docs/decisions/README.md\n", want: "decision-confirmed"},
		{name: "none on a record with the label", body: "Decision: none", files: "docs/decisions/0001-use-one-way.md\n", labels: "decision-confirmed"},
		{name: "none beside docs/decisions look-alike", body: "Decision: none", files: "docs/decisions-notes.md\n"},
		{name: "none on an ordinary path", body: "Decision: none", files: "README.md\ninternal/cli/x.go\n"},
		{name: "none with trailing space and CRLF", body: "Intro\r\nDecision: none  \r\n", files: "README.md\n"},
		{name: "missing", body: "Summary only", files: "README.md\n", want: "no Decision line"},
		{name: "empty body", body: "", files: "README.md\n", want: "no Decision line"},
		{name: "malformed", body: "Decision: maybe", files: "README.md\n", want: "no Decision line"},
		{name: "indented does not count", body: "  Decision: none", files: "README.md\n", want: "no Decision line"},
		{name: "duplicate", body: "Decision: none\nDecision: ADR-0001\n", files: "README.md\n", want: "2 Decision lines"},
		{name: "ADR file missing", body: "Decision: ADR-0009", files: "README.md\n", want: "names no file"},
		{name: "ADR not in README but changed", body: "Decision: ADR-0001", files: "docs/decisions/0001-use-one-way.md\n", readme: "# Decisions\n"},
		{name: "none on internal/sap", body: "Decision: none", files: "internal/sap/a.go\n", want: "decision-confirmed"},
		{name: "none on internal/store", body: "Decision: none", files: "internal/store/a.go\n", want: "decision-confirmed"},
		{name: "none on permissions.go", body: "Decision: none", files: "internal/admin/permissions.go\n", want: "decision-confirmed"},
		{name: "none on internal/config", body: "Decision: none", files: "internal/config/a.go\n", want: "decision-confirmed"},
		{name: "none on internal/endpoint", body: "Decision: none", files: "internal/endpoint/a.go\n", want: "decision-confirmed"},
		{name: "none on internal/mcp", body: "Decision: none", files: "internal/mcp/a.go\n", want: "decision-confirmed"},
		{name: "none on internal/mcp with the label", body: "Decision: none", files: "internal/mcp/a.go\n", labels: "decision-confirmed"},
		{name: "none on internal/host", body: "Decision: none", files: "internal/host/a.go\n", want: "decision-confirmed"},
		{name: "none on internal/host with the label", body: "Decision: none", files: "internal/host/a.go\n", labels: "decision-confirmed"},
		{name: "none on internal/spool", body: "Decision: none", files: "internal/spool/a.go\n", want: "decision-confirmed"},
		{name: "none on internal/spool with the label", body: "Decision: none", files: "internal/spool/a.go\n", labels: "decision-confirmed"},
		{name: "none on internal/render", body: "Decision: none", files: "internal/render/a.go\n", want: "decision-confirmed"},
		{name: "none on internal/render with the label", body: "Decision: none", files: "internal/render/a.go\n", labels: "decision-confirmed"},
		{name: "none on plugin", body: "Decision: none", files: "plugin/a.json\n", want: "decision-confirmed"},
		{name: "none on the merge gate .mergify.yml", body: "Decision: none", files: ".mergify.yml\n", want: "Decision: none"},
		{name: "none on the merge gate .mergify.yml with the label", body: "Decision: none", files: ".mergify.yml\n", labels: "decision-confirmed"},
		{name: "ADR on the merge gate .mergify.yml", body: "Decision: ADR-0001", files: ".mergify.yml\ndocs/decisions/0001-use-one-way.md\n"},
		{name: "none on a workflow", body: "Decision: none", files: ".github/workflows/x.yml\n", want: "Decision: none"},
		{name: "none on a workflow with the label", body: "Decision: none", files: ".github/workflows/x.yml\n", labels: "decision-confirmed"},
		{name: "ADR on a workflow", body: "Decision: ADR-0001", files: ".github/workflows/x.yml\ndocs/decisions/0001-use-one-way.md\n"},
		{name: "none on the linter's own source", body: "Decision: none", files: "scripts/adrlint/main.go\n", want: "Decision: none"},
		{name: "none on the linter's own source with the label", body: "Decision: none", files: "scripts/adrlint/main.go\n", labels: "decision-confirmed"},
		{name: "ADR on the linter's own source", body: "Decision: ADR-0001", files: "scripts/adrlint/main.go\ndocs/decisions/0001-use-one-way.md\n"},
		{name: "none on another admin file", body: "Decision: none", files: "internal/admin/other.go\n"},
		{name: "none on a look-alike prefix", body: "Decision: none", files: "internal/sapper/a.go\ninternal/storex/a.go\n"},
		{name: "none on protected with the label", body: "Decision: none", files: "internal/sap/a.go\n", labels: "bug, decision-confirmed"},
		{name: "none on protected with another label", body: "Decision: none", files: "internal/sap/a.go\n", labels: "bug,decision-confirmed-not", want: "decision-confirmed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			readme := goodReadme
			if tc.readme != "" {
				readme = tc.readme
			}
			root := t.TempDir()
			dir := filepath.Join(root, "docs", "decisions")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for n, c := range with(good(), "README.md", readme) {
				os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644)
			}
			fl := filepath.Join(t.TempDir(), "files")
			os.WriteFile(fl, []byte(tc.files), 0o644)
			code, out, errs := runCLI(t, nil, map[string]string{"PR_BODY": tc.body, "PR_LABELS": tc.labels}, "pr", "--files", fl, "--root", root)
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

// --root points the checker at another tree: files resolve under it and git runs as "git -C root".
func TestRootFlag(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "decisions")
	os.MkdirAll(dir, 0o755)
	for n, c := range with(good(), "0001-use-one-way.md", edit(goodADR, "Chosen option", "Changed option")) {
		os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644)
	}
	var first []string
	g := func(args ...string) ([]byte, error) {
		if len(args) < 3 || args[0] != "-C" {
			t.Errorf("git called without -C root: %v", args)
			return nil, errors.New("no -C")
		}
		first = append(first, args[1])
		return fakeGit(map[string]string{"docs/decisions/0001-use-one-way.md": goodADR})(args[2:]...)
	}
	// The cwd is not the tree: without --root the default directory does not exist there.
	if code, out, _ := runCLI(t, g, nil, "docs", "--root", root, "--base", "good"); code != 1 || !strings.Contains(out, "immutable") {
		t.Errorf("docs --root: %d %q", code, out)
	}
	if len(first) == 0 || first[0] != root {
		t.Errorf("git -C roots: %v, want %s", first, root)
	}
	if code, _, _ := runCLI(t, nil, nil, "docs"); code != 2 {
		t.Errorf("docs without --root outside a tree: %d, want 2", code)
	}
}

// TestRepositoryADRsPass lints the repository's real ADRs, so they are always covered.
func TestRepositoryADRsPass(t *testing.T) {
	vs, err := lintDocs(filepath.Join("..", "..", "docs", "decisions"), extraVocab(os.Getenv(forbiddenWordsEnv)))
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) > 0 {
		t.Fatalf("the repository's ADRs have violations:\n%s", strings.Join(vs, "\n"))
	}
}

func TestForbiddenWordsFromEnv(t *testing.T) {
	adr := "0001-use-one-way.md"
	files := func(body string) map[string]string { return with(good(), adr, edit(goodADR, "A driver.", body)) }
	run := func(words, body string) string {
		dir := tree(t, files(body))
		_, out, _ := runCLI(t, nil, map[string]string{forbiddenWordsEnv: words}, "docs", dir)
		return out
	}
	tests := []struct {
		name, words, body string
		fail              bool
	}{
		{"empty default changes nothing", "", "A zebra.", false},
		{"body occurrence fails", "zebra,quokka", "A zebra here.", true},
		{"second word fails", "zebra, quokka ", "A quokka here.", true},
		{"case-insensitive", "zebra", "A ZeBrA here.", true},
		{"whole word only", "zebra", "Many zebras and zebrafish.", false},
		{"empty entries ignored", ",, ,zebra,", "A horse.", false},
		{"only empty entries match nothing", ",, ,", "A horse.", false},
		{"escaped line passes", "zebra", "A zebra. <!-- adrlint:allow vocabulary -->", false},
		{"metacharacters are literal", "a.c", "An abc.", false},
		{"built-in rule still applies", "zebra", "A lane.", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := run(tc.words, tc.body)
			if got := strings.Contains(out, "forbidden vocabulary"); got != tc.fail {
				t.Errorf("fail = %v, want %v; output:\n%s", got, tc.fail, out)
			}
		})
	}
}

func indexTree(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, "docs", "decisions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"README.md":           goodReadme,
		"0002-second-one.md":  edit(edit(goodADR, "status: accepted", "status: accepted; a long note | with a pipe"), "# Use one way to do the thing", "# Second | one"),
		"0001-use-one-way.md": goodADR,
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestIndex_printsSortedTableAndWriteThenCheckPasses(t *testing.T) {
	root := indexTree(t)
	code, out, errs := runCLI(t, nil, nil, "index", "--root", root)
	if code != 0 || errs != "" {
		t.Fatalf("index: %d %q", code, errs)
	}
	for _, w := range []string{
		"<!-- generated by scripts/adrlint index --write; do not edit -->\n",
		"| ADR | Title | Status | Date |",
		"| [0001](0001-use-one-way.md) | Use one way to do the thing | accepted | 2026-10-31 |",
		`| [0002](0002-second-one.md) | Second \| one | accepted | 2026-10-31 |`, // note after ";" collapsed
	} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if strings.Index(out, "[0001]") > strings.Index(out, "[0002]") {
		t.Errorf("not sorted by number:\n%s", out)
	}
	if strings.Contains(out, "README.md |") {
		t.Errorf("README is not a record:\n%s", out)
	}
	if code, out, _ := runCLI(t, nil, nil, "index", "--check", "--root", root); code != 1 || !strings.Contains(out, "missing") {
		t.Fatalf("check without a file: %d %q", code, out)
	}
	if code, _, errs := runCLI(t, nil, nil, "index", "--write", "--root", root); code != 0 {
		t.Fatalf("write: %d %q", code, errs)
	}
	if code, out, _ := runCLI(t, nil, nil, "index", "--check", "--root", root); code != 0 || out != "" {
		t.Fatalf("fresh check: %d %q", code, out)
	}
	disk, _ := os.ReadFile(filepath.Join(root, "docs", "decisions", "INDEX.md"))
	if string(disk) != out {
		t.Errorf("written file differs from printed output")
	}
}

func TestIndex_checkFailsWhenStale(t *testing.T) {
	root := indexTree(t)
	runCLI(t, nil, nil, "index", "--write", "--root", root)
	// A new record makes the file stale; so does an edited status.
	if err := os.WriteFile(filepath.Join(root, "docs", "decisions", "0003-third.md"), []byte(goodADR), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCLI(t, nil, nil, "index", "--check", "--root", root)
	if code != 1 || !strings.Contains(out, "stale") || !strings.Contains(out, "0003") || !strings.Contains(out, "--write") {
		t.Fatalf("stale check: %d %q", code, out)
	}
}

func TestIndex_usage(t *testing.T) {
	root := indexTree(t)
	if code, _, _ := runCLI(t, nil, nil, "index", "--write", "--check", "--root", root); code != 2 {
		t.Errorf("--write with --check: %d, want 2", code)
	}
	if code, _, _ := runCLI(t, nil, nil, "index", "--root", filepath.Join(root, "nope")); code != 2 {
		t.Errorf("missing directory: %d, want 2", code)
	}
}
