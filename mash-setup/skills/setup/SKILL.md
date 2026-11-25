---
name: setup
description: >-
  Set mash up on this machine: install the mash and mashctl programs, make a
  NATS server for this machine, install the mash Claude Code plugin and make
  the first agent mailboxes. Use when the person asks to install or set up
  mash, to give their Claude Code sessions mail, or to let agents message each
  other, and `mash` is not installed or `mashctl status` prints nothing (no
  server is configured here). If
  mash already works, use the day-2 skill instead.
---

# Set mash up on this machine

For a person, or for an agent following it. Linux or macOS (see Platforms
below for which are tested). One machine, one
user, a server only this machine can reach. It follows the mash walkthrough:
plan, install, make the server, make the endpoints, send mail.

mash gives each AI agent session an inbox that keeps its mail, so agents can
message each other. The inbox lives on a NATS server (an open-source message
server). `mashctl` is mash's admin tool; it makes and removes the mailboxes
(endpoints). `mash` is what one agent uses to send and read mail.

You are an agent. You may propose every answer, but the person decides. Ask
every question in step 1 before you change anything. Each step below that
changes something says what it needs a yes for: ask for that yes, once, for
that step. "Set mash up" alone is not a yes for all of them.

Everything is installed as the local user: no `sudo` and no system service,
except that the person may choose a system route for `nats-server` and `nats`
in step 2 and say yes to its exact command. You run each step's commands
yourself after that step's yes.

Platforms. The release scripts are tested on Ubuntu, Debian and macOS. At
each step that runs one (step 3 downloads and checks them; step 4 runs
`install.sh` and `mash-local-nats-setup.sh`; the step 2 `nats-server` and
`nats` blocks are a worked example and follow the same rule):

- On Ubuntu, Debian or macOS: run the checked script, as written.
- On anything else (step 1 shows it): (1) tell the person this platform is
  outside what the scripts are tested on; (2) try the script only if they
  agree; (3) if it fails, do the equivalent steps by hand, using the script
  as the reference, and report exactly what differed (the command, its
  output, and what you did instead). Do not patch the scripts for another
  distribution.

One yes per step. The yes in step 1 is for the plan only. Before each step
that changes something (steps 2 to 6; every step marked "Needs a yes"), say in
one or two lines what it will do, show each script before you run it (every
time, not just the first), and wait for that step's own yes. Never run two
steps on one yes. "Defaults" or "yes" to step 1 is not a yes for a later step.

Every command block runs on its own in a fresh shell. Blocks marked
`sh person` are only for what the person must do themselves: a typed
confirmation, and starting their own Claude Code sessions. Write the exact
command out, say what it does, ask them to run it in their own terminal,
outside your session, then check the result with the read-only commands. A
typed confirmation cannot show a human is present, because a program in your
session can type it too, so you never run those blocks yourself.

## Stop and ask the person when

- a command prints `error:`, `not ready:`, a line with `FAIL` (FAILED, a doctor
  row), a doctor summary of problems ("1 problem."), or a line you do not
  understand
- a checksum does not match
- a folder, port or program already exists and this guide did not make it
- you are asked to use root, to skip a yes, or to turn off a check
- `claude plugin marketplace list` shows a `mash` marketplace from a
  different source than `bogworks/mash`
- the plugin shows more than one MCP server or any hook other than the three
  named in step 5

Say which of these it is, say the next command you would run, and wait.

## 1. Plan: ask, then read it back

First ask which setup the person wants, and wait for the answer:

- Demo setup: the quickest way to try mash, with every choice made for them
  (owner `acme`, group `demo`, agents `bob` and `alice`, the default folders,
  the default download route for every program). Say exactly what will happen:
  the names, the folders, and what gets installed and how (use the read-back
  below, filled in with the defaults). Ask for one yes, then run steps 2 to 8 to
  completion. That one yes covers every step, so the "one yes per step" rule
  below does not apply; the stop-and-ask rules and the `sh person` blocks still
  do.
- General setup: the questions below, and a yes before each step, as written.

Both also ask which Claude account this is, because it decides the idle wake
(see step 8): a personal account, or a Team or Enterprise organisation's (the
person can type `/status` in Claude Code to see which). Both end with the same
closing example in step 8. In the demo setup, skip the
questions below and use their defaults; the person may still change a name
when they answer the first question.

