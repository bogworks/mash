# Branch protection for `main`

These are **repository settings, applied by the maintainer in GitHub**. They are not code, nothing in this repository
enforces or applies them, and changing this file changes nothing until the settings are made.

Settings > Branches > Add branch ruleset (or classic branch protection) for `main`:

- **Require a pull request** before merging. No direct pushes to `main`, with one exception: add the Mergify GitHub App
  to the ruleset's bypass list ("Allow specified actors to bypass required pull requests"). The queue merges by
  fast-forward, so Mergify pushes the tested queue commit to `main` itself (`.mergify.yml`); without the bypass every
  queued merge is rejected.
- **Dismiss stale pull request approvals when new commits are pushed.** Without it an outside contributor can get
  a maintainer's approval, push more code, and still satisfy the queue's `approved-reviews-by` condition; `.mergify.yml`
  also dismisses approvals on an outside author's push.
- **Require status checks to pass**, with branches up to date:
  - `test (linux)`, `test (macos)`, `reproducible` (from `ci.yml`; the release gate in
    `scripts/release-check` requires exactly these names, so never rename them),
  - `lint` (from `lint.yml`),
  - `adr-lint` (from `adr-lint.yml`; it checks the ADRs and the `Decision:` line in the pull request body, and is also a Mergify
    queue condition, so add it as required once the workflow is on `main`),
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
