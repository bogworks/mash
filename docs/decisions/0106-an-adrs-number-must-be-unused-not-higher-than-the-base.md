---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# An ADR's number must be unused, not higher than the base

Amends: ADR-0089

## Context and Problem Statement

[ADR 0089](0089-an-adr-takes-any-number-above-the-highest-on-the-base-branch-and-there-is-no-next-file.md) has `adrlint docs
--base` refuse an ADR file a pull request adds unless its number is greater than the highest ADR number at the base tip. With
eight ADRs in flight across three streams, every merge of a higher-numbered ADR made every open pull request with a lower
number stale: the merge of 0102 broke 0096, 0098, 0099, 0100 and 0101, and each needed a renumber that touched the file name,
the links and the title for no reason of substance.

What does the number check need to guarantee, so that it stops costing renumber churn?

## Decision Drivers

* Two records never share a number.
* A merge must not invalidate an unrelated open pull request.
* No counter file and no file that every pull request edits (ADR 0089).
* `INDEX.md` stays sorted by number, so the order of merging does not matter to a reader.

## Considered Options

* Uniqueness: a new number is not used at the base tip or by another file in the pull request.
* Keep "greater than the base tip" (ADR 0089 as written).
* A reservation file or counter that hands out numbers.

## Decision Outcome

Chosen option: "Uniqueness", because the only thing a number must do is name one record, and uniqueness checks exactly that.

* A new ADR's number must not be used by any ADR file that exists at the tip of the base ref. A number below the base's
  highest is fine when no file there uses it.
* It must not collide with another file in the pull request: rule c of `adrlint` already checks uniqueness in the combined
  tree, and stays.
* Gaps and out-of-order merges are allowed. There is no counter file and no reservation.
* `INDEX.md` sorts by number and is written at release preparation only (ADR 0068); it is not regenerated here.
* The refusal names the existing file that holds the number and tells the author to take another number that is free on the
  base and in the pull request.
* This amends ADR 0089's number rule only. Its retirement of `docs/decisions/NEXT` stands.

### Confirmation

`scripts/adrlint` tests cover a number below the base's highest but unused (passes), a number equal to one at the base (refused,
naming the file), two added files with the same number (refused by rule c), and a gap above the highest (passes).

### Consequences

* Good, because a merge of one ADR no longer invalidates the others in flight.
* Good, because the check still refuses a number a merged record already holds.
* Bad, because numbers may merge out of order, so the file listing is no longer in order of decision; the dates in the front
  matter carry that.
* Bad, because two pull requests that pick the same free number are found only by rule c on the queue's combined tree, as before.

## Pros and Cons of the Options

### Uniqueness (chosen)

* Good, because no renumber is forced by an unrelated merge.
* Good, because it needs nothing beyond the files already in the tree.
* Bad, because a number can be taken twice until the queue's combined check runs.

### Keep "greater than the base tip"

* Good, because numbers merge in increasing order.
* Bad, because each merge of a higher number stales every lower open pull request, which is the churn this record removes.

### A reservation file or counter

* Good, because numbers are handed out once.
* Bad, because every pull request edits one file and conflicts on it, which ADR 0089 removed.

## More Information

Amends [ADR 0089](0089-an-adr-takes-any-number-above-the-highest-on-the-base-branch-and-there-is-no-next-file.md).
