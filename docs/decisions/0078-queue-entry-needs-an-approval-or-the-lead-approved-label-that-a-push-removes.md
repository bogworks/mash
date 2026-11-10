---
status: accepted
date: 2026-11-10
decision-makers: Randy Bias (mash maintainer)
---

# Queue entry needs an approval or an approval label, and a push removes the label

## Context and Problem Statement

The Mergify queue let a pull request in when its author was the maintainer's login or when that login had approved it
(`.mergify.yml`). Every agent pushes as that login, so the author condition is true of every agent pull request: it
proves who holds the credentials, not that anyone reviewed the change. Only the `hold` label then stood between an
agent's pull request and the queue, and `hold` is a human brake that must be applied in time. It failed once: a pull
request that should have waited entered the queue.

## Decision Drivers

* Entry must need a positive act by the maintainer's side, not the absence of a brake.
* An approval must not cover code pushed after it.
* The maintainer works through agents, so the act must be possible without a second human account.
* The rule must stay checkable by a test, and the new automation must not run pull request code.

## Considered Options

* Keep the author rule and rely on the `hold` label.
* Approvals only: drop the author rule and require `approved-reviews-by`.
* `approved-reviews-by` or a `lead-approved` label, with the label removed on every push. <!-- adrlint:allow vocabulary -->
* Require two approvals.

## Decision Outcome

Chosen option: `approved-reviews-by` or the `lead-approved` label, with a workflow that removes the label on every push. <!-- adrlint:allow vocabulary -->

* **Entry.** The `or:` group in the default queue rule's `queue_conditions` holds exactly
  `approved-reviews-by = <maintainer>` and `label = lead-approved`. `author = <maintainer>` is removed. <!-- adrlint:allow vocabulary -->
* **The label.** The maintainer applies `lead-approved` at merge time, after reading the change. <!-- adrlint:allow vocabulary -->
* **The reset.** `.github/workflows/lead-approved-reset.yml` runs on `pull_request_target` `synchronize`, holds <!-- adrlint:allow vocabulary -->
  `pull-requests: write` only, checks out nothing and runs no pull request code, and removes the label if it is set.
  Approvals stay dismissed on an outside author's push by the existing rule.
* **The brake.** `label != hold` stays in `queue_conditions` and in `auto_merge_conditions`. The approval gate is not
  repeated in `auto_merge_conditions`: those conditions only decide when Mergify queues a pull request by itself, and
  `queue_conditions` apply to every queued pull request however it was queued.

### Consequences

* Good: an agent's pull request cannot enter the queue without a recorded approval or the label.
* Good: the label cannot cover later code, since a push removes it.
* Good: no second account is needed; the label works where an approval by the author's own login cannot.
* Bad: the maintainer has one more step per pull request, and re-applies the label after every push.
* Bad: a push after the label is applied leaves it set until the workflow runs; the queue run still needs
  `codex-review` and `pr-check` on the new head, and the window is the workflow's start time.
* Bad: the label has no provenance. Mergify checks that it is set, not who set it, and write access lets any identity
  that agents push as apply it to its own pull request. The gate therefore turns an unintended entry into a deliberate
  act that the label's event log records; it does not stop a determined agent. Stopping that needs the approval path
  (a recorded review by an account the agents do not hold), which stays accepted as an alternative to the label.
* Bad: if the reset workflow fails, the label stays on the new head and the gate is open for that push. The job is
  short and its failure shows as a failed check on the pull request, and entry still waits for `codex-review` and
  `pr-check` on the new head, which take minutes, so the window is the time to notice a failed reset.
* Bad: the workflow takes effect only once it is on `main`, so the pull request that adds it is not covered by it.

### Confirmation

* `TestMergify_entryNeedsApprovalOrLabel` in `internal/release/mergify_test.go`: no `author =` condition, the entry
  group is exactly the approval and `label = lead-approved`, and `label != hold` is in both places. <!-- adrlint:allow vocabulary -->
* `TestWorkflows_leadApprovedResetsOnPush`: the workflow triggers on `pull_request_target` `synchronize` only, holds <!-- adrlint:allow vocabulary -->
  `pull-requests: write` only, contains no `actions/checkout`, and removes `lead-approved`. <!-- adrlint:allow vocabulary -->

## Pros and Cons of the Options

### Keep the author rule and rely on the `hold` label

* Good, because nothing changes.
* Bad, because authorship is true of every agent pull request and the brake is a human reflex that already failed.

### Approvals only

* Good, because it is the simplest rule.
* Bad, because GitHub does not let an author approve their own pull request, and agents push as the maintainer's login,
  so every agent pull request would need a second account.

### `approved-reviews-by` or `lead-approved`, with a reset on push (chosen) <!-- adrlint:allow vocabulary -->

* Good, because entry needs a positive act and the label is bound to the head it was applied on.
* Bad, because it adds a workflow and a label to keep.

### Require two approvals

* Good, because it is the strongest gate.
* Bad, because mash has one maintainer; the gate would block every pull request.

## More Information

* Amends the queue rule in `.mergify.yml`; the process is described in `docs/branch-protection.md`.
* Related: [ADR 0070](0070-the-next-adr-number-is-a-file-next-and-the-lint-refuses-a-duplicate-or-stale-number.md) (the other check the queue waits on).
