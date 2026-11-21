# Branch protection for `main`

These are **repository settings, made by a repository admin in GitHub**. They are not code: nothing in this repository applies or enforces them, and changing this file changes nothing until the settings are made. This page states what is required of `main` and why. The merge queue is configured in `.mergify.yml`; the checks are jobs in `.github/workflows/`.

## What is set

**One ruleset, `main-protection`, with these rules:**

- **`update` (restrict updates).** Nothing can move `main` except an actor on the ruleset's bypass list. The only bypass actor is the Mergify app (mode `always`). There is no user or role bypass: a user bypass would let every contributor push to `main`. Mergify's queue merges by fast-forward and pushes the tested queue commit to `main` itself (`.mergify.yml`), which is why it is the one actor that bypasses. The effect is that a direct merge (`gh pr merge`, or the merge button) is refused, and the queue is the only way into `main`.
- **Deletion and non-fast-forward are blocked.**
- **Required status checks: four.** `pr-check`, `label-reset` and `adr-lint` are pinned to GitHub Actions as their expected source (`integration_id`), so a check of that name posted by anything else does not satisfy the ruleset. `Mergify Merge Protections` is required too. These four are the only checks the ruleset requires today. The longer list under "Required status checks" below is a recommendation, except where it says a check is one of these four.

There is no required pull-request rule and no required-reviews rule, and classic branch protection is not used.

**Break-glass.** An admin edits the ruleset (for example to add a temporary bypass) and restores it afterwards from a saved copy of the ruleset JSON. The ruleset history and the organisation audit log record both changes.

**No human approving review is required in the ruleset, by design.** The merge bypass is closed (only Mergify can update `main`), and the model is an independent automated review plus a maintainer's clearance at queue entry, so a required-reviews rule is not recommended. See [ADR 0078](decisions/0078-queue-entry-needs-an-approval-or-the-lead-approved-label-that-a-push-removes.md).

## Required status checks

The ruleset requires only `pr-check`, `label-reset`, `adr-lint` and `Mergify Merge Protections` today (see "What is set"). The rest of this list is recommended, not applied: the release gate and the queue conditions already depend on several of these checks, but the ruleset does not require them. Recommended settings: require status checks to pass, with branches up to date, for:

- `test (linux)` and `reproducible` (from `ci.yml`). The release gate in `scripts/release-check` requires exactly these names, so never rename them. `test (macos)` is advisory ([ADR 0079](decisions/0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md)): do not require it until the macOS runner no longer pauses running jobs, then add it here, to `pr-check`'s needs and to `release.RequiredChecks`.
- `pr-check` (from `ci.yml`), the aggregate of every required job. Required today, pinned to GitHub Actions.
- `lint` (from `lint.yml`). It passes while staticcheck findings are tolerated (see `lint.yml`).
- `adr-lint` (from `adr-lint.yml`). It checks the ADRs and the `Decision:` line in the pull request body, and is also a Mergify queue condition. Required today, pinned to GitHub Actions.
- `label-reset` (from `label-reset.yml`). It is also a Mergify queue condition. Required today, pinned to GitHub Actions.
- `govulncheck` and `codeql` (from `security.yml`). `codeql` runs on public repositories only, since code scanning is not available on a private one.
- **Code scanning results** (the separate check GitHub adds once code scanning is on). The `codeql` job passing only proves the analysis finished, so also require code scanning results in the ruleset, with the alert threshold set to block high and above (Settings > Rules > the ruleset > Require code scanning results > CodeQL).
- `Mergify Merge Protections`, see below. Required today.

A check can only be selected as required after it has run once, so add `lint`, `govulncheck` and `codeql` after their first runs on `main`.

`lint`, `govulncheck` and `codeql` run on every pull request (no path filter), so docs-only pull requests are not left pending.

### Docs-only pull requests

A docs-only pull request skips the heavy jobs (`test (linux)`, `test (macos)`, `reproducible`, `lint`, `govulncheck`, `codeql`) with a job-level `if` on the `changes` job's `code` output, never with a workflow-level `paths` or `paths-ignore`, because a required check that never reports stalls the queue. `conformance` (advisory, in no `needs` and no queue condition) is skipped on docs-only pull requests too, since the review watcher waits only on `pr-check`; a Mergify draft still runs it. Every check name still reports, as skipped, and `pr-check`, `adr-lint`, `label-reset` and `adr-numbers` run as always. `pr-check` accepts a skipped heavy job only when `changes` succeeded and said `code=false`.

