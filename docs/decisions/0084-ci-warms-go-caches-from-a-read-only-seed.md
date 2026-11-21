---
status: accepted
date: 2026-11-11
decision-makers: Randy Bias (mash maintainer), ruling relayed 2026-11-10
---

# CI warms its Go caches from a read-only seed copied per job

## Context and Problem Statement

ADR 0079 moved every job to self-hosted runners, and `actions/setup-go` now has `cache: false` everywhere because
restoring and saving the GitHub cache cost minutes against about two minutes of real work. Every job therefore builds
cold. A runner could instead keep one shared Go cache for all jobs, but pull request code runs on these runners (a fork
pull request too, once a person approves the run), and the Go build cache is keyed by an action ID that the code can
compute. A job that writes a shared cache can plant an object that a later job, a merge queue run included, reads as its
own. How do jobs get warm caches without sharing a writable one?

## Decision Drivers

* No job that runs pull request code may write anything a later job reads.
* A cold build is acceptable; a failed job because of the cache is not.
* The runner hosts are operated outside this repository, so the change must work with no seed and wait for one.
* The `cache: false` guard (`workflows_setupgo_test.go`) must keep holding.

## Considered Options

* A read-only seed, copied into each job's temp directory (chosen).
* Cold per-job caches (the state after `cache: false`).
* One shared writable cache on the runner.
* An overlay mount over a seed.
* `actions/setup-go` with `cache: true`.

## Decision Outcome

Chosen: a read-only seed copied per job. `scripts/ci-go-caches <kind>` runs as a step before `actions/setup-go` in the
jobs `test (linux)` (kind `race`), `conformance`, `lint` and `govulncheck` (kind `check`), as exactly
`if [ -f scripts/ci-go-caches ]; then scripts/ci-go-caches <kind>; fi`. The `-f` guard lets a branch that predates the
script run.

The script does nothing unless the runner's environment sets `MASH_CI_WARM=1`. Then it uses the seed
`$MASH_GO_SEED/<kind>` (default `/run/go-seed/<kind>`) and refuses it, printing `go caches: SHARED (<reason>)` and leaving
the environment untouched, if the seed is missing `go-build/` or `go-mod/`, holds a symlink, has any content the runner
user can write at any depth, or has any content the runner user owns. Otherwise it copies the seed to
`$RUNNER_TEMP/go-seed-<kind>`, makes the copy writable and writes `GOCACHE`, `GOMODCACHE`, `GOLANGCI_LINT_CACHE` and
`GOFLAGS` with `-modcacherw` to `$GITHUB_ENV`. It never fails the job. `cache: false` stays on every setup-go step.

The seed is provisioned by operations, not by this change: its owner, location, refresh and the job that builds it are
outside this repository. Without a seed the script is a no-op that logs `SHARED`, and the jobs build cold as they do now.
[docs/ci-go-caches.md](../ci-go-caches.md) states what the seed must look like.

These jobs never call the script: `release.yml`, `reproducible` and `publish-check` (a release is built from them, so
they stay cold), `adr-lint` (it runs under `pull_request_target`), `codeql`, `adr-numbers` and `test (macos)` (a
different OS; the script needs GNU `find`, and that job keeps job-local caches).

The design and the script are adopted from the maintainer's sibling project, `randybias/rangehand`: its `scripts/ci-go-caches` and its gap record G305 (`docs/plan/gaps/G305.md`). <!-- adrlint:allow vocabulary -->
mash renames the environment variables and the kinds and drops what only that project's hosts need.

### The runners (ruling, 2026-11-11)

The maintainer's operations owner ruled on cache isolation between jobs (mail #147, 2026-11-11):

* **Now.** Pull request jobs keep a persistent per-runner tmpfs. Pull requests from this repository's own branches all
  come from the maintainer's own automated sessions, under one user and one host credential, so a cache that persists
  between them adds no new trust boundary. Approving a fork's pull request admits its code to the runner pool, and the
  approver must treat it that way.
* **Release.** Release jobs run only on the separate, ephemeral `rh-linux-release` runners, never on the pull request
  pool.
* **Later.** Once operations builds the read-only seed, the pull request runners become ephemeral too, and this script
  restores the seed in each job.

### Consequences

* Good, because with a seed, a job's writes cannot reach another job.
* Bad, because without a seed (refused or missing) a job uses the runner's own caches, which persist between that
  runner's jobs. That is the runner's behaviour today, and the runner ruling below accepts it until the seed exists.
* Good, because the change ships and tests without the seed.
* Bad, because there is no speed gain until operations builds a seed, and each job logs `SHARED` until then.
* Bad, because the script is part of the pull request's own checkout, so a pull request can edit it. Never call it from
  a job that has secrets or a write token.
* Bad, because each concurrent job holds a full copy of the seed in `RUNNER_TEMP`: disk per concurrent copy.
* Bad, because the script takes no lock. A seed refreshed in place can give a running job a torn copy, so the seed must be
  swapped atomically (build the new directory beside it and rename).

### Confirmation

`internal/release/ci_go_caches_test.go`: `TestCIGoCaches_copiesAReadOnlySeed`, `TestCIGoCaches_fallsBackToTheSharedCaches`
(each refusal leaves the environment empty and names its reason; a runner that is not warm prints nothing) and
`TestWorkflows_goCacheStepsCallCIGoCaches` (the four jobs run the pinned line before setup-go; no other job runs it).
`workflows_setupgo_test.go` keeps `cache: false` literal.

## Pros and Cons of the Options

### Read-only seed copied per job (chosen)

* Good, because it isolates jobs and keeps most of the warm speed.
* Bad, because it needs a seed that operations provides.

### Cold per-job caches

* Good, because it needs nothing.
* Bad, because the sibling project measured a cold `go vet` at about thirteen times the warm time.

### One shared writable cache

* Good, because it is fastest.
* Bad, because pull request code can poison it.

### Overlay mount

* Good, because the copy is free.
* Bad, because the runner has no mount rights.

### `setup-go` with `cache: true`

* Good, because it needs no host work.
* Bad, because it is the minutes of restore and save that ADR 0079's follow-up removed.

## More Information

* Script: `scripts/ci-go-caches`. Operator page: [docs/ci-go-caches.md](../ci-go-caches.md).
* Related: [ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md).
