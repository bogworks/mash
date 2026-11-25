---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer)
---

# A pull request enters the queue by itself when its checks and codex are clean

Amends: ADR-0116

## Context and Problem Statement

[ADR 0116](0116-the-maintainers-review-is-a-commit-status-on-the-exact-head-and-the-queue-requires-it-beside-the-label-for-one-week.md)
made the maintainer's review a commit status, `lead-review`, on the exact head, required in `queue_conditions` beside the
approval-or-`lead-approved` gate of [ADR 0078](0078-queue-entry-needs-an-approval-or-the-lead-approved-label-that-a-push-removes.md).
Every pull request therefore waited for a person to approve it after codex-review and the checks had already passed. The
wait is a queue stall with no new information in it: the independent review has run, the checks are green, and the
status proved only that someone posted it. The question is whether a person's approval should still stand between a clean
pull request and the queue.

## Decision Drivers

* A pull request that has passed `codex-review`, `pr-check`, `adr-lint` and `label-reset` should not wait for a person.
* The approval gate was not authentication: `check-success` matches a status whoever posted it, so the gate was a
  deliberate act, not a proof.
* The brake must stay: some changes need a person's judgement before they merge.
* The queue must not deadlock (#424): nothing new may be added to `merge_protections` that the queue draft lacks.

## Considered Options

* Keep lead approval as a queue condition.
* Let a pull request enter by itself when its checks and codex are clean, with `hold` as the only brake.

## Decision Outcome

Chosen option: a pull request enters the queue by itself, with `hold` as the only brake.

* **Entry.** `queue_conditions` of the default queue are `base = main`, `-draft`, `-conflict`, `label != hold`,
  `check-success = codex-review`, and the app-qualified `pr-check`, `adr-lint` and `label-reset`. No lead approval is needed.
* **Removed.** The `lead-review` status and the `approved-reviews-by` / `lead-approved` or-group leave the queue
  conditions, and the approval group leaves the merge protection. The rule is renamed "not held before merge" and its one
  success condition is `label != hold`.
* **`hold` is a deliberate brake.** It is set with its reason written on the pull request. It is kept for: contract
  requirements, ADRs that change one, spec files, CI and queue configuration, and order-16 code (0.6.0 policy code).
* **Unchanged.** `scripts/lead-approve` stays as an optional tool; `label-reset` still strips `lead-approved` and
  `decision-confirmed`; the rule that dismisses an outside author's approvals on push stays.

Source: Randy, via the planner ("No holding"), dev-process 0.14.0 rule 27.

### Consequences

* Good: a clean pull request is queued without waiting for a person, so the queue is no longer stalled on approval.
* Good: fewer conditions and one list less to keep in step between `queue_conditions` and `merge_protections`.
* Bad: nothing but `codex-review` and the checks stands between an agent's pull request and `main`, apart from a
  deliberate `hold`; a change that needed a person's eye is stopped only if someone sets `hold`.
* Bad: a forged `codex-review` status is not stopped by a person's approval any more (the residual risk in
  `docs/security/threat-model.md` stands, and the approval gate never closed it).

### Confirmation

* `internal/release/mergify_test.go`: `TestMergify_entryNeedsNoApproval` (no `lead-review`, approval or or-group in
  `queue_conditions`; `label != hold` in them and in `auto_merge_conditions`), `TestMergify_mergeProtectionsMirrorsQueueEntry`
  (the rule is "not held before merge" with `success_conditions` exactly `label != hold`),
  `TestMergify_mergeProtectionsHoldNoCheckConditions` and `TestMergify_actionsChecksAreAppQualified`.

## Pros and Cons of the Options

### Keep lead approval as a queue condition

* Good, because a person sees every pull request before it merges.
* Bad, because it stalls clean pull requests, and the status is as spoofable as the label it replaced.

### Enter by itself when checks and codex are clean (chosen)

* Good, because it removes the wait and two lists to keep in step.
* Bad, because the only human stop is `hold`, set by someone who notices.

## More Information

* Amends [ADR 0116](0116-the-maintainers-review-is-a-commit-status-on-the-exact-head-and-the-queue-requires-it-beside-the-label-for-one-week.md)
  and departs from the approval gate of [ADR 0078](0078-queue-entry-needs-an-approval-or-the-lead-approved-label-that-a-push-removes.md);
  neither file is changed.
* `.mergify.yml`, `docs/branch-protection.md`, `docs/release.md`, `docs/security/threat-model.md`.
