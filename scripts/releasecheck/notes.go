package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/releasenotes"
)

// From releasenotes.NotesFileFrom (0.5.7) the rule is two documents instead: the change-log entry is the record,
// judged by releasenotes.ChangelogEntry, and the release notes file docs/release-notes/<version>.md is the release page's
// text and shares the entry's Summary and Upgrading. Versions below it keep what follows.
//
// A release's notes and its change-log section are the same text, and it opens with prose before any list (ADR 0105).
// The tests live in internal/releasenotes, which scripts/changelog (the draft job) uses too.
//
// Two gates apply them, each only from the cutover recorded at X (docs/release-cutover); with no record, nothing is judged:
//
//   - the change-log section, before tagging: judged for the version at or above the cutover, so the cutover release's own
//     section must pass. Earlier sections are never judged, and neither is a neighbouring section of a judged version.
//   - the release body, at --assets and publish: judged only for a version STRICTLY above the cutover. The cutover release
//     itself (v0.4.0) was published before this rule and its body is not re-judged; earlier ones never are.

// changelogSectionOpens refuses a candidate whose change log has no section for the version, or whose section fails the
// opening test or holds the placeholder. It reads CHANGELOG.md at x from git, never the working tree.
func changelogSectionOpens(o Options, x string) error {
	cutover, ok, err := cutoverAt(o, x)
	if err != nil {
		return err
	}
	if !ok {
		return nil // no cutover recorded: not judged
	}
	c, _ := parseSemver(cutover) // cutoverAt vouched for it
	v, ok := parseSemver("v" + o.Version)
	if !ok {
		return fmt.Errorf("%q is not a release version X.Y.Z, so its change log section cannot be judged", o.Version)
	}
	if semverLess(v, c) {
		return nil // below the cutover: not judged
	}
	cl, has, err := fileAt(o.Repo, x, "CHANGELOG.md")
	if err != nil {
		return err
	}
	if !has {
		return fmt.Errorf("CHANGELOG.md is missing at %s", x)
	}
	body, found := releasenotes.SectionBody(cl, o.Version)
	if !found {
		return fmt.Errorf("CHANGELOG.md at %s has no section \"## [%s] - date\": run go run ./scripts/changelog assemble --version %s --date YYYY-MM-DD", x, o.Version, o.Version)
	}
	what := "the CHANGELOG.md section for " + o.Version
	if !releasenotes.AtLeast(o.Version, releasenotes.NotesFileFrom) {
		return errors.Join(prefixed(what, releasenotes.NoPlaceholder(body)), prefixed(what, releasenotes.Opening(body)), prefixed(what, releasenotes.NoCommitHashLines(body)))
	}
	// From releasenotes.NotesFileFrom: the entry is the record and conforms to the rubric; the release notes file
	// is the reading document and shares its Summary and Upgrading.
	errs := []error{prefixed(what, releasenotes.ChangelogEntry(body, o.Version))}
	file := notesFilePath(o.Version)
	notes, has, err := fileAt(o.Repo, x, file)
	if err != nil {
		return err
	}
	if !has {
		errs = append(errs, fmt.Errorf("%s is missing at %s: write the release notes (why this release, upgrading step by step, how to verify, known issues, what is next) and keep its Summary and Upgrading the same as the change log entry's", file, x))
	} else {
		errs = append(errs, prefixed(file, releasenotes.ReleaseNotesFile(notes, body)))
	}
	return errors.Join(errs...)
}

// notesFilePath is the release notes file of a version.
func notesFilePath(version string) string { return "docs/release-notes/" + version + ".md" }

// changelogStepName names the pre-tag gate step that judges the change log entry (and, from 0.5.7, the notes file).
func changelogStepName(version string) string {
	if releasenotes.AtLeast(version, releasenotes.NotesFileFrom) {
		return "the change log entry for " + version + " is one screen and conforms to the rubric, and the release notes file " + notesFilePath(version) + " exists and shares its Summary and Upgrading"
	}
	return "the change log section for " + version + " opens with a Summary and an Upgrading section"
}

func prefixed(what string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}

