---
status: accepted
date: 2026-11-20
decision-makers: Randy Bias (mash maintainer)
---

# The release carries a one-time 0.5.5 migration script

## Context and Problem Statement

Mash 0.5.6 renames two keys in `config.json`: `url` becomes `servers` and `tls_ca` becomes `ca`
([ADR 0147](0147-the-0-5-6-command-line-and-mcp-surface-is-built-from-a-specification-that-supersedes-contract-revision-26.md)),
and the old keys are no longer read. A person whose machine ran 0.5.5 has a `config.json` with the old keys, so 0.5.6
stops with a message ("holds a mash 0.5.5 configuration") and needs a one-time rewrite of that file. Nothing on the server
needs migrating: 0.5.6 reuses the stores and endpoint records 0.5.5 made. The planner ruled that this rewrite is a script,
and wrote it into the specification (rule "A folder left by mash 0.5.5 is named for what it is", and a setup entry for the
script). [ADR 0145](0145-an-endpoint-address-is-owner-group-instance-and-0-5-5-is-a-hard-cut-with-no-migration-in-mash.md)
made 0.5.5 a hard cut with no migration in mash: a migration is never a flag or a verb of `mash` or `mashctl`. Where does
the script come from, and how does a person know it is the project's?

[ADR 0150](0150-releases-carry-install-and-local-nats-setup-scripts-as-attested-assets.md) already makes the release carry
two scripts with proof of origin. A third script that rewrites a file in the person's configuration folder deserves the
same proof as the other two.

## Decision Drivers

* A script that people pipe into a shell runs with their authority before they have read it: it needs the proof the
  other scripts have.
* The command in the specification, `releases/latest/download/mash-migrate-0.5.5.sh`, must serve the project's script and
  nothing else.
* No second mechanism: one pipeline, one gate, one order of checksums.
* No migration code in the `mash` or `mashctl` binaries (ADR 0145).

## Considered Options

* Serve the script from the repository's `main` branch.
* Put the rewrite in a `mashctl` verb or a flag.
* Carry the script as a third attested release asset, the same way as the other two (chosen).

## Decision Outcome

Chosen: the release carries `scripts/mash-migrate-0.5.5.sh` as the asset `mash-migrate-0.5.5.sh`, by exactly the pipeline
of ADR 0150, with no second mechanism. This extends ADR 0150's list of two scripts to three; its other points stand.

1. **A third script.** A release carries the eight binaries, the three scripts, the eight SBOMs, `SHA256SUMS` and
   `mash.intoto.jsonl`. The script has no SBOM. `release.ScriptNames()` lists `install.sh`, `mash-local-nats-setup.sh`,
   `mash-migrate-0.5.5.sh`, in that order.
2. **The repository's own file.** The `sums` job copies the tagged `scripts/mash-migrate-0.5.5.sh` into `dist` unchanged,
   mode 0755, and the `draft` job and `release-check` compare it with the repository at the release commit, byte for byte.
3. **Checksums and attestation.** `SHA256SUMS` lists it after `mash-local-nats-setup.sh` and before the SBOMs. It is a
   subject of the provenance attestation, and the `dry-run-verify` job and `release-check` verify it like the other two.
4. **The gate.** `release-check --assets` and `publish` refuse a release that lacks it, its `SHA256SUMS` line or its
   attestation, or whose copy differs from the repository's.
5. **What it is for.** It is run once, by the person or by whatever manages the machine. The script itself, its test and
   its behavior are the specification's, not this decision's; this decision is only how it reaches people.

### Consequences

* Good, because the command a person pastes fetches a file the release gate has checked against the repository, and
  a person can verify it before running it with the same commands as for the other scripts.
* Good, because there is no migration code in `mash` or `mashctl`, and no second release mechanism to maintain.
* Bad, because a release has twenty-one assets (eight binaries, three scripts, eight SBOMs, `SHA256SUMS` and the
  provenance bundle), besides the signature file.
* Bad, because a script specific to one past version stays in every later release until a release drops it, and dropping
  it is a new decision that amends this one.
* Neutral: a fix to the script reaches people only with the next release.

### Confirmation

Tests in `internal/release` and `scripts/releasecheck` require the third script, its `SHA256SUMS` line and its
attestation, and refuse a release missing any or carrying a script that is not the repository's. The workflow tests
require it in the sums, attest, draft and dry-run-verify jobs and in the `sha256sum` order, which must match
`release.ExpectedSums`.

## Pros and Cons of the Options

### Serve the script from the repository's `main` branch

* Good, because nothing changes in the release.
* Bad, because the script a person fetches is whatever `main` holds that minute, not what the gate checked.

### Put the rewrite in a `mashctl` verb or a flag

* Good, because there is one thing to download.
* Bad, because it puts migration code in the binary for ever, against ADR 0145, for a step each person takes once.

### Carry the script as a third attested release asset

* Good, because it gets the same proof as the binaries and the other scripts, with no new mechanism.
* Bad, because the gate, the checks and the workflow carry one more asset.

## More Information

* Extends [ADR 0150](0150-releases-carry-install-and-local-nats-setup-scripts-as-attested-assets.md): the list of scripts. ADR 0150 is not superseded, and keeps its status, as 0150 left 0148's.
* Follows [ADR 0145](0145-an-endpoint-address-is-owner-group-instance-and-0-5-5-is-a-hard-cut-with-no-migration-in-mash.md): no migration in mash.
* The reader's account: [docs/install.md](../install.md#if-you-ran-mash-055).
