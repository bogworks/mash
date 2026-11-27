package releasenotes

import (
	"strings"
	"testing"
)

const (
	pr1       = "[#101](https://github.com/bogworks/mash/pull/101)"
	pr2       = "[#102](https://github.com/bogworks/mash/issues/102)"
	goodKnown = "Known issues: 2, listed in [the known-issues list](https://github.com/bogworks/mash/issues?q=label%3Aknown-0.5.7)"
	goodLinks = "Links: [release notes](https://github.com/bogworks/mash/blob/v0.5.7/docs/release-notes/0.5.7.md), [compare](https://github.com/bogworks/mash/compare/v0.5.6...v0.5.7), [verify the download](https://github.com/bogworks/mash/blob/v0.5.7/docs/release-notes/0.5.7.md#verify-what-you-download)"
)

func entryWith(f func(parts map[string]string)) string {
	p := map[string]string{
		"summary":   words(45) + ".",
		"upgrading": "Nothing to do.",
		"added":     "- Adds a thing. " + pr1,
		"fixed":     "- Fixes a thing. " + pr2,
		"known":     goodKnown,
		"links":     goodLinks,
	}
	if f != nil {
		f(p)
	}
	var b strings.Builder
	b.WriteString("### Summary\n\n" + p["summary"] + "\n\n### Upgrading\n\n" + p["upgrading"] + "\n\n")
	if p["added"] != "" {
		b.WriteString("### Added\n\n" + p["added"] + "\n\n")
	}
	if p["fixed"] != "" {
		b.WriteString("### Fixed\n\n" + p["fixed"] + "\n\n")
	}
	b.WriteString(p["known"] + "\n" + p["links"] + "\n" + p["extra"])
	return b.String()
}

