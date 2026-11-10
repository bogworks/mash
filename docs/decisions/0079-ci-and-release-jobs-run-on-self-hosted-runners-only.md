---
status: accepted
date: 2026-11-10
decision-makers: Randy Bias (mash maintainer)
---

# CI and release jobs run on self-hosted runners only

## Context and Problem Statement

Every workflow chose its runner with `vars.RUNNER_LINUX || 'ubuntu-latest'`, forced `ubuntu-latest` for a fork pull
request, and ci.yml also ran its test matrix on `macos-latest`. A job could therefore land on a GitHub-hosted runner
whenever the variable was unset or the pull request came from a fork. The maintainer's standing order is that no job runs
on a GitHub-hosted runner. How do the workflows pick a runner?

## Decision Drivers

* No job may reach a GitHub-hosted runner by default, by a fallback, or by a matrix label.
* A misconfiguration must fail visibly (a queued job) and never route somewhere else.
* Required check names should change as little as possible.
* The rule must be enforced by a test, not by review.

## Considered Options

* Self-hosted runners only, selected by two literal label lists (chosen).
* Keep the hosted fallback for forks, for an unset variable and for macOS.
* Select the runner with a repository variable and no fallback.

## Decision Outcome

Every job in `.github/workflows/*.yml` has `runs-on: [self-hosted, rh-linux]` or `runs-on: [self-hosted, rh-macos]`:
literal labels, no variable, no `||`, no fallback, no hosted label, no host name. If no runner carries the labels the job
stays queued and never goes to GitHub. A reusable-workflow caller job (the SLSA generator in release.yml) has no `runs-on`
of its own.

Fork pull requests run on the same labels as every other event. The repository already holds an outside contributor's
run for a person's approval; that setting, not the runner choice, is what keeps unreviewed fork code from starting. The
`adr-lint` workflow keeps its `pull_request_target` handling unchanged (checker built from the base branch, the pull
request as data only); its fork safety does not depend on the runner.

The macOS test leg stays. ci.yml's `test` matrix has two entries, `os: linux` on `[self-hosted, rh-linux]` and
`os: macos` on `[self-hosted, rh-macos]`, so the check names are `test (linux)` and `test (macos)`. The macOS runner is
Intel x64 with 2 vCPU and runs one job at a time; a queued macOS job waits, and that wait is accepted.

Required check names change: `test (ubuntu-latest)` becomes `test (linux)` and `test (macos-latest)` becomes
`test (macos)`; `reproducible`, `lint`, `adr-lint`, `govulncheck`, `codeql` and `pr-check` keep their names.
`release.RequiredChecks` and the branch ruleset must carry the new names, and the ruleset must change in the same step as
this merge.

A guard test, `internal/release/workflows_selfhosted_test.go`, fails unless every job-level `runs-on` is exactly the list
`[self-hosted, rh-linux]` or `[self-hosted, rh-macos]`. A matrix-driven `runs-on: ${{ matrix.<key> }}` passes only if
every value the matrix supplies is one of those two lists. It also fails on `||` in a `runs-on`, on a hosted label
(`ubuntu-*`, `macos-*`, `windows-*`) anywhere outside a comment, and on a host name.

### Consequences

* Good, because no job can reach a GitHub-hosted runner.
* Good, because a missing runner is visible as a queued job.
* Good, because macOS stays tested and only the two `test` check names change.
* Bad, because fork pull request code runs on a runner the maintainers operate once a person approves the run; the
  approval setting is the control and it lives outside the repository.
* Bad, because one macOS job at a time makes macOS the slowest leg in the queue.
* Bad, because `--deny-self-hosted-runners` stays in `scripts/releasecheck/supply.go`, `release.yml` and
  `docs/release.md`, and it refuses attestations made on a self-hosted runner. Until the maintainer decides what replaces
  it, release verification refuses the attestations these runners produce. That is an open security decision.

### Confirmation

`TestWorkflows_onlySelfHostedRunners` runs over every workflow file, and `TestSelfHostedGuard_catchesEachViolation` keeps
a negative fixture for each violation type. The required-check names are covered by
`TestCI_producesTheCheckNamesTheGateRequires`.

## Pros and Cons of the Options

### Two literal label lists (chosen)

* Good, because it matches the order, fails closed and needs no variable.
* Bad, because moving to another runner means editing the workflows.

### Keep the hosted fallback

* Good, because nothing queues.
* Bad, because it contradicts the order: a job can land on a hosted runner.

### A repository variable with no fallback

* Good, because the runner can move without a commit.
* Bad, because an unset or edited variable is invisible in review and the guard cannot check its value.

## More Information

* Guard: `internal/release/workflows_selfhosted_test.go`.
* Related: [docs/branch-protection.md](../branch-protection.md), [docs/release.md](../release.md).