In the general setup, ask in order, all in one message. Each has a default;
the person may take it.

If the person's request already names an owner, a group, agent names or full
addresses (for example `acme.demo.alice`), take them as the answers to
questions 1 to 3, say so in the read-back, and ask only what is still open.
The login-name default for the owner applies only when they gave none.

1. Owner name. Default: the login name, if it is lower-case letters and
   digits. An owner is one namespace: the first part of every address
   (`acme` in `acme.demo.bob`). It cannot change later without starting over.
   Lower-case letters, digits and single hyphens, starting with a letter, up
   to 32 characters.
2. Group name. Default `demo`. A group is a set of agents that belong together.
3. Two agent names. Default `bob` and `alice`. Each agent's address is
   `owner.group.name`, for example `acme.demo.bob`.

4. Install folder for the programs (`mash`, `mashctl`, `nats-server` and
   `nats`). Default `~/.local/bin`. It is the installer's `MASH_INSTALL_DIR`.
5. Server folder. Default `~/mash-nats`. It is the setup script's `--dir`; give
   it as an absolute path (not `~`, not relative).
6. Download folder for the two scripts. Default `~/mash-setup-dl`.

Everything else is fixed here: the server listens on this machine only, and
the repository is `bogworks/mash`.

The owner, group and agent names in the later blocks (`acme`, `demo`, `bob`,
`alice`) are examples: replace each with the person's answers.

Carry the answers to every later command. Each block runs in a fresh shell, so
start each block that needs them with the line that sets them, with the
person's values (these are the defaults). Where a later block or sentence
names `~/.local/bin`, `~/mash-nats` or `~/mash-setup-dl`, use their folder:

```sh
BIN="$HOME/.local/bin"; SRV="$HOME/mash-nats"; DL="$HOME/mash-setup-dl"
```

Read-only look at the machine, in one block. It changes nothing:

```sh
uname -sm; command -v mash mashctl nats-server nats openssl claude; ls -d "$HOME/mash-nats" "$HOME/.local/bin" "$HOME/mash-setup-dl" 2>&1; claude plugin list 2>&1 | head -20; (. /etc/os-release && echo "$ID $ID_LIKE") 2>/dev/null
```

The last command prints the Linux distribution (`ubuntu`, `debian`, ...);
on macOS it prints nothing and `uname -s` says `Darwin`. Use it for the
Platforms rule.

Then say this to the person, filled in, and wait for a yes:

```
I will:
  platform                          Ubuntu (tested)
                                    [else: "<name>: not tested; I will ask
                                     first"]
  download mash and mashctl         from the latest release (one tag, checked
                                    against its checksums), into ~/.local/bin
  nats-server and nats              you choose how: the default is a download
                                    from their GitHub releases, checked against
                                    SHA256SUMS, into ~/.local/bin
  use no sudo                       unless you choose a system route and say
                                    yes to that exact command
  make a NATS server                in ~/mash-nats, this machine only, run as you
                                    (a program you run: after a reboot it is
                                     stopped; start it again with --start)
  use the owner name                acme
  install the mash plugin           from the bogworks/mash marketplace
  make two agent mailboxes          acme.demo.bob and acme.demo.alice
  one command per agent             `bob` and `alice`, small files in
                                    ~/.local/bin that start Claude Code as
                                    that agent, named by its address
  idle wake                         [personal account: only with Claude
                                    Code's development flag, your choice in
                                    step 8; organisation: needs your admin;
                                    else mail arrives at the next prompt]
Say yes to go ahead. [General setup: I will ask again before each step.
Demo setup: this one yes covers every step.]
```

## 2. The server's programs

