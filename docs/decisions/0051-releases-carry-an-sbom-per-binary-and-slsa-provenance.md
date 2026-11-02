---
status: proposed
date: 2026-11-01
decision-makers: the release maintainer, 2026-11-01
---

# Releases carry an SBOM per binary and SLSA provenance from an isolated generator

## Context and Problem Statement

A mash release carries a GitHub build provenance attestation per binary (from 0.3.1) and a maintainer-signed
`SHA256SUMS` ([0042](0042-sign-sha256sums-with-sshsig.md)). Two things are missing. A downloader cannot see what is
inside a binary: which Go modules, at which versions, went into it, so a vulnerability scanner has nothing exact to
read. And the provenance is made by a step of mash's own release workflow, which meets SLSA v1.0 Build Level 2. A
reusable workflow outside the control of the calling workflow can sign provenance with an identity the caller's steps
cannot use, but with the generic generator the digests it attests are supplied by the caller, so that does not by itself
reach Build Level 3.

## Decision Drivers

- A scanner or an auditor must be able to list exactly what is in a shipped binary, not what the source tree might
  contain.
- The provenance should be signed by an identity the release workflow's own steps cannot use, in a fixed format.
- The artifact bytes must keep coming from mash's reproducible recipe, which is built twice in CI and rebuilt and
  compared byte for byte on the maintainer's machine before signing ([0042](0042-sign-sha256sums-with-sshsig.md)).
- Publishing is one resumable command ([0001](0001-publishing-is-one-resumable-command.md)); no new check may be
  skippable, and a verifier must refuse when it cannot verify.
- Say honestly which SLSA level is met, and what is not.

## Considered Options

- SBOM in CycloneDX 1.5 JSON, one per binary, from `cyclonedx-gomod bin`.
- SBOM in SPDX, for example from syft.
- SLSA provenance from `slsa-github-generator`'s generic generator, fed the digests of the binaries mash built.
- SLSA provenance from `slsa-github-generator`'s Go builder, which builds the binaries itself.
- Keep `actions/attest-build-provenance` alone.
- Adopt goreleaser for build, SBOM and provenance.

## Decision Outcome

Chosen options: CycloneDX from `cyclonedx-gomod bin`, plus the generic SLSA generator over mash's own binaries, because
together they describe the real artifact, add a signer identity the release workflow's steps cannot use, and leave the
bytes to the reproducible recipe. mash meets SLSA v1.0 **Build L2**; it does not claim L3.

1. **SBOM format and source.** One CycloneDX 1.5 JSON document per binary, generated from the built binary by
   `cyclonedx-gomod bin`. It reads the module build information embedded in the binary, so it describes what is
   actually inside the artifact, not the source tree. CycloneDX is aimed at security tooling, the generator is
   Go-native, and it records exact module versions and hashes. SPDX stays a possible later addition.
2. **SBOM naming and trust.** `mash-<os>-<arch>.cdx.json`, attached to the release, listed in the maintainer-signed
   `SHA256SUMS`, and attested with `actions/attest-sbom` (predicate type `https://cyclonedx.org/bom`).
3. **SLSA provenance.** The existing `actions/attest-build-provenance` from a workflow in this repository meets SLSA
   v1.0 Build Level 2. In addition the release calls `slsa-framework/slsa-github-generator`'s generic generator
   (`generator_generic_slsa3.yml`, pinned to a release tag, since the generator requires a tag ref) with the sha256 of
   the four binaries that mash's build job produced. It yields `mash.intoto.jsonl`, signed by the generator's isolated
   reusable workflow identity, whose signature mash's steps cannot forge; the digests it attests come from mash's
   workflow (the `hashes` job). A `generator-pin` job fails the run unless the tag still resolves to the recorded commit.
4. **Generic, not the Go builder.** The Go builder would rebuild the binaries itself. The release invariant is that the
   artifact bytes come from mash's recipe (`scripts/build-twice`), built twice in CI and rebuilt and compared on the
   maintainer's machine before signing. A second build by another tool would produce bytes that recipe never vouched
   for.
5. **The level we meet.** mash meets SLSA v1.0 **Build L2**, and does not claim L3. SLSA Build L3 is a property of the
   build platform. With the generic generator the subjects are supplied by mash's own workflow, so a compromised build
   or `hashes` step would still get a genuine generator signature over arbitrary digests. The generator adds a signer
   identity mash's steps cannot use and a fixed provenance format; it does not raise the release to L3. The control
   that goes beyond L2 is the two-clone reproducibility check in CI plus the maintainer's independent rebuild before
   signing `SHA256SUMS`.
