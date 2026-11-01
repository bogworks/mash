---
status: accepted
date: 2026-10-31
decision-makers: the release maintainer, 2026-10-31
---

# A release's SHA256SUMS is signed with SSHSIG, namespace mash-release, after a maintainer-side rebuild

## Context and Problem Statement

A mash release carried `SHA256SUMS`, but nothing signed it. Anyone who could edit the release's assets could change a
binary and its checksum together, and a downloader had nothing to check them against. The tag is signed, but a tag
does not cover the assets. The release jobs run on a self-hosted runner that pull request jobs also use, so a build made
there could have been tampered with. This is issue [#156](https://github.com/bogworks/mash/issues/156).

## Decision Drivers

- A downloader must be able to tell a real release from a tampered one with a key it already holds.
- A signature must mean "this is what the tagged commit builds to", not "this is what CI produced".
- A release key is guarded by people; fewer keys are easier to guard.
- Publishing is one resumable command ([0001](0001-publishing-is-one-resumable-command.md)); signing must not be skippable.

## Considered Options

- Sign `SHA256SUMS` in CI.
- Rely on GitHub build provenance attestations alone.
- Rebuild on the maintainer's machine, then sign `SHA256SUMS` with SSHSIG there.

## Decision Outcome

Chosen option: "Rebuild on the maintainer's machine, then sign", because only a rebuild outside the shared runner shows
the binaries are what the tagged commit builds to, and the signature then vouches for that.

1. **Sign `SHA256SUMS` with SSHSIG.** `ssh-keygen -Y sign -n mash-release` makes an armored `SHA256SUMS.sig`, attached to
   the release. Verify with `ssh-keygen -Y verify -f ci/release.allowed_signers -I release -n mash-release`.
2. **Namespace `mash-release`.** The namespace is part of what is signed, so a signature this key makes for another
   purpose or project does not verify for mash, and the reverse.
3. **One release key.** The maintainer's release key signs the tag and the sums. `ci/release.allowed_signers` lists it
   (principal `release`) and may list a second key for rotation.
4. **Rebuild before signing.** CI's `SHA256SUMS` is never signed on trust. Inside the `--assets` gate, which `publish`
   always runs and which has no skip flag, every `mash-<os>-<arch>` line must equal a fresh reproducible build of the
   tagged commit. A malformed line, a duplicate name, a listed name that is not attached, or an attached `mash-*` asset
   that is not listed is refused.
5. **Verify against the file at the tag.** The signature is verified with `ci/release.allowed_signers` as it stands at X
   (`git show`), never a file from the home directory, before the upload and again on the draft read back.
6. **Resumable, and publishing needs it.** A draft whose `SHA256SUMS.sig` already verifies keeps it, and the rerun needs
   no key. One that does not verify is refused, not replaced. Publishing re-verifies the draft's signature and refuses
   without it. A published release is never amended.
7. **The key.** `RELEASE_SIGNING_KEY`, the key that signs the tag (default `~/.ssh/id_ed25519.pub`, signing through
   ssh-agent). There is no second flag or variable. The key is used only by `ssh-keygen`, and only when a draft has no
   verifying signature. `RELEASE_SIGNERS` (the maintainer's file for the tag) is not used for the sums, which are
   verified against the committed `ci/release.allowed_signers` that downloaders use.

### Consequences

- Good, because a tampered build on the shared runner can't be signed: the rebuild refuses it.
- Good, because a downloader verifies with a committed file, not with anything GitHub serves alongside the asset.
- Bad, because a release needs the maintainer's key on the maintainer's machine, as the tag signing already does.
- Neutral: a release made before this has no `SHA256SUMS.sig`; older releases are not re-judged.
- Rotating the key means a release signed by the old key that adds the new one to `ci/release.allowed_signers`.

### Confirmation

`scripts/releasecheck/sums_test.go` covers signing and verifying, a tampered sums file, a wrong namespace, an unlisted
key, a missing signature at publish and a digest mismatch (nothing is signed).

## Pros and Cons of the Options

### Sign `SHA256SUMS` in CI

- Bad, because the key would live in CI, and a signature made beside pull request jobs proves nothing about the build.

### Build provenance attestations alone

- Good, because they record which workflow built which file.
- Bad, because they don't show the build on a shared runner was clean. They complement this decision and don't replace
  it.

## More Information

Closes [#156](https://github.com/bogworks/mash/issues/156).
