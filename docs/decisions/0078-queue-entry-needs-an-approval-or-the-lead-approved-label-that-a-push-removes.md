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

Chosen option: `approved-reviews-by` or the `lead-approved` label, with a workflow that removes the approval labels on every push. <!-- adrlint:allow vocabulary -->

* **Entry.** The `or:` group in the default queue rule's `queue_conditions` holds exactly
  `approved-reviews-by = <maintainer>` and `label = lead-approved`. `author = <maintainer>` is removed. <!-- adrlint:allow vocabulary -->
* **The label.** The maintainer applies `lead-approved` at merge time, after reading the change. <!-- adrlint:allow vocabulary -->
* **The reset.** `.github/workflows/label-reset.yml` (job and check `label-reset`) runs on `pull_request_target`
  `opened`, `reopened` and `synchronize`, holds `pull-requests: write` and `contents: read` and nothing else (the read
  scope is for `gh pr view`, which may need it on this repository; it is read-only), checks out nothing and runs no pull
  request code. It reads the current labels and removes both `lead-approved` <!-- adrlint:allow vocabulary -->
  and `decision-confirmed` when set, on `opened` too: either label can be preset when a pull request is created, and a
  preset label must not count (adr-lint accepts `decision-confirmed` applied to an earlier head). It swallows no
  error: an API or token failure fails the job. After the removals it reads the labels again and fails the job, naming the
  label, if either is still set. Approvals stay dismissed on an outside author's push by the existing
  rule.
* **The race.** On a push adr-lint and label-reset run together, so adr-lint could read `decision-confirmed` before it is
  removed, and the removal (by the workflow token) starts no new run. So adr-lint ignores `decision-confirmed` when the
  event action is `synchronize`, `reopened` or `opened` (passed as `PR_ACTION`); a confirmation must come from a
  `labeled` run on the current head, and adr-lint reads the labels live with `gh pr view --json labels`, failing on an
  API error. label-reset uses `runs-on: ${{ vars.RUNNER_LINUX }}` and no GitHub-hosted runner: it is safe there because
  it runs from the base branch and executes nothing from the pull request. The variable `RUNNER_LINUX` must be set, or
  the check never appears and the queue keeps the pull request out (fail closed).
* **Fail closed.** `check-success = label-reset` is in `queue_conditions`, so the reset must have run, and succeeded,
  on a head before it can enter the queue.
* **The brake.** `label != hold` stays in `queue_conditions` and in `auto_merge_conditions`. The approval gate is not
  repeated in `auto_merge_conditions`: those conditions only decide when Mergify queues a pull request by itself, and
  `queue_conditions` apply to every queued pull request however it was queued.

### Consequences

* Good: an agent's pull request cannot enter the queue without a recorded approval or the label.
* Good: the label cannot cover later code, since a push removes it.
* Good: no second account is needed; the label works where an approval by the author's own login cannot.
* Bad: the maintainer has one more step per pull request, and re-applies the label after every push.
* Bad: the label has no provenance. Mergify checks that it is set, not who set it, and write access lets any identity
  that agents push as apply it to its own pull request. The gate therefore turns an unintended entry into a deliberate
  act that the label's event log records; it does not stop a determined agent. Stopping that needs the approval path
  (a recorded review by an account the agents do not hold), which stays accepted as an alternative to the label.
* Good: the entry is fail closed. A head without a green `label-reset` check cannot enter the queue, so a failed or
  missing reset blocks the pull request instead of leaving the label open for that push.
* Bad: the workflow takes effect only once it is on `main`, so the pull request that adds it is not covered by it.
* Bad, the label gate guards the queue only: the ruleset has no pull-request or review rule, so a direct
  `gh pr merge` (or the merge button) by an identity with write access skips Mergify, and with it every condition in
  `.mergify.yml`, entirely.
