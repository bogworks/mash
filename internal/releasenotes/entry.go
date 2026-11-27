package releasenotes

// From 0.5.7 a release has two documents. The change-log entry is the record, about one screen, in a fixed
// format; the release notes (docs/release-notes/<version>.md) are the reading document, and the release page is built from
// that file. ChangelogEntry judges the entry against the machine-checkable questions of the rule's rubric; ReleaseNotesFile
// judges the notes file and that it shares the entry's Summary and Upgrading text.

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// NotesFileFrom is the first version whose release page is the release notes file. Earlier versions keep the
	// ADR 0105 rule: their release notes are their change-log section, byte for byte.
	NotesFileFrom = "0.5.7"
	// MaxEntryLines is the most lines a change-log entry may have (rubric 1).
	MaxEntryLines = 60
)

// ChangeHeadings are the headings of the grouped changes, in the order they must appear after Summary and Upgrading.
var ChangeHeadings = []string{"Added", "Changed", "Removed", "Fixed", "Security"}

var (
	mdLinkRE     = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]*)\)`)
	bareURLRE    = regexp.MustCompile(`https?://[^\s)>\]]+`)
	changeURLRE  = regexp.MustCompile(`^https://github\.com/[^/\s]+/[^/\s]+/(pull|issues)/[0-9]+(#\S*)?$`)
	bareIssueRE  = regexp.MustCompile(`(^|[^\w/&])#[0-9]+\b`)
	hexWordRE    = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	roadmapRE    = regexp.MustCompile(`(?i)roadmap`)
	futureRE     = regexp.MustCompile(`(?i)^\W*(not in|coming in|planned for)\b`)
	shellLineRE  = regexp.MustCompile(`^(curl\b|sh\s|\$\s)`)
	digitsRE     = regexp.MustCompile(`[0-9]+`)
	versionPartE = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)$`)
)

// AtLeast is whether version (X.Y.Z, optionally with a leading v) is at or above from. A version that is not X.Y.Z is
// never at or above.
func AtLeast(version, from string) bool {
	v, ok1 := parseVersion(version)
	f, ok2 := parseVersion(from)
	if !ok1 || !ok2 {
		return false
	}
	for i := range v {
		if v[i] != f[i] {
			return v[i] > f[i]
		}
	}
	return true
}

func parseVersion(s string) ([3]int, bool) {
	m := versionPartE.FindStringSubmatch(s)
	var v [3]int
	if m == nil {
		return v, false
	}
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func rubric(n int, format string, a ...any) error {
	return fmt.Errorf("rubric %d: %s", n, fmt.Sprintf(format, a...))
}

// linkURLs is every URL a line links: markdown link targets and bare URLs.
func linkURLs(s string) []string {
	var urls []string
	for _, m := range mdLinkRE.FindAllStringSubmatch(s, -1) {
		urls = append(urls, m[2])
	}
	rest := mdLinkRE.ReplaceAllString(s, "$1")
	return append(urls, bareURLRE.FindAllString(rest, -1)...)
}

// changeLinks is how many pull request or issue links a bullet holds: markdown links and bare URLs to
// github.com/<org>/<repo>/(pull|issues)/N, and bare "#N" references.
func changeLinks(s string) int {
	n := 0
	for _, m := range mdLinkRE.FindAllStringSubmatch(s, -1) {
		if changeURLRE.MatchString(m[2]) {
			n++
		}
	}
	rest := mdLinkRE.ReplaceAllString(s, " ")
	for _, u := range bareURLRE.FindAllString(rest, -1) {
		if changeURLRE.MatchString(u) {
			n++
		}
	}
	rest = bareURLRE.ReplaceAllString(rest, " ")
	return n + len(bareIssueRE.FindAllString(rest, -1))
}

// withoutLinks is s with every markdown link replaced by its text and every bare URL removed.
func withoutLinks(s string) string {
	return bareURLRE.ReplaceAllString(mdLinkRE.ReplaceAllString(s, "$1"), " ")
}

func headingLevel(text string) int {
	t := strings.TrimSpace(text)
	return len(t) - len(strings.TrimLeft(t, "#"))
}

func headingName(text string) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(text), "#")), "#"))
}

// ChangelogEntry judges one change-log entry body (the text under "## [X.Y.Z] - date", without that heading) for version
// against the machine-checkable questions of the change-log rubric: 1, 2, 4, 6, 7, 8, 9, 10 and 12. Questions 3, 5, 11
// and 13 are a reader's call. It returns every failure, each naming its rubric number.
func ChangelogEntry(body, version string) error {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	raw := strings.Split(strings.TrimRight(body, " \t\n"), "\n")
	ls := classify(body)
	var errs []error
	add := func(err error) { errs = append(errs, err) }

	// 1: one screen.
	if len(raw) > MaxEntryLines {
		add(rubric(1, "the entry has %d lines, at most %d are allowed: cut it to one screen; the reading belongs in docs/release-notes/%s.md", len(raw), MaxEntryLines, version))
	}
	// 2 and 4: the same opening test the release notes face.
	first, err := openingSummary(ls)
	if err != nil {
		add(rubric(2, "%v", err))
	} else if err := openingUpgrading(ls, first); err != nil {
		add(rubric(4, "%v", err))
	}
	if err := NoPlaceholder(body); err != nil {
		add(rubric(2, "%v", err))
	}

	// 6: only the seven fixed headings, in order; every list item under one of them.
	order := append([]string{"Summary", "Upgrading"}, ChangeHeadings...)
	current := ""
	seen := map[string]bool{}
	nextAllowed := 0
	for i, l := range ls {
		n := i + 1
		switch l.kind {
		case heading:
			if lvl := headingLevel(l.text); lvl != 3 {
				add(rubric(6, "line %d: %q is a level-%d heading: only the seven \"###\" headings Summary, Upgrading, Added, Changed, Removed, Fixed and Security are allowed", n, strings.TrimSpace(l.text), lvl))
				current = ""
				continue
			}
			name := headingName(l.text)
			idx := -1
			for j, o := range order {
				if o == name {
					idx = j
				}
			}
			switch {
			case idx < 0:
				add(rubric(6, "line %d: %q is not one of the fixed headings (Summary, Upgrading, Added, Changed, Removed, Fixed, Security): move its lines under one of them or delete it", n, strings.TrimSpace(l.text)))
				current = ""
			case seen[name]:
				add(rubric(6, "line %d: the heading %q appears twice: merge the two", n, name))
				current = name
			case idx < nextAllowed:
				add(rubric(6, "line %d: %q is out of order: the order is Summary, Upgrading, Added, Changed, Removed, Fixed, Security", n, name))
				current = name
				seen[name] = true
			default:
				seen[name] = true
				nextAllowed = idx + 1
				current = name
			}
		case list:
			switch {
			case current == "":
				add(rubric(6, "line %d: a list item sits under no fixed heading: put it under Added, Changed, Removed, Fixed or Security", n))
			case current == "Summary":
				add(rubric(6, "line %d: a list item under Summary: the Summary is prose; move the item under a change heading", n))
			}
		}
	}

	// 7: every change bullet links exactly one pull request or issue, with no commit hash.
	current = ""
	for i, l := range ls {
		n := i + 1
		if l.kind == heading {
			current = headingName(l.text)
			continue
		}
		if !isChange(current) {
			continue
		}
		t := strings.TrimSpace(l.text)
		if l.kind == para && !strings.HasPrefix(t, "Known issues:") && !strings.HasPrefix(t, "Links:") {
			add(rubric(7, "line %d: a line under %s that is not a bullet: every change is one bullet line with one link (a wrapped line belongs on the bullet's line)", n, current))
			continue
		}
		if l.kind != list {
			continue
		}
		if c := changeLinks(t); c != 1 {
			add(rubric(7, "line %d: the change line links %d pull requests or issues, exactly 1 is required: add or remove a link to github.com/<org>/<repo>/pull/N or /issues/N", n, c))
		}
		for _, w := range hexWordRE.FindAllString(withoutLinks(t), -1) {
			if strings.ContainsAny(w, "0123456789") && strings.ContainsAny(w, "abcdef") {
				add(rubric(7, "line %d: %q looks like a commit hash: say what the user sees and link the pull request or issue instead", n, w))
			}
		}
	}

	// 8 and 9, over every line of the entry.
	for i, ln := range raw {
		n := i + 1
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~"):
			add(rubric(8, "line %d: a fenced code block: the entry holds no example or install step; move it to docs/release-notes/%s.md", n, version))
		case strings.HasPrefix(ln, "    ") || strings.HasPrefix(ln, "\t"):
			add(rubric(8, "line %d: an indented line (code): the entry holds no example; move it to docs/release-notes/%s.md", n, version))
		case shellLineRE.MatchString(t):
			add(rubric(8, "line %d: a shell command (%q): the entry holds no install step or quick start; move it to docs/release-notes/%s.md", n, firstWords(t), version))
		}
		if roadmapRE.MatchString(ln) {
			add(rubric(9, "line %d: mentions a roadmap: the entry holds no roadmap; say what is next in docs/release-notes/%s.md", n, version))
		} else if futureRE.MatchString(ln) {
			add(rubric(9, "line %d: starts with %q: the entry says what changed, not what is planned; say it in docs/release-notes/%s.md", n, firstWords(t), version))
		}
	}
	if err := NoCommitHashLines(body); err != nil {
		add(rubric(7, "%v", err))
	}

	// 10: exactly one Known issues line with a count and a link; 12: exactly one Links line with three links.
	var known, links []string
	for _, ln := range raw {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "Known issues:") {
			known = append(known, t)
		}
		if strings.HasPrefix(t, "Links:") {
			links = append(links, t)
		}
	}
	switch {
	case len(known) != 1:
		add(rubric(10, "the entry has %d lines starting \"Known issues:\", exactly 1 is required: one line with a count and a link to the full list, for example \"Known issues: 2, see [the list](https://github.com/<org>/<repo>/issues?q=...)\"", len(known)))
	default:
		urls := linkURLs(known[0])
		if len(urls) != 1 {
			add(rubric(10, "the Known issues line has %d links, exactly 1 is required: link the full list", len(urls)))
		}
		if !digitsRE.MatchString(withoutLinks(known[0])) {
			add(rubric(10, "the Known issues line has no count: say how many (\"0\" when there are none)"))
		}
	}
	switch {
	case len(links) != 1:
		add(rubric(12, "the entry has %d lines starting \"Links:\", exactly 1 is required: it links the release notes, the compare view and the verify instructions", len(links)))
	default:
		urls := linkURLs(links[0])
		var notes, compare, verify bool
		for _, u := range urls {
			notes = notes || strings.Contains(u, "release-notes/"+version+".md")
			compare = compare || strings.Contains(u, "compare/")
			verify = verify || strings.Contains(strings.ToLower(u), "verify")
		}
		if !notes {
			add(rubric(12, "the Links line has no link to the release notes (a URL containing %q): add it", "release-notes/"+version+".md"))
		}
		if !compare {
			add(rubric(12, "the Links line has no compare link (a URL containing \"compare/\"): add the compare view from the previous tag"))
		}
		if !verify {
			add(rubric(12, "the Links line has no link to how to verify the download (a URL containing \"verify\", such as docs/release-notes/%s.md#verify-what-you-download): add it", version))
		}
		if len(urls) < 3 {
			add(rubric(12, "the Links line has %d links, at least 3 (release notes, compare, verify) are required", len(urls)))
		}
	}
	return errors.Join(errs...)
}

func isChange(h string) bool {
	for _, c := range ChangeHeadings {
		if c == h {
			return true
		}
	}
	return false
}

func firstWords(s string) string {
	f := strings.Fields(s)
	if len(f) > 3 {
		f = f[:3]
	}
	return strings.Join(f, " ")
}

func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// upgradingSection is the text under the "### Upgrading" heading up to the next heading of level 3 or higher.
func upgradingSection(text string) (string, bool) {
	ls := classify(text)
	for i, l := range ls {
		if !headingIs(l, UpgradingHeading) {
			continue
		}
		var b []string
		for _, m := range ls[i+1:] {
			if m.kind == heading && headingLevel(m.text) <= 3 {
				break
			}
			b = append(b, m.text)
		}
		return normalize(strings.Join(b, "\n")), true
	}
	return "", false
}

func summaryProse(text string) (string, bool) {
	ls := classify(text)
	for i, l := range ls {
		if headingIs(l, SummaryHeading) {
			return normalize(prose(ls, i)), true
		}
	}
	return "", false
}

// ReleaseNotesFile judges a release notes file (docs/release-notes/<version>.md) against the change-log entry of the same
// release: it opens with a Summary and an Upgrading section, holds no placeholder and no line ending in a commit hash, and
// its Summary prose and Upgrading section are the entry's, whitespace aside (the same sentences in both).
func ReleaseNotesFile(notes, entry string) error {
	errs := []error{NoPlaceholder(notes), Opening(notes), NoCommitHashLines(notes)}
	ns, ok1 := summaryProse(notes)
	es, ok2 := summaryProse(entry)
	if ok1 && ok2 && ns != es {
		errs = append(errs, errors.New("the Summary of the release notes file differs from the Summary of the change-log entry: the two must be the same sentences; copy one over the other"))
	}
	nu, ok1 := upgradingSection(notes)
	eu, ok2 := upgradingSection(entry)
	if ok1 && ok2 && nu != eu {
		errs = append(errs, errors.New("the Upgrading section of the release notes file differs from the Upgrading section of the change-log entry: the two must be the same text; copy one over the other"))
	}
	return errors.Join(errs...)
}
