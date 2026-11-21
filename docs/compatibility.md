# Compatibility

This page says which versions of the things mash depends on have been tried with mash 0.5.6. "Tested" means a run on that exact version, by our automated checks or a recorded test. Anything else is a guess and is marked as one.

## What 0.5.6 has been run on

| Component | Tested | Minimum | Expected to work (not tested) |
|---|---|---|---|
| Claude Code | No 0.5.6 run on a live Claude Code session. Earlier mash releases passed live runs on 2.1.312 and 2.1.316. The automated checks use a stand-in for Claude Code, so they do not exercise a real one. | 2.1.312 or newer is what the README and install guide ask for. mash does not refuse older versions. | Later versions than 2.1.316. Versions between 2.1.312 and 2.1.316. |
| `mash` and `mashctl` together | Both built from one commit, as in the automated checks. | Not stated. The install guide says the plugin and the `mash` binary should be the same release (major and minor). | Using the same release for both. |
| `mash` 0.5.6 mixed with `mashctl` of another version | Not tested. | Not stated. | Not expected to work; use the same release. |
| 0.5.0 | Not tested. | 0.5.0 state is not read by 0.5.6; start a fresh deployment. | Nothing. |
| `nats-server` | 2.15.1, the version the source pins. The automated checks run it inside the test process, built from the same pin. | The local setup script `mash-local-nats-setup.sh` refuses anything older than 2.10.7. `mash` and `mashctl` themselves check no server version. | Other 2.x releases that have JetStream and operator mode, including 2.15.2. Versions from 2.10.7 up to 2.15.0. |
| Synadia Cloud | Not tested on 0.5.6. | Not stated. | `mash` is documented to work against any conforming server, but `mashctl` does not support Synadia Cloud in 0.5.6 ([#876](https://github.com/bogworks/mash/issues/876)). A test of an earlier release against Synadia Cloud was run on 2026-10-31. |
| SAP protocol | 0.3, the version mash declares and the only one it implements. | 0.3. | Other SAP versions: not expected to work. |
| Go toolchain (only to build from source) | 1.27.4, the version the automated checks use. | 1.27.4 (`go.mod`). | Later Go releases. |
| Linux, amd64 | The release binary is built, and the automated tests run on Linux amd64 machines. These tests gate every merge and release. | Not stated. | Any current Linux distribution. |
| Linux, arm64 | The release binary is built and checked for a reproducible build. Tests are not run on it. | Not stated. | Works as on amd64. |
| macOS, amd64 (Intel) | The release binary is built and checked for a reproducible build. Tests are not run on it. | Not stated. | Works as on arm64. |
| macOS, arm64 (Apple silicon) | The automated tests run on Apple silicon Macs. That check is advisory: it does not block a release. | Not stated. | Current macOS releases. |
| Windows | Not tested. | Not supported. | Does not work natively. WSL 2 with the Linux binary is expected to work. |

Notes:

- Release binaries exist for Linux and macOS on amd64 and arm64 only.
- The tests run on the project's own machines: Linux amd64 and macOS arm64.
- The advisory macOS job and the Linux jobs run the same Go tests.
- Version numbers above are those we saw, not promises that other versions fail.

## Release scripts

The release scripts, `install.sh` and `mash-local-nats-setup.sh`, are supported on Ubuntu, Debian and macOS only.

| Platform | Release scripts |
|---|---|
| Ubuntu 24.04 | Tested: every step passes as a plain user, with no `sudo`. |
| Ubuntu 22.04 | Tested: every step passes as a plain user, with no `sudo`. |
| Debian 12 | Supported; run in progress. |
| Debian 13 | Supported; run in progress. |
| macOS | Supported; run in progress. |
| Every other platform | Not supported by the scripts. Use the [setup skill](../mash-setup/skills/setup/SKILL.md): your agent works it out. |

## How to report a combination that fails

Open an issue at <https://github.com/bogworks/mash/issues>. Say which versions you used (`mash version`, `claude --version`, `nats-server --version`), your operating system and architecture, and what you saw.
