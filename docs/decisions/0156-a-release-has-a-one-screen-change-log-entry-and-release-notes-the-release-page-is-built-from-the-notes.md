---
status: accepted
date: 2026-11-21
decision-makers: Randy Bias (mash maintainer)
---

# A release has a one-screen change-log entry and separate release notes, the release page is built from the notes, and the gate checks nine of the thirteen rubric questions

## Context and Problem Statement

[ADR 0105](0105-a-releases-notes-are-its-change-log-section-and-open-with-a-summary-and-an-upgrading-section-and-the-release-gate-refuses-one-that-does-not.md) made a release's notes and its change-log section the same text. That
fixed the laundry list of commit subjects, and then the single text grew: the 0.5.6 change-log section holds a quick start,
install steps, a roadmap and the full list of known issues. The maintainer, on 2026-11-21: "Quick Start does not belong in
a CHANGELOG ... develop a cleaner CHANGELOG format with masterplanner and a rubric to evaluate it against."

The release standard (release@0.9.0, rule 23) now says a release has two documents with two jobs. How does mash build and
gate them?

## Decision Drivers

* A change log is a record: one screen, what changed for a user, one link per change.
* Release notes are for reading: why, upgrading step by step, how to verify, the known issues a newcomer meets first.
* The two must not contradict each other: the Summary and Upgrading are the same sentences in both.
* The gate must refuse what a machine can judge and leave the rest to the reviewer.
* A release already cut under the old rule (0.5.6) must not be re-judged.

## Considered Options

* Two documents: the change-log entry is the record, `docs/release-notes/<version>.md` is the reading document, the
  release page is built from the notes, and a rubric the gate applies.
* Keep one text and move the quick start out by convention.
* Build the release page from the change log and link the notes.

## Decision Outcome

Chosen option: "Two documents", because it is the standard's rule and the only option where each text can be held to its
own job by a check.

* **The change-log entry** (`## [X.Y.Z] - date` in `CHANGELOG.md`) is at most 60 lines. It holds `### Summary` (at
  least 40 words of prose), `### Upgrading`, then `### Added`, `### Changed`, `### Removed`, `### Fixed` and
  `### Security` (no other heading), one line per change with exactly one pull-request or issue link and no commit hash,
  and two trailing plain lines: `Known issues:` (one line, a count and a link) and `Links:` (the release notes, the compare
  view and the verify instructions). It holds no code block, no install steps, no roadmap text.
* **The release notes** (`docs/release-notes/<version>.md`) open with the same Summary and Upgrading text, then hold why
  this release, upgrading in detail, how to verify, the known issues, what is not in it, the quick start and links.
* **The release page is built from the notes file**, not from the change log (`scripts/changelog release-page`, used by
  `release.yml`'s draft job and the dry run).
* **The gate** (`internal/releasenotes`, used by `scripts/changelog` and `scripts/releasecheck`) checks rubric questions
  1, 2, 4, 6, 7, 8, 9, 10 and 12 of rule 23 on the entry, requires the notes file at X with the same Summary and
  Upgrading, and at `publish` and `--assets` requires the page body to equal the notes file (plus the final
  `Full changes:` paragraph the draft job appends). Questions 3, 5, 11 and 13 are a reader's call, made in review.
* **From 0.5.7.** Versions below `releasenotes.Rule23From` keep the ADR 0105 behaviour: v0.5.6 was cut under it, and its
  change-log section was its release page. Its page is rebuilt from `docs/release-notes/0.5.6.md` after publishing, which
  rule 25 allows (the tag does not move).

### Confirmation

Unit tests in `internal/releasenotes` cover each machine-checked question, passing and failing, and the two-document
comparison. `scripts/releasecheck` tests cover the entry gate, the notes-file gate and the body gate at a version at or
above `Rule23From`, and that a version below it behaves as before. `scripts/changelog` tests cover `release-page` and
`check-entry`; `internal/release` checks the workflow text.

### Consequences

* Good, because the change log reads in one screen and the notes carry the reading.
* Good, because the release page is the notes the maintainer reviewed, checked by the gate.
* Bad, because there are two documents to write, and a Summary and Upgrading that must be kept equal by hand (the gate
  refuses when they differ).
* Bad, because the checks are heuristics over Markdown: a 40-word Summary of filler passes, and a reviewer still reads it.
* Bad, because versions below 0.5.7 keep a second code path until it is no longer exercised.

## Pros and Cons of the Options

### Two documents (chosen)

* Good, because each document has one job and a check.
* Bad, because of the duplicated Summary and Upgrading.

### Keep one text and move the quick start out by convention

* Good, because it needs no code.
* Bad, because nothing holds the line: that is how the quick start got in.

### Build the release page from the change log and link the notes

* Good, because it changes less.
* Bad, because the page a newcomer lands on is the record, not the reading document, which is what the standard rules out.

## More Information

Release standard rules 23 and 25 (bogworks-dev-standards, release@0.9.0). This partly supersedes
[ADR 0105](0105-a-releases-notes-are-its-change-log-section-and-open-with-a-summary-and-an-upgrading-section-and-the-release-gate-refuses-one-that-does-not.md)
(its same-text rule) from 0.5.7. See [docs/release.md](../release.md) for the procedure.
