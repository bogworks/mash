package releasenotes

import (
	"strings"
	"testing"
)

func words(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) }

func notes(summary, upgrading string) string {
	return "### Summary\n\n" + summary + "\n\n### Upgrading\n\n" + upgrading + "\n\n### Added\n\n- a thing\n"
}

func TestOpening(t *testing.T) {
	cases := map[string]struct {
		text string
		want string // "" passes
	}{
		"good":                   {notes(words(39)+" done.", "Nothing to do."), ""},
		"39 words refused":       {notes(words(39), "Nothing to do."), "39 words"},
		"40 words pass":          {notes(words(40), "Nothing to do."), ""},
		"words over two lines":   {notes(words(20)+"\n"+words(20), "Nothing to do."), ""},
		"summary opens a list":   {"### Summary\n\n- " + words(50) + "\n\n### Upgrading\n\nNothing to do.\n", "0 words"},
		"words after a list":     {"### Summary\n\n" + words(10) + "\n- x\n" + words(50) + "\n\n### Upgrading\n\nNothing to do.\n", "10 words"},
		"no heading":             {words(60) + "\n", "no \"### Summary\""},
		"list before heading":    {"- a\n\n### Summary\n\n" + words(50) + "\n", "start with a list"},
		"summary not first":      {"### Added\n\n- x\n\n" + notes(words(50), "Nothing to do."), "not \"### Summary\""},
		"upgrading missing":      {"### Summary\n\n" + words(50) + "\n\n### Added\n\n- x\n", "no \"### Upgrading\""},
		"upgrading before":       {"### Upgrading\n\nNothing to do.\n\n### Summary\n\n" + words(50) + "\n", "not \"### Summary\""},
		"upgrading list first":   {notes(words(50), "- run init"), "Upgrading section has no sentence"},
		"upgrading empty":        {"### Summary\n\n" + words(50) + "\n\n### Upgrading\n\n### Added\n\n- x\n", "Upgrading section has no sentence"},
		"upgrading numbered":     {notes(words(50), "1. run init"), "Upgrading section has no sentence"},
		"fence is not prose":     {"### Summary\n\n```\n" + words(60) + "\n```\n\n### Upgrading\n\nNothing to do.\n", "0 words"},
		"table is not prose":     {"### Summary\n\n| a | b |\n|---|---|\n| " + words(60) + " | x |\n\n### Upgrading\n\nNothing to do.\n", "0 words"},
		"fence in upgrading":     {notes(words(50), "```\nmash admin init.\n```"), "Upgrading section has no sentence"},
		"upgrading then a steps": {notes(words(50), "Run init once.\n\n- mash admin init"), ""},
		"star and plus lists":    {"### Summary\n\n* " + words(50) + "\n+ x\n\n### Upgrading\n\nNothing to do.\n", "0 words"},
		"crlf":                   {strings.ReplaceAll(notes(words(50), "Nothing to do."), "\n", "\r\n"), ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := Opening(c.text)
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

func TestNoCommitHashLines(t *testing.T) {
	for text, bad := range map[string]bool{
		"- fix the thing (a1b2c3d)\n":                        true,
		"fix (a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2 ) \n": true,
		"fix (abc123)\n":                                     false, // six digits
		"see (a1b2c3d) for more\n":                           false,
		"fix (deadbeef)\r\nx\r\n":                            true,
		"fix ([ADR 0001](x.md))\n":                           false,
		"a\n- b (ABCDEF1)\n":                                 false, // upper case is not a hash here
	} {
		err := NoCommitHashLines(text)
		if (err != nil) != bad {
			t.Errorf("%q: err = %v, want refused %v", text, err, bad)
		}
	}
}

func TestHasCompareLink(t *testing.T) {
	link := "Full changes: [v0.3.3...v0.4.1](https://github.com/o/r/compare/v0.3.3...v0.4.1)"
	if err := HasCompareLink(link, "0.3.3", "0.4.1"); err != nil {
		t.Error(err)
	}
	if err := HasCompareLink(link, "v0.3.3", "v0.4.1"); err != nil {
		t.Error(err)
	}
	if err := HasCompareLink(link, "0.3.2", "0.4.1"); err == nil {
		t.Error("the wrong previous version passed")
	}
	if err := HasCompareLink("no link", "0.3.3", "0.4.1"); err == nil {
		t.Error("no link passed")
	}
	if err := HasCompareLink(link, "", "0.4.1"); err != nil {
		t.Errorf("unknown previous: %v", err)
	}
	if err := HasCompareLink(link, "", "0.4.2"); err == nil {
		t.Error("unknown previous, wrong version passed")
	}
	if err := HasCompareLink("...v0.4.1 only", "", "0.4.1"); err == nil {
		t.Error("unknown previous, no compare path passed")
	}
}

func TestNoPlaceholder(t *testing.T) {
	if err := NoPlaceholder("fine"); err != nil {
		t.Fatal(err)
	}
	if err := NoPlaceholder("x REPLACE THIS PARAGRAPH: y"); err == nil {
		t.Fatal("placeholder passed")
	}
}

func TestSectionBody(t *testing.T) {
	cl := "# C\n\n## [Unreleased]\n\n## [0.4.0] - d\n\n### Summary\n\nProse.\n\n```\n## not a heading\n```\n\n## [0.3.0] - d\n\nold\n"
	got, ok := SectionBody(cl, "0.4.0")
	if !ok || got != "### Summary\n\nProse.\n\n```\n## not a heading\n```\n" {
		t.Fatalf("%v %q", ok, got)
	}
	if got, ok := SectionBody(cl, "0.3.0"); !ok || got != "old\n" {
		t.Fatalf("%v %q", ok, got)
	}
	if _, ok := SectionBody(cl, "0.4"); ok {
		t.Fatal("a prefix matched")
	}
}
