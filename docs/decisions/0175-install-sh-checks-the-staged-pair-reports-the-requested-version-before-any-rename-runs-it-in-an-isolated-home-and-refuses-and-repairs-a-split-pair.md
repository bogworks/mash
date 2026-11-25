---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer)
---

# install.sh checks the staged pair reports the requested version before any rename, runs it in an isolated home, and refuses and repairs a split pair

## Context and Problem Statement

[ADR 0150](0150-releases-carry-install-and-local-nats-setup-scripts-as-attested-assets.md) has `install.sh` accept the release binaries once they match `SHA256SUMS` (and, with a new enough `gh`, the attestation). [ADR 0171](0171-install-sh-pins-the-attestation-to-the-tags-commit-and-installs-nothing-when-it-cannot-read-it.md) pins that attestation to the tag's commit. Neither says what the installer does when the two programs it is about to install are not one release: a `mash` and a `mashctl` of different versions (a split pair, #1353), whether from a bad asset or a hard kill between the two renames. Running the programs to find out has a cost of its own: since 0.5.6 both refuse every command, `version` included, when the user's configuration folder holds 0.5.5 keys ([ADR 0170](0170-the-0-5-5-migration-script-and-the-refusal-of-a-0-5-5-configuration-folder-ship-again.md)), so a check that runs under the user's environment fails for a reason that has nothing to do with the download.

## Decision Drivers

* The installer must leave both programs the requested release, or change nothing.
* A user's configuration or `MASH_*` variables must not decide whether an install is accepted or reported.
* The installer must never run the programs it replaces: they are the user's, and may be old or broken.

## Considered Options

* Trust the checksums and attestation alone, and run nothing.
* Run the downloaded programs' `version` under the user's environment.
* Run the downloaded programs' `version` in an isolated environment, and require the requested version (chosen).

## Decision Outcome

Chosen: run them isolated and require the version. This is one more condition on accepting release binaries, added to ADR 0150's, which stands.

* After the checksums (and attestation) pass, `install.sh` stages both new programs in the install folder. Before either rename, each staged program must run `version` and exit 0, report its own name (`mash` for the first, `mashctl` for the second), and report the requested release's version (`mash v0.5.6 (3f9a41c)` and `mashctl 0.5.6` both report `0.5.6`). If either does not run, or reports another version, it prints `error: ... nothing was installed` and exits 1; nothing in the folder has changed.
* Every `version` run the installer makes, before the renames and in the final `Installed <line> in <folder>` report, is isolated: `env -i` with an empty temporary `HOME` and `XDG_*` folders, `PATH` only, and no `MASH_*` variable. The user's configuration, 0.5.5 keys included, cannot make a program refuse. If a program reports nothing, the installer exits 1 with an error, as before.
* A split pair is never accepted: two programs reporting different versions are refused before any rename. A split pair already on disk (one program missing, from a kill between the renames) is repaired by running the installer again, which replaces both and says `only one of mash and mashctl is in <folder>: installing <tag> of both`.
* The installer never runs the old `mash` or `mashctl` in the folder; it only copies the old `mash` aside to undo the first rename if the second fails.

### Confirmation

`scripts/install_sh_test.go` checks that a pair reporting another version, or exiting non-zero, installs nothing; that a missing half is repaired; and that fake programs which refuse under a 0.5.5 `config.json` (`url`, `tls_ca`) in the user's `HOME` or `XDG_CONFIG_HOME` still install and are reported with their `Installed` lines.

### Consequences

* Good, because a split pair is never installed, and a repair needs only a rerun.
* Good, because a user with an old configuration folder can install the release that will tell them how to migrate it.
* Bad, because a release whose `version` output changes shape stops the installer; the version line is part of the compatibility contract ([ADR 0034](0034-text-output-and-plugin-commands-are-a-compatibility-contract.md)).

## Pros and Cons of the Options

### Trust the checksums and attestation alone

* Good, because nothing downloaded runs before it is installed.
* Bad, because a mismatched pair that is correctly signed (or a gap between the renames) goes unseen.

### Run `version` under the user's environment

* Good, because it is the simplest check.
* Bad, because the user's configuration can make it refuse, for a reason unrelated to the download.

## More Information

Issue #1353; pull request #1515 and its review. This extends ADR 0150 and ADR 0171; it supersedes neither.
