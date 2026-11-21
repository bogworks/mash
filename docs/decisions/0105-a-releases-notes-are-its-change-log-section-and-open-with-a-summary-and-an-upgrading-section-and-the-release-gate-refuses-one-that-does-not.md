---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# A release's notes are its change-log section and open with a summary and an Upgrading section, and the release gate refuses one that does not

## Context and Problem Statement

The release standard's rule 23 says a release's notes and its change-log section are the same text, and that it opens with
prose before any list. The draft job did not do that: it wrote `# <tag>` and the `git log` subject lines of the range, and
the change log assembled from fragments was only lists. The maintainer, on 2026-11-12: "There is no prose, as required, in
the changelog or the release. It's just a laundry list of bullet points instead of what we agreed to already."

Nothing in the gate noticed, because nothing judged the text. How does mash make the rule a check that a release cannot get
around, without re-judging releases that were made before it?

## Decision Drivers

* A reader of a release (an operator deciding whether to upgrade) needs what the release is for, what to do and how to
  verify it before any list of changes.
* The notes and the change-log section must not drift apart: one text, produced once.
* The test must be one implementation, used by the workflow and by the gate, so they cannot disagree.
* A maintainer must not be able to ship the template: the text `assemble` writes has to fail the gate.
* Releases already published keep their notes, and the cutover release's own notes were made before the rule.

## Considered Options

* Judge the text: a Summary and an Upgrading section, the change-log section as the release notes, and gates that refuse
  what fails, from the cutover on.
* Keep the notes as generated from commits and add a prose paragraph by hand on the release page.
* State the rule in the release docs only.

## Decision Outcome

Chosen option: "Judge the text", because it is the only option where the rule fails closed, and one shared implementation
keeps the workflow and the gate in agreement.

* **The opening test** (package `internal/releasenotes`, used by `scripts/changelog`, `scripts/releasecheck` and their
  tests). The first heading is `### Summary`, followed by at least **40 words** of paragraph text before the first list item
  or heading; a later `### Upgrading` heading is followed by at least **one sentence** of paragraph text before any list (a
  release with nothing to do says so in a sentence: "Nothing to do."). Paragraph text is a line that is not a heading, a
  list item (`-`, `*`, `+`, `1.`), a table row (starts with `|`) or inside a code fence; the words are counted over the
  paragraph lines between the heading and the first list or heading.
* **The placeholder.** `go run ./scripts/changelog assemble` writes, directly under the new `## [X.Y.Z] - date` heading, a
  `### Summary` with a paragraph starting `REPLACE THIS PARAGRAPH:` and a `### Upgrading` with a sentence starting
  `REPLACE THIS:`, then the existing fragment-derived lists. Text containing `REPLACE THIS` is refused by every gate below.
* **Check 1, the change-log section, before tagging.** `release-check <X> <version>` has a step that reads `CHANGELOG.md` at
  X from git (never the working tree) and refuses a version whose `## [<version>] - date` section is missing, fails the
  opening test or holds the placeholder. Only that version's section is judged. It applies when X records a cutover
  (`docs/release-cutover`) and the version is **at or above** it, so the cutover release's own section is judged and must
  pass; below the cutover, or with no cutover record, nothing is judged.
* **Check 2, the release body, at `--assets` and `publish`.** The draft or published release's body (`gh release view <tag>
  --json body`) is refused when it fails the same opening test, has no link containing
  `compare/v<previous>...v<version>` (the previous release is the highest earlier published `vN.N.N`; when none is known,
  any `/compare/v` link ending `...v<version>`), has a line ending in a parenthesized commit hash of 7 to 40 hex digits, or
  contains `REPLACE THIS`. It is judged only for a version **strictly above** the cutover: the cutover release (v0.4.0) was
  published before this rule and its body is not re-judged, and earlier releases never are. The section is judged from the
  cutover up and the body from just above it, because a section can still be corrected before the tag exists and a published
  body cannot.
* **Check 3, the draft job.** `release.yml`'s `draft` job writes `dist/CHANGELOG.md` from
  `go run ./scripts/changelog section --version X.Y.Z` (the section body without its heading; it fails when the version has
  no section), appends one final paragraph, `Full changes: [v<previous>...v<tag>](https://github.com/<repository>/compare/v<previous>...v<tag>)`,
  with the previous release from `scripts/notes-base`, and runs `go run ./scripts/changelog check-notes` on the result. A
  missing section or notes that fail the test fail the job before the draft is created. Raw commit subjects are no longer
  the notes. The first release, which has no previous release, has no compare line.

### Confirmation

Unit tests in `internal/releasenotes` cover the 39-word and 40-word summaries, a Summary that opens with a list, a Summary
that is not the first heading, a missing Upgrading, an Upgrading that opens with a list, code fences and table lines, the
hash-ending line, the compare link and the placeholder. `scripts/releasecheck` tests cover the section gate (a good section
passes at X, a bad one is refused, a version below or with no cutover and an older neighbouring section are not judged) and
the body gate (strictly-above scope, good, bad). `scripts/changelog` tests cover `section`, `check-notes` and the
placeholders after `assemble`, and `internal/release` runs the draft job's notes step against a scratch repository.

### Consequences

* Good, because the notes a reader sees open with what the release is for and what to do, and are the text the maintainer
  reviewed in the change log.
* Good, because the template cannot be released: the placeholder fails both gates and the draft job.
* Good, because the workflow and the gate share one implementation.
* Bad, because release preparation now has a writing step (two placeholders) that no tool can do.
* Bad, because the thresholds are a floor: 40 words of filler passes. A reviewer still reads the Summary.
* Bad, because the previous release the gate expects (highest earlier published tag) and the one the draft job uses (highest
  published ancestor, from `scripts/notes-base`) are found differently; when they differ the gate refuses the body and the
  maintainer corrects the link.

## Pros and Cons of the Options

### Judge the text (chosen)

* Good, because the rule fails closed and is checked where the release is made and where it is published.
* Bad, because it is a heuristic over Markdown, not a judgement of the writing.

### Keep the notes as generated from commits and add a prose paragraph by hand on the release page

* Good, because it needs no change to the workflow.
* Bad, because the change log and the notes diverge, and a hand edit on the release page is not reviewed or checked.

### State the rule in the release docs only

* Good, because it is free.
* Bad, because that is the state that produced a release of bullet points; nothing checked it.

## More Information

The rule is rule 23 of the release standard. The cutover is recorded by [ADR 0095](0095-releases-from-the-cutover-version-are-keyless-the-tag-is-pushed-by-the-minting-service-and-attestations-replace-the-signatures.md);
the change log is assembled from fragments by [ADR 0068](0068-a-change-log-entry-is-a-fragment-file-and-the-adr-index-is-generated.md).
See [docs/release.md](../release.md) for the procedure.