The NATS script needs `nats-server` 2.10.7 or newer (mash is tested with
2.15.1; the requirements are in
https://github.com/bogworks/mash/blob/main/docs/nats-server.md#requirements)
and the `nats` command (natscli 0.5.2 is tested), both on the PATH of the user
who will run mash. It also needs `openssl`, `curl`, `tar`, the port tool
(`ss` on Linux, `lsof` on macOS), and `unzip`, `python3` or `dpkg-deb` (Debian)
for the nats download. If one of those is missing, say so in one line
and stop and ask: the person decides how to get it.

Ask. If either program is missing or too old (check with
`command -v nats-server nats` and `nats-server --version`), ask the person how
they want them installed, and list the choices:

- (a) download each from its GitHub release into their own folder `BIN` (the
  default; recommended; no sudo)
- (b) Homebrew or Linuxbrew: `brew install nats-server nats`
- (c) the system's package manager
- (d) build from source
- (e) something else they name

Wait for the answer. That question is the ask for the install route; the yes
covers the blocks of the chosen route, shown to the person before they run.

Then do it. Install for this machine and this choice, and say what you are
doing before each command, in one line. Guard rails:

- Prefer a per-user install.
- Never use `sudo` unless the person chose a route that needs it and said yes
  to that exact command.
- Chain every checksum check to the step after it with `&&` or `set -e`, so a
  mismatch stops before unpack or install.
- After a system package, find where it landed without relying on PATH
  (`command -v` can fail after a good install: Debian's package puts
  `nats-server` in a folder such as `/usr/sbin` that is often not on a
  user's PATH). On Debian and Ubuntu use
  `dpkg -L nats-server | grep 'bin/nats-server$'`; for another package
  manager use its file list (for example `rpm -ql` or `pacman -Ql`). Use
  the folder that list shows, not a remembered one. Tell the person the folder,
  and put it on PATH for the commands of this session
  (`PATH="<folder>:$PATH"` in front of commands, as for `BIN`); do not edit
  their shell files. The setup script and Verify use that PATH too. Say
  that a restart after a reboot needs the folder too (the day-2 skill finds
  it again from the package). Also check whether the package started a
  service (for example `systemctl is-active nats-server` where systemd
  exists), and tell the person.
- The Debian and Ubuntu package starts an unauthenticated server on port 4222
  (a known example). Say so, and let the person decide whether to stop it.
- Never disable or stop a service the person did not ask to stop.
- If the route fails, say what failed and ask. Do not switch routes silently.

The default route (a), as a worked example. Adapt it for another machine type
or choice; do not run it for a different choice. It needs a yes for
downloading each program from its project's GitHub release into `BIN`.
On a platform outside Ubuntu, Debian and macOS, follow the Platforms rule
first.

`BIN` may not exist yet. Debian's default umask is 002, so a folder made with
`mkdir -p` is writable by its group, and `install.sh` refuses to install
there. `install.sh` checks every folder above it too, and `install -d -m`
sets the mode only on the folders it is given, so each block creates the
missing folders above the chosen `BIN` itself, top down, each with mode 755
(the `mkp` helper). It never changes a folder that already exists:
`install.sh` refuses a group-writable one and says so, and the person
decides. Each block works in a fresh `mktemp -d` folder: a throwaway
folder. After the install you remove only that one folder, the `T` the block
set (`rm -r "$T"`), never a glob.

`nats-server` (tested v2.15.1). The block names the operating system and the
processor (`linux` or `darwin`; `amd64` or `arm64`), downloads the release
and its `SHA256SUMS`, checks the archive against them and installs it:

```sh
set -e
BIN="$HOME/.local/bin"; V=2.15.1
case "$(uname -s)" in Linux) OS=linux ;; Darwin) OS=darwin ;; esac
case "$(uname -m)" in x86_64) ARCH=amd64 ;; aarch64 | arm64) ARCH=arm64 ;; esac
if command -v sha256sum >/dev/null; then sha() { sha256sum -c -; }; else sha() { shasum -a 256 -c -; }; fi
mkp() { [ -d "$1" ] && return 0; mkp "$(dirname "$1")"; install -d -m 755 "$1"; }
mkp "$BIN"
T=$(mktemp -d) && cd "$T"
curl -fsSLO https://github.com/nats-io/nats-server/releases/download/v$V/nats-server-v$V-$OS-$ARCH.tar.gz
curl -fsSLO https://github.com/nats-io/nats-server/releases/download/v$V/SHA256SUMS
grep " nats-server-v$V-$OS-$ARCH.tar.gz\$" SHA256SUMS | sha
tar -xzf nats-server-v$V-$OS-$ARCH.tar.gz
install -m 0755 nats-server-v$V-$OS-$ARCH/nats-server "$BIN/nats-server"
cd "$HOME" && rm -r "$T"
"$BIN/nats-server" --version
```

The `nats` command (natscli v0.5.2), the same way:

```sh
set -e
BIN="$HOME/.local/bin"; V=0.5.2
case "$(uname -s)" in Linux) OS=linux ;; Darwin) OS=darwin ;; esac
case "$(uname -m)" in x86_64) ARCH=amd64 ;; aarch64 | arm64) ARCH=arm64 ;; esac
if command -v sha256sum >/dev/null; then sha() { sha256sum -c -; }; else sha() { shasum -a 256 -c -; }; fi
mkp() { [ -d "$1" ] && return 0; mkp "$(dirname "$1")"; install -d -m 755 "$1"; }
mkp "$BIN"
T=$(mktemp -d) && cd "$T"
if command -v unzip >/dev/null || command -v python3 >/dev/null; then
  F=nats-$V-$OS-$ARCH.zip
elif [ "$OS" = linux ] && command -v dpkg-deb >/dev/null; then
  F=nats-$V-$ARCH.deb
else
  echo "need unzip, python3 or dpkg-deb (Debian) to unpack nats" >&2; false
fi
curl -fsSLO https://github.com/nats-io/natscli/releases/download/v$V/$F
curl -fsSLO https://github.com/nats-io/natscli/releases/download/v$V/SHA256SUMS
grep " $F\$" SHA256SUMS | sha
case $F in
*.deb) dpkg-deb -x "$F" deb; install -m 0755 deb/usr/local/bin/nats "$BIN/nats" ;;
*)
  if command -v unzip >/dev/null; then unzip -q "$F"
  else python3 -m zipfile -e "$F" .; fi
  install -m 0755 nats-$V-$OS-$ARCH/nats "$BIN/nats" ;;
esac
cd "$HOME" && rm -r "$T"
"$BIN/nats" --version
```

Each block starts with `set -e`, so a checksum that does not match stops it
before anything is unpacked or installed. Each checksum line should end in `OK`
(a mismatch: stop, install nothing, ask).
`nats-server --version` prints `nats-server: v2.15.1` and `nats --version`
prints `0.5.2`. `BIN` must be on the PATH: if it is not, tell the person to
add it (do not edit their shell files); for this session use the full path, or
put `PATH="$BIN:$PATH"` in front of a command. Both programs are explained at
https://github.com/bogworks/mash/blob/main/docs/nats-server.md#getting-nats-server-and-the-nats-command

Verify: `nats-server --version` is 2.10.7 or newer, and
`command -v nats-server nats` finds both: on the PATH, or in the folder the
package put them in, with that folder on this session's PATH. Only then go on.

## 3. Download and check the two scripts

Needs a yes: downloading two scripts from the mash release and checking them.
You never pipe a download into a shell. You download each file and check it
against the release's checksums here; step 4 shows each script to the person
and runs it after a yes. On a platform outside Ubuntu, Debian and macOS,
follow the Platforms rule first.

Ask, and wait for the yes, before running the downloads below.

Use the person's download folder for `DL` (the first assignment below).
Resolve the release tag once, and use it for the downloads and for the
installer, so all three agree even if a newer release appears in between:

```sh
DL="$HOME/mash-setup-dl"; TAG=$(curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/bogworks/mash/releases/latest); TAG=${TAG##*/}; echo "$TAG" && mkdir -p "$DL" && cd "$DL" && U=https://github.com/bogworks/mash/releases/download/$TAG && curl -fsSL -O "$U/SHA256SUMS" -O "$U/install.sh" -O "$U/mash-local-nats-setup.sh" && grep -E ' (install.sh|mash-local-nats-setup.sh)$' SHA256SUMS | sha256sum -c -
```

Say the tag back to the person (it looks like `v0.5.6`) and use that same
value as `TAG` in step 4.

The checksums come from the same download as the scripts, so on their own
they only show the files are intact. If `gh` (the GitHub CLI) 2.102.0 or
newer is installed (`gh --version`; older versions check `--signer-workflow`
only as a prefix, so they cannot be trusted with it), also check the
release's signed attestation of `SHA256SUMS`, pinned to the tag, before
either script runs:

```sh
DL="$HOME/mash-setup-dl"; TAG=vX.Y.Z; cd "$DL" && U=https://github.com/bogworks/mash/releases/download/$TAG && curl -fsSL -O "$U/mash.intoto.jsonl" && gh attestation verify SHA256SUMS -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref "refs/tags/$TAG"
```

Replace `vX.Y.Z` with the tag from the block above (each block runs in a
fresh shell). Expected: it exits 0 and says it verified. Any failure: stop,
run nothing.
If `gh` is missing, does not run, or is older than 2.102.0, do not run the
block above. Say so to the person in one sentence: the scripts are then
checked against checksums from the same release, not authenticated (the
installer says the same). Run them only if the person says yes
to that.

Expected: `install.sh: OK` and `mash-local-nats-setup.sh: OK`. On macOS
`sha256sum` may be missing: use `shasum -a 256 -c -`. Anything else, or a
`404`: stop and ask. A mismatch means the file is not the release's file:
do not run it.

## 4. Install mash and mashctl, then make the server

Each script has one section below: show it, ask, run it. If a later release
changes how a script is run, change only its section. On a platform outside
Ubuntu, Debian and macOS, follow the Platforms rule first.

### Run install.sh

Show it to the person (`cat "$DL/install.sh"`), say in one sentence what it
does (it installs `mash` and `mashctl` into the install folder, checked
against the release's checksums), and ask. Needs a yes: running it. Then run
it, pinned to the tag from step 3:

```sh
BIN="$HOME/.local/bin"; DL="$HOME/mash-setup-dl"; TAG=vX.Y.Z
MASH_INSTALL_DIR="$BIN" MASH_VERSION="$TAG" bash "$DL/install.sh"
```

Replace `vX.Y.Z` with the tag from step 3.

Expected: `Installed mash v<version> (<commit>) in <folder>` and the same for
`mashctl`. If it says the folder is not on the PATH, tell the person to add it
(do not edit their shell files): for this session use the full path the line
printed. The folder is the one in the output; take it from there. If it says
the folder `is writable by its group or other users`, see the table at the
end. A line on stderr starting `warning: gh not found` (or `gh did not run`, `gh
version unreadable`, `gh is below 2.102.0`) is normal: it says the download
was checked against checksums only, not verified. If `gh` 2.102.0 or newer is
installed, the installer prints `Verified the release attestation for` and the
repository and tag; if it stops with `the release failed verification`, stop
and ask. To verify by hand, see `docs/install.md`.

Check, read-only: `mash version` and `mashctl version` print the same version.

### Run mash-local-nats-setup.sh

Show it to the person (`cat "$DL/mash-local-nats-setup.sh"`), say in one
sentence what it does (it makes a NATS server in the server folder, as the
person's own user, listening on this machine only), and ask. Needs a yes:
starting a NATS server on this machine. Then run it with the install folder
first on the PATH, so it finds `nats-server` and `nats`. If step 2 found
them in a package folder, that folder goes in front too:
`PATH="<package folder>:$BIN:$PATH"`. The command below is the default route:

```sh
BIN="$HOME/.local/bin"; SRV="$HOME/mash-nats"; DL="$HOME/mash-setup-dl"
PATH="$BIN:$PATH" bash "$DL/mash-local-nats-setup.sh" --dir "$SRV" --owner acme
```

It prints a line for each step and ends with the next command, with the path
it actually wrote. If it says a port other than 4222 was used, that is fine:
another `nats-server` (one the machine already runs) holds 4222, so leave it
alone and never stop it; mash uses the other port. Run the last line it
printed as it printed it (`mashctl init --config <folder>/mash-setup.yaml`);
take the path from the output, never from memory. Then check: `mashctl status`
prints the server's address, `ok` and the endpoint count.

The server is a program running as you. It does not come back after a reboot:
after one, run the script again with `--start` (day-2 skill). Tell the person
this now, before they reboot. If `nats-server` came from a system package
and its folder is not on their PATH, name that folder too: the restart
command needs it in front of the PATH, as `PATH="<folder>:$BIN:$PATH"`.

## 5. Install the mash plugin

Needs a yes: installing the mash plugin. Its files are not otherwise verified
by this guide. It adds one MCP server and three hooks (SessionStart,
PostToolUse, UserPromptSubmit) and loads in the next Claude Code session.

The `bogworks/mash` marketplace is already added (it is where this skill came
from). Install:

```sh
claude plugin install mash@mash
```

Check before any session loads it:

```sh
claude plugin list 2>&1 | grep -A4 'mash@mash'
```

Expected: `mash@mash` enabled at the version of step 4. Then read the installed
plugin's `.mcp.json` and `hooks/hooks.json` (the folder is in the plugin's
list entry): exactly one server running the plugin's own launcher with
`serve`, and exactly the three hooks. Each hook runs the launcher; the two
`hook` ones also make mash's state folder and write `hook.log` in it. Any
other component: stop, tell the person, and hand them
`claude plugin uninstall mash@mash` as a `sh person` block.

## 6. Make the agents' mailboxes

Needs a yes: making the two mailboxes read back in step 1. For each of
the two agents:

```sh
mashctl endpoint create acme.demo.bob
```

Expected: `Created endpoint acme.demo.bob`. Then the same for the second
agent.
Check:

```sh
MASH_ENDPOINT=acme.demo.bob mash status
```

Expected: `acme.demo.bob: ready, offline`. Ready means the server is there and
reachable and the credentials are valid. Offline means no session holds the
mailbox open yet.

## 7. Send mail

```sh
MASH_ENDPOINT=acme.demo.bob mash send acme.demo.alice "lunch at noon?"
```

```sh
MASH_ENDPOINT=acme.demo.alice mash inbox
```

Expected: `Mail sent (number 1)`, then `1 message for acme.demo.alice, 1 new,
0 urgent.` and a one-row table with `lunch at noon?`.
`MASH_ENDPOINT=acme.demo.alice mash read 1` shows the message in full,
with `trust: verified`.

## 8. Use it from Claude Code

Needs a yes: writing one small command file per agent in the install folder.

Mail always arrives at a session's next prompt or tool call. Waking an idle
session needs Claude Code to load mash's channel, and Claude Code says:
"During the preview, `--channels` only accepts plugins from an
Anthropic-maintained allowlist, or from your organization's allowlist if an
admin has set `allowedChannelPlugins`."
(https://code.claude.com/docs/en/channels). mash is not on Anthropic's list.
So the wake depends on the account (ask if step 1 did not):

- Personal account: the only way today is Claude Code's development flag,
  `--dangerously-load-development-channels plugin:mash@mash`. Tell the person
  plainly what it is, and ask; the default is no. It lets a plugin that is not
  on Anthropic's list push a message into the session, Claude Code shows a
  warning screen and asks them to confirm at every start, and nothing else
  changes. Without it, mail still arrives at the next prompt or tool call and an
  idle session is not woken. Never add it on your own word.
- Team or Enterprise organisation: an owner can turn channels off
  (`channelsEnabled`) or limit them to a managed `allowedChannelPlugins` list.
  Check, read-only: ask the person to type `/status` and tell you what
  "Setting sources" shows, and read the managed settings file if there is one
  (`/etc/claude-code/managed-settings.json` on Linux,
  `/Library/Application Support/ClaudeCode/managed-settings.json` on macOS, and
  any `managed-settings.d` folder beside it). Never edit it.
  - If a managed file turns channels off, or lists other plugins and not mash:
    say so plainly. The person asks their administrator (Organization
    settings > Claude Code > Channels, and add
    `{ "marketplace": "mash", "plugin": "mash" }` to `allowedChannelPlugins`),
    and until then use no channel flag and there is no idle wake.
  - If there is no managed file, or it says nothing about channels: use
    `--channels plugin:mash@mash`. Measured on an organisation login with no
    managed settings, it loads and wakes an idle session in seconds. Say that
    it is not certain, and that if Claude Code shows an allowlist notice at
    start, mail still arrives at the next prompt or tool call.
  - If a managed file allows mash: use `--channels plugin:mash@mash`.
- Either way, no wake is a fine answer: use no channel flag.

Then write one command per agent, so the person types one plain word per
session and cannot leave out the address. Use the agent's own name as the
file name (`bob`, `alice`). Check first that the name is free:
`command -v bob` must print nothing; if something is there, stop and ask for
another name. Show each file before you write it. In the file, `CHANNEL` is
the flag chosen above, or nothing, and `MODE` is one of the two sentences
below; keep the full three-part address inside:

```sh
#!/bin/sh
# Start Claude Code as acme.demo.bob. Written by mash-setup.
export MASH_ENDPOINT=acme.demo.bob
if [ "$#" -eq 0 ]; then
  set -- "In one line, say you are acme.demo.bob and MODE. If Claude Code showed any channel warning or error at start, say that too. Then wait."
fi
exec claude --name acme.demo.bob "$@" CHANNEL
```

Put `CHANNEL` last. Its flag takes every following word as a channel entry, so a
prompt after it fails with "entries must be tagged".

- With a channel flag, MODE is "mail wakes you when you are idle (a channel
  was requested)".
- With none, MODE is "mail reaches you only at your next prompt or tool call
  (no channel)".

Write each file into the install folder with mode 755 (`install -m 755 FILE
"$BIN/bob"`), then check `command -v bob` finds it. The person then starts
their sessions in two terminals, with one word each (`sh person`):

```sh person
bob
```

```sh person
alice
```

Each session says on its first turn which mode it is in. `--name` shows the
agent's address as the session's name; for a session already open,
`/rename acme.demo.alice` does the same.

Say the setup is done only when one session has received mail from the other
(`mash inbox` in the second terminal shows it as verified). Do not claim mail
is broken because your own session started before the plugin was installed:
the plugin loads in the next session.

### Try it: a first job for the pair

In both setups, finish by giving the person this to paste into the first
session (the one started with `MASH_ENDPOINT=acme.demo.bob`), with the
person's own agent names in place of `bob` and `alice` (the first agent is the
lead, the second the peer):

```
Bob, you are the lead coordinator for this pair programming team and today we
will be working on a basic Hello World in Rust. Alice is your peer and she'll
be taking a look at your code and making suggestions. This Hello World needs
solid code coverage. Maybe it's best if Alice writes the tests. Talk to Alice
and come up with a plan.
```

## Remove it all

Everything mash made is under the home folder: `~/.local/bin` (the install
folder), `~/.config/mash`, `~/mash-nats`, `~/mash-setup-dl` and
`~/.local/state/mash`. Use the person's folders from step 1. Each step needs
a yes, in this order (the script is in the download folder, so that goes
last):

1. The server. Stop it, which keeps its folder:

   ```sh
   SRV="$HOME/mash-nats"; DL="$HOME/mash-setup-dl"
   bash "$DL/mash-local-nats-setup.sh" --dir "$SRV" --stop
   ```

2. The server folder, which holds every key and all stored mail. It comes
   before the download folder is deleted, because `--remove` needs the
   downloaded script. It cannot be undone, so the person runs it (day-2
   skill, "Remove the whole local server"); it asks them to type the
   folder path. Fill in the script path and the server folder:

   ```sh person
   SRV="$HOME/mash-nats"; DL="$HOME/mash-setup-dl"
   bash "$DL/mash-local-nats-setup.sh" --dir "$SRV" --remove
   ```

   If the person wants to keep the server folder, skip this step and say
   plainly that the folder and its keys stay on disk.
3. The plugin: `claude plugin uninstall mash@mash`.
4. The programs this setup installed, only the named ones. Say which route
   step 2 took and ask before removing any package or stopping any service.
   Never remove a program or service that was there before. `mash` and
   `mashctl` come from the install in step 1. Name only the installed files
   in `rm`. `nats-server` and `nats` depend on the route:

   - (a) default: the installed files, for example when this setup installed
     all four:

     ```sh
     BIN="$HOME/.local/bin"
     rm "$BIN/mash" "$BIN/mashctl" "$BIN/nats-server" "$BIN/nats"
     ```

     If the person chose another install folder in step 1, fill in that path.
     Also remove each agent's command file from step 8 (`"$BIN/bob"`,
     `"$BIN/alice"`), only the ones setup wrote: check each still starts with
     the line `# Start Claude Code as ... Written by mash-setup.` first.
   - (b) Homebrew: `brew uninstall nats-server nats`
   - (c) system package: the package manager's remove command for the
     package step 2 installed. If the package started a service (step 2 told
     the person), stopping and disabling it is a separate step, for this
     package only. A system route may need `sudo`: show the exact command
     and do not run it without a yes to that command.
   - (d) source: the files the person says they built into.
   - (e) other: ask.

   For (b) and (c), each command is shown exactly and run only after the
   person says yes to it. For (a), `mash` and `mashctl` use the same `rm`.
5. The download folder, `~/mash-setup-dl`: only the files setup downloaded
   into it, by name, then the folder if it is empty. If the person chose
   another folder in step 1, fill in that path:

   ```sh
   DL="$HOME/mash-setup-dl"
   cd "$DL" && rm -f install.sh mash-local-nats-setup.sh SHA256SUMS \
     mash.intoto.jsonl; cd "$HOME" && rmdir "$DL"
   ```

   `rmdir` fails harmlessly if the folder is not empty: a folder that was
   already there with other files stays, with those files.

6. mash's config and state folders. They hold credentials: deleting them
   cannot be undone, so the person runs it. First work out both active
   folders and show them. The config folder is `$MASH_CONFIG_DIR`, else
   `$XDG_CONFIG_HOME/mash`, else `~/.config/mash` (never `XDG_CONFIG_HOME`
   itself). The state folder is `$MASH_STATE_DIR`, else
   `$XDG_STATE_HOME/mash`, else `~/.local/state/mash` (never
   `XDG_STATE_HOME` itself). Ask the person to confirm the two paths, then
   fill them in:

   ```sh person
   CFG="<the confirmed config folder>"
   STATE="<the confirmed state folder>"
   rm -r "$CFG" "$STATE"
   ```

## Do not

- Do not pipe a download into a shell. Do not run a script you have not shown.
- Do not add `--dangerously-load-development-channels` on your own word: only
  after the person has been told what it is (step 8) and said yes.
- Do not use `sudo` or a system package, except for the route the person chose
  in step 2 and the exact command they said yes to.
  Do not run a typed confirmation or any `sh person` block yourself.
- Do not print, paste or mail a seed, a credentials file or a key. Do not read
  files in `~/mash-nats` or `~/.config/mash` aloud.
- Do not invent a name or a path. Take every path from a command's output.
- Do not stop, change or remove a NATS server this guide did not make.
- Do not use `pkill`, `killall` or `pgrep -f`.

## What this opens

The server listens on 127.0.0.1 only, but every user on this machine can reach
that port; none can connect without credentials. An agent you run as yourself
can read every key in your folders and so can make any identity and read all
mail. mash does not defend against that: if it matters, run the agent in a
sandbox. The keys are in `~/mash-nats`, and after `mashctl init` mashctl keeps
its own copies in `~/.config/mash`.

## If something is wrong

| You see | Cause | Fix |
|---|---|---|
| `error: missing: ...` from the server script | a program is not installed | step 2 |
| `error: missing: nats-server` while it is installed | the install folder is not on the PATH | run the script with `PATH="$BIN:$PATH"` in front of `bash` (a package folder from step 2 goes in front too) |
| `error: ... is writable by its group or other users: not installing there` | a folder made with umask 002 is group-writable | `chmod go-w "$BIN"` (you may run it on the folder you made, after a yes) |
| `sha256sum: ... FAILED` | the download is not the release's file | stop; do not run it; ask |
| the script used another port, such as 4223 | 4222 is taken by a server this guide did not make | fine: leave that server alone, mash uses the other port |
| `not ready: the server did not answer: <url>` (from `mash status`) | the server is not running | `mash-local-nats-setup.sh --start` (day-2 skill) |
| `error: the server did not come up` on a Mac | another user may hold the port: macOS shows you only your own listeners | ask the person to try another folder or free the port; do not stop it |
| `mash: command not found` after install | the install folder is not on the PATH | use the full path from the install line; the person adds it |
| mail does not arrive in a session | the session started before the plugin was installed | restart that session |
