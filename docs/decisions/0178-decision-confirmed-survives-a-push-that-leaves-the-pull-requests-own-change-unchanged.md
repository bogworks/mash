---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer; issue #1584)
---

# decision-confirmed survives a push that leaves the pull request's own change unchanged

## Context and Problem Statement

[ADR 0078](0078-queue-entry-needs-an-approval-or-the-lead-approved-label-that-a-push-removes.md) has `label-reset` remove the `lead-approved` and `decision-confirmed` labels on every push, so that no label covers code nobody looked at, and has `adr-lint` ignore `decision-confirmed` on a `synchronize` run, because that run races the removal. <!-- adrlint:allow vocabulary -->

`decision-confirmed` is the reviewer's confirmation that a `Decision: none` pull request which touches a protected path involves no decision. A push that only rebases the pull request onto a newer `main` changes nothing the reviewer read, and the label is still removed, so the reviewer confirms the same change again. Can the label stay across a push that leaves the pull request's own change unchanged, without opening a way to ship a change nobody reviewed?

## Decision Drivers

* The label must still mean "a person looked at this change": a push that alters the change removes it.
* A rebase is the common push that alters nothing the reviewer read.
* The gate fails closed: any doubt removes the label.
* `label-reset` and `adr-lint` run together on a push, so both must reach the same verdict from one implementation.
* The workflows run on `pull_request_target`: nothing from the pull request may be built or run, and the token stays minimal.
* ADR 0001's rule of trust holds: the checker is built from the base branch.

## Considered Options

* A. Keep the label when the pull request's own change is identical, judged by `git patch-id --verbatim`, the changed paths and every changed file's blob.
* B. Keep it when the protected files are unchanged.
* C. No change: remove the label on every push.

## Decision Outcome

Chosen option: "A". The label stays on a push when `adrlint keep-label` says the pull request's own change is unchanged. `lead-approved` is removed on every push as before. <!-- adrlint:allow vocabulary -->

The change of a head is the diff from `merge-base(base, head)` to the head. `adrlint keep-label --base B --before X --after Y --pr N --before-checks FILE [--repo DIR]` prints `keep` and exits 0 only when all of these hold; otherwise it prints `reset: <reason>` and exits 1 (a usage error exits 2, which callers treat as a reset):

