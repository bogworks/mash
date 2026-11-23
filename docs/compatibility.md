# Compatibility

This page says which versions of the things mash depends on have been tried with mash 0.5.8 (release commit `8e8fe56`). "Tested" means someone ran it on that exact version and kept the record. "Untested" means nobody did, and any "expected to work" there is a guess.

One thing to know first. The upgrade runs below used test builds made from source, called candidates (commits `d958c6f` and `6d13e7d`), not the published 0.5.8 binaries. Both were built before the release commit `8e8fe56`, and no upgrade run was repeated on the published binaries. Candidate `6d13e7d` predates the `--audit-users` change ([#1354](https://github.com/bogworks/mash/pull/1354)) and the `mashctl doctor` change ([#1358](https://github.com/bogworks/mash/pull/1358)). Candidate `d958c6f` predates the `mashctl doctor` change. So no upgrade run covered the released `mashctl doctor`. Where this page says "a 0.5.8 candidate", that is what it means. What was run on the published release is listed under "Run on the published 0.5.8 release".

## What 0.5.8 has been run on

| Component | Tested | Minimum | Expected to work (untested) |
|---|---|---|---|
| Claude Code | Untested. No 0.5.8 run on a live Claude Code session is recorded. The automated checks use a stand-in for Claude Code. The last live runs were on earlier releases, on 2.1.312 and 2.1.316. | 2.1.312 or newer is what the README and install guide ask for. mash does not refuse older versions. | Later versions than 2.1.316. Versions between 2.1.312 and 2.1.316. |
| `mash` and `mashctl` together | Both built from one commit, in the automated checks and in every upgrade run below. | Not stated. The install guide says the plugin and the `mash` binary should be the same release (for example 0.5.8). | Using the same release for both. |
| `mash` 0.5.7 next to a 0.5.8 candidate | Tested on Linux and macOS (see "Upgrading from 0.5.7"): one endpoint ran the 0.5.7 `mash`, another the candidate `mash`, against a server the 0.5.7 setup script made. Mail went both ways. This does not make mixing versions supported. | Not stated. | Nothing beyond that run. |
| `mashctl` 0.5.7 with `mash` 0.5.8, or the other way round | Untested. | Not stated. | Not expected to work; use the same release for both. The upgrade is one-way; see the release notes. |
| 0.5.0 | Untested. | 0.5.0 state is not read by 0.5.8; start a fresh deployment. | Nothing. |
| `nats-server` | 2.15.3, in the upgrade runs on Linux and macOS: installed on the test machine and started by the 0.5.7 setup script. Also 2.15.1, the version the source pins, run inside the automated tests. | The local setup script `mash-local-nats-setup.sh` refuses anything older than 2.10.7. `mash` and `mashctl` themselves check no server version. | Other 2.x releases that have JetStream and operator mode. Versions from 2.10.7 up to 2.15.0. |
| Synadia Cloud | Untested on 0.5.8. | Not stated. | Not supported in 0.5.8 ([#876](https://github.com/bogworks/mash/issues/876)). |
| SAP protocol | 0.3, the version the source declares and the only one it implements. The 0.5.8 runs below used it. | 0.3. | Other SAP versions: not expected to work. |
| Go toolchain (only to build from source) | 1.27.4: the automated checks use it (`go.mod`), and both candidate builds in the upgrade runs used it. | 1.27.4. | Later Go releases. |
| Linux, amd64 | Tested: the automated tests passed on the release commit `8e8fe56`. The upgrade runs below ran on candidates, not on `8e8fe56`, in a Debian container. | Not stated. | Any current Linux distribution. |
| Linux, arm64 | The release binary is built and checked for a reproducible build. Tests are not run on it. Untested. | Not stated. | Works as on amd64. |
| macOS, amd64 (Intel) | The release binary is built and checked for a reproducible build. Tests are not run on it. Untested. | Not stated. | Works as on arm64. |
| macOS, arm64 (Apple silicon) | Partly tested: upgrade runs passed; the automated suite has one failure ([#1365](https://github.com/bogworks/mash/issues/1365)). The upgrade runs below ran on macOS 15.7.7. The automated tests ran on the 0.5.8 tag (see "What failed"). | Not stated. | Other current macOS releases. |
| Windows | Untested. | Not supported. | Does not work natively. WSL 2 with the Linux binary is expected to work. |

Notes:

- Release binaries exist for Linux and macOS on amd64 and arm64 only.
- The automated tests run on the project's own machines: Linux amd64 and macOS arm64.
- Version numbers above are those we saw, not promises that other versions fail.

## Upgrading from 0.5.7

These runs started from a 0.5.7 install and a 0.5.7 local server, then moved to a 0.5.8 candidate built from source. The upgrade is one-way; see the release notes. Each ran as a plain user, with no `sudo`, in a fresh throwaway setup that was removed afterwards.

| Where | Candidate | Result |
|---|---|---|
| Linux, amd64: Debian container (the image is "debian:stable", the current Debian stable container image; the record does not print its release number), `nats-server` 2.15.3 | `6d13e7d` | Passed every step: install 0.5.7, 0.5.7 local server, two endpoints, mail waiting in the inbox, upgrade without restarting the server, identical inbox afterwards, mail both ways, 0.5.7 and candidate `mash` side by side, and the 0.5.7 server folder under the 0.5.8 setup script (`--stop`, `--start`, then `--remove`). |
| Linux, amd64: the same container setup | `d958c6f` | Passed every step it ran, including a `mashctl doctor` and `doctor --strict` check after the upgrade with no configuration change. |
| macOS 15.7.7, arm64: a throwaway virtual machine, `nats-server` 2.15.3, Go 1.27.4 | `6d13e7d` | Passed every step after two stops that are not product failures of the upgrade (see "What failed"). The `--remove` step was proven on a server that was already stopped. |

What these runs do not cover:

- The published 0.5.8 binaries. Every upgrade run used a candidate built from source.
- Linux arm64, macOS amd64, any distribution or release other than the ones named above.
- Claude Code. No run used it.
- `--remove` on a running server on macOS. It passed on a running server on Linux.

## Run on the published 0.5.8 release

- Install on Linux: the documented install from the latest release, as a plain user in a `debian:stable-slim` container, installed `mash` and `mashctl` v0.5.8 (`8e8fe56`) and both reported that version. The record does not say which architecture the container ran on. `gh` was not in the container, so the installer trusted the download's TLS, as documented.
- Install on macOS: untested.
- The release files: for all eight binaries, the three scripts and `SHA256SUMS`, the checksums matched and the build attestation named commit `8e8fe56`. This checks what was published. It does not run the programs.

## What failed

- macOS, first upgrade run: it stopped at step 2, before the candidate was used. The 0.5.7 `mash-local-nats-setup.sh` refuses `--dir` under `/tmp` on macOS, because the macOS `stat` command drops the sticky bit. The candidate's script behaves the same. It is tracked as [#1341](https://github.com/bogworks/mash/issues/1341); its hint `chmod go-w /tmp` is wrong: do not follow it; the default `~/mash-nats` works. On macOS, pick a folder in your home directory. The second run used one.
- macOS, `--remove` step in the second run: it failed because the test typed the confirmation wrongly, so the setup script refused it, as it should. A rerun that typed the confirmation on a terminal passed.
- macOS, automated tests on the 0.5.8 tag: `TestBuiltBinaryPrintsTheReleaseVersion` failed, because it assumes a build at a tag carries no version stamp. That check does not block a release. It is tracked as [#1365](https://github.com/bogworks/mash/issues/1365).

## Release scripts

The release scripts, `install.sh` and `mash-local-nats-setup.sh`, are supported on Ubuntu, Debian and macOS only. The table says what has been run on 0.5.8.

| Platform | Release scripts on 0.5.8 |
|---|---|
| Ubuntu 24.04 | Untested on 0.5.8. |
| Ubuntu 22.04 | Untested on 0.5.8. |
| Debian (container "debian:stable" and "debian:stable-slim") | Tested, as in the tables above. The release number was not recorded (architecture not recorded). |
| Debian 12 | Untested on 0.5.8 by name. |
| Debian 13 | Untested on 0.5.8 by name. |
| macOS 15.7.7, arm64 | Tested for the 0.5.7 install script, the 0.5.7 setup script, and the candidate upgrade; the 0.5.8 setup script was run from candidate `6d13e7d`. The 0.5.8 `install.sh` was not run on macOS. |
| Every other platform | Not supported by the scripts. Use the [setup skill](../mash-setup/skills/setup/SKILL.md): your agent works it out. May work; not tested or supported. |

## How to report a combination that fails

Open an issue at <https://github.com/bogworks/mash/issues>. Say which versions you used (`mash version`, `claude --version`, `nats-server --version`), your operating system and architecture, and what you saw.
