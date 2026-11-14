---
status: accepted
date: 2026-11-14
decision-makers: Randy Bias (mash maintainer)
---

# An advisory shadow test job runs on ephemeral runners under a fourth label list

Amends: ADR-0079

## Context and Problem Statement

[ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md) and
[ADR 0086](0086-the-queue-s-gate-jobs-run-on-small-dedicated-gate-runners.md) put every job on one of three literal label
lists. x-ops3 is piloting ephemeral one-job runners: a runner group `mash-ephemeral` with 6 runners that carry the label
`rh-linux-eph`, each taking one job and then being replaced (bogworks/cicd, `docs/pieces/ephemeral-runners.md`). Before
anything depends on them, the pilot needs the Linux test job to run on them next to the same job on `rh-linux`, so the two
can be compared on the same commits. How does mash give the pilot that run without letting it gate a merge or a release,
and without leaving self-hosted runners?

## Decision Drivers

* The comparison needs the same steps on the same events, so the two runs must not drift apart.
* A pilot must not gate anything: a missing or broken ephemeral runner must not block a merge or a release.
* Every job stays on a self-hosted runner picked by a literal list, with no fallback and no GitHub-hosted label.
* The existing `rh-linux` pool must not take the shadow job, and the ephemeral runners must not take any other job.

## Considered Options

* A fourth literal list, `[self-hosted, rh-linux-eph]`, carried only by the ephemeral runners, for an advisory shadow job
* Add the `rh-linux` label to the ephemeral runners
* Run the shadow job on a GitHub-hosted runner, or with a fallback to `rh-linux`

## Decision Outcome

Chosen option: "A fourth literal list". ci.yml gains the job `test-eph`, named exactly `test (linux, eph)`, on
`[self-hosted, rh-linux-eph]`. It has the same `needs`, `if` and timeout as `test`, and runs the same steps: they move
into the local composite action `.github/actions/go-test`, and both jobs check out and then call it, so the two cannot
drift. The job is advisory like `test (macos)` ([ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md)):
it is not in pr-check's `needs`, not in `release.RequiredChecks`, not in `.mergify.yml` and not in the required list of
[docs/branch-protection.md](../branch-protection.md), so it gates no merge and no release. It has no fallback and no
hosted label: with no ephemeral runner online it waits. This amends ADR 0079 by adding the fourth list; its other rules (a
literal list, no fallback, no hosted label, no host names) hold unchanged.

The guard test (`internal/release/workflows_selfhosted_test.go`) allows the fourth list, and `TestEphJobUsesEphemeralRunners`
pins it: exactly `ci.yml/test-eph` uses `rh-linux-eph`, and no other job does.

### Confirmation

`TestEphJobUsesEphemeralRunners` and the self-hosted guard run in CI; either fails if another job uses `rh-linux-eph` or
`test-eph` leaves it. `TestCI_ephShadowJobGatesNothing` fails if `test-eph` enters pr-check's `needs` or verdict,
`release.RequiredChecks` or `.mergify.yml`. `TestWorkflows_goCacheStepInTheSharedTestAction` fails if either test job stops
using the shared action.

### Consequences

* Good, because the pilot gets a like-for-like Linux run to compare with `rh-linux`, with no change to what gates a merge.
* Good, because the steps live in one place, and the two jobs cannot drift.
* Good, because the guard still fails on any other list, fallback or hosted label.
* Bad, because the Linux test suite runs twice on a code change, which costs runner time on the pilot pool.
* Bad, because a composite action cannot read repository variables, so each job passes `MASH_FORBIDDEN_WORDS` in as an input.
* Bad, because the check is advisory: a failure on the ephemeral runners is visible but stops nothing, until a later
  decision promotes the job or removes it.

## Pros and Cons of the Options

### A fourth literal list (chosen)

* Good, because it separates the pilot from every other pool by label alone and needs no variable.
* Bad, because promoting the job, or moving it to another runner, means editing the workflows and the guard.

### Add the `rh-linux` label to the ephemeral runners

* Good, because no list is added.
* Bad, because other jobs could land on the pilot runners, and the shadow job could land on the regular pool, so the
  comparison would mean nothing.

### Run the shadow job on a GitHub-hosted runner, or with a fallback to `rh-linux`

* Good, because the job would always run.
* Bad, because it contradicts ADR 0079: a job could land on a runner this project does not control, and a fallback would
  hide which runner produced a result.

## More Information

* Amends [ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md). Related:
  [ADR 0086](0086-the-queue-s-gate-jobs-run-on-small-dedicated-gate-runners.md).
* Guard: `internal/release/workflows_selfhosted_test.go`. Shared steps: `.github/actions/go-test/action.yml`.
