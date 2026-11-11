---
status: accepted
date: 2026-11-11
decision-makers: Randy Bias (mash maintainer)
---

# An ADR takes any number above the highest on the base branch, and there is no NEXT file

## Context and Problem Statement

[ADR 0070](0070-the-next-adr-number-is-a-file-next-and-the-lint-refuses-a-duplicate-or-stale-number.md) made the next ADR
number a file, `docs/decisions/NEXT`, that every pull request adding an ADR reads and bumps. A shared counter that many
branches write is a merge conflict by design. In a single day it cost six rebases and three numbering collisions (0078,
0086 and 0088): two pull requests that take the same number both change `NEXT` from N to N+1, which git merges cleanly
and which proves nothing, and a pull request that took a number and then fell behind main meets a conflict on `NEXT`
every time another ADR merges. The file also fails a pull request for a stale value after any rebase, although the
number the author took is still free.

What the file was for is a check, not a counter: a number is used once, and a pull request cannot take one that is
already there. That needs no shared state.

## Decision Drivers

* A duplicate number must still be caught before it reaches `main`, by a check ([ADR 0070](0070-the-next-adr-number-is-a-file-next-and-the-lint-refuses-a-duplicate-or-stale-number.md)'s driver, kept).
* Taking a number must not touch a file other pull requests also touch.
* A pull request that fell behind main must be told what is wrong with its number, not hit a conflict.
* The index of records stays release preparation's, never a pull request's
  ([ADR 0068](0068-a-change-log-entry-is-a-fragment-file-and-the-adr-index-is-generated.md), unchanged).

## Considered Options

* Keep `NEXT` and fix the pain with tooling (a bot that bumps it, or a merge driver).
* Derive the number from the queue or an outside counter.
* No counter file: a new record's number must be greater than every number at the base branch and not taken.

## Decision Outcome

Chosen option: no counter file; the lint compares a new record's number with the base branch.

* **`NEXT` is retired.** The file is deleted. A branch that still has it, or a pull request that adds it back, fails
  the lint with a message that says to delete it.
* **The rule.** With `--base`, the lint takes the ADR files the pull request's own diff adds (in the diff from the
  merge-base, and absent at the merge-base) and requires each one's number to be greater than the highest ADR number at
  the base branch's tip. A number that is equal, lower, or already taken at the tip fails, and the message names the
  highest number and says to take any number above it. Numbers need not be consecutive. A pull request that adds no ADR
  is unaffected.
* **The duplicate check stays.** The `adr-numbers` job still refuses two records with one number on the checked-out tree.
  For the merge queue that tree is the combined commit, so two pull requests that each pass alone because they took the
  same number meet there: the second is ejected and renumbers on its next run, as in
  [ADR 0070](0070-the-next-adr-number-is-a-file-next-and-the-lint-refuses-a-duplicate-or-stale-number.md).
* **Author practice.** Take a number above the highest in `docs/decisions/` on `main`. If another record merges first
  and the number is taken, the lint says so and the author renumbers: rename the file, and fix links to it.
* **Nothing else changes.** An accepted ADR stays immutable (only its status line and one "Superseded by" line may
  change), and `INDEX.md` is still written at release preparation, not in a pull request.

* **Bootstrap order.** The `adr-lint` job builds its checker from the base branch, so a pull request cannot pass a
  rule its base does not yet have. Retiring `NEXT` therefore took two steps: first a pull request that made `NEXT`
  optional and added the number rule (it left `NEXT` in place, and merged as #398); then the pull request that
  deletes `NEXT` and makes its presence a violation, which runs the first step's checker and passes.

ADR 0070 is superseded by this record; its `NEXT` file, its stale-value lint and its "bump NEXT" step are gone, and its
duplicate check is kept.

### Consequences

* Good, because no two pull requests write a common file, so a pull request that adds an ADR no longer conflicts on it.
* Good, because a stale number after a rebase is reported as what it is (taken or too low), with the number to beat.
* Good, because the check that matters, no duplicate on the combined commit, is unchanged.
* Bad, because the lint's rule compares with the base tip: an author who took a number before another record merged must
  renumber, which the old file would have forced as a conflict anyway.
* Bad, because numbers can have gaps (a pull request that takes 0095 while the highest is 0088 leaves 0089 to 0094 for
  later pull requests, which then fail the "greater than" rule).

### Confirmation

* `scripts/adrlint` tests: a new record above the base tip's highest passes (consecutive or not); one equal to, lower
  than or already at the tip fails; a pull request that adds no record passes; a `NEXT` file fails, with and without
  `--base`; two records with the same number still fail; no `NEXT` is required.
* The `adr-numbers` job in `.github/workflows/ci.yml` runs the lint on the combined commit.

## Pros and Cons of the Options

### Keep `NEXT` and add tooling

* Good, because the number is read from one place.
* Bad, because the file is still a shared write, so tooling only hides the conflict, and a bot would need a gate path.

### Derive the number from the queue or an outside counter

* Good, because it can hand out unique numbers.
* Bad, because it needs a service or a queue-aware tool, and the author does not know the number when writing the file.

### No counter file

* Good, because it removes the shared write and keeps the duplicate guarantee where it already is.
* Bad, because a gap in the numbers makes a pull request that took a lower one renumber.

## More Information

Supersedes [ADR 0070](0070-the-next-adr-number-is-a-file-next-and-the-lint-refuses-a-duplicate-or-stale-number.md).
Related: [ADR 0002](0002-record-architecture-decisions-as-madr-records.md) (a number is used once),
[ADR 0068](0068-a-change-log-entry-is-a-fragment-file-and-the-adr-index-is-generated.md) (the index is generated).
