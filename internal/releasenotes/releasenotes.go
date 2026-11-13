// Package releasenotes is the one place that judges a release's notes (ADR 0105, release standard rule 23): a release's
// notes and its change-log section are the same text, and it opens with prose before any list.
//
// The opening test (Opening): the first heading is "### Summary" and at least MinSummaryWords words of paragraph text
// follow it before the first list item or heading; a later "### Upgrading" heading is followed by at least one sentence
// of paragraph text before any list (a release with nothing to do says so in a sentence). Paragraph text is any line that
// is not a heading, a list item ("-", "*", "+" or "1."), a table row (starts with "|") or inside a code fence. The
// release body adds three more tests: NoCommitHashLines, HasCompareLink and NoPlaceholder. NoPlaceholder also applies to
// the change-log section.
//
// scripts/changelog (the draft job's notes) and scripts/releasecheck (the gate) both import this package, so the two can
// never judge differently.
package releasenotes

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// MinSummaryWords is the least paragraph text a Summary must hold before its first list or heading.
	MinSummaryWords = 40
	// Placeholder is the marker `changelog assemble` writes for the maintainer to replace; every gate refuses it.
	Placeholder = "REPLACE THIS"
	// SummaryHeading and UpgradingHeading are the two headings a release opens with, in this order.
	SummaryHeading   = "### Summary"
	UpgradingHeading = "### Upgrading"
)

var (
	listItemRE = regexp.MustCompile(`^\s*([-*+]|[0-9]+\.)(\s|$)`)
	headingRE  = regexp.MustCompile(`^\s{0,3}#{1,6}(\s|$)`)
	hashLineRE = regexp.MustCompile(`\(\s*[0-9a-f]{7,40}\s*\)\s*$`)
)

type kind int

const (
	para kind = iota
	heading
	list
	skipped // blank, table row, code fence line
)

type line struct {
	text string
	kind kind
}

func classify(text string) []line {
	var out []line
	fence := false
	for _, ln := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~"):
			fence = !fence
			out = append(out, line{ln, skipped})
		case fence, t == "", strings.HasPrefix(t, "|"):
			out = append(out, line{ln, skipped})
		case headingRE.MatchString(ln):
			out = append(out, line{ln, heading})
		case listItemRE.MatchString(ln):
			out = append(out, line{ln, list})
		default:
			out = append(out, line{ln, para})
		}
	}
	return out
}

// prose returns the paragraph text between the heading at index h and the next list item or heading.
func prose(ls []line, h int) string {
	var b []string
	for _, l := range ls[h+1:] {
		if l.kind == heading || l.kind == list {
			break
		}
		if l.kind == para {
			b = append(b, strings.TrimSpace(l.text))
		}
	}
	return strings.Join(b, "\n")
}

func headingIs(l line, want string) bool {
	return l.kind == heading && strings.EqualFold(strings.TrimRight(strings.TrimSpace(l.text), " #"), want)
}

// Opening judges the opening of a release's notes or of one change-log section body (the text under the
// "## [X.Y.Z] - date" heading, without that heading).
func Opening(text string) error {
	ls := classify(text)
	first := -1
	for i, l := range ls {
		if l.kind == heading {
			first = i
			break
		}
		if l.kind == list {
			return fmt.Errorf("the notes start with a list before any heading: they must open with %q and prose", SummaryHeading)
		}
	}
	if first < 0 {
		return fmt.Errorf("the notes have no %q heading", SummaryHeading)
	}
	if !headingIs(ls[first], SummaryHeading) {
		return fmt.Errorf("the first heading is %q, not %q: the notes must open with a summary", strings.TrimSpace(ls[first].text), SummaryHeading)
	}
	if n := len(strings.Fields(prose(ls, first))); n < MinSummaryWords {
		return fmt.Errorf("the Summary has %d words of paragraph text before its first list or heading, at least %d are required: say what the release is for, what changed for the user and how to verify it", n, MinSummaryWords)
	}
	up := -1
	for i := first + 1; i < len(ls); i++ {
		if headingIs(ls[i], UpgradingHeading) {
			up = i
			break
		}
	}
	if up < 0 {
		return fmt.Errorf("there is no %q heading after the Summary: say what an operator must do after upgrading, or write \"Nothing to do.\"", UpgradingHeading)
	}
	p := prose(ls, up)
	if len(strings.Fields(p)) < 2 || !strings.ContainsAny(p, ".!?") {
		return fmt.Errorf("the Upgrading section has no sentence of paragraph text before its first list or heading: say what an operator must do, or write \"Nothing to do.\"")
	}
	return nil
}

// NoCommitHashLines refuses text with a line that ends in a parenthesized commit hash of 7 to 40 hex digits, the
// signature of a pasted `git log` subject list.
func NoCommitHashLines(text string) error {
	for i, ln := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if hashLineRE.MatchString(ln) {
			return fmt.Errorf("line %d ends in a parenthesized commit hash (%q): the notes are prose, not a commit list", i+1, strings.TrimSpace(ln))
		}
	}
	return nil
}

// HasCompareLink requires a link to the comparison of the previous release and this one: `compare/v<prev>...v<version>`.
// With prev empty (the previous release is not known) it requires only `/compare/v` and `...v<version>`.
func HasCompareLink(text, prev, version string) error {
	prev, version = strings.TrimPrefix(prev, "v"), strings.TrimPrefix(version, "v")
	if prev != "" {
		if want := "compare/v" + prev + "...v" + version; !strings.Contains(text, want) {
			return fmt.Errorf("the notes have no link to the full changes (%q)", want)
		}
		return nil
	}
	if !strings.Contains(text, "/compare/v") || !strings.Contains(text, "...v"+version) {
		return fmt.Errorf("the notes have no link to the full changes (\"/compare/v<previous>...v%s\")", version)
	}
	return nil
}

// NoPlaceholder refuses text that still holds the marker `changelog assemble` wrote.
func NoPlaceholder(text string) error {
	if strings.Contains(text, Placeholder) {
		return fmt.Errorf("the text still contains %q: replace the Summary and Upgrading placeholders", Placeholder)
	}
	return nil
}

// SectionBody is the text under the "## [version]" heading of the change log up to the next "## " heading, without the
// heading line and without leading or trailing blank lines. ok is false when there is no such section.
func SectionBody(changelog, version string) (body string, ok bool) {
	lines := strings.Split(strings.ReplaceAll(changelog, "\r\n", "\n"), "\n")
	start := -1
	fence := false
	for i, ln := range lines {
		if strings.HasPrefix(ln, "```") || strings.HasPrefix(ln, "~~~") {
			fence = !fence
		}
		if fence {
			continue
		}
		if start < 0 {
			if strings.HasPrefix(ln, "## ["+version+"]") {
				start = i
			}
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			lines = lines[:i]
			break
		}
	}
	if start < 0 {
		return "", false
	}
	return strings.Trim(strings.Join(lines[start+1:], "\n"), "\n") + "\n", true
}