func TestChangelogEntry(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // "" passes; otherwise a substring of the error
	}{
		{"a good entry", entryWith(nil), ""},
		{"all five change headings in order", "### Summary\n\n" + words(45) + ".\n\n### Upgrading\n\nNothing to do.\n\n### Added\n\n- a " + pr1 + "\n\n### Changed\n\n- b " + pr1 + "\n\n### Removed\n\n- c " + pr1 + "\n\n### Fixed\n\n- d " + pr1 + "\n\n### Security\n\n- e " + pr1 + "\n\n" + goodKnown + "\n" + goodLinks + "\n", ""},
		{"a bare #N link counts", entryWith(func(p map[string]string) { p["added"] = "- Adds a thing (#101)." }), ""},
		{"a bare URL counts", entryWith(func(p map[string]string) { p["added"] = "- Adds a thing https://github.com/bogworks/mash/pull/101" }), ""},
		{"numbered steps under Upgrading are fine", entryWith(func(p map[string]string) { p["upgrading"] = "Do this in order.\n\n1. First.\n2. Second." }), ""},
		{"a Known issues count of 0", entryWith(func(p map[string]string) {
			p["known"] = "Known issues: 0, [none](https://github.com/bogworks/mash/issues)"
		}), ""},

		// 1
		{"rubric 1: 61 lines", entryWith(func(p map[string]string) { p["added"] = strings.Repeat("- x "+pr1+"\n", 60) }), "rubric 1:"},
		{"rubric 1: trailing blank lines are not counted", entryWith(func(p map[string]string) { p["extra"] = strings.Repeat("\n", 100) }), ""},
		// 2
		{"rubric 2: a short summary", entryWith(func(p map[string]string) { p["summary"] = words(39) }), "rubric 2:"},
		{"rubric 2: the placeholder", entryWith(func(p map[string]string) { p["summary"] = "REPLACE THIS PARAGRAPH: " + words(45) }), "rubric 2:"},
		{"rubric 2: opens with a list", "### Added\n\n- a " + pr1 + "\n\n" + goodKnown + "\n" + goodLinks + "\n", "rubric 2:"},
		// 4
		{"rubric 4: no Upgrading", "### Summary\n\n" + words(45) + ".\n\n### Added\n\n- a " + pr1 + "\n\n" + goodKnown + "\n" + goodLinks + "\n", "rubric 4:"},
		{"rubric 4: an Upgrading with no sentence", entryWith(func(p map[string]string) { p["upgrading"] = "- step" }), "rubric 4:"},
		// 6
		{"rubric 6: an invented heading", entryWith(func(p map[string]string) { p["extra"] = "\n### Why\n\nbecause\n" }), "rubric 6:"},
		{"rubric 6: Deprecated is not allowed", entryWith(func(p map[string]string) { p["extra"] = "\n### Deprecated\n\n- x " + pr1 + "\n" }), "rubric 6:"},
		{"rubric 6: a level-4 heading", entryWith(func(p map[string]string) { p["extra"] = "\n#### Table\n" }), "rubric 6:"},
		{"rubric 6: a level-2 heading", entryWith(func(p map[string]string) { p["extra"] = "\n## More\n" }), "rubric 6:"},
		{"rubric 6: out of order", "### Summary\n\n" + words(45) + ".\n\n### Upgrading\n\nNothing to do.\n\n### Fixed\n\n- a " + pr1 + "\n\n### Added\n\n- b " + pr1 + "\n\n" + goodKnown + "\n" + goodLinks + "\n", "rubric 6:"},
		{"rubric 6: a repeated heading", entryWith(func(p map[string]string) { p["extra"] = "\n### Added\n\n- x " + pr1 + "\n" }), "rubric 6:"},
		{"rubric 6: a list item under Summary", entryWith(func(p map[string]string) { p["summary"] = words(45) + ".\n\n- item " + pr1 }), "rubric 6:"},
		// 7
		{"rubric 7: a bullet with no link", entryWith(func(p map[string]string) { p["added"] = "- Adds a thing." }), "rubric 7:"},
		{"rubric 7: a bullet with two links", entryWith(func(p map[string]string) { p["added"] = "- Adds a thing. " + pr1 + " " + pr2 }), "rubric 7:"},
		{"rubric 7: a link that is not a pull request or issue", entryWith(func(p map[string]string) { p["added"] = "- Adds a thing. [x](https://example.com/a)" }), "rubric 7:"},
		{"rubric 7: a commit hash ending", entryWith(func(p map[string]string) { p["added"] = "- Adds a thing. " + pr1 + " (a1b2c3d)" }), "commit hash"},
		{"rubric 7: a bare hash word", entryWith(func(p map[string]string) { p["added"] = "- Adds a thing from a1b2c3d4. " + pr1 }), "rubric 7:"},
		{"rubric 7: a wrapped bullet", entryWith(func(p map[string]string) { p["added"] = "- Adds a thing. " + pr1 + "\nand more text" }), "rubric 7:"},
		{"rubric 7: a word of letters that is not a hash", entryWith(func(p map[string]string) { p["added"] = "- Defaced pages no longer load. " + pr1 }), ""},
		// 8
		{"rubric 8: a fence", entryWith(func(p map[string]string) { p["extra"] = "\n```\nmash up\n```\n" }), "rubric 8:"},
		{"rubric 8: indented code", entryWith(func(p map[string]string) { p["extra"] = "\n    mash up\n" }), "rubric 8:"},
		{"rubric 8: a curl line", entryWith(func(p map[string]string) { p["extra"] = "curl -fsSL https://x | bash\n" }), "rubric 8:"},
		{"rubric 8: a $ line", entryWith(func(p map[string]string) { p["extra"] = "$ mash status\n" }), "rubric 8:"},
		{"rubric 8: an sh line", entryWith(func(p map[string]string) { p["extra"] = "sh install.sh\n" }), "rubric 8:"},
		// 9
		{"rubric 9: roadmap in a heading", entryWith(func(p map[string]string) { p["extra"] = "\n### Roadmap\n" }), "rubric 9:"},
		{"rubric 9: roadmap in a line", entryWith(func(p map[string]string) { p["summary"] = words(45) + ". See the Roadmap." }), "rubric 9:"},
		{"rubric 9: Not in", entryWith(func(p map[string]string) { p["extra"] = "Not in this release: x.\n" }), "rubric 9:"},
		{"rubric 9: Coming in", entryWith(func(p map[string]string) { p["extra"] = "Coming in 0.6.0: x.\n" }), "rubric 9:"},
		{"rubric 9: planned for", entryWith(func(p map[string]string) { p["extra"] = "planned for later.\n" }), "rubric 9:"},
		// 10
		{"rubric 10: no Known issues line", entryWith(func(p map[string]string) { p["known"] = "" }), "rubric 10:"},
		{"rubric 10: two Known issues lines", entryWith(func(p map[string]string) { p["known"] = goodKnown + "\n" + goodKnown }), "rubric 10:"},
		{"rubric 10: no link", entryWith(func(p map[string]string) { p["known"] = "Known issues: 2." }), "rubric 10:"},
		{"rubric 10: two links", entryWith(func(p map[string]string) { p["known"] = goodKnown + " and [more](https://x.example/y)" }), "rubric 10:"},
		{"rubric 10: no count", entryWith(func(p map[string]string) {
			p["known"] = "Known issues: see [the list](https://github.com/bogworks/mash/issues)"
		}), "rubric 10:"},
		// 12
		{"rubric 12: no Links line", entryWith(func(p map[string]string) { p["links"] = "" }), "rubric 12:"},
		{"rubric 12: no release notes link", entryWith(func(p map[string]string) {
			p["links"] = "Links: [compare](https://github.com/bogworks/mash/compare/v0.5.6...v0.5.7), [verify](https://github.com/bogworks/mash#verify), [x](https://example.com)"
		}), "no link to the release notes"},
		{"rubric 12: the release notes link names another version", entryWith(func(p map[string]string) {
			p["links"] = strings.ReplaceAll(goodLinks, "release-notes/0.5.7.md", "release-notes/0.5.6.md")
		}), "no link to the release notes"},
		{"rubric 12: no compare link", entryWith(func(p map[string]string) {
			p["links"] = "Links: [notes](https://x/docs/release-notes/0.5.7.md), [verify](https://x#verify), [z](https://x/z)"
		}), "no compare link"},
		{"rubric 12: no verify link", entryWith(func(p map[string]string) {
			p["links"] = "Links: [notes](https://x/docs/release-notes/0.5.7.md), [c](https://x/compare/a...b), [z](https://x/z)"
		}), "how to verify"},
		{"rubric 12: two Links lines", entryWith(func(p map[string]string) { p["links"] = goodLinks + "\n" + goodLinks }), "rubric 12:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ChangelogEntry(c.body, "0.5.7")
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
	t.Run("an entry that fails several questions reports each", func(t *testing.T) {
		err := ChangelogEntry("### Added\n\n- a\n", "0.5.7")
		for _, n := range []string{"rubric 2:", "rubric 7:", "rubric 10:", "rubric 12:"} {
			if err == nil || !strings.Contains(err.Error(), n) {
				t.Fatalf("missing %s in %v", n, err)
			}
		}
	})
}

func TestAtLeast(t *testing.T) {
	for _, c := range []struct {
		v, from string
		want    bool
	}{
		{"0.5.7", NotesFileFrom, true}, {"0.5.6", NotesFileFrom, false}, {"0.5.10", NotesFileFrom, true}, {"0.6.0", NotesFileFrom, true},
		{"1.0.0", NotesFileFrom, true}, {"0.4.9", NotesFileFrom, false}, {"v0.5.7", NotesFileFrom, true}, {"nightly", NotesFileFrom, false},
	} {
		if got := AtLeast(c.v, c.from); got != c.want {
			t.Errorf("AtLeast(%q, %q) = %v", c.v, c.from, got)
		}
	}
}

func TestReleaseNotesFile(t *testing.T) {
	entry := entryWith(nil)
	same := "### Summary\n\n" + words(45) + ".\n\nSecond paragraph.\n\n### Upgrading\n\nNothing to do.\n\n### Added\n\n- x " + pr1 + "\n"
	entry2 := strings.Replace(entry, words(45)+".", words(45)+".\n\nSecond paragraph.", 1)
	cases := []struct {
		name, notes, entry, want string
	}{
		{"identical text", same, entry2, ""},
		{"whitespace differences are ignored", strings.ReplaceAll(same, "\n\n", "\n\n\n"), entry2, ""},
		{"a different section after Upgrading is free", strings.Replace(same, "### Added", "### Known issues\n\nnone\n\n### Added", 1), entry2, ""},
		{"a changed Summary", strings.Replace(same, "Second paragraph.", "Other.", 1), entry2, "Summary of the release notes file differs"},
		{"a changed Upgrading", strings.Replace(same, "Nothing to do.", "Run it again.", 1), entry2, "Upgrading section of the release notes file differs"},
		{"a longer Upgrading with a subsection that differs", strings.Replace(same, "Nothing to do.", "Nothing to do.\n\n#### Detail\n\nMore.", 1), entry2, "Upgrading section"},
		{"the placeholder", strings.Replace(same, "Second", "REPLACE THIS Second", 1), entry2, "REPLACE THIS"},
		{"no Summary heading", "Hello.\n", entry2, "no \"### Summary\""},
		{"a commit hash line", same + "- x (a1b2c3d)\n", entry2, "commit hash"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ReleaseNotesFile(c.notes, c.entry)
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
