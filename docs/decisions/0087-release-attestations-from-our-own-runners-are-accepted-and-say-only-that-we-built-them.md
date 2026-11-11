---
status: accepted
date: 2026-11-11
decision-makers: Randy Bias (mash maintainer)
---

# Release attestations from our own runners are accepted and say only that we built them

Amends: ADR-0079

## Context and Problem Statement

[ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md) moved every job, the release jobs included, onto
self-hosted runners, and left one item open (B1): `--deny-self-hosted-runners` stays in `scripts/releasecheck/supply.go`,
`.github/workflows/release.yml` and `docs/release.md`, and it refuses every attestation made on a self-hosted runner. With
no dedicated release runner, no release could pass verification. Does verification keep refusing our own runners, or
does it accept them and say what the attestation does and does not claim?

## Decision Drivers

* A release must be possible and must verify.
* The attestation must not claim more than is true.
* The signer workflow, the source repository and the source ref must stay pinned.

## Considered Options

* Keep `--deny-self-hosted-runners` and refuse
* A hosted or trusted builder for releases
* Accept our self-hosted runners and state the claims

## Decision Outcome

Chosen option: "Accept our self-hosted runners and state the claims". The maintainer ruled this (D194, option a): "for now
we attest that we did it and people can decide if they trust us." `--deny-self-hosted-runners` is removed from
`release-check`, the release workflow's dry-run verification and the consumer commands in `docs/release.md`. Verification
still pins the signer workflow (`<repo>/.github/workflows/release.yml`), the source repository and the source ref, and
still checks the provenance's workflow entry point.

The attestation claims that this release workflow of `bogworks/mash`, at this tag, on a runner the maintainers operate,
built and attested these exact bytes, recorded in Sigstore. It does not claim the runner was uncompromised or ephemeral:
the runner also runs pull request jobs, so a consumer decides whether to trust the maintainers' runners. The level is SLSA
Build L2 only. A trusted-build mechanism is on the backlog as card 143. This closes the open attestation question of ADR
0079 (item B1) and amends it.

### Confirmation

The `scripts/releasecheck` tests run a fake `gh` that fails when `--deny-self-hosted-runners` is passed and when the
signer workflow is not exactly `<repo>/.github/workflows/release.yml`;
`TestAssets_verifiesSBOMAndBuildProvenanceOfEveryBinary` covers both. `internal/release/workflows_test.go` asserts the flag
is absent from the dry-run verification and that all three verifies carry the signer and the source ref.

### Consequences

* Good, because releases made on our runners verify.
* Good, because the pinned signer, repository and ref still refuse an attestation from any other workflow.
* Bad, because the signature comes from a runner that also runs pull request jobs: a compromise of it could produce a
  genuine attestation for bad bytes, and the attestation cannot show otherwise.
* Bad, because consumers must decide themselves whether to trust the maintainers' runners.

## Pros and Cons of the Options

### Keep `--deny-self-hosted-runners` and refuse

* Good, because it claims the most.
* Bad, because no release can verify, so no release can be made.

### A hosted or trusted builder for releases

* Good, because the attestation would then say more about the build environment.
* Bad, because none is available now; card 143 tracks a trusted-build mechanism.

### Accept our self-hosted runners and state the claims (chosen)

* Good, because it is true, and it lets releases go out.
* Bad, because it claims only that we built the bytes.

## More Information

* Amends [ADR 0079](0079-ci-and-release-jobs-run-on-self-hosted-runners-only.md); [ADR 0051](0051-releases-carry-an-sbom-per-binary-and-slsa-provenance.md) introduced the flag.
* [docs/release.md](../release.md), [docs/security/threat-model.md](../security/threat-model.md).
