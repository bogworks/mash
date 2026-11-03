---
status: accepted
date: 2026-11-03
decision-makers: Randy Bias (mash maintainer)
---

# The next ADR number is a file, `NEXT`, and the lint refuses a duplicate or stale number

## Context and Problem Statement

A pull request that adds an ADR picks the next free number by looking at the directory. Two pull requests open at the
same time pick the same number, and nothing notices until the second one merges: each is correct against `main` and
the two records only collide on the combined tree. [ADR 0068](0068-a-change-log-entry-is-a-fragment-file-and-the-adr-index-is-generated.md)
removed the shared index so that records no longer conflict, which also removed the one place where two pull requests
that took the same number would have met. [ADR 0002](0002-record-architecture-decisions-as-madr-records.md) says a
number is used once but gave no way to hand one out.

## Decision Drivers

* A duplicate number must be caught before it reaches `main`, by a check, not by a reviewer's memory.
* Taking a number must stay cheap: no service, no reservation step, no new command.
* The check must see what the queue is about to merge, not only each pull request on its own.

## Considered Options

* Ask authors to check the open pull requests for a number before taking one.
* Derive the number from the queue, or from a counter held outside the repository.
* A `docs/decisions/NEXT` file that names the next number, plus a check of numbers on the combined commit.

## Decision Outcome

Chosen option: a `NEXT` file and a check on the combined commit.

* **The file.** `docs/decisions/NEXT` holds the next free number, four digits and a newline. A pull request that adds an
  ADR takes the number `NEXT` names and bumps `NEXT` in the same pull request.
* **The lint.** `adrlint docs` reports a missing `NEXT`, a `NEXT` that is not exactly four digits and a newline, and a
  `NEXT` that is not the highest ADR number plus one (0001 when there are no records). The message names the file, the
  value found and the value expected. The existing check that no two records share a number stays.
* **The check.** CI has an `adr-numbers` job that runs `go run ./scripts/adrlint docs` on the checked-out tree, on every
  event, and `pr-check` requires it. `ci.yml` also runs on `merge_group`. Mergify's queue run is a pull request run on
  the combined commit, so the job sees the records of every pull request ahead in the queue.

Bumping `NEXT` alone does not stop the collision: two pull requests that both take 0070 both change `NEXT` from 0070 to
0071, git merges that cleanly, and no conflict appears. A conflict arises only when the two differ, for instance when one
takes a number and the other bumps by a different amount. The guarantee therefore comes from the duplicate check on the
queue's merged commit: the second pull request in the queue fails there, is ejected, and on its next run takes 0071.

### Consequences

* Good: a number is read from one file, with no search of open pull requests.
* Good: a duplicate or a forgotten bump fails a check, on the pull request and on the queue's combined commit.
* Good: no new command or flag; the rule lives in the existing lint.
* Bad: a pull request that adds an ADR touches one more file, and a stale `NEXT` after a rebase fails the lint until it is bumped.
* Bad: the loser of a race is ejected from the queue and rerun, which costs one run.

### Confirmation

* Tests for `adrlint docs`: a correct `NEXT` passes; a missing, stale, too high or malformed `NEXT` is refused; two
  records with the same number and a `NEXT` that follows them are refused as a duplicate.
* The `adr-numbers` job in `.github/workflows/ci.yml` and its place in `pr-check`'s `needs`.

## Pros and Cons of the Options

### Ask authors to check open pull requests

* Good, because nothing changes.
* Bad, because a pull request opened a minute later is invisible, and the check is a habit, not a gate.

### Derive the number from the queue or an outside counter

* Good, because it can hand out a number to each pull request uniquely.
* Bad, because it needs a service or a queue-aware tool, and the number is unknown when the author writes the file.

### A `NEXT` file and a check on the combined commit

* Good, because the number is in the tree and the guarantee comes from a check that already runs in the queue.
* Bad, because the loser of a race is ejected and reruns.

## More Information

* Amends [ADR 0068](0068-a-change-log-entry-is-a-fragment-file-and-the-adr-index-is-generated.md) (records no longer meet in a shared index) and [ADR 0002](0002-record-architecture-decisions-as-madr-records.md) (how a number is taken).