1. The newest `label-reset` check run of the GitHub Actions app on the old head is completed with conclusion success (`FILE` holds that head's check runs), and the run must belong to this pull request: only runs whose `pull_requests` list holds `N` count, because two pull requests can share a head commit and the check-runs endpoint is keyed by commit (a missing or non-positive `--pr` is a usage error, exit 2). Without this a push whose own reset failed or was missed would leave a label that a later rebase keeps, covering a change nobody reviewed. A skipped run made no judgement and is ignored; a head with nothing but skipped runs, or a newest real run that failed, was cancelled or is still running, is a broken chain.
2. Each head has exactly one merge base (`git merge-base --all`), because a criss-cross history makes "the change" depend on which base git printed.
3. The `git patch-id --verbatim` of each head's change is equal and not empty. Binary files are not diffed, and a change over 16 MiB resets.
4. The set of changed paths is equal.
5. For every changed path, protected or not, its blob is identical in the old and the new head. `patch-id` ignores line numbers, so the same edit applied to another copy of a repeated block keeps the id; the blob comparison closes that. A rebase over an upstream change of a file the pull request also changes therefore resets.

Any error resets: a missing object, a failed fetch, a git failure, an empty change, a base whose checker has no `keep-label`, and `before` equal to `after`. The printed reason is cleaned of control characters and `::`, because it can hold a path from the pull request.

Where it runs, on `synchronize` only:

* `label-reset.yml` checks out the **base** commit, builds `adrlint` from it, fetches the base, the old head and the new head into that checkout as data (`git diff` runs with `--no-ext-diff --no-textconv`; nothing from the pull request runs), reads the old head's check runs, and leaves `decision-confirmed` alone only on `keep`. The fetch is the only step that holds the token. `opened` and `reopened` always reset. The removal step runs `always()`, so a failed build or fetch resets. The job gains `checks: read`. `edited` is not added to its triggers: an edit moves no head.
* `adr-lint.yml` runs the same three steps with the checker it already builds, and passes the verdict to `adrlint pr` as `PR_KEEP`. On a `synchronize` run the label counts only if `PR_KEEP` is exactly `yes`; on `opened` and `reopened` it never counts. It reads the pull request's labels from the API on every run, never from the event payload, so a label added later is seen by the run its `labeled` event starts, with no toggle or rerun.

Both jobs call one checker on the same commits, so they agree. A rare disagreement (the base moving between the two runs) ends safe: if `label-reset` removes a label that `adr-lint` counted, the removal fires `unlabeled`, which re-runs `adr-lint`, which then fails for want of the label.

The rule is the one bogworks/homerange adopted in its ADRs 0013 (the rule) and 0014 (the chain check ignores skipped runs).

### Consequences

* Good, because a rebase no longer needs the reviewer again; a push that changes the reviewed change still does.
* Good, because the label's meaning stays "a person looked at this change".
* Bad, because two workflows now build and run the base's checker and fetch commits, a few seconds more per push.
* Bad, because a disagreement between the two jobs can leave `adr-lint` red with the label still on; re-running it or editing the body clears it.
* Risk accepted: a rebase can change what a reviewed workflow runs. If a reviewed workflow calls a script and only `main`'s copy of that script changes under the rebase, the label stays and the workflow runs `main`'s new script. That change was gated or reviewed when it landed on `main`; the label covers the pull request's own change, not the base.
* Risk accepted: the patch-id, the path set and the blob comparison are the whole proof that the change is the same; a flaw in them keeps a stale label.
* Reversible: remove the keep steps from the two workflows and `PR_KEEP` from `adrlint pr`; the label then resets on every push as ADR 0078 had it.
* Extends ADR 0078: its reset of `lead-approved`, its fail-closed queue condition and its protected paths stand; its reset of `decision-confirmed` on every push, and `adr-lint` ignoring the label on every `synchronize` run, no longer do. <!-- adrlint:allow vocabulary -->

### Confirmation

* `scripts/adrlint/keeplabel_test.go` runs against real git repositories: a pure rebase keeps; a rebase over an upstream edit of a changed file resets; a relocated edit resets; each kind of change to the patch (a protected line, a second file, a removal, whitespace, the same change elsewhere) resets; the same edit in another copy of a repeated block resets for a protected and an unprotected file; unknown commits, a non-sha, `before` equal to `after`, an unknown base and an empty change reset; two merge bases reset; the command prints `reset: ...` and exits 1, and a usage error exits 2. The chain check is covered for a failed, cancelled, skipped, running, missing and other-app run, for the newest of several runs deciding, for a newer skipped run being ignored, a run attached only to another pull request or to no pull request being ignored, and against a captured check-runs payload. A broken chain resets even when the push is a pure rebase.
* `TestPRKeepVerdictCountsOnlyOnSynchronize` in `scripts/adrlint/main_test.go`: `PR_KEEP=yes` makes the label count on `synchronize` only; `no`, empty, any other value, `reopened` and `opened` do not.
* `TestRuleA_bothWorkflowsRunTheSameKeepLabelVerdict`, `TestRuleA_labelResetKeepsOnlyDecisionConfirmed` and `TestRuleA_adrLintPassesTheVerdictAndReadsLabelsLive` in `internal/release`: both workflows fetch and decide on `synchronize` only, the verdict steps hold no token, `label-reset.yml`'s only checkout is the base commit, its removal step always runs and narrows only on an exact `yes`, and `adr-lint.yml` passes `PR_KEEP` and reads labels with `gh pr view`. `TestWorkflows_labelResetRemovesApprovalLabels` is updated for `checks: read` and the base checkout.
* On the first push to a labelled pull request after this merges, a rebase keeps the label and an edit to the change removes it.

## Pros and Cons of the Options

### A. Identical change, checked per file

* Good, because it keeps the label exactly when the reviewed change is unchanged and fails closed.
* Good, because one implementation serves both jobs.
* Bad, because there is more code in the gate.

### B. Protected files unchanged

* Good, because it is simple.
* Bad, because a reviewed workflow runs files outside the protected paths (scripts, tests, Go code), so a later push could change what the reviewed workflow executes.

### C. Remove the label on every push

* Good, because there is no change to the gate.
* Bad, because every rebase costs the reviewer another look at an unchanged change.

## More Information

Issue #1584. Extends [ADR 0078](0078-queue-entry-needs-an-approval-or-the-lead-approved-label-that-a-push-removes.md). `.github/workflows/label-reset.yml`, `.github/workflows/adr-lint.yml`, `scripts/adrlint/keeplabel.go`, `docs/branch-protection.md`. Ported from bogworks/homerange's ADRs 0013 and 0014, adapted to mash's runner labels and trust model. <!-- adrlint:allow vocabulary -->
