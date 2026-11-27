---
status: accepted
date: 2026-11-27
decision-makers: Randy Bias (mash maintainer)
---

# From the signed-from version a release tag is signed with the mash-release key; the attestations stay

Amends: ADR-0095

## Context and Problem Statement

[ADR 0095](0095-releases-from-the-cutover-version-are-keyless-the-tag-is-pushed-by-the-minting-service-and-attestations-replace-the-signatures.md)
made releases from v0.4.0 keyless. The maintainer had ruled on 2026-11-12 that "no release signing key lives on any
machine" and that releases move to a "keyless standard", and that v0.4.0 ships by exception: "Exception and I'll make as
many more as I want until the proper process in place. It's unproven right now." So the tag of v0.4.0 to v0.6.0 is
annotated and unsigned, and nothing in the release lets a person check, with one command, who made the tag.

On 2026-11-27 the maintainer ruled: "Then we should do a 0.6.1 that's the same as 0.6.0, but signed properly." and "Make
sure that's all baked into the release process, please."

How does the release gate require a signature a person can see and check, without changing releases that were made
before and without dropping the build attestations?

## Decision Drivers

* A signature a person can check with one git command.
* The build attestations stay, and keep pinning the workflow, the tag, the commit and the repository.
* Tags v0.4.0 to v0.6.0 exist and cannot be re-made; they stay as released.
* The rule is one reviewed line read at the candidate commit, and the gate fails closed on a record it cannot read.
* `release-check publish` never makes a tag, and the tag rulesets stay as they are.

## Considered Options

* Sign from a version recorded in `docs/release-cutover` (`signed-from`).
* Sign every release from v0.4.0 retroactively.
* Make the tag in `release-check publish` with a key on the maintainer's machine.
* Sign `SHA256SUMS` too.

## Decision Outcome

Chosen option: "Sign from a version recorded in `docs/release-cutover`", because one reviewed line says where the
behaviours meet, and it is the only option that does not need an existing tag to change.

This record amends ADR 0095's rule that a tag is unsigned, for tags at or above signed-from only; everything else in
ADR 0095 stands.

* **The record.** `docs/release-cutover` holds one version line, `v0.4.0` (the cutover, as before), and at most one
  `signed-from` line, `signed-from v0.6.2`, in either order. The gate reads both at the candidate commit X. An absent file means no cutover; a
  file that is present and malformed fails closed.
* **The key.** The tag is signed with an ed25519 SSH key, fingerprint
  `SHA256:AtXzPvoOLeTfX05xeum8jJl6fvlmIB+BIFlun7qhQzA`, public key `ssh-ed25519
  AAAAC3NzaC1lZDI1NTE5AAAAIN+y4LF/JU6z+cNrfeZnwAyjT/j/K3IyrhS5iulw0bTu`, principal `mash-release`, listed in
  `docs/allowed_signers`. The project's release operations hold it on their release host. That choice was made on the
  maintainer's behalf, and the maintainer may overturn it.
* **Who makes and pushes the tag.** Release operations make the annotated tag at X and sign it with that key. The push
  stays with the deploy key, so the rulesets (creation by deploy keys only; update and deletion blocked) are
  unchanged. `release-check publish` never makes the tag: with no tag it refuses.
* **The gate.** For a version at or above signed-from, `release-check` requires the tag to be annotated, to peel to X,
  and to be SSH-signed, and verifies it against `docs/allowed_signers` accepting the principal `mash-release` only. That
  principal must appear on exactly one line, and the key on it must have the fingerprint above, which is also a constant
  in the code: a second key added to the file cannot pass the gate. A
  tag signed by the old release key (principal `randybias@gmail.com`) is refused, even though that key is in the file.
* **The guard.** `release.yml` at or above signed-from (read at the tag's commit, failing closed) checks only that an
  SSH signature is present and that no PGP or X.509 one is. It does not verify the signature or the principal; the runner
  holds no key, and `release-check` verifies it.
* **Between v0.4.0 and signed-from** nothing changes: the tag is annotated and unsigned. **Below v0.4.0** nothing
  changes. Tags v0.4.0 to v0.6.0 stay unsigned for good; v0.6.0 stays as released. v0.6.1 is signed with the mash-release key, but it was gated by the keyless path above: the gate did not require the signature of it. `signed-from v0.6.2` takes effect from the next tag after v0.6.1, so that tag is the first whose signature the gate and the guard require. The record says v0.6.2, not v0.6.1, because that is the honest account: a record of v0.6.1 would claim that the gate checked a signature it did not check.
* **`SHA256SUMS`** stays attested and unsigned, with no `.sig`, so `ci/release.allowed_signers` stays frozen and
  unchanged. The attestations still pin `-R`, `--signer-workflow`, `--source-ref` and `--source-digest`.
* **`docs/allowed_signers`** gets a header that names both keys and which versions each signs, and the new key line.
* **`docs/release.md`** gets the step 4 wording and the lines a person types: `git -c
  gpg.ssh.allowedSignersFile=docs/allowed_signers verify-tag v<version>`, which must report a good signature for
  `mash-release` with the fingerprint above, and the attestation check. Both are needed; neither replaces the other.

### Confirmation

Tests in `scripts/releasecheck` cover the record (both lines, malformed forms failing closed), a tag at or above
signed-from that is unsigned, signed by the old key, or signed by `mash-release`, the versions between the cutover and
signed-from, `publish` making no tag, and the repository's own records (`docs/release-cutover`, the key in
`docs/allowed_signers`, `ci/release.allowed_signers` unchanged). `internal/release` runs the guard against the record
with and without signed-from.

### Consequences

* Good, because a person can check who made the tag with one git command.
* Good, because the attestations still show which workflow, tag, commit and repository built the files.
* Good, because earlier tags and the earlier rules are untouched.
* Bad, because there is one more thing to hold: a signing key on the release host. The maintainer's 2026-11-12 ruling
  had excluded one; this record rules it back in for tags only.
* Bad, because the signature proves that the holder of the key made the tag, not that the gate ran.
* Bad, because the key lives on a host the maintainer does not hold, and that choice was made on the maintainer's behalf
  until the maintainer confirms it.

## Pros and Cons of the Options

### Sign from a version recorded in `docs/release-cutover` (chosen)

* Good, because one reviewed line, no per-release edit, and existing tags keep verifying as they are.
* Bad, because a wrong version in the file changes the mode for later releases; the file is security-critical for
  review.

### Sign every release from v0.4.0 retroactively

* Good, because every release from v0.4.0 would carry a signature.
* Bad, because it is impossible: the tags exist, and a tag never moves.

### Make the tag in `release-check publish` with a key on the maintainer's machine

* Good, because publishing stays one command.
* Bad, because it puts a signing key on a developer machine, which the maintainer ruled out.

### Sign `SHA256SUMS` too

* Good, because the sums would carry a second proof.
* Bad, because the attestations already cover `SHA256SUMS`, and a second key list would have to be kept.

## More Information

The plan: v0.6.1 is the same content as v0.6.0 with a signed tag, made before this enforcement; the record then applies from the next tag.
