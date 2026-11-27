---
status: proposed
date: 2026-11-27
decision-makers: operations' order (x-lead, 2026-11-27) to use the runners' pinned copy; taken to the planner by the mash lead
---

# The release SBOM uses the runners' pinned cyclonedx-gomod, checked before use

## Context and Problem Statement

[ADR 0051](0051-releases-carry-an-sbom-per-binary-and-slsa-provenance.md) makes one CycloneDX SBOM per binary with
`cyclonedx-gomod bin`, and lists the tool among the ones we trust as "version-pinned, fetched through the Go module
proxy and checked by the Go checksum database". So far the release workflow's `sbom` job did this with
`go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.9.0` every time a release was made.

Operations now install cyclonedx-gomod v1.9.0 at `/opt/ci-tools/bin/cyclonedx-gomod` on the self-hosted runners
(dev1, dev2, bf2, bf3). They ordered the release to use that copy, with no `go install` and no fallback, and on
self-hosted runners only. That changes how the tool is obtained and who vouches for it, so ADR 0051 is amended.

## Decision Drivers

- The SBOM must still come from the tool and version ADR 0051 chose: cyclonedx-gomod v1.9.0, `bin`, CycloneDX 1.5.
- A release must not depend on reaching the module proxy at release time, and must not build tools on the runner.
- No GitHub-hosted runner and no silent fallback: a runner without the tool fails loudly.
- What the job trusts must be stated, and checked where it can be.

## Considered Options

- (a) The runners' pinned copy at `/opt/ci-tools/bin`, checked before use.
- (b) Keep `go install` through the module proxy and the checksum database (ADR 0051's trust model).

## Decision Outcome

Chosen option: (a), the runners' pinned copy, checked before use, because it is the operations order and it keeps the
tool and version ADR 0051 chose.

This record amends ADR 0051 in one point only: how the release gets cyclonedx-gomod. Everything else in ADR 0051 stays,
including the SBOM's format, naming, attestation and place in `SHA256SUMS`.

Before it makes any SBOM, the `sbom` job checks three things and stops if any fails:
- `/opt/ci-tools/bin/cyclonedx-gomod` exists and can be run;
- its `version` reports exactly `v1.9.0`;
- the file's sha256 is exactly `64440820e5d881ec20bc7b9937fdc9bd67d15ba4637b2e7959a8f31dd12c5b21`. That is the binary
  from the vendor's v1.9.0 linux amd64 release, which operations installed on all four runners. The vendor publishes the
  archive's sha256, `e6d3b3a409b1c84ccef79ad15f9127d80c430a92d5c4f9e621bc2f0f3ee6d423`, which operations checked against
  the vendor's `checksums.txt` and recorded, with the binary's digest, in their runner pins.

If a check fails, the job prints an error naming the path, what it found and what it wanted, and exits 1. It never
falls back to `go install`. The job no longer sets up Go: `cyclonedx-gomod bin` reads the build information embedded
in the binary and needs no Go toolchain.

### Consequences

- Good, because a release no longer fetches or builds a tool at release time, and a missing or wrong tool stops the
  release with a message saying what to fix.
- Good, because the SBOM tool and version are the ones ADR 0051 chose, so the SBOMs keep the same form.
- Bad, because the tool's integrity now rests on operations' install of it on each runner, not on the Go checksum
  database at release time. The
  binary's digest is pinned in this workflow, so a copy that differs on any runner fails the release.
- Bad, because a runner without the tool, or with a different version, fails the release until operations fix it.
- Neutral: changing the tool's version is now a change on the runners and in this workflow together.

### Confirmation

`internal/release/workflows_test.go` (`TestRelease_sbomJobMakesAPinnedCycloneDXSBOMPerBinary`) checks:
- the path, the wanted version and the wanted sha256 in the step's environment;
- the executable check, the digest check, the version check and their order before `bin`;
- that the job has no `go install` and no `setup-go`.

`TestRelease_noJobOrStepCanBypassAFailure` still forbids `|| true` in the step.

## Pros and Cons of the Options

### (a) The runners' pinned copy, checked (chosen)

- Good, because nothing is fetched or built at release time.
- Good, because a missing or wrong tool fails the release at once, with a clear message.
- Bad, because the trust moves from the checksum database to the runners' install.

### (b) go install through the proxy and the checksum database

- Good, because the module's source is checked against the public checksum database on every release.
- Bad, because every release depends on reaching the proxy and builds the tool on the runner.
- Bad, because it is not what operations ordered for the runners.

## More Information

Pull request #1961 makes the change. The tool is installed and kept by operations on the self-hosted runners.
[ADR 0051](0051-releases-carry-an-sbom-per-binary-and-slsa-provenance.md) is the record this amends.
