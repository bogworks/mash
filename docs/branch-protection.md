# Branch protection for `main`

These are **repository settings, applied by the maintainer in GitHub**. They are not code, nothing in this repository
enforces or applies them, and changing this file changes nothing until the settings are made.

## What is set today (D195 and D204, applied 2026-11-11 and 2026-11-12)

The facts in this section are the maintainer's decisions as reported by the lead; they were not verified live while
writing this file, except where a date and a test are named.

**Ruleset 26308864 (`main-protection`) and the `update` rule (D204).** The ruleset has the `update` rule (restrict
updates to `main`), so nothing can move `main` except an actor on the bypass list. Its **only** bypass actor is the
Mergify app (integration 10562, mode `always`). There is **no user or role bypass**, because the lanes push as the
org owner: a user bypass would let every lane push to `main`. Mergify's queue merges by fast-forward and pushes the
tested queue commit to `main` itself (`.mergify.yml`), which is why it is the one actor that bypasses. D204 was applied
on 2026-11-12 at about 00:30Z, and the HTTP 405 refusal of a direct merge was verified then, on a test pull request
(mash #423): a direct `gh pr merge` is refused, so the queue is the only way into `main`.

**Break-glass.** The org owner edits the ruleset (for example to add a temporary bypass) and restores it afterwards
from the saved ruleset JSON. The ruleset's history and the org audit log record both changes.

**Pinned checks (D195).** `pr-check` and `label-reset` were pinned in the ruleset with GitHub Actions as the expected
source (`integration_id`) on 2026-11-11 at about 10:06Z, and `adr-lint` the same way at about 11:01Z, so a check of
that name posted by anything else does not satisfy the ruleset. Also required: `Mergify Merge Protections`. A Mergify
queue draft is skipped by a job-level `if` in `adr-lint.yml` that requires the head ref prefix
`mergify/merge-queue/`, a same-repository head and the Mergify bot as author, and a skipped job is accepted by
Mergify and the ruleset. Everything in the settings list below that is not in this section is a recommendation, not
a setting.

## Snapshot verified 2026-11-10 (history)

This is the state as read on 2026-11-10, before D195 and D204; it is kept as history and no longer describes the
ruleset. Read-only, with `gh api repos/bogworks/mash/rulesets/26308864`: the only branch rule on the default branch was
the ruleset `main-protection` (enforcement active, no bypass actors) with exactly three rules: deletion blocked,
non-fast-forward blocked, and one required status check, the context `Mergify Merge Protections`. There was **no
required pull-request rule and no required-reviews rule**, and classic branch protection was not set (404). An earlier
version of this file described the settings below as if they were in force; the file and the ruleset disagreed, and
the ruleset was the fact.

**The label gate then guarded the queue only.** The ruleset had no pull-request or review rule, so a direct
`gh pr merge` (or the merge button) by an identity with write access skipped Mergify entirely, and with it every queue
condition; D204's `update` rule above closed that. Also
verified read-only (2026-11-10), and describing the state **before** the `merge_protections` rule below: the check
`Mergify Merge Protections` did **not** go red without the label or while a protection was blocking. On PR #349
(labelled `hold`) its conclusion was `success` while its summary said "1 of 2 protections blocking"; on pull requests
it did not apply to it was `neutral`. The ruleset accepts it either way (check-neutral and check-success), so then the
ruleset did not enforce the queue-entry gate; only the queue did.

**The `merge_protections` rule.** `.mergify.yml` now defines one rule, "codex-review and approval before merge", that
mirrors the queue entry conditions that matter at an API merge: `check-success = codex-review`, an approval by the
maintainer or the `lead-approved` label, and `label != hold`. It applies to pull requests into `main` and skips
Mergify's own queue drafts, which have neither an approval nor a label and would otherwise deadlock the queue. A draft
is recognised by both its head ref (`mergify/merge-queue/<hash>`) and its author (`mergify[bot]`); a head ref alone is
not enough, since any same-repository writer can name a branch that way and would otherwise switch the rule off. `pr-check`, `adr-lint` and `label-reset` are not repeated: the ruleset and the
queue already require them. With the rule, the ruleset's required check `Mergify Merge Protections` is meaningful: it
should fail while the rule's conditions are unmet. `internal/release/mergify_test.go` checks the rule against the
queue conditions. This is **not yet verified live**: it is verified only after this change is merged. Read-only check
to run then: on a pull request without codex-review or approval,
`gh api repos/bogworks/mash/commits/<head-sha>/check-runs --jq '.check_runs[] | select(.name=="Mergify Merge Protections") | .conclusion'`
should print `failure` (not `success`), and on a queue draft it should not be `failure`. Record the date and result
here when done.

**Residual risk.** Apart from `Mergify Merge Protections`, `pr-check`, `label-reset` and `adr-lint`, nothing in GitHub
pins `codex-review`. It is a condition only in the queue rule and the `merge_protections` rule of `.mergify.yml`, and Mergify's `check-success = <name>` matches a check run or commit status of that name whoever posted
it. A pull request that adds or edits a workflow can post a check (or, with a token, a status) named `codex-review`,
so that check proves nothing against a pull request author who controls workflows. What holds instead:
changes to `.github/workflows/`, `.github/actions/`, `.mergify.yml`, `scripts/adrlint/`, `internal/release/` and this file
are protected paths (judged on `git diff --no-renames`, so a rename out of one counts), so `Decision: none` on them
needs the reviewer's `decision-confirmed` label and otherwise an ADR is required; `label-reset` runs from the base
branch (`pull_request_target`), not from the pull request; and the lead's approval or `lead-approved` label is still
required for entry.

**What would close it:** require the remaining check, `codex-review`, in the ruleset with an expected source, that is a
`required_status_checks` entry with an `integration_id` (the GitHub App that must post it), as is already done for
`pr-check`, `label-reset` and `adr-lint`. That is a
repository-settings change for the maintainer; it is not part of any pull request.

**No human approving review is required, by design.** The merge bypass is closed (only Mergify can update `main`), and
the maintainer's model is the independent Codex review plus the lead's clearance (D204), not a human approving review
in the ruleset, so a required-reviews rule is not recommended. See
[ADR 0078](decisions/0078-queue-entry-needs-an-approval-or-the-lead-approved-label-that-a-push-removes.md).

## Settings and recommendations

The ruleset's applied state is in "What is set today" above; the list below is the wider recommended set, and an item
is a setting only if that section says so. Settings > Branches > Add branch ruleset (or classic branch protection) for
`main`:

- **No direct pushes to `main`.** Applied by D204 as the `update` rule, with the Mergify app as the only bypass actor
  and no user or role bypass (see above). The queue merges by fast-forward, so Mergify pushes the tested queue commit
  to `main` itself (`.mergify.yml`); without that one bypass every queued merge is rejected.
- **Dismiss stale pull request approvals when new commits are pushed.** Without it an outside contributor can get
  a maintainer's approval, push more code, and still satisfy the queue's `approved-reviews-by` condition; `.mergify.yml`
  also dismisses approvals on an outside author's push.
- **Require status checks to pass**, with branches up to date:
  - `test (linux)`, `reproducible` (from `ci.yml`; the release gate in
    `scripts/release-check` requires exactly these names, so never rename them). `test (macos)` is advisory
    (ADR 0079): do not require it until x-lead confirms the macOS runner no longer pauses running jobs,
    then add it here, to `pr-check`'s needs and to `release.RequiredChecks`,
  - `lint` (from `lint.yml`),
  - `adr-lint` (from `adr-lint.yml`; it checks the ADRs and the `Decision:` line in the pull request body, and is also a Mergify
    queue condition; the ruleset already requires it, pinned, per D195),
  - `label-reset` (from `label-reset.yml`; it is also a Mergify queue condition; the ruleset already requires it, pinned, per D195),
  - `govulncheck` and `codeql` (from `security.yml`; `codeql` runs on public repositories only, since code scanning is not
    available on a private one),
  - **Code scanning results** (the separate check GitHub adds once code scanning is on): the `codeql` job passing only
    proves the analysis finished, so also require code scanning results in the ruleset, with the alert threshold set to
    block high and above (Settings > Rules > the ruleset > Require code scanning results > CodeQL).
  `lint`, `govulncheck` and `codeql` run on every pull request (no path filter), so docs-only PRs are not left pending.
  A check can only be selected as required after it has run once, so add `lint`, `govulncheck` and `codeql` after
  their first runs on `main`. `lint` passes while staticcheck findings are tolerated (see `lint.yml`).
- **Merge queue.** mash queues with Mergify (`.mergify.yml`), which tests a queued pull request on its own branch and
  pull request, so the required workflows' `pull_request` trigger already runs on queued merges. If GitHub's native
  merge queue is ever enabled instead, first add a `merge_group` trigger to every required workflow, or queued merges
  will wait for checks that never start.
- **Queue entry** (`.mergify.yml`). A pull request enters the queue only with the maintainer's recorded approval
  (`approved-reviews-by`) or the `lead-approved` label, which the lead applies at merge time. Authorship is not a
  condition: the maintainer's login authors agent pull requests too, so it proves nothing. The `hold` label is the
  brake on top: while it is set, the pull request is neither auto-queued nor allowed into the queue. Create the
  `lead-approved`, `decision-confirmed` and `hold` labels (Settings > Labels).
  `.github/workflows/label-reset.yml` removes `lead-approved` on every push (`pull_request_target`; it holds
  `pull-requests: write` and `contents: read` and nothing else, and checks out no code), so the label never covers code
  pushed after it. A recorded approval is dismissed on push by Mergify only for an outside author's pull request (the
  rule skips the maintainer's login, which agents push as). The same
  workflow removes `decision-confirmed`, because adr-lint accepts that label applied to an earlier head. It runs on
  `opened`, `reopened` and `synchronize`, and on `opened` it removes the labels too (either can be preset when a pull
  request is created), so the `label-reset` check exists on every head. `label-reset` is a queue condition, so the gate fails closed: a head
  without a green `label-reset` check cannot enter the queue. After removing the labels it reads them again and fails
  the job if either is still set (re-applied meanwhile, or the removal did not take). It takes effect only once it is on
  `main`.
  adr-lint ignores `decision-confirmed` on `synchronize`, `reopened` and `opened` runs (the event action is passed to it), because
  it runs beside label-reset and could read the label before the removal, which starts no new run; the label counts only
  from a `labeled` run on the current head, and adr-lint reads labels live with `gh pr view`. Every job, adr-lint and label-reset included, runs on the self-hosted labels `[self-hosted, rh-linux]`,
  `[self-hosted, rh-macos]` or, for the gate jobs (adr-lint and label-reset among them), `[self-hosted, rh-gate]` ([ADR 0079](decisions/0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md)), fork pull requests
  included once a person approves the run; both gate workflows are safe there because they run from the base branch and execute nothing
  from the pull request. If no runner with the label is online, label-reset never starts and the check never appears, so the queue keeps the
  pull request out (fail closed).
  See [ADR 0078](decisions/0078-queue-entry-needs-an-approval-or-the-lead-approved-label-that-a-push-removes.md).
- **Require linear history** (squash or rebase merges only).
- **Block force pushes and deletions** of `main`.
- **Code owners review is not required** (no CODEOWNERS file).

## Releases

Releases are **signed annotated tags** `vN.N.N` pushed by the maintainer at a commit on `main` (see `release.md`).
Optionally add a tag ruleset for `v*` that restricts creation to the maintainer and blocks deletion and updates.

## Security features

Settings > Code security: enable **Dependabot alerts** and **Dependabot security updates** (version updates are
configured by `.github/dependabot.yml`), **secret scanning** with **push protection**, and **private vulnerability
reporting**. CodeQL results appear under Security > Code scanning.
