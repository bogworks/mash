---
status: accepted
date: 2026-11-20
decision-makers: Randy Bias (mash maintainer)
---

# Releases carry the install and local NATS setup scripts as attested assets

## Context and Problem Statement

A person who wants mash today downloads a binary, checks it by hand and moves it into place, and gets `mashctl` with
`go install`. The release page asks for a pasted block of shell. Two scripts would shorten that: `install.sh`, which
installs `mash` and `mashctl` from one release, and `mash-local-nats-setup.sh`, which makes a NATS server on the
person's machine. A script that people pipe into a shell is the most trusted file a release has: it runs with the
person's own authority before they have read it. Where do the scripts come from, and how does a person know they are the
project's?

[ADR 0148](0148-releases-build-mashctl-beside-mash-with-the-same-supply-chain-checks.md) lists the release's assets: eight
binaries, eight SBOMs, `SHA256SUMS` and the provenance bundle. The scripts are not among them.

## Decision Drivers

* The address in the command a person pastes, `releases/latest/download/<script>`, must serve the project's script and
  nothing else, and `install.sh` must find the binaries of the very same release.
* The scripts must get the same proof of origin as the binaries: listed in `SHA256SUMS`, subjects of the attestation,
  checked by the release gate.
* A script in a release must be the repository's reviewed file, not a copy someone changed on the way.
* No second pipeline and no asset the gate cannot check.
* A person with no `gh` must still be able to install, with the checksums checked and a plain statement of what was not.

## Considered Options

* Serve the scripts from the repository's `main` branch (`raw.githubusercontent.com`).
* Attach the scripts to the release without listing or attesting them.
* Carry the scripts as release assets, listed in `SHA256SUMS`, attested, and compared with the repository by the gate (chosen).

## Decision Outcome

Chosen: carry the two scripts as release assets, named exactly `install.sh` and `mash-local-nats-setup.sh`, with the
same supply-chain checks as the binaries.

1. **Two more assets.** A release carries the eight binaries, the two scripts, the eight SBOMs, `SHA256SUMS` and
   `mash.intoto.jsonl`. The scripts have no SBOM: they hold no dependencies to list. This amends ADR 0148's list.
2. **The repository's own files.** The `sums` job copies `scripts/install.sh` and `scripts/mash-local-nats-setup.sh`
   from the checkout of the tagged commit into `dist`, unchanged, and hands them to the later jobs as one artifact. The
   `draft` job compares them with `git show <commit>:scripts/<name>` before attaching them, and `release-check` compares
   the attached digests with the same bytes.
3. **Checksums, in an order.** `SHA256SUMS` lists the eight binaries, then `install.sh` and
   `mash-local-nats-setup.sh`, then the eight SBOMs (and, below the cutover, `mash.intoto.jsonl`). The order is written
   once, in `release.ScriptNames()` and `release.ExpectedSums`, and a workflow test requires the `sha256sum` line of the
   `sums` job to agree with it.
4. **Attestation.** The `attest` job lists the two scripts as subjects of the build provenance attestation, beside the
   eight binaries and `SHA256SUMS`. The `dry-run-verify` job and `release-check` verify each script with
   `gh attestation verify <script> --bundle mash.intoto.jsonl` and the held attestation, pinned to the release workflow,
   tag and commit like a binary.
5. **The gate.** `release-check --assets` and `publish` refuse a release that lacks either script, its `SHA256SUMS`
   line or its attestation, and one whose script differs from the repository's at the release commit.
6. **What `install.sh` does.** It takes `mash-<os>-<arch>`, `mashctl-<os>-<arch>` and `SHA256SUMS` from one release
   (`MASH_VERSION` names a tag; without it the script first resolves `releases/latest` to its tag, and a `MASH_RELEASE_BASE`
   mirror needs `MASH_VERSION`), refuses a platform mash is not built for, checks each binary against `SHA256SUMS`, and
   installs into `~/.local/bin` or `MASH_INSTALL_DIR` (a symbolic link to a folder is followed) only when the real folder
   is owned by the user or root and neither it nor any folder above it is writable by group or others (a root-owned
   sticky folder such as `/tmp` above it is allowed). `MASH_REPO` (default `bogworks/mash`) names the repository for the
   download, the tag lookup and the attestation check. Both binaries are staged in the folder before either is renamed
   into place, and the first rename is undone if the second fails. If a `gh` new enough to verify attestations is
   installed it first verifies `SHA256SUMS` against the attestation (`-R` the repository, `--bundle mash.intoto.jsonl`,
   the release workflow as signer, and always `--source-ref refs/tags/<tag>`) and refuses on failure; if not, it says in
   one line that the checksums come from the same download and so trust github.com's TLS. The whole script is one function called on its last
   line, so a truncated download runs nothing.
7. **Agents.** An agent does not pipe a download into a shell. It downloads the script, checks it against `SHA256SUMS`,
   shows the person what it will run and runs it after a yes.

### Consequences

* Good, because the command a person pastes fetches a file the release gate has checked against the repository, and
  `install.sh` and the binaries come from one release, so they cannot disagree.
* Good, because a person can verify a script before running it with the same commands as for a binary.
* Good, because a failed check leaves nothing installed.
* Bad, because a release has twenty assets (eight binaries, two scripts, eight SBOMs, `SHA256SUMS` and the provenance bundle), besides the signature file.
* Bad, because a change to either script reaches people only with the next release.
* Good, because the tag is always known before the download (a pinned `MASH_VERSION`, or `latest` resolved first), so the
  attestation is always tied to that tag: a dispatch dry run of an unreviewed commit, or another release, does not verify.
* Neutral: without a new enough `gh` the install trusts github.com's TLS for the checksums, and says so.

### Confirmation

Tests in `internal/release` and `scripts/releasecheck` require both scripts, their `SHA256SUMS` lines and their
attestations, and refuse a release missing any or carrying a script that is not the repository's. A workflow test requires
the scripts in the sums, attest, draft and dry-run-verify jobs, and the `sha256sum` order to match `release.ExpectedSums`.
`scripts/install_sh_test.go` runs `install.sh` against a fake release. The workflow itself runs only on a tag or a
dispatch, so its first full run is the dry run on `main` before the release that carries the scripts.

## Pros and Cons of the Options

### Serve the scripts from the repository's `main` branch

* Good, because nothing changes in the release.
* Bad, because the script a person fetches is whatever `main` holds that minute, not what a release gate checked, and
  it is not tied to the release whose binaries it installs.

### Attach the scripts to the release without listing or attesting them

* Good, because it is a small change to the workflow.
* Bad, because the most trusted file would carry the least proof, and an asset swapped after the gate would go unnoticed.

### Carry the scripts as release assets, listed, attested and compared with the repository

* Good, because the scripts get the same proof as the binaries, and a changed script is refused by the gate.
* Bad, because the gate, the checks and the workflow have two more assets to carry and to keep in order.

## More Information

* Amends [ADR 0148](0148-releases-build-mashctl-beside-mash-with-the-same-supply-chain-checks.md): the asset list.
* Verification steps: [docs/install.md](../install.md#verify-what-you-download) and [docs/release.md](../release.md#verify-a-release).
