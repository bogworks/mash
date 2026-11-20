---
status: accepted
date: 2026-11-20
decision-makers: Randy Bias (mash maintainer)
---

# Releases build mashctl beside mash with the same supply-chain checks

## Context and Problem Statement

A release carries four `mash-<os>-<arch>` binaries. `mashctl`, the tool that sets up servers and administers endpoints,
is not a release asset: a person who wants it must have Go and run `go install`, which compiles on their machine, is not
the attested build, and prints no commit. The release page therefore says "install mashctl with go install" and the
supply chain that protects `mash` (reproducible build, SBOM, attestation, checksums) does not reach the program that
holds the most authority over a deployment.

[ADR 0051](0051-releases-carry-an-sbom-per-binary-and-slsa-provenance.md) says a release carries an SBOM per binary and
names four. What does a release build and attest now, and how much of the pipeline changes?

## Decision Drivers

* `mashctl` creates accounts, keys and credentials: a person who runs it needs the same proof of origin as for `mash`.
* One recipe, one gate: no second pipeline to keep in step, and no asset that the gate cannot check.
* A release missing any `mashctl` asset must be refused by the same checks that refuse a missing `mash` asset.
* The plugin launcher runs `mash` only. Nothing about `mashctl` may change what the launcher downloads or pins.
* `install.sh` is a separate decision and stays as it is.

## Considered Options

* Keep `go install` as the only way to get `mashctl`.
* Build `mashctl` in the release workflow but outside the gate (attach it, check nothing).
* Build `mashctl` beside `mash` through the same recipe and the same checks (chosen).

## Decision Outcome

Chosen: build `mashctl` beside `mash`, through the same recipe, SBOM, attestation, checksums and gate.

1. **Eight binaries.** For each of linux and darwin on amd64 and arm64, a release carries `mash-<os>-<arch>` and
   `mashctl-<os>-<arch>`.
2. **One recipe.** `scripts/buildrelease` takes `-cmd mash|mashctl` (default `mash`) and `scripts/build-twice` takes the
   program as an optional last argument (default `mash`); the caller names the output. Both programs get the same
   flags: `-trimpath`, the pinned toolchain, no cgo, and the version stamped into `internal/endpoint`, which both link.
   Each is built twice in fresh clones and the two hashes must be equal.
3. **Eight SBOMs.** Each binary has a CycloneDX 1.5 SBOM, `<program>-<os>-<arch>.cdx.json`, attested with
   `actions/attest-sbom` with that binary as its subject. This amends ADR 0051, which names four.
4. **One attestation bundle.** The `attest` job attests the eight binaries and `SHA256SUMS` in one step, and the bundle
   is still kept as `mash.intoto.jsonl`.
5. **Checksums.** `SHA256SUMS` lists the eight binaries, `quickstart-synadia.sh`, then the eight SBOMs (and, below the
   cutover, `mash.intoto.jsonl`), in that order.
6. **The gate.** `release-check --assets` and `publish` require the full list of assets, rebuild all eight binaries and
   compare each, check every SBOM and its attestation, and verify the bundle for each binary. A release missing a
   `mashctl` binary, SBOM or checksum line is refused. The list lives in one place, `release.Binaries()`.
7. **The workflow.** The existing four-target matrix builds both programs in each cell and uploads one artifact per
   binary (`mash-<os>-<arch>`, `mashctl-<os>-<arch>`; SBOMs `sbom-<os>-<arch>` and `sbom-mashctl-<os>-<arch>`). Downloads
   by pattern name both families (`mash-*` does not match `mashctl-*`).
8. **Unchanged.** The plugin launcher and `plugin/scripts/pin-plugin` pin `mash` only. `install.sh` and the quick
   start text are not changed by this decision; they change when a release that carries `mashctl` is published.

### Consequences

* Good, because `mashctl` gets the same reproducibility, SBOM and attestation as `mash`, and a user can verify it the
  same way.
* Good, because one asset list serves the gate, the checksums check and the workflow tests, so they cannot disagree.
* Bad, because a release has nineteen assets instead of eleven (besides the signature file), and the build, SBOM and attestation jobs run longer.
* Bad, because a failure in the `mashctl` build blocks the release of `mash`: the two are one release.
* Neutral: `mashctl` has no `commit` variable, so `mashctl version` prints the version only.

### Confirmation

Tests in `internal/release` and `scripts/releasecheck` require every `mashctl` asset, SBOM and checksum line, and refuse
a release missing any. A workflow test requires `mashctl` in the build, sbom, attest, attest-sbom, sums, draft and verify
jobs. The workflow itself runs only on a tag or a dispatch, so its first full run is the dry run on `main`
(`workflow_dispatch`) before the release that carries `mashctl`.

## Pros and Cons of the Options

### Keep `go install` as the only way to get `mashctl`

* Good, because nothing changes.
* Bad, because the program with the most authority has the weakest provenance, and every user needs a Go toolchain.

### Build `mashctl` in the release workflow but outside the gate

* Good, because it is a smaller change to the gate.
* Bad, because an asset nobody checks is a gap in the supply chain: it could be replaced after the gate passed.

### Build `mashctl` beside `mash` through the same recipe and checks

* Good, because one recipe and one gate cover both, and a missing asset is refused.
* Bad, because every release job does twice the work and a release is held up by either program's build.

## More Information

* Amends [ADR 0051](0051-releases-carry-an-sbom-per-binary-and-slsa-provenance.md): the SBOM per binary is now eight.
* Verification steps: [docs/release.md](../release.md#verify-a-release).