6. **The gate.** `release-check --assets` and therefore `publish` verify, before publishing: each binary's build
   provenance attestation, each binary's SBOM attestation, and `mash.intoto.jsonl` with `slsa-verifier`. There is no
   skip flag. A missing `slsa-verifier` is a refusal, not a pass. `gh attestation verify` is pinned with
   `--signer-workflow <repo>/.github/workflows/release.yml`, `--source-ref refs/tags/v<version>` and
   `--deny-self-hosted-runners`, since `-R` alone accepts any workflow of the repository on any ref. `slsa-verifier` is
   pinned with `--builder-id` to the generic generator at `refs/tags/v2.1.0`, and the gate requires the provenance's
   workflow entry point to be `.github/workflows/release.yml`. The SBOM JSON must not repeat a key at any level.
7. **Release assets from 0.3.2.** The four binaries, the four `.cdx.json` files, `mash.intoto.jsonl`,
   `quickstart-synadia.sh`, `SHA256SUMS` and `SHA256SUMS.sig`. Releases before 0.3.2 have no SBOM and no
   `mash.intoto.jsonl`; 0.3.1 has build provenance only.

### Not yet true / residual risks

- The generator is pinned by release tag, not by commit SHA, because it requires a tag ref. A moved tag would change
  what runs. The mitigation is the `generator-pin` job, which `provenance` needs: it resolves the tag through the GitHub
  API and fails unless it equals the commit recorded in the workflow (`SLSA_GENERATOR_SHA`), and `slsa-verifier
  --builder-id` refuses another generator version. This detects a moved tag; it does not stop the generator's own
  maintainers from shipping bad code at the recorded commit.
- Build L3 is not met (see point 5): the subjects the generator attests are produced by mash's workflow.
- The SBOM is generated in a job of mash's own workflow and attested by mash's workflow, so it is Build L2-grade, not
  generator-isolated.
- Trust roots are GitHub's OIDC and Sigstore, and the maintainer's SSH release key.
- The tools we trust: `cyclonedx-gomod` (version-pinned, fetched through the Go module proxy and checked by the Go
  checksum database), `slsa-verifier` (version-pinned, installed by the verifier), the SLSA generator, and the GitHub
  attestation actions (pinned to commits).
- Whoever verifies must install `slsa-verifier` themselves; the gate refuses without it.
- The code to generate, attach, attest and verify these assets lands with the pull request that adds this record; until
  a release is cut with it, no release carries them.

### Consequences

- Good, because a scanner can read exactly which modules are in each binary.
- Good, because the SLSA provenance is signed by an identity the release workflow's steps cannot use (Build L2, not L3).
- Good, because the artifact bytes still come from the reproducible recipe.
- Bad, because the release workflow gains a third-party reusable workflow and two more tools to trust and keep current.
- Bad, because verifying needs `slsa-verifier` as well as `gh`.
- Good, because a `workflow_dispatch` dry run on main exercises the build, SBOM, attestation and provenance jobs and verifies them with `slsa-verifier` and `gh attestation verify`, publishing nothing, so the pipeline can be proven before a tag depends on it.
- Neutral: the release has eleven assets besides the signature file, and the draft job needs all of the attestations.

### Confirmation

The `--assets` gate and its tests in `scripts/releasecheck` cover each missing or failing attestation, a missing
`mash.intoto.jsonl`, a failing or absent `slsa-verifier`, and an unlisted or unattached `.cdx.json`. The release
workflow's `draft` job needs the SBOM, attestation and generator jobs, so a draft exists only once all of them
succeeded. The security review of this record follows; until then it is proposed.

## Pros and Cons of the Options

### CycloneDX 1.5 from `cyclonedx-gomod bin`

- Good, because it reads the binary's own build information: exact module versions and hashes of what shipped.
- Good, because security scanners consume it widely.
- Bad, because it covers Go modules only, and the toolchain appears as a version, not as components.

### SPDX, for example from syft

- Good, because SPDX is an ISO standard and common in compliance work.
- Bad, because syft is a general scanner that infers packages, and for a Go binary it reads the same build information
  with less Go-specific detail. It may be added later beside CycloneDX.

### Generic SLSA generator over mash's binaries

- Good, because the provenance is signed by an isolated identity (whose signature mash's steps cannot forge) and the
  bytes stay mash's.
- Bad, because the generator attests digests it is given, so it does not witness the build itself, and mash stays at
  Build L2: the signature is genuine even over digests a compromised step supplied.

### Go builder of the SLSA generator

- Good, because the build itself is inside the isolated workflow, which is a stronger claim.
- Bad, because it rebuilds, so the released bytes would not be the ones the recipe, the two-clone check and the
  maintainer's rebuild vouch for.

### `actions/attest-build-provenance` alone

- Good, because it is already in place and simple.
- Bad, because the attesting step is in the caller's own workflow (Build L2, as is the chosen option).

### goreleaser

- Good, because it can produce binaries, SBOMs and checksums in one tool.
- Bad, because it replaces the reproducible recipe mash's invariants rest on, for no gain over the chosen tools.

## More Information

How to verify a release: [docs/release.md](../release.md#verify-a-release). Builds on
[0042](0042-sign-sha256sums-with-sshsig.md) and [0001](0001-publishing-is-one-resumable-command.md).
