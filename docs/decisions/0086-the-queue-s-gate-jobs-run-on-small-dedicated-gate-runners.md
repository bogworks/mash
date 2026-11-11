---
status: accepted
date: 2026-11-11
decision-makers: Randy Bias (mash maintainer)
---

# The queue's gate jobs run on small dedicated gate runners

Amends: ADR-0079

## Context and Problem Statement

[ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md) put every job on one of two literal label
lists, `[self-hosted, rh-linux]` or `[self-hosted, rh-macos]`. The gate jobs (`label-reset`, `adr-lint`, and the
`changes` and `pr-check` jobs of ci.yml) are short, but they shared the `rh-linux` pool with the test jobs. They waited
more than 25 minutes behind test jobs, so no pull request could enter the merge queue. How do the gate jobs stay out
from behind the heavy jobs without leaving self-hosted runners?

## Decision Drivers

* The gate checks must start within seconds, because the queue admits nothing without them.
* Every job stays on a self-hosted runner picked by a literal list, with no fallback and no GitHub-hosted label.
* Heavy jobs must not be able to occupy the gate runners.

## Considered Options

* A third literal list, `[self-hosted, rh-gate]`, carried only by small dedicated runners
* Keep one pool and add more `rh-linux` runners
* Run the gate jobs on a GitHub-hosted runner

## Decision Outcome

Chosen option: "A third literal list". The gate jobs run on `[self-hosted, rh-gate]`. That label is carried only by
small dedicated runners (1 CPU, 4 GB each) that carry no `rh-linux` label, so a test job cannot take them. Heavy jobs
stay on `[self-hosted, rh-linux]`, and the macOS job on `[self-hosted, rh-macos]`. This amends ADR 0079 by adding the
third list; its other rules (a literal list, no fallback, no hosted label, no host names) hold unchanged.

The guard test (`internal/release/workflows_selfhosted_test.go`) allows the third list and
`TestGateJobsUseGateRunners` pins it: exactly the four gate jobs use `rh-gate`, and no other job does.

### Consequences

* Good, because the gate checks no longer queue behind test jobs.
* Good, because the guard still fails on any other list, fallback or hosted label.
* Bad, because a gate runner is a single point: with none online the gate checks never start and the queue keeps the
  pull request out (fail closed, as before).

## Pros and Cons of the Options

### A third literal list (chosen)

* Good, because it separates the gate from the heavy pool by label alone and needs no variable.
* Bad, because moving a gate job to another runner means editing the workflows.

### Keep one pool and add more `rh-linux` runners

* Good, because nothing in the workflows changes.
* Bad, because a burst of test jobs can still fill every runner, and the gate waits again.

### Run the gate jobs on a GitHub-hosted runner

* Good, because they would never queue.
* Bad, because it contradicts ADR 0079: a job could land on a runner this project does not control.

## More Information

* Amends [ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md).
* Guard: `internal/release/workflows_selfhosted_test.go`. Related: [docs/branch-protection.md](../branch-protection.md),
  [docs/release.md](../release.md).
