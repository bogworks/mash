# Compatibility

This page says which versions of the things mash depends on go with mash 0.6.0. Three kinds of entry are kept apart:

- **Tested** is an exact version someone ran, with the date and the record that shows it. Where the record is not in yet, the cell says so.
- **Minimum** is a floor, with the feature that needs it.
- **Expected** is a rule we state, not a run. It is a guess until someone tests it.

Most of the runs below are older: the mixed 0.5.8 and 0.5.9 runs were made on 2026-11-25 in throwaway Linux containers (Go 1.27 image, `nats-server` 2.15.1), on a build from source of the 0.5.9 release candidate (commit `3fd115b1`), not on published binaries. They were not repeated on 0.6.0, and each cell says which version it was run on. The one 0.6.0 run is in "Upgrading from 0.5.10 to 0.6.0". Cells marked **PENDING** are not filled in yet and make no claim.

## What 0.6.0 goes with

| Component | Tested | Minimum | Expected to work (a rule, untested) |
|---|---|---|---|
| Claude Code | Tested on 0.5.9, not yet run on 0.6.0: 2026-11-25, with a stand-in `claude` command that prints a version: 2.1.311 makes `mash doctor` fail (exit 1), 2.1.312 passes, 2.2.0 warns. Also 2026-11-25, a real Claude Code 2.1.330 on a Linux VM, with the from-source candidate: both plugins updated from 0.5.8 to 0.5.9 and a new session after the restart got mail. The other automated checks use a stand-in for Claude Code. | 2.1.312, which `mash doctor` enforces: it fails below it. `mash doctor` warns above the 2.1 series, because mash was tested with 2.1 only. | Later 2.1 versions. 2.2 or later may work; `mash doctor` warns. |
| `mash` and `mashctl` together | Both built from one commit, in the automated checks. | Not stated. Install both from the same release. | Using the same release for both. |
| `mash` 0.5.8 next to `mash` 0.5.9, on one server | Tested on 0.5.8 and 0.5.9, not yet run on 0.6.0: 2026-11-25, Linux amd64, one server: `mash send`, `inbox`, `read` and `who` work in both directions between a 0.5.8 endpoint and a 0.5.9 endpoint, mail reads as `verified`, exit 0 each time. | Not stated. | Nothing beyond what the runs show. |
| `mashctl` 0.5.8 and 0.5.9 on one server | Tested on 0.5.8 and 0.5.9, not yet run on 0.6.0: 2026-11-25, Linux amd64: each `mashctl` created, listed, showed and deleted endpoints made by the other, and each `mashctl doctor` ended with no problems afterwards (16 steps, all ok). | Not stated. | Use the same release for both. The upgrade is one-way; see the release notes. |
| 0.5.0 | Untested. | 0.5.0 state is not read by 0.5.9, and 0.6.0 has not been checked against it; start a fresh deployment. | Nothing. |
| `nats-server` | 2.15.1, the version the source pins (`go.mod`), run inside the automated tests. 2.15.1 also on the test server in the mixed-version runs of 2026-11-25 (0.5.8 and 0.5.9). | 2.10.7, which `mash-local-nats-setup.sh` enforces. `mash` and `mashctl` themselves check no server version. | Other 2.x releases that have JetStream and operator mode. |
| Synadia Cloud | Untested. | Not stated. | Not supported ([#876](https://github.com/bogworks/mash/issues/876)). |
| SAP protocol | 0.3, the version the source declares and the only one it implements. | 0.3. | Other SAP versions: not expected to work. |
| Go toolchain (only to build from source) | 1.27.4: the automated checks use it (`go.mod`). | 1.27.4. | Later Go releases. |
| Linux, amd64 | The automated tests on the release commit. The mixed-version runs of 2026-11-25 (0.5.8 and 0.5.9) were on a Go 1.27 container image. The nightly run on the 0.5.9 candidate (commit `3fd115b1`, job `test (linux, eph)`, run 45567864348, 2026-11-25) passed. Not yet run on 0.6.0. | Not stated. | Any current Linux distribution. |
| Linux, arm64 | The release binary is built and checked for a reproducible build. Tests are not run on it. Untested. | Not stated. | Works as on amd64. |
| macOS, amd64 (Intel) | The release binary is built and checked for a reproducible build. Tests are not run on it. Untested. | Not stated. | Works as on arm64. |
| macOS, arm64 (Apple silicon) | The automated tests on the release commit. Tested on 0.5.9, not yet run on 0.6.0: on 2026-11-25, on macOS 15.7.7 arm64 in a throwaway tart VM, from the from-source candidate (`3fd115b1`): installing 0.5.9 by rename over 0.5.8 (release files checked against their checksums) exited 0 and the running server was left alone; six numbered messages were each delivered exactly once; `mash doctor` and `mashctl doctor` exited 0 before and after; and the 0.5.9 setup script's `--start` and `--stop` worked on 0.5.8 setup files. Not run on macOS: a live Claude Code session, the plugin update (no `claude` on the VM; the Linux run covers it), the 0.5.9 installer and `--remove`. | Not stated. | Other current macOS releases. |
| Windows | Untested. | Not supported. | Does not work natively. WSL 2 with the Linux binary is expected to work. |

Notes:

- Release binaries exist for Linux and macOS on amd64 and arm64 only.
- The automated tests run on the project's own machines: Linux amd64 and macOS arm64.
- Version numbers above are those we saw, not promises that other versions fail.

## Upgrading from 0.5.10 to 0.6.0

This is how to move a machine that runs 0.5.10 to 0.6.0. Your endpoints, keys and mail stay where they are. The upgrade is one-way: there is no supported way back to 0.5.10. Do the steps in this order, on each machine that has endpoints, and then on the administrator's machine.

1. Check what you run now. `mash version` shows the version. `mash doctor` should end with no problem and exit 0. On 0.5.10, if the machine has more than one endpoint, a bare `mash doctor` stops with exit status 2 and says there is more than one endpoint: run `mash doctor --endpoint <address>` for each endpoint instead. As administrator, run `mashctl doctor` too. If one of them reports a problem, fix that first; see [troubleshooting](troubleshooting.md).
2. Install both programs of 0.6.0 together, `mash` and `mashctl`, with the install script of the release. It checks each file before it installs anything. It installs into `~/.local/bin`. If your current `mash` is in another folder (`command -v mash` shows where), add `MASH_INSTALL_DIR=<that folder>` before `bash install.sh`, so the new programs replace the old ones.

   ```sh
   BASE=https://github.com/bogworks/mash/releases/download/v0.6.0
   curl -fsSLO $BASE/install.sh -O $BASE/SHA256SUMS &&
   grep ' install.sh$' SHA256SUMS | sha256sum -c - &&
   MASH_VERSION=v0.6.0 bash install.sh
   ```

   On macOS use `shasum -a 256 -c -` instead of `sha256sum -c -`.

3. Update both plugins in Claude Code: `claude plugin update mash@mash` and `claude plugin update mash-setup@mash`. The plugin that runs the 0.6.0 program comes out shortly after the release. Ask a session "which mash version is this?": it says `mash v0.6.0` when you have it. Until then, update again.
4. Restart each Claude Code session. Installing the new release, or updating the plugin, does not change the program that an open session uses. An open session keeps running the old version, and keeps sending and receiving mail, until you restart it. It moves to the new version only when it restarts. The first time a session starts on 0.6.0 it checks your stored mail once before it delivers any, and `mash status` says so while it does: `<endpoint>: not ready: mail is waiting while stored mail is checked`. The check is quick, so you may not see that line; that is fine. If you run `mash serve` yourself, outside Claude Code, restart it after installing.
5. Check again: `mash version` now says `mash v0.6.0`. Run `mash doctor` and, as administrator, `mashctl doctor`. Each should end with no problem. On 0.6.0 a bare `mash doctor` works with more than one endpoint: it checks each one.

On 2026-11-26, in a throwaway Linux container, a 0.5.10 install (release files, a local server set up by the 0.5.10 script, endpoints `acme.demo.alice` and `acme.demo.bob`) was upgraded to a build from source of main (commit `ba25c0f`) with these steps, and it worked end to end. This is a run of a from-source candidate, not of published 0.6.0 binaries: the install script ran against a local copy of the release files, and the plugins were not updated (no Claude Code in the container). The run did, in order: the checks of step 1; the install of step 2; a message sent while the old `mash serve` still ran; a restart of `mash serve`; the checks of step 5; and a message sent after the upgrade.

## Upgrading from 0.5.8

The upgrade is one-way; see the release notes. These runs were made for 0.5.9 and not repeated on 0.6.0. On 2026-11-25 a Linux container run, from a 0.5.8 install and a server set up by the 0.5.8 script, ran these steps and they passed: `mashctl init --config <file>` once more (it adds `signing_key_pub` to `config.json` and changes nothing else), the doctor exit codes, `mashctl status` with no server, and `mashctl init --json`. Mail kept flowing, in both directions, when a session was not restarted after the programs were replaced, and with the 0.5.8 plugin and the 0.5.9 program, and the other way round. In a real Claude Code 2.1.330 on a Linux VM (2026-11-25, from-source candidate, no marketplace refresh), `claude plugin update mash@mash` and `claude plugin update mash-setup@mash` took both plugins from 0.5.8 to 0.5.9 (exit 0, "Restart to apply changes"). After the restart a new session received mail, the doctor tool showed no problems, and a reply reached a 0.5.8 peer. The macOS run is in the macOS row of the table in "What 0.6.0 goes with".

## Run on the published 0.6.0 release

**PENDING:** filled in after the release is published: the install on Linux and macOS, and the check of each release file against its checksum and build attestation.

## Release scripts

The release scripts, `install.sh` and `mash-local-nats-setup.sh`, are supported on Ubuntu, Debian and macOS only.

| Platform | Release scripts on 0.6.0 |
|---|---|
| Ubuntu 24.04 | Untested on 0.6.0. |
| Ubuntu 22.04 | Untested on 0.6.0. |
| Debian | **PENDING**: the 2026-11-25 runs used a Go 1.27 container image; its Debian release was not recorded. |
| macOS, arm64 | Not run on 0.6.0. On 0.5.9: the setup script's `--start` and `--stop` worked on 0.5.8 setup files (macOS 15.7.7, tart VM, 2026-11-25); the installer and `--remove` were not run. |
| Every other platform | Not supported by the scripts. Use the [setup skill](../mash-setup/skills/setup/SKILL.md): your agent works it out. May work; not tested or supported. |

## How to report a combination that fails

Open an issue at <https://github.com/bogworks/mash/issues>. Say which versions you used (`mash version`, `claude --version`, `nats-server --version`), your operating system and architecture, and what you saw.
