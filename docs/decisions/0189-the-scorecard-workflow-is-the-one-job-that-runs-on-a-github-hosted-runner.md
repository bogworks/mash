---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# The Scorecard workflow is the one job that runs on a GitHub-hosted runner

Amends: ADR-0079

## Context and Problem Statement

[ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md) says every CI and release job runs on our
self-hosted runners, picked by literal label lists, with no GitHub-hosted label and no fallback. Decision 309 (Randy
Bias, 2026-11-26) asks for an OpenSSF Scorecard workflow. The Scorecard action's `publish_results: true` sends its
result to the OpenSSF API, and the API accepts it only from a GitHub-hosted runner that can mint an OIDC token for the
workflow. A self-hosted runner cannot publish. Does the Scorecard workflow stay on the self-hosted runners, or is it the
one exception?

## Decision Drivers

* The OpenSSF API refuses results from a self-hosted runner, so a self-hosted Scorecard run could not publish.
* ADR 0079's reasons stand for every other job: a runner this project does not control must not take its work.
* The exception must be narrow enough that a guard test can name it exactly.

## Considered Options

* Keep ADR 0079 whole: no Scorecard workflow, or one that runs without `publish_results`.
* Allow one workflow, `scorecard.yml`, on `ubuntu-latest` (chosen).
* Allow GitHub-hosted runners for any job that needs OIDC.

## Decision Outcome

Chosen: `.github/workflows/scorecard.yml` is the one workflow whose job runs on a GitHub-hosted runner. This amends
[ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md) for that workflow only; it supersedes nothing.
The workflow:

* triggers only on `schedule` (weekly) and `push` to `main`; never on `pull_request`, `pull_request_target` or
  `workflow_dispatch`;
* has one job, `analysis`, on `runs-on: ubuntu-latest`, with `if: github.repository == 'bogworks/mash'`, so a fork
  skips it;
* sets `permissions: read-all` at the top and, on the job, `security-events: write`, `id-token: write`,
  `contents: read` and `actions: read`, as the action's documentation asks for `publish_results`;
* pins `actions/checkout`, `ossf/scorecard-action` and `github/codeql-action/upload-sarif` by full commit sha with a
  version comment, and uploads the SARIF result to code scanning.

What stays true from ADR 0079: no other job moves; there is no `||` fallback to a hosted label; forks skip; and the
guard allows a GitHub-hosted label only in the file named exactly `scorecard.yml`, only `ubuntu-latest`, only for the
job `analysis`, and only while the file's triggers are `schedule` and `push`. The same text under another file name, a
pull-request trigger, another job name, another label or a fallback fails the guard.

### Consequences

* Good, because the project gets a published Scorecard result without moving any other job off the self-hosted runners.
* Good, because the hosted runner takes no untrusted input: the workflow never runs on a pull request, only on the
  schedule and on a push to `main`.
* Bad, because one job now runs on a runner this project does not control, which ADR 0079 ruled out; the guard keeps it
  to this one file.
* Neutral, because there is no Scorecard badge yet; none is added to the README or the docs.

### Confirmation

* `internal/release/workflows_selfhosted_test.go`: `TestWorkflows_onlySelfHostedRunners` (the real workflow passes),
  `TestSelfHostedGuard_scorecardException` (the exception holds only as described) and
  `TestSelfHostedGuard_catchesEachViolation`.

## Pros and Cons of the Options

### Keep ADR 0079 whole

* Good, because no rule changes.
* Bad, because there is no published Scorecard result.

### Allow one workflow on `ubuntu-latest`

* Good, because it is the smallest change that lets `publish_results` work.
* Bad, because it is an exception the guard has to carry by name.

### Allow GitHub-hosted runners for any job that needs OIDC

* Good, because later jobs with the same need would not need a new ADR.
* Bad, because the rule would stop being checkable by file name, and any job could claim the need.

## More Information

* Amends [ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md). Decision 309, 2026-11-26.
* Workflow: `.github/workflows/scorecard.yml`. Guard: `internal/release/workflows_selfhosted_test.go`.