* Bad, and verified read-only on 2026-11-10: the check `Mergify Merge Protections` does not go red without the label or
  while a protection is blocking. On pull request #349 (labelled `hold`) its conclusion is `success` while its summary
  says "1 of 2 protections blocking"; on pull requests it does not apply to it is `neutral`. The ruleset accepts it
  either way (check-neutral and check-success), so the ruleset does not enforce the queue-entry gate; only the queue
  itself does.
* Bad, residual risk: no GitHub rule pins the checks the queue waits on. Verified read-only on 2026-11-10 with
  `gh api repos/bogworks/mash/rulesets/26308864`: the only branch rule on the default branch is the ruleset
  `main-protection` (enforcement active, no bypass actors) with deletion blocked, non-fast-forward blocked and one
  required status check, `Mergify Merge Protections`; there is no required pull-request or review rule, and classic
  branch protection is not set (404). So `codex-review`, `pr-check`, `adr-lint` and `label-reset` are conditions only in
  `.mergify.yml`, and `check-success = <name>` matches a check run or status of that name whoever posted it. A pull
  request that adds or edits a workflow can post a check (or, with a token, a status) with one of those names, so
  those checks prove nothing against an author who controls workflows. What holds: `.mergify.yml`,
  `.github/workflows/`, `.github/actions/`, `scripts/adrlint/`, `internal/release/` and `docs/branch-protection.md`
  are now protected paths in adr-lint (judged on the changed-path list from `git diff --no-renames`, so a rename out of
  one counts), so `Decision: none` on them needs the
  `decision-confirmed` label and otherwise an ADR; `label-reset` runs from the base branch (`pull_request_target`); and
  the approval or the `lead-approved` label is still required for entry. <!-- adrlint:allow vocabulary -->
  Closing it means requiring the checks in the ruleset with an expected source (`required_status_checks` with an
  `integration_id`) and a required-reviews rule. That is a repository-settings change for the maintainer, not part of
  this decision's pull request. `docs/branch-protection.md` had described more than the ruleset holds; it now states
  the verified settings and marks the rest as the target.

### Confirmation

* `TestMergify_entryNeedsApprovalOrLabel` in `internal/release/mergify_test.go`: no `author =` condition, the entry
  group is exactly the approval and `label = lead-approved`, and `label != hold` is in both places. <!-- adrlint:allow vocabulary -->
* `TestWorkflows_labelResetRemovesApprovalLabels`: `label-reset.yml` exists and `lead-approved-reset.yml` does not; it <!-- adrlint:allow vocabulary -->
  triggers on `pull_request_target` `opened`, `reopened` and `synchronize` only (no early exit for `opened`), holds
  exactly `pull-requests: write` and `contents: read`, contains no `actions/checkout`, removes `lead-approved` <!-- adrlint:allow vocabulary --> and `decision-confirmed`,
  has no `|| true` or `continue-on-error`, re-reads the labels with a second `gh pr view` after the removals and fails
  (`exit 1`) if one is still set, and its job is named `label-reset`.
* `TestPR` in `scripts/adrlint/main_test.go`: `Decision: none` on `.mergify.yml`, a file under `.github/workflows/`,
  `.github/actions/`, `scripts/adrlint/` or `internal/release/`, or on `docs/branch-protection.md` fails without the `decision-confirmed` label and passes with it or with an ADR.
* `TestMergify_waitsOnlyForChecksThatExist`: `check-success = label-reset` is in `queue_conditions`, and each
  `check-success` name is the `name:` of a job (its key when it has none) in `ci.yml`, `adr-lint.yml` or
  `label-reset.yml`; `label-reset.yml` triggers on `pull_request_target`.
* `TestWorkflows_changedFileListsKeepRenameSources`: every `git diff --name-only` in the workflows passes
  `--no-renames`, so the old path of a rename is in the changed-path list adr-lint judges (a rename out of a protected
  path is a change to it); `TestPR` covers a rename's old path and a deletion.

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
