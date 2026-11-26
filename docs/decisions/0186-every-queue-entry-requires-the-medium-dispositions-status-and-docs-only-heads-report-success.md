---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# Every queue entry requires the `medium-dispositions` status, and docs-only heads report success

Amends: ADR-0157, ADR-0177

## Context and Problem Statement

[ADR 0157](0157-a-docs-only-pull-request-has-its-own-queue-and-needs-only-the-docs-lint.md) says a docs-only pull request
needs only the docs lint to enter its queue. [ADR 0177](0177-a-pull-request-enters-the-queue-by-itself-when-its-checks-and-codex-are-clean.md)
records the checks a pull request needs to enter the queue by itself. Neither names the `medium-dispositions` status, and
nothing gated a merge on it: #1709 and #1707 merged with a codex medium unanswered. Should the queue entry require it?

## Decision Drivers

* A codex medium must not merge unanswered.
* A pull request that has no medium to answer must not wait for one.
* The status is posted outside this repository, like `codex-review`.

## Considered Options

* Require `medium-dispositions` in both queues' entry conditions.
* Leave it advisory.

## Decision Outcome

Chosen option: require it. Both queues (default and docs) list `check-success = medium-dispositions` among their entry
conditions. The external review watcher posts the status on every head: it has done so on every head since
2026-11-25 15:53Z. A docs-only head, and a head with no review text, reports `success: no medium findings`, so docs-only
pull requests (#1705, #1715) are not held. A head with an unanswered medium reports `failure` (#1707). A dependabot or
outside pull request stays `pending` until the codex review runs, as it does for `codex-review`, so it is no worse off.
ADR 0157's "needs only the docs lint" and ADR 0177's list of entry checks are amended by this one added check.

### Consequences

* Good, because a medium cannot merge unanswered.
* Good, because docs-only and no-medium heads pass without anything to answer.
* Bad, because every queue entry now depends on the external watcher posting the status.

### Confirmation

`internal/release/mergify_test.go` requires the plain `medium-dispositions` status in both queues' conditions, and
`docs/branch-protection.md` names it.

## Pros and Cons of the Options

### Require it

* Good, because nothing merges past an unanswered medium.
* Bad, because a stopped watcher stops the queues.

### Leave it advisory

* Good, because the queues do not depend on one more producer.
* Bad, because it is what let #1709 and #1707 merge.

## More Information

* Amends ADR 0157 and ADR 0177 for the added queue condition only.
* The status is a plain one, the same trust model as `codex-review` and `security-review`.