Docs-only means every changed path is under `docs/` or is a `*.md` file, and is not one a build or test reads; `scripts/ci-changes` is the definition. Code, and so the full set, is everything else: Go sources and module files, `.github/`, `.mergify.yml`, `scripts/`, `plugin/`, `cmd/`, `internal/` and `ci/` (their Markdown included), test data, `CHANGELOG.md`, `changelog.d/`, `docs/branch-protection.md`, `docs/review/`, `docs/allowed_signers`, `docs/skipped-releases.txt` and binary assets under `docs/`. A push, a merge group and a Mergify queue draft (head ref `mergify/merge-queue/`) always run the full set, so the combined commit and `main` are always tested.

**Hardening of the classifier.** For a pull request the `changes` job runs `scripts/ci-changes` from the base revision (`git show "$BASE:scripts/ci-changes"`), not from the pull request, and the step itself says `code=true` whenever the diff touches `.github/` or `scripts/ci-changes`, without asking the classifier. A base with no classifier also means `code=true`. `internal/release/workflows_docsonly_test.go` runs the step against a rewritten classifier and a changed workflow.

**Residual risk.** GitHub runs a pull request's own workflow files, so a pull request that edits the `changes` step itself is controlled by review only; the step's guard cannot protect against its own removal.

## The merge queue and queue entry

mash queues with Mergify (`.mergify.yml`), which tests a queued pull request on its own branch and pull request, so the required workflows' `pull_request` trigger already runs on queued merges. If GitHub's native merge queue is ever enabled instead, first add a `merge_group` trigger to every required workflow, or queued merges will wait for checks that never start.

**Entry conditions.** A pull request enters the queue only when all of these hold (`.mergify.yml`):

- it is not a draft, has no conflict and does not carry the `hold` label. `hold` is the brake: while it is set, the pull request is neither auto-queued nor allowed into the queue;
- `codex-review`, `pr-check`, `adr-lint` and `label-reset` are successful on the head. The last three are written `@github-actions/<name>`, which binds them to checks posted by GitHub Actions, so a forged commit status of that name does not satisfy them. `codex-review` and `lead-review` are commit statuses and stay plain;
- the reviewer has cleared it: the `lead-review` status (below) and either a recorded approval by a listed maintainer (`approved-reviews-by`) or the `lead-approved` label. Authorship is not a condition, because the maintainers' login also authors agent pull requests and so proves nothing. The approvers are listed explicitly rather than "any approval".

Create the `lead-approved`, `decision-confirmed` and `hold` labels (Settings > Labels).

**`label-reset`.** `.github/workflows/label-reset.yml` removes `lead-approved` and `decision-confirmed` on every push, so a label never covers code pushed after it. It runs on `opened`, `reopened` and `synchronize` (on `opened` too, because either label can be preset when a pull request is created), under `pull_request_target`, so it runs from the base branch and executes nothing from the pull request. It holds `pull-requests: write` and `contents: read` and nothing else, and checks out no code. After removing the labels it reads them again and fails the job if either is still set (re-applied meanwhile, or the removal did not take). `label-reset` is a queue condition, so the gate fails closed: a head without a green `label-reset` check cannot enter the queue. It takes effect only once it is on `main`.

`adr-lint` ignores `decision-confirmed` on `synchronize`, `reopened` and `opened` runs (the event action is passed to it), because it runs beside `label-reset` and could read the label before the removal, which starts no new run. The label counts only from a `labeled` run on the current head, and `adr-lint` reads labels live with `gh pr view`.

**Runners.** Every job, `adr-lint` and `label-reset` included, runs on the self-hosted labels `[self-hosted, rh-linux]`, `[self-hosted, rh-macos]` or, for the gate jobs (`adr-lint` and `label-reset` among them), `[self-hosted, rh-gate]` ([ADR 0079](decisions/0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md)). Fork pull requests run there once a person approves the run; both gate workflows are safe there because they run from the base branch and execute nothing from the pull request. If no runner with the label is online, `label-reset` never starts and its check never appears, so the queue keeps the pull request out (fail closed).

**Stale approvals.** Dismiss stale pull request approvals when new commits are pushed. Without it an outside contributor can get a maintainer's approval, push more code, and still satisfy the queue's `approved-reviews-by` condition. `.mergify.yml` dismisses approvals on an outside author's push; it skips pull requests authored by the maintainers' own login, which agents also push as, and there the label path is the gate that `label-reset` resets on every push. GitHub's own setting would cover every author, but the ruleset has no pull-request rule, so it is not set.

