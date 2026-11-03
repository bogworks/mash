---
status: accepted
date: 2026-11-03
decision-makers: Randy Bias (mash maintainer)
---

# A change-log entry is a fragment file in `changelog.d/`, and the ADR index is generated, so a pull request edits neither shared file

## Context and Problem Statement

Two files are edited by nearly every pull request: `CHANGELOG.md` (an entry under Unreleased) and the ADR index in
`docs/decisions/README.md` (a row per record). Both are append-at-the-end lists, so any two pull requests in the merge
queue at the same time conflict on them. On 2026-11-02 eight queue ejections were such conflicts, and each costs a
rebase, a new run of the checks and a trip through review again. [ADR 0002](0002-record-architecture-decisions-as-madr-records.md)
made the README the index of records and said a pull request that adds a record also lists it.

## Decision Drivers

* A conflict that two correct pull requests cannot avoid must be designed out, not resolved by hand.
* Entries stay reviewable in the pull request that makes the change.
* The release gate must still prove that the change log and the index are complete at the release commit.

## Considered Options

* Keep the shared files and ask contributors to rebase more often.
* A merge driver or `.gitattributes` union merge for the two files.
* One fragment file per pull request, assembled into `CHANGELOG.md` when a release is prepared; the ADR index generated from the records.

## Decision Outcome

Chosen option: fragments and a generated index.

* **Fragments.** A user-facing change adds `changelog.d/<slug>.<kind>.md`, where kind is one of `added`, `changed`,
  `deprecated`, `removed`, `fixed`, `security` (the Keep a Changelog sections) and the file holds the entry's text
  without a leading bullet. `go run ./scripts/changelog assemble --version X.Y.Z --date YYYY-MM-DD` folds every
  fragment, and whatever is already under Unreleased, into a new `## [X.Y.Z] - date` section in the usual section order,
  leaves Unreleased empty and removes the fragments. `go run ./scripts/changelog check` lints the fragments (name, kind,
  not empty, no leading bullet) and runs in CI. A pull request does not edit `CHANGELOG.md`; release preparation does.
* **Index.** `go run ./scripts/adrlint index --write` regenerates `docs/decisions/INDEX.md` from the records' front
  matter (number, title, status, date). `adrlint docs` no longer requires a record to be listed in the README or a
  `Decision: ADR-NNNN` line to name a listed one: the record existing in the directory, and in the pull request's
  changed paths, is the rule. The README keeps its prose and its table of layers up to 0066, frozen. `INDEX.md` is
  regenerated in release preparation, with the change log, so it is never part of an ordinary pull request.
* **Gate.** `scripts/release-check` refuses a release commit that still has fragments in `changelog.d/` (they were not
  assembled) or whose `INDEX.md` differs from what `adrlint index` generates.

A union merge driver is rejected because it depends on every clone's configuration and still mixes entries from
different pull requests without an owner. More frequent rebasing is rejected because the queue, not the author, orders
the merges.

### Consequences

* Good: two pull requests no longer conflict on the change log or the index.
* Good: an entry is a small file reviewed with its change; release preparation is one command.
* Bad: the index is stale between releases; `adrlint index` prints the current one at any time.
* Bad: the README's layer column is not regenerated; records after 0066 have no layer in an index.
* Bad: a pull request that forgets its fragment has no entry; review checks for one on user-facing changes.

### Confirmation

* Tests for `scripts/changelog` (assembly order, empty Unreleased, bad names and kinds) and for `adrlint index` (output,
  `--check` against a stale file) and `adrlint docs` accepting an unlisted record.
* `release-check` tests for the two new refusals.

## Pros and Cons of the Options

### Ask for more frequent rebases

* Good, because nothing changes.
* Bad, because the queue merges in its own order and the conflict returns.

### A merge driver

* Good, because the files stay as they are.
* Bad, because every clone must configure it, and CI and the web editor do not.

### Fragments and a generated index

* Good, because the shared files are written once, by one command, at release time.
* Bad, because the index is not current between releases.

## More Information

* Amends [ADR 0002](0002-record-architecture-decisions-as-madr-records.md) (the index) and the change-log practice in CONTRIBUTING.
* Sources: the 2026-11-02 review of queue ejections.
