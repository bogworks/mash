# Compatibility

This page says which versions of the things mash depends on go with mash 0.5.9. Three kinds of entry are kept apart:

- **Tested** is an exact version someone ran, with the date and the record that shows it. Where the record is not in yet, the cell says so.
- **Minimum** is a floor, with the feature that needs it.
- **Expected** is a rule we state, not a run. It is a guess until someone tests it.

Cells marked **PENDING AT-8** hold the mixed 0.5.8 and 0.5.9 results, and are filled in when those runs are recorded. Nothing in them is a claim yet.

## What 0.5.9 goes with

| Component | Tested | Minimum | Expected to work (a rule, untested) |
|---|---|---|---|
| Claude Code | **PENDING AT-8**: the versions, dates and run records of the live sessions on 0.5.9. The automated checks use a stand-in for Claude Code. | 2.1.312, which `mash doctor` enforces: it fails below it. `mash doctor` warns above the 2.1 series, because mash was tested with 2.1 only. | Later 2.1 versions. 2.2 or later may work; `mash doctor` warns. |
| `mash` and `mashctl` together | Both built from one commit, in the automated checks. | Not stated. Install both from the same release. | Using the same release for both. |
| `mash` 0.5.8 next to `mash` 0.5.9, on one server | **PENDING AT-8**: results in both directions, with dates and run records. | Not stated. | Nothing beyond what the runs show. |
| `mashctl` 0.5.8 with `mash` 0.5.9, or the other way round | **PENDING AT-8**. | Not stated. | Use the same release for both. The upgrade is one-way; see the release notes. |
| 0.5.0 | Untested. | 0.5.0 state is not read by 0.5.9; start a fresh deployment. | Nothing. |
| `nats-server` | 2.15.1, the version the source pins, run inside the automated tests. **PENDING AT-8** for the version on the test machines. | 2.10.7, which `mash-local-nats-setup.sh` enforces. `mash` and `mashctl` themselves check no server version. | Other 2.x releases that have JetStream and operator mode. |
| Synadia Cloud | Untested on 0.5.9. | Not stated. | Not supported ([#876](https://github.com/bogworks/mash/issues/876)). |
| SAP protocol | 0.3, the version the source declares and the only one it implements. | 0.3. | Other SAP versions: not expected to work. |
| Go toolchain (only to build from source) | 1.27.4: the automated checks use it (`go.mod`). | 1.27.4. | Later Go releases. |
| Linux, amd64 | The automated tests on the release commit. **PENDING AT-8** for the upgrade runs. | Not stated. | Any current Linux distribution. |
| Linux, arm64 | The release binary is built and checked for a reproducible build. Tests are not run on it. Untested. | Not stated. | Works as on amd64. |
| macOS, amd64 (Intel) | The release binary is built and checked for a reproducible build. Tests are not run on it. Untested. | Not stated. | Works as on arm64. |
| macOS, arm64 (Apple silicon) | The automated tests on the release commit. **PENDING AT-8** for the upgrade runs. | Not stated. | Other current macOS releases. |
| Windows | Untested. | Not supported. | Does not work natively. WSL 2 with the Linux binary is expected to work. |

Notes:

- Release binaries exist for Linux and macOS on amd64 and arm64 only.
- The automated tests run on the project's own machines: Linux amd64 and macOS arm64.
- Version numbers above are those we saw, not promises that other versions fail.

## Upgrading from 0.5.8

The upgrade is one-way; see the release notes. **PENDING AT-8:** the Linux and macOS upgrade runs from a 0.5.8 install and a 0.5.8 local server, with the steps each run ran, the dates and the run records.

## Run on the published 0.5.9 release

**PENDING:** filled in after the release is published: the install on Linux and macOS, and the check of each release file against its checksum and build attestation.

## Release scripts

The release scripts, `install.sh` and `mash-local-nats-setup.sh`, are supported on Ubuntu, Debian and macOS only.

| Platform | Release scripts on 0.5.9 |
|---|---|
| Ubuntu 24.04 | Untested on 0.5.9. |
| Ubuntu 22.04 | Untested on 0.5.9. |
| Debian | **PENDING AT-8**. |
| macOS, arm64 | **PENDING AT-8**. |
| Every other platform | Not supported by the scripts. Use the [setup skill](../mash-setup/skills/setup/SKILL.md): your agent works it out. May work; not tested or supported. |

## How to report a combination that fails

Open an issue at <https://github.com/bogworks/mash/issues>. Say which versions you used (`mash version`, `claude --version`, `nats-server --version`), your operating system and architecture, and what you saw.
