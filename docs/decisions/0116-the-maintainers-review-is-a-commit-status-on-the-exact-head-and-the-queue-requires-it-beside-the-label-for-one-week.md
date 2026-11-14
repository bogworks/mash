---
status: accepted
date: 2026-11-14
decision-makers: Randy Bias (mash maintainer)
---

# The maintainer's review is a commit status on the exact head, and the queue requires it beside the label for one week

Amends: ADR-0078

## Context and Problem Statement

[ADR 0078](0078-queue-entry-needs-an-approval-or-the-lead-approved-label-that-a-push-removes.md) lets a pull request into <!-- adrlint:allow vocabulary -->
the merge queue on a recorded approval or the `lead-approved` label, and a workflow (`label-reset`) removes the label on <!-- adrlint:allow vocabulary -->
every push. The label works, but it is not bound to a commit: it covers whatever the head is when the queue reads it, and
the reset is a second job racing the push. A review that is recorded against the exact head SHA drops by itself when the
head changes, with no job to run, race or fail. <!-- adrlint:allow vocabulary -->

## Decision Drivers

* The approval must be bound to the commit the maintainer reviewed, so a push cannot inherit it.
* It must not deadlock the queue the way [#424](https://github.com/bogworks/mash/pull/424) did.
* The maintainer works through agents that push as the maintainer's login, so the act must work without a second account.
* The move must not drop the existing gate before the new one has been used.

## Considered Options

* Keep the label and the reset only.
* A commit status `lead-review` on the head, in `queue_conditions`, required beside the existing gate for one week, then <!-- adrlint:allow vocabulary -->
  replacing the label. <!-- adrlint:allow vocabulary -->
* The same status as a `merge_protections` condition.
* The same status inside the entry `or` from the start.

## Decision Outcome

Chosen option: the `lead-review` commit status, as a plain `queue_conditions` entry, required beside the existing gate <!-- adrlint:allow vocabulary -->
for a migration week, then replacing the label. <!-- adrlint:allow vocabulary -->

* **The status.** The maintainer's approval is the commit status `lead-review` = success on the exact head SHA that was <!-- adrlint:allow vocabulary -->
  reviewed. A new head has no such status, so a push drops the approval by itself. `.mergify.yml` requires it with the
  plain entry `check-success = lead-review`. <!-- adrlint:allow vocabulary -->
* **Why `queue_conditions` only.** Mergify applies a `merge_protections` rule's conditions to the queue draft's commit as
  merge conditions too, and a status posted on the pull request's head does not exist on that commit; this is what
  deadlocked the queue after #424 for `adr-lint`. So the status is not a `merge_protections` condition, and the
  `merge_protections` check ban and its test stay as they are. It is not inside the entry `or` either: a bare
  `check-success` inside an `or` is banned (ADR 0078, `TestMergify_entryNeedsApprovalOrLabel`), because a check matches
  whoever posted it and an `or` would let a check alone satisfy the group.
* **The migration week, 2026-11-14 to 2026-11-21.** The plain entry is required **alongside** the existing gate (the
  approval or `lead-approved`), so for the week it adds a requirement and removes nothing. On **2026-11-21** the entry <!-- adrlint:allow vocabulary -->
  `or` becomes the status or the approval: the `lead-approved` alternative drops, and the status moves into the `or` as <!-- adrlint:allow vocabulary -->
  its replacement (the `or` then holds the status and `approved-reviews-by`, and the ban on a bare check in an `or`
  is lifted for exactly this one status, by a new ADR that says so). <!-- adrlint:allow vocabulary -->
  The files that change then are `.mergify.yml` (the `or` and the `merge_protections` rule's approval group, which keeps
  mirroring it), `internal/release/mergify_test.go` (the entry tests), `.github/workflows/label-reset.yml` (it stops
  stripping `lead-approved`; it keeps stripping `decision-confirmed`), and `docs/branch-protection.md`. That change is <!-- adrlint:allow vocabulary -->
  not made here.
* **How it is posted.** `scripts/lead-approve <pr>` is the sanctioned path. It needs `LEAD_LOGIN` (no default) and <!-- adrlint:allow vocabulary -->
  refuses unless `gh` is authenticated as that login, the head's `codex-review` status and `label-reset` check are
  success on that exact SHA, and the `hold` label is absent. It posts the status on the SHA it resolved, then reads the
  head again and, if it moved, warns that the status cannot approve the new head and exits non-zero.
  `scripts/lead-approve --audit <pr>` lists the `lead-review` statuses on the head with their creator and flags any <!-- adrlint:allow vocabulary -->
  whose creator is not `LEAD_LOGIN`.
* **Unchanged.** `hold` stays the veto label, in `queue_conditions` and `auto_merge_conditions`. `decision-confirmed`
  stays a label. `label-reset` behaves as before.

### Spoofability

`check-success = <name>` matches a check run or commit status of that name **whoever posted it** (ADR 0078), and Mergify
has no filter on the creator of a status. So any token with status access to the repository can post `lead-review`, <!-- adrlint:allow vocabulary -->
exactly as the same token could apply the `lead-approved` label today. **The gain is binding to the head commit, not <!-- adrlint:allow vocabulary -->
authentication**: a push drops the approval without any job running, but the status does not prove that the maintainer
posted it. Residual risk, in the form ADR 0078 stated it for the label (and the threat model carries): an identity with
write access, which includes every agent that pushes as the maintainer's login, can post the status on its own pull
request, so the gate makes an unintended entry a deliberate, logged act (a status records its creator) and not an
impossible one. Mitigations: `scripts/lead-approve` is the sanctioned path and refuses unless `codex-review` and <!-- adrlint:allow vocabulary -->
`label-reset` pass on that head and the caller is `LEAD_LOGIN`; `--audit` flags a `lead-review` status whose creator is <!-- adrlint:allow vocabulary -->
another login, so a forged one is visible when someone looks; the protected-path rule still needs an ADR or a
confirmation for a change to the gate files. Closing the residual needs a status or check pinned to its source in the
ruleset (`required_status_checks` with an `integration_id`) or a required-review rule, a repository-settings change for the
maintainer and not part of this decision. <!-- adrlint:allow vocabulary -->

### Consequences

* Good: the approval is bound to one head SHA; a push drops it with no reset job to run or race.
* Good: no deadlock, since the status is evaluated at queue entry only, never on the queue draft's commit.
* Good: the label stays through the week, so a failure of the new path cannot stop merging.
* Bad: for the week the maintainer's step is doubled, the label or approval and the status.
* Bad: the status has the same provenance gap as the label (see Spoofability); it is not stronger against a token with
  status access.
* Bad: the entry is not fail-closed on the status's source: only the audit shows who posted it.

### Confirmation

* `TestMergify_queueNeedsLeadReviewStatus` in `internal/release/mergify_test.go`: `queue_conditions` hold
  `check-success = lead-review` as a plain list entry and no `or` member names it. `TestMergify_entryNeedsApprovalOrLabel` <!-- adrlint:allow vocabulary -->
  still bans a `check-success` in the `or`, and `TestMergify_mergeProtectionsHoldNoCheckConditions` still bans one in
  `merge_protections`; `TestMergify_waitsOnlyForChecksThatExist` knows `lead-review` is a status, not a job. <!-- adrlint:allow vocabulary -->
* `scripts/lead_approve_test.go` (a fake `gh` on `PATH`): `lead-approve` refuses without `LEAD_LOGIN`, on a wrong login, <!-- adrlint:allow vocabulary -->
  when `codex-review` or `label-reset` is not success, and on `hold`; it posts on the exact SHA; it warns and exits
  non-zero when the head moved; `--audit` flags a foreign creator.

## Pros and Cons of the Options

### Keep the label and the reset only

* Good, because nothing changes.
* Bad, because the approval is not bound to a commit and depends on a job winning a race with the push.

### `lead-review` in `queue_conditions`, beside the label for a week, then replacing it (chosen) <!-- adrlint:allow vocabulary -->

* Good, because it is bound to the head and cannot deadlock the queue.
* Good, because the week shows the new path working before the old one drops.
* Bad, because it is as spoofable as the label.

### The status as a `merge_protections` condition

* Bad, because the queue draft's commit has no such status, so the queue would refuse every pull request (#424).

### The status inside the entry `or` from the start

* Bad, because it drops the label gate before the status has been used, and a bare check in an `or` is banned.

## More Information

* Amends [ADR 0078](0078-queue-entry-needs-an-approval-or-the-lead-approved-label-that-a-push-removes.md); its file is <!-- adrlint:allow vocabulary -->
  unchanged. Where this ADR and 0078 differ on the label's future, this one governs after 2026-11-21.
* `.mergify.yml`, `scripts/lead-approve`, `docs/branch-protection.md`, `docs/security/threat-model.md`. <!-- adrlint:allow vocabulary -->
