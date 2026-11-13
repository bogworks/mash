# Go caches for CI jobs: provisioning the seed

This page is for whoever runs the self-hosted runners. [ADR 0084](decisions/0084-ci-warms-go-caches-from-a-read-only-seed.md)
records the decision. The repository does not build or install the seed; without one, every job builds cold.

## What the jobs do

A job that runs pull request code calls `scripts/ci-go-caches <kind>` before `actions/setup-go`. The script does nothing
unless the runner's own environment sets `MASH_CI_WARM=1`. With it set, the script looks for the seed
`$MASH_GO_SEED/<kind>` (`MASH_GO_SEED` defaults to `/run/go-seed`), copies it into the job's `$RUNNER_TEMP/go-seed-<kind>`,
makes the copy writable, and writes `GOCACHE`, `GOMODCACHE`, `GOLANGCI_LINT_CACHE` and `GOFLAGS=... -modcacherw` to
`$GITHUB_ENV`. Nothing the job writes reaches the seed or another job.

Set `MASH_CI_WARM=1` in the runner's environment (its service environment or `.env`), not in a workflow.

| Kind | Jobs |
|---|---|
| `race` | `test (linux)` in ci.yml |
| `check` | `conformance` in ci.yml, `lint` in lint.yml, `govulncheck` in security.yml |

The release jobs, `reproducible`, `adr-lint`, `publish-check`, `codeql` and `test (macos)` never call it.
`test (macos)` uses the host's shared module cache (it leaves `GOPATH` and `GOMODCACHE` at the host's defaults and keeps only `GOCACHE` and `TMPDIR` job-local), and runs `go mod verify` after `actions/setup-go` so the shared cache is checked against `go.sum` on every run.

## What the seed must look like

```
/run/go-seed/<kind>/go-build/    the Go build cache
/run/go-seed/<kind>/go-mod/      the module cache (with the go1.27.4 toolchain module if the runner's go is older)
/run/go-seed/<kind>/golangci/    optional, golangci-lint's cache
```

* Owned by a user other than the runner user. Directories 0555, files 0444. No symlinks.
* The path to it must be stable too. `MASH_GO_SEED` and every directory above it must not be writable by the runner
  user, so a job cannot rename or replace the seed, or a parent, between the script's checks and its copy. The script
  checks the seed's contents, not its parents: a read-only mount, or a root-owned path, gives that guarantee.
* Nothing in it, at any depth, may be writable by the runner user (mode, group or ACL). Root passes every write test,
  so the runner must not run as root.
* Built only by a job that runs no pull request code (a main-only workflow, on a runner pull requests cannot select),
  trimmed to what `go vet` and `go test -race ./...` (kind `race`) or the lint and vulnerability checks (kind `check`)
  used. Keep it on tmpfs or another fast disk; `cp -a` of it runs once per job.
* Refresh it by building the new seed beside the old one and swapping the directories with `mv` (an atomic rename). The
  script takes no lock, so editing a seed in place can give a running job a torn copy.
* Disk: each concurrent job holds a full copy in its `RUNNER_TEMP`. Measure the seed and size the disk for the runner's
  concurrent jobs.
  Each runner container today has its own 4 GB tmpfs for `_work`, `_temp` and its caches, so a 1 to 1.5 GB copy is
  tight. Size that tmpfs for the seed before setting `MASH_CI_WARM=1`.
* Toolchain: CI sets `GOTOOLCHAIN=go1.27.4`. With a per-job `GOMODCACHE`, the seed must hold that toolchain module, or
  each job downloads it again. A read-only toolchain cache mounted at `RUNNER_TOOL_CACHE` serves setup-go; it is a
  separate thing from this seed, and the two do not overlap.

## What a refusal looks like

The script never fails a job. If the seed is missing, holds a symlink, has content the runner user can write or own, or
cannot be copied, it prints one line and leaves the environment alone:

```
go caches: SHARED (no seed at /run/go-seed/race): the runner's own caches are in use for this job (ADR 0084)
```

`cache: false` stays on every `actions/setup-go` step, so a refused job builds with whatever the runner already has.
`scripts/ci-go-caches` is part of the pull request's checkout, so a pull request can change it: never call it from a job
that has secrets or a write token.
