---
status: proposed
date: 2026-11-15
decision-makers: Randy Bias (mash maintainer)
---

# The advisory eph test job sends its JUnit file to Mergify CI Insights with an OIDC token and no stored key

Amends: ADR-0123

## Context and Problem Statement

Mergify CI Insights and its Test Engine record job timing and per-test health (flaky, broken) from a JUnit file that a
test job uploads to api.mergify.com. mash has a flaky-test problem and no per-test history. x-ops3 prepared the tools
(bogworks/cicd `docs/pieces/ci-insights.md`). This adds a third-party data flow from CI: what does mash send, from
which job, and how does that job prove who it is to Mergify?

## Decision Drivers

* Nothing may gate a merge or a release: the data flow is a trial on the advisory job.
* No long-lived secret in the repository or its settings, and nothing downloaded in a job.
* The required `test (linux)` and `test (macos)` jobs keep running exactly their current steps.
* The job must still fail when the tests fail.

## Considered Options

* Only `test (linux, eph)` uploads, with the pinned host tools and an OIDC token (no stored key)
* The same, with an application key stored as the repository secret `MERGIFY_TOKEN`
* Every test job uploads

## Decision Outcome

Chosen option: "Only `test (linux, eph)` uploads, with OIDC". The local action `.github/actions/go-test` gains an input
`insights`, default `false`, and only `test-eph` passes `"true"`. With it unset the action runs the same steps as before
(the `go test` step carries the condition `inputs.insights != 'true'`). With it set, the action runs `go test` through the
pinned `gotestsum` (same flags) with `continue-on-error`, uploads the JUnit file with the pinned `mergify ci junit-process`
only on `success() || failure()` and never for a pull request from a fork, and its last step fails the job when the tests
did not succeed, so the job's result is the same as a plain `go test`. Both tools are pinned by version and SHA-256 in
bogworks/cicd (`runners/pins/pins.yaml`) and installed read-only in `/opt/ci-tools/bin` on the runners. `test-eph` alone
gets `id-token: write`; the CLI trades GitHub's ID token (audience `https://api.mergify.com`) for a one-hour Mergify token
limited to this repository, under a trust policy in the Mergify dashboard that allows `push` and `merge_group` for this
workflow only. No application key exists.

**What leaves the runner.** To api.mergify.com: the JUnit file (test names, durations, results), the repository, branch,
commit and job name. For a failing test the file holds that test's full output, so **failure output, stack traces and
the file paths in them, leaves the runner**. It is not stripped. A check of a full run's file found no host names, IP or
e-mail addresses, environment values or token-like strings; a failing test that prints such a thing into its output
would send it, so tests must not. Separately, once CI Insights is enabled for the repository, Mergify's GitHub App reads
the repository's workflow runs and job logs from GitHub.

### Confirmation

`TestCI_insightsRunsOnlyInTheEphJobAndStillFailsOnFailingTests` fails if any job other than `test-eph` passes `insights`
or sets permissions, if `test-eph` holds anything but `contents: read` and `id-token: write`, if the plain `go test` step
changes or loses its condition, if an insights step runs without the input, if the upload stops being limited to
`success() || failure()` and non-fork runs or starts using a token, or if the last step stops failing the job.
`TestCI_ephShadowJobGatesNothing` still fails if the job enters pr-check, `RequiredChecks` or `.mergify.yml`.

### Consequences

* Good, because mash gets flaky-test detection and job timing with no secret to store or rotate.
* Good, because the required jobs run the steps they ran before and no merge or release depends on Mergify.
* Bad, because failure output and the paths in it go to a third party.
* Bad, because the trust policy and the repository setting live in the Mergify dashboard, outside the repository.

## Pros and Cons of the Options

### Only `test (linux, eph)` uploads, with OIDC

* Good, because no secret exists, and the required jobs and every merge and release are untouched.
* Bad, because the OIDC trust policy is set in the Mergify dashboard by an organization owner, outside the repository.

### The same, with an application key as the repository secret `MERGIFY_TOKEN`

* Good, because it needs no trust policy.
* Bad, because it is a long-lived secret in the repository's settings, which our rules forbid, and a pull request from a fork would not get it anyway.

### Every test job uploads

* Good, because it gives the most data.
* Bad, because it puts a third-party data flow and a network dependency on the required jobs before the trial has shown it is worth it.

## More Information

Preparation and the data check: bogworks/cicd `docs/pieces/ci-insights.md` (pinned tools, what the hosts hold). Mergify documents
CI Insights, the Go and Deno JUnit set-up and the OIDC exchange at docs.mergify.com (read 2026-11-15). The change goes in with the
label `hold` and the hold is lifted after the v0.5.0 tag. If the trial is dropped, remove the `insights` steps and
the `test-eph` permissions, untick CI Insights for the repository and delete the trust policy in the dashboard.