**The `lead-review` status** ([ADR 0116](decisions/0116-the-maintainers-review-is-a-commit-status-on-the-exact-head-and-the-queue-requires-it-beside-the-label-for-one-week.md)). The reviewer's approval is also a commit status `lead-review` = success on the exact head SHA that was reviewed, posted by `scripts/lead-approve <pr>`. The script needs `LEAD_LOGIN`, and refuses unless `codex-review` and `label-reset` are success on that head, the caller is `LEAD_LOGIN` and `hold` is absent. `.mergify.yml` requires the status as a plain `queue_conditions` entry beside the approval-or-`lead-approved` gate. A new head has no such status, so a push drops it by itself. It is a queue condition only, never a `merge_protections` one, because the queue draft's commit has no such status. It is not authentication: `check-success` matches a status whoever posted it and Mergify cannot filter by creator, so any token with status access can post `lead-review`, as it could apply the label. `scripts/lead-approve --audit <pr>` flags statuses from any creator other than `LEAD_LOGIN`. The ADR sets the end of the transition period in which the status is required beside the label; when it ends, the entry condition becomes the status or the approval, and the files that change are `.mergify.yml`, `internal/release/mergify_test.go`, `.github/workflows/label-reset.yml` and this file.

### The `Mergify Merge Protections` check

The ruleset requires `Mergify Merge Protections`. `.mergify.yml` defines one `merge_protections` rule, "codex-review and approval before merge", whose conditions are only the two that Mergify evaluates on the pull request itself: an approval by a listed maintainer or the `lead-approved` label, and `label != hold`. It applies to pull requests into `main` and skips Mergify's own queue drafts, recognised by both their head ref (`mergify/merge-queue/<hash>`) and their author (`mergify[bot]`); a head ref alone is not enough, since any same-repository writer can name a branch that way.

**The rule holds no `check-success` condition, on purpose.** Mergify applies a rule's conditions to the queue draft's commit as merge conditions too, and `adr-lint` is skipped on a draft, so a rule that waited for `adr-lint` or `codex-review` made the queue refuse every pull request ("failing checks: adr-lint") and deadlocked it. `codex-review` and `adr-lint` are enforced by the queue's `queue_conditions` at entry and, for `adr-lint`, by the ruleset, and the `update` rule refuses any merge that is not Mergify's, so no gate is lost. `internal/release/mergify_test.go` compares the rule's label conditions with the queue conditions and fails if the rule gains a check condition.

Not verified live: whether the check goes red on a pull request that has neither approval nor label. To check, read it on such a pull request:

```sh
gh api repos/{owner}/{repo}/commits/<head-sha>/check-runs --jq '.check_runs[] | select(.name=="Mergify Merge Protections") | .conclusion'
```

It should print `failure` (not `success`), and on a queue draft it should not be `failure`.

### Residual risk: `codex-review`

Apart from `Mergify Merge Protections`, `pr-check`, `label-reset` and `adr-lint`, nothing in GitHub pins `codex-review`. It is a condition only in the queue rule of `.mergify.yml`, and Mergify's `check-success = <name>` matches a check run or commit status of that name, whoever posted it. A pull request that adds or edits a workflow can post a check (or, with a token, a status) named `codex-review`, so that check proves nothing against a pull request author who controls workflows. What holds instead:

- changes to `.github/workflows/`, `.github/actions/`, `.mergify.yml`, `scripts/adrlint/`, `internal/release/` and this file are protected paths (judged on `git diff --no-renames`, so a rename out of one counts), so `Decision: none` on them needs the reviewer's `decision-confirmed` label, and otherwise an ADR is required;
- `label-reset` runs from the base branch (`pull_request_target`), not from the pull request;
- the reviewer's approval or `lead-approved` label, and the `lead-review` status, are still required for entry.

What would close it: require `codex-review` in the ruleset with an expected source, that is a `required_status_checks` entry with an `integration_id` (the GitHub App that must post it), as is already done for `pr-check`, `label-reset` and `adr-lint`. That is a repository-settings change, not part of any pull request.

## Other settings

- **Require linear history** (squash or rebase merges only).
- **Block force pushes and deletions** of `main`.
- **Code owners review is not required** (no CODEOWNERS file).

## Releases

Below the cutover version (`docs/release-cutover`) releases are **signed annotated tags** `vN.N.N` pushed by a maintainer at a commit on `main` (see `release.md`). From the cutover the tag is annotated and unsigned, pushed by the minting service, and the tag rulesets below carry the trust that the SSH tag signature carried before.

**Tag rulesets.** Two rulesets on `v*` tags:

- **Ruleset A (creation):** only deploy keys may create a `v*` tag.
- **Ruleset B (update and deletion):** updating and deleting a `v*` tag are blocked, with no bypass actor.

There is a single write deploy key, held by the minting service; no person and no other automation holds it. **Immutable releases** are on, so a published release's assets cannot change. These are set by a repository admin in GitHub. They are not applied or checked by this repository: the gate cannot read them, and this repository's tests do not yet verify they are applied.

## Security features

Settings > Code security: enable **Dependabot alerts** and **Dependabot security updates** (version updates are configured by `.github/dependabot.yml`), **secret scanning** with **push protection**, and **private vulnerability reporting**. CodeQL results appear under Security > Code scanning.