// releaseBodyReads refuses a release whose body is not the prose the standard asks for: the opening test, a link to
// compare/v<previous>...v<version>, no line ending in a parenthesized commit hash and no placeholder. It judges only a
// version strictly above the cutover recorded at x (see above). A failure to read the body or the release list is the
// gate's, not the candidate's.
func releaseBodyReads(o Options, x, tag string) error {
	cutover, ok, err := cutoverAt(o, x)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	c, _ := parseSemver(cutover)
	v, ok := parseSemver(tag)
	if !ok || !semverLess(c, v) {
		return nil // at or below the cutover: the body is not judged
	}
	out, err := gh(o, "release", "view", tag, "--repo", o.GHRepo, "--json", "body")
	if err != nil {
		return op(err)
	}
	var resp struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return op(fmt.Errorf("could not read the release's body: %w", err))
	}
	releases, err := listReleases(o)
	if err != nil {
		return err
	}
	prev := previousRelease(tag, releases)
	body := strings.ReplaceAll(resp.Body, "\r\n", "\n")
	// Below releasenotes.NotesFileFrom the notes are the change-log section at X (ADR 0105); from it, the release notes
	// file at X: an edited draft must not publish other text. The only addition allowed is the draft job's last
	// paragraph, the "Full changes:" compare link.
	var sameErr error
	if releasenotes.AtLeast(o.Version, releasenotes.NotesFileFrom) {
		file := notesFilePath(o.Version)
		notes, has, err := fileAt(o.Repo, x, file)
		if err != nil {
			return err
		}
		if !has {
			return fmt.Errorf("%s is missing at %s to compare the release body with", file, x)
		}
		sameErr = bodyIsText(body, notes, "the release body is not "+file+" (plus the final \"Full changes:\" compare link): the release page is built from the release notes file")
	} else {
		changelog, has, err := fileAt(o.Repo, x, "CHANGELOG.md")
		if err != nil {
			return err
		}
		section, found := releasenotes.SectionBody(changelog, o.Version)
		if !has || !found {
			return fmt.Errorf("CHANGELOG.md at %s has no section for %s to compare the release body with", x, o.Version)
		}
		sameErr = bodyIsSection(body, section)
	}
	return errors.Join(
		sameErr,
		releasenotes.NoPlaceholder(body),
		releasenotes.Opening(body),
		releasenotes.HasCompareLink(body, prev, o.Version),
		releasenotes.NoCommitHashLines(body),
	)
}

// previousRelease is the highest published release tag vN.N.N below tag, or "" when there is none.
func previousRelease(tag string, releases []releaseRow) string {
	t, ok := parseSemver(tag)
	if !ok {
		return ""
	}
	var best string
	var bv [3]int
	for _, r := range releases {
		v, ok := parseSemver(r.TagName)
		if r.IsDraft || !ok || !semverLess(v, t) {
			continue
		}
		if best == "" || semverLess(bv, v) {
			best, bv = r.TagName, v
		}
	}
	return strings.TrimPrefix(best, "v")
}

// bodyIsSection is nil when body is the change-log section text, optionally followed by one final paragraph that
// starts with "Full changes:" (the compare link the draft job appends). Trailing blank space is not compared.
func bodyIsSection(body, section string) error {
	return bodyIsText(body, section, "the release body is not the CHANGELOG.md section for this version (plus the final \"Full changes:\" compare link): the release notes and the change log must be the same text (ADR 0105)")
}

// bodyIsText is nil when body is section, optionally followed by one "Full changes:" paragraph; msg is the refusal.
func bodyIsText(body, section, msg string) error {
	body = strings.TrimRight(body, " \t\n")
	section = strings.TrimRight(strings.ReplaceAll(section, "\r\n", "\n"), " \t\n")
	if body == section {
		return nil
	}
	if rest, ok := strings.CutPrefix(body, section+"\n\n"); ok && strings.HasPrefix(rest, "Full changes:") && !strings.Contains(rest, "\n") {
		return nil
	}
	return errors.New(msg)
}

// releaseBodyStepName names the step that judges the release page's body.
func releaseBodyStepName(version string) string {
	if releasenotes.AtLeast(version, releasenotes.NotesFileFrom) {
		return "the release notes are " + notesFilePath(version) + " at X, open with a Summary and an Upgrading section, link the full changes, and are not a commit list"
	}
	return "the release notes open with a Summary and an Upgrading section, link the full changes, and are not a commit list"
}
