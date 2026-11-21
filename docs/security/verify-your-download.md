# Verify your download

This page shows how to check that a mash release is the one this repository built. It uses release v0.5.6. Change `MASH_VERSION` for another release.

Release tags are annotated but not signed, so there is no tag signature to check. You check the files instead. Each step proves something different.

## What you need

- `curl`, and `sha256sum` (on macOS, `shasum -a 256`).
- The [GitHub CLI](https://cli.github.com/) (`gh`) **2.102.0 or newer**. The checks below pin the release tag with `--source-ref`, which first appeared in gh 2.68.0, and the release workflow with `--signer-workflow`, which gh 2.101.0 and older check only as a prefix, so an attestation from another workflow whose name starts the same way can pass ([GHSA-wjmr-j3rp-mh2g](https://github.com/cli/cli/security/advisories/GHSA-wjmr-j3rp-mh2g), fixed in 2.102.0). Older versions lack `gh attestation` entirely (Debian 13 ships 2.46, Ubuntu 24.04 ships 2.45). Check yours with `gh --version`. Get a current gh from [cli.github.com](https://cli.github.com/) or its [releases page](https://github.com/cli/cli/releases).
- A network connection to GitHub. With the bundle from step 1, the checks need no GitHub login.

`install.sh` runs the same check when it finds gh. With gh 2.48 to 2.67 it stops and installs nothing ([#1153](https://github.com/bogworks/mash/issues/1153)). With gh 2.68 to 2.101 it runs the check but does not refuse the older gh. Upgrade gh, or run `install.sh` with gh off your `PATH`, in which case it checks the checksums only.

## 1. Download

Set the release and your platform. The platform is `linux` or `darwin`, and `amd64` or `arm64`.

```sh
MASH_VERSION=v0.5.6
OS=linux
ARCH=amd64
BASE=https://github.com/bogworks/mash/releases/download/$MASH_VERSION
mkdir mash-verify && cd mash-verify
curl -fsSLO $BASE/mash-$OS-$ARCH -O $BASE/mashctl-$OS-$ARCH -O $BASE/SHA256SUMS -O $BASE/mash.intoto.jsonl
```

`mash.intoto.jsonl` is the attestation bundle.

## 2. Check the hashes

```sh
grep " mash-$OS-$ARCH\$" SHA256SUMS | sha256sum -c -        # macOS: shasum -a 256 -c -
grep " mashctl-$OS-$ARCH\$" SHA256SUMS | sha256sum -c -
```

Each line should end in `OK`. This shows the file matches `SHA256SUMS`. It does not show who made `SHA256SUMS`. Step 3 does that.

## 3. Check the attestation

```sh
gh attestation verify SHA256SUMS -R bogworks/mash --bundle mash.intoto.jsonl \
  --signer-workflow bogworks/mash/.github/workflows/release.yml \
  --source-ref refs/tags/$MASH_VERSION
```

This is the check `install.sh` runs. It shows that the release workflow of `bogworks/mash`, run at the tag `v0.5.6`, made `SHA256SUMS`. Without `--signer-workflow` and `--source-ref`, the check would accept any workflow of the repository on any ref.

The binaries are subjects of the same attestation, so you can check them directly:

```sh
gh attestation verify mash-$OS-$ARCH -R bogworks/mash --bundle mash.intoto.jsonl \
  --signer-workflow bogworks/mash/.github/workflows/release.yml \
  --source-ref refs/tags/$MASH_VERSION
gh attestation verify mashctl-$OS-$ARCH -R bogworks/mash --bundle mash.intoto.jsonl \
  --signer-workflow bogworks/mash/.github/workflows/release.yml \
  --source-ref refs/tags/$MASH_VERSION
```

To also pin the commit, add `--source-digest` with the commit the tag points to:

```sh
git ls-remote https://github.com/bogworks/mash.git "refs/tags/$MASH_VERSION^{}"
```

What this does not show: the release jobs run on self-hosted runners that the maintainers operate. The attestation says the maintainers' workflow built these bytes. You still decide whether to trust the maintainers. The build level is SLSA build level 2, not 3. See [docs/release.md](../release.md) for the reasoning.

## 4. Run the binary

```sh
chmod +x mash-$OS-$ARCH mashctl-$OS-$ARCH
./mash-$OS-$ARCH version
./mashctl-$OS-$ARCH version
```

They print `mash v0.5.6 (X)` and `mashctl v0.5.6 (X)`, where X is the first 7 characters of the release commit. For v0.5.6 that is `dd696e9`.

## Optional: the SBOM

Each binary has a CycloneDX SBOM, for example `mash-linux-amd64.cdx.json`. It lists the Go modules recorded in the binary. It says nothing about vulnerabilities by itself. The SBOM's own attestation is not in `mash.intoto.jsonl`: `gh` fetches it from GitHub, so this step needs `gh auth login` first. The SBOM file itself is listed in `SHA256SUMS`, so step 2's check works for it without a login.

```sh
curl -fsSLO $BASE/mash-$OS-$ARCH.cdx.json
gh attestation verify mash-$OS-$ARCH -R bogworks/mash \
  --signer-workflow bogworks/mash/.github/workflows/release.yml \
  --source-ref refs/tags/$MASH_VERSION --predicate-type https://cyclonedx.org/bom
```

## Optional: rebuild it yourself

The build is meant to be reproducible: `scripts/buildrelease` builds a binary from a clean clone of the release commit with a pinned Go toolchain, and the release workflow builds each binary twice and fails unless the two match. To check a binary yourself, follow "What makes the build reproducible" in [docs/release.md](../release.md#what-makes-the-build-reproducible) and compare the result with the downloaded file.

## The scripts

`install.sh`, `mash-local-nats-setup.sh` and `mash-migrate-0.5.5.sh` are release files too. They are listed in `SHA256SUMS` and covered by the same attestation. Check one the same way before you run it. See [Verify what you download](../install.md#verify-what-you-download).
