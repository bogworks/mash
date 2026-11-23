---
status: accepted
date: 2026-11-23
decision-makers: Randy Bias (dev-process rule 21); x-lead and mash-lead for the table
---

# macOS runs on tagged releases only, the ephemeral pilot runs nightly, and one pinned table says what runs when

Amends: ADR-0079, ADR-0123

## Context and Problem Statement

Rule 21 (dev-process, Randy Bias, 2026-11-21) says a change is tested once, nothing is tested twice, macOS runs on tagged releases only, and an ephemeral-runner pilot belongs off the per-change path. Today `test (macos)` and `test (linux, eph)` run on every merge-queue draft (ADR 0079, ADR 0123). Both are advisory and gate nothing, but each takes a runner slot and holds a queue batch open when its runner is busy or off.

## Decision Drivers

* The queue tests a batch once; an advisory leg must not lengthen it.
* macOS still has to be exercised for every release.
* The ephemeral-runner pilot needs its comparison with `rh-linux`, but not per change.
* What runs on which event must be one table that a test pins.

## Considered Options

* Keep both advisory legs on queue drafts.
* Move macOS to the release workflow and the eph pilot to a nightly schedule on main, with one pinned table.
* Delete the eph pilot.

## Decision Outcome

Chosen option: the second.

* `ci.yml` has no `test-macos` and no `test-eph`. A queue draft runs `changes`, `test (linux)`, `reproducible`, `pr-check` and the cheap jobs; an ordinary pull request push runs `changes`, `conformance`, `test (linux)` and the cheap jobs.
* `release.yml` gains `test (macos)` on every `v*` tag (and the dry run), on `[self-hosted, rh-macos]`, advisory as in ADR 0079: it is not in `release.RequiredChecks` and no job needs it, so a macOS runner outage never blocks a tag.
* `nightly.yml` runs `test (linux, eph)` on `[self-hosted, rh-linux-eph]` on a schedule on main (03:10 UTC). It gates nothing: not in `pr-check`, not in `RequiredChecks`, not in `.mergify.yml`.
* `docs/ci-table.md` is the one table of job by event, pinned by `internal/release/ci_table_test.go`.

### Consequences

* Good: nothing on macOS or eph per change; a queue batch waits for neither runner pool.
* Good: the pilot keeps one comparison a day; macOS is exercised for every tag.
* Bad: a macOS-only regression is seen at the tag, not on the change; the release dry run and the tag's `test (macos)` are where it shows.
* Bad: the nightly eph run has no per-change signal.

### Confirmation

* `internal/release/ci_table_test.go`, `workflows_test.go` and `workflows_selfhosted_test.go` pass; `adrlint` passes.

## Pros and Cons of the Options

### Keep both advisory legs on queue drafts

* Bad, because it is the per-change macOS and eph work rule 21 removes.

### Move macOS to tags and eph to nightly, with one pinned table

* Good, because it follows the rule and keeps both signals.
* Bad, because macOS findings arrive later.

### Delete the eph pilot

* Bad, because the runner pilot loses its comparison signal.

## More Information

Amends [ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md) and [ADR 0123](0123-an-advisory-shadow-test-job-runs-on-ephemeral-runners-under-a-fourth-label-list.md). Decided 2026-11-23.
