# Compatibility

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
