# Branch protection for `main`

These are **repository settings, applied by the maintainer in GitHub**. They are not code, nothing in this repository
enforces or applies them, and changing this file changes nothing until the settings are made.

## What is set today (verified 2026-11-10)

Read-only, with `gh api repos/bogworks/mash/rulesets/26308864`: the only branch rule on the default branch is the
ruleset `main-protection` (enforcement active, no bypass actors) with exactly three rules: deletion blocked,
non-fast-forward blocked, and one required status check, the context `Mergify Merge Protections`. There is **no
required pull-request rule and no required-reviews rule**, and classic branch protection is not set (404). An earlier
version of this file described the target below as if it were in force; the file and the ruleset disagreed, and the
ruleset is the fact. Everything in the list below that is not in this paragraph is a recommendation, not a setting.

**The label gate guards the queue only.** The ruleset has no pull-request or review rule, so a direct `gh pr merge` (or
the merge button) by an identity with write access skips Mergify entirely, and with it every queue condition. Also
verified read-only (2026-11-10): the check `Mergify Merge Protections` does **not** go red without the label or while a
protection is blocking. On PR #349 (labelled `hold`) its conclusion is `success` while its summary says "1 of 2
protections blocking"; on pull requests it does not apply to it is `neutral`. The ruleset accepts it either way
(check-neutral and check-success), so the ruleset does not enforce the queue-entry gate; only the queue does.

**Residual risk.** Because no check is required by GitHub itself except `Mergify Merge Protections`, nothing in GitHub
pins `codex-review`, `pr-check`, `adr-lint` or `label-reset`. They are conditions only in the queue rule of
`.mergify.yml`, and Mergify's `check-success = <name>` matches a check run or commit status of that name whoever posted
it. A pull request that adds or edits a workflow can post a check (or, with a token, a status) named `codex-review` or
`pr-check`, so those checks prove nothing against a pull request author who controls workflows. What holds instead:
changes to `.github/workflows/`, `.github/actions/`, `.mergify.yml`, `scripts/adrlint/`, `internal/release/` and this file
are protected paths (judged on `git diff --no-renames`, so a rename out of one counts), so `Decision: none` on them
needs the reviewer's `decision-confirmed` label and otherwise an ADR is required; `label-reset` runs from the base
branch (`pull_request_target`), not from the pull request; and the lead's approval or `lead-approved` label is still
required for entry.

**What would close it:** require the checks in the ruleset with an expected source, that is `required_status_checks`
entries with an `integration_id` (the GitHub App that must post each one), and add a required-reviews rule. That is a
repository-settings change for the maintainer; it is not part of any pull request. See
[ADR 0078](decisions/0078-queue-entry-needs-an-approval-or-the-lead-approved-label-that-a-push-removes.md).

## Target settings

Settings > Branches > Add branch ruleset (or classic branch protection) for `main`:

- **Require a pull request** before merging. No direct pushes to `main`, with one exception: add the Mergify GitHub App
  to the ruleset's bypass list ("Allow specified actors to bypass required pull requests"). The queue merges by
  fast-forward, so Mergify pushes the tested queue commit to `main` itself (`.mergify.yml`); without the bypass every
  queued merge is rejected.
- **Dismiss stale pull request approvals when new commits are pushed.** Without it an outside contributor can get
  a maintainer's approval, push more code, and still satisfy the queue's `approved-reviews-by` condition; `.mergify.yml`
  also dismisses approvals on an outside author's push.
- **Require status checks to pass**, with branches up to date:
  - `test (ubuntu-latest)`, `test (macos-latest)`, `reproducible` (from `ci.yml`; the release gate in
    `scripts/release-check` requires exactly these names, so never rename them),
  - `lint` (from `lint.yml`),
  - `adr-lint` (from `adr-lint.yml`; it checks the ADRs and the `Decision:` line in the pull request body, and is also a Mergify
    queue condition, so add it as required once the workflow is on `main`),
  - `label-reset` (from `label-reset.yml`; it is also a Mergify queue condition, so add it as required once the
    workflow is on `main`),
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
  from a `labeled` run on the current head, and adr-lint reads labels live with `gh pr view`. adr-lint runs on the self-hosted runners labelled `self-hosted, rh-linux`, fork pull requests included once a person
  approves the run. label-reset runs on the
  runner named by the repository variable `RUNNER_LINUX` (no GitHub-hosted runner; safe because it runs from the base
  branch and executes nothing from the pull request); set that variable, or the check never appears and the queue keeps the
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
