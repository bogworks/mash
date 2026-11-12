---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# Releases from the cutover version are keyless: the tag is pushed by the minting service and attestations replace the signatures

Supersedes: ADR-0042
Amends: ADR-0001, ADR-0087

## Context and Problem Statement

[ADR 0042](0042-sign-sha256sums-with-sshsig.md) has the maintainer's SSH key sign the release tag and `SHA256SUMS`, on the
maintainer's machine. [ADR 0001](0001-publishing-is-one-resumable-command.md) makes `release-check publish` create that
signed tag. The maintainer ruled on 2026-11-12 that no release signing key lives on any machine, and that releases move to a
keyless standard (a minting service pushes the tag, and attestations carry the proof). The standard is not finished, so the
maintainer ruled that v0.4.0 ships by exception: "Exception and I'll make as many more as I want until the proper process in
place. It's unproven right now."

How does mash's release gate treat a release whose tag is unsigned and whose `SHA256SUMS` has no signature, without weakening
releases that were made before?

## Decision Drivers

* No release signing key on a machine.
* Releases already published (v0.3.x and older) keep verifying exactly as they do.
* Every verification pins the workflow, the tag, the commit and the repository, so an attestation from another source is
  refused.
* The change is reviewed through the queue, and the gate reads the rule at the candidate commit, not from a maintainer's
  environment.

## Considered Options

* A cutover version recorded once at the candidate commit; below it nothing changes, at or above it the release is keyless.
* A per-version exceptions file.
* Keep SSH signing and add a second key.

## Decision Outcome

Chosen option: "A cutover version recorded once", because one reviewed line says where the two behaviours meet and needs no
per-release edit.

* **The record.** `docs/release-cutover` holds one version line (`v0.4.0`). The gate reads it at the candidate commit X.
  A file that is absent means no cutover (behaviour as before); a file that is present and malformed fails closed.
* **Below the cutover** nothing changes: an SSH-signed annotated tag, `SHA256SUMS.sig`, and ADR 0042 as written.
  `docs/allowed_signers` and `ci/release.allowed_signers` stay, frozen, with a header line "used for releases up to v0.3.x".
* **At or above the cutover:**
  * The tag is annotated and peels to X, with no signature. It is pushed with a deploy key by the minting service at the
    maintainer's request, never made by `release-check`: `release-check publish` refuses when no tag exists.
  * No `ssh-keygen -Y sign` and no `SHA256SUMS.sig`; a `.sig` attached to such a release is refused.
  * `SHA256SUMS` is made before the attest job and is one of the attested subjects, and the gate verifies that attestation. It
    no longer lists `mash.intoto.jsonl`, which is the bundle of that same attestation and cannot list itself; the bundle stays
    an attached asset, verified by attestation. Every line is still checked against the gate's own fresh builds of X.
  * Every `gh attestation verify` pins four facts: `-R <repository>`, `--signer-workflow <repository>/.github/workflows/release.yml`,
    `--source-ref refs/tags/v<version>` and `--source-digest <X>`; `plugin/scripts/pin-plugin --attested` does the same, taking X
    from `git ls-remote` on the peeled tag and refusing when it cannot be read.
  * `release.yml`'s guard keeps the shape, annotated and on-`main` checks and, at or above the cutover (read at the tag's
    commit, failing closed), drops the signature check. The `push: tags` trigger stays for this release because the deploy-key
    push starts it.
* **The trust carrier** is the tag rulesets (creation by deploy keys only; update and deletion blocked, no bypass), the
  service's single write deploy key, and the attestations. mash's tests cannot read the rulesets.
* **What this does not prove.** That a checked gate run authorized the release (only that the maintainer asked for the tag and
  the gate was run by hand); that the rulesets held, while others could still edit them; that the published bytes are the ones
  the gate checked (the `--assets` run is the only check, and publication is by hand). The attestations show that
  `release.yml` of this repository, at this tag and commit, built the files on a runner the maintainers operate
  ([ADR 0087](0087-release-attestations-from-our-own-runners-are-accepted-and-say-only-that-we-built-them.md)).
* **Supersession.** This record supersedes ADR 0042 (the mechanism: SSH signing of the tag and of `SHA256SUMS`, and its
  rotation rule, which governed one SSH key replacing another). It amends ADR 0001 (publishing no longer creates the tag
  from the cutover) and ADR 0087 (the attestation now also covers `SHA256SUMS` and pins the source digest).

### Confirmation

Tests in `scripts/releasecheck` cover both sides of the cutover (tag, sums, `publish` creating no tag, the four pinned flags
against a fake `gh`), `internal/release` asserts the workflow's guard and that `SHA256SUMS` is made before and attested by the
attest job, and `plugin/scripts/test-launcher.sh` has a fake `gh` that refuses a foreign workflow, ref or commit.

### Consequences

* Good, because no release signing key is needed from the cutover on.
* Good, because earlier releases verify as before.
* Good, because every attestation check pins the commit as well as the workflow and tag.
* Bad, because an unsigned tag's trust rests on settings (the rulesets, the deploy key) that the repository's tests cannot see.
* Bad, because a keyless release proves less than a gated one; the list above says what.

## Pros and Cons of the Options

### A cutover version recorded once (chosen)

* Good, because one reviewed line and no per-release edit.
* Bad, because a wrong version in the file changes the mode for later releases; the file is security-critical for review.

### A per-version exceptions file

* Good, because each exception is explicit.
* Bad, because every release needs an edit, and the exceptions have no end.

### Keep SSH signing and add a second key

* Good, because nothing in the gate changes.
* Bad, because it keeps a signing key on a machine, which the maintainer ruled out.

## More Information

The plan: the release-standard migration notes (mash v0.4.0 exception, preconditions 1 to 6).
