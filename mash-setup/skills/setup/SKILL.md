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

For a person, or for an agent following it. Linux or macOS. One machine, one
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

Everything is installed as the local user: no `sudo`, no system package, no
system service. You run each step's commands yourself after that step's yes.

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

Ask in order, all in one message. Each has a default; the person may take
it.

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
uname -sm; command -v mash mashctl nats-server nats openssl claude; ls -d "$HOME/mash-nats" "$HOME/.local/bin" "$HOME/mash-setup-dl" 2>&1; claude plugin list 2>&1 | head -20
```

Then say this to the person, filled in, and wait for a yes:

```
I will:
  download mash and mashctl         from the latest release (one tag, checked
                                    against its checksums), into ~/.local/bin
  download nats-server and nats     from their GitHub releases (checked against
                                    each release's SHA256SUMS), into ~/.local/bin
  use no sudo                       everything is installed as you, nothing
                                    system-wide
  make a NATS server                in ~/mash-nats, this machine only, run as you
                                    (a program you run: after a reboot it is
                                     stopped; start it again with --start)
  use the owner name                acme
  install the mash plugin           from the bogworks/mash marketplace
  make two agent mailboxes          acme.demo.bob and acme.demo.alice
  start sessions plainly            without the development flag; mail arrives
                                    with the next prompt or tool call
Say yes to go ahead. I will ask again before each step.
```

## 2. The server's programs

The NATS script needs `nats-server` (2.10.7 or newer; the requirements are in
https://github.com/bogworks/mash/blob/main/docs/nats-server.md#requirements),
the `nats` command and `openssl`. If `nats-server` or `nats` is missing, tell
the person where it comes from and ask for a yes. Needs a yes: downloading
each program from its project's GitHub release into the install folder `BIN`
of step 1. You run these blocks yourself; they need no `sudo`. If `openssl`,
`curl`, `tar` or the port tool (`ss` on Linux, `lsof` on macOS), or both
`unzip` and `python3`, is missing, say so in one line and stop and ask: the
person decides how to get it.

On a Mac, Homebrew is an alternative: `brew install nats-server nats`. Then
skip the two blocks below.

`BIN` may not exist yet. Debian's default umask is 002, so a folder made with
`mkdir -p` is writable by its group, and `install.sh` refuses to install
there. `install.sh` checks every folder above it too, and `install -d -m`
sets the mode only on the folders it is given, so each block names `~/.local`
as well as `BIN` (add any other new folder above a custom `BIN`).
Each block makes them first with the right mode. Each block works in a
fresh `mktemp -d` folder: a throwaway folder. After the install you remove
only that one folder, the `T` the block set (`rm -r "$T"`), never a glob.

`nats-server` (tested v2.15.1). The block names the operating system and the
processor (`linux` or `darwin`; `amd64` or `arm64`), downloads the release
and its `SHA256SUMS`, checks the archive against them and installs it:

```sh
set -e
BIN="$HOME/.local/bin"; V=2.15.1
case "$(uname -s)" in Linux) OS=linux ;; Darwin) OS=darwin ;; esac
case "$(uname -m)" in x86_64) ARCH=amd64 ;; aarch64 | arm64) ARCH=arm64 ;; esac
SUM="sha256sum -c -"; command -v sha256sum >/dev/null || SUM="shasum -a 256 -c -"
install -d -m 755 "$HOME/.local" "$BIN"
T=$(mktemp -d) && cd "$T"
curl -fsSLO https://github.com/nats-io/nats-server/releases/download/v$V/nats-server-v$V-$OS-$ARCH.tar.gz
curl -fsSLO https://github.com/nats-io/nats-server/releases/download/v$V/SHA256SUMS
grep " nats-server-v$V-$OS-$ARCH.tar.gz\$" SHA256SUMS | $SUM
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
SUM="sha256sum -c -"; command -v sha256sum >/dev/null || SUM="shasum -a 256 -c -"
install -d -m 755 "$HOME/.local" "$BIN"
T=$(mktemp -d) && cd "$T"
curl -fsSLO https://github.com/nats-io/natscli/releases/download/v$V/nats-$V-$OS-$ARCH.zip
curl -fsSLO https://github.com/nats-io/natscli/releases/download/v$V/SHA256SUMS
grep " nats-$V-$OS-$ARCH.zip\$" SHA256SUMS | $SUM
if command -v unzip >/dev/null; then unzip -q nats-$V-$OS-$ARCH.zip
else python3 -m zipfile -e nats-$V-$OS-$ARCH.zip .; fi
install -m 0755 nats-$V-$OS-$ARCH/nats "$BIN/nats"
cd "$HOME" && rm -r "$T"
"$BIN/nats" --version
```

Each block starts with `set -e`, so a checksum that does not match stops it
before anything is unpacked or installed. Each checksum line should end in `OK`
(a mismatch: stop, install nothing, ask).
`nats-server --version` prints `nats-server: v2.15.1` and `nats --version`
prints `0.5.2`. Then check `command -v nats-server nats`. `BIN` must be on the
PATH: if it is not, tell the person to add it (do not edit their shell files);
for this session use the full path, or put `PATH="$BIN:$PATH"` in front of a
command. Both programs are explained at
https://github.com/bogworks/mash/blob/main/docs/nats-server.md#getting-nats-server-and-the-nats-command

## 3. Download and check the two scripts

Needs a yes: downloading two scripts from the mash release and checking them.
You never pipe a download into a shell. You download each file and check it
against the release's checksums here; step 4 shows each script to the person
and runs it after a yes.

Use the person's download folder for `DL` (the first assignment below).
Resolve the release tag once, and use it for the downloads and for the
installer, so all three agree even if a newer release appears in between:

```sh
DL="$HOME/mash-setup-dl"; TAG=$(curl -fsSL -o /dev/null -w '%{url_effective}' https://github.com/bogworks/mash/releases/latest); TAG=${TAG##*/}; echo "$TAG" && mkdir -p "$DL" && cd "$DL" && U=https://github.com/bogworks/mash/releases/download/$TAG && curl -fsSL -O "$U/SHA256SUMS" -O "$U/install.sh" -O "$U/mash-local-nats-setup.sh" && grep -E ' (install.sh|mash-local-nats-setup.sh)$' SHA256SUMS | sha256sum -c -
```

Say the tag back to the person (it looks like `v0.5.6`) and use that same
value as `TAG` in step 4.

The checksums come from the same download as the scripts, so on their own
they only show the files are intact. If `gh` (the GitHub CLI) is installed,
also check the release's signed attestation of `SHA256SUMS`, pinned to the
tag, before either script runs:

```sh
DL="$HOME/mash-setup-dl"; TAG=vX.Y.Z; cd "$DL" && U=https://github.com/bogworks/mash/releases/download/$TAG && curl -fsSL -O "$U/mash.intoto.jsonl" && gh attestation verify SHA256SUMS -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref "refs/tags/$TAG"
```

Replace `vX.Y.Z` with the tag from the block above (each block runs in a
fresh shell). Expected: it exits 0 and says it verified. Any failure: stop,
run nothing.
If `gh` is missing, or too old to have `gh attestation verify`, say so to the
person in one sentence: the scripts are then trusted on github.com's TLS
alone (the installer says the same). Run them only if the person says yes
to that.

Expected: `install.sh: OK` and `mash-local-nats-setup.sh: OK`. On macOS
`sha256sum` may be missing: use `shasum -a 256 -c -`. Anything else, or a
`404`: stop and ask. A mismatch means the file is not the release's file:
do not run it.

## 4. Install mash and mashctl, then make the server

Each script has one section below: show it, ask, run it. If a later release
changes how a script is run, change only its section.

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
end. A line saying `gh not found` is normal: it says the checksums come from
the same download. If `gh` is installed, step 3 can also verify the
attestation (see `docs/install.md`); if it fails, stop and ask.

Check, read-only: `mash version` and `mashctl version` print the same version.

### Run mash-local-nats-setup.sh

Show it to the person (`cat "$DL/mash-local-nats-setup.sh"`), say in one
sentence what it does (it makes a NATS server in the server folder, as the
person's own user, listening on this machine only), and ask. Needs a yes:
starting a NATS server on this machine. Then run it with the install folder
first on the PATH, so it finds `nats-server` and `nats`:

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
this now, before they reboot.

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

Expected: `Mail sent (number 1).`, then `1 message for acme.demo.alice, 1 new,
0 urgent.` and a one-row table with `lunch at noon?`.
`MASH_ENDPOINT=acme.demo.alice mash read 1` shows the message in full,
with `trust: verified`.

## 8. Use it from Claude Code

Restarting your own session is the person's act. Give them these two lines to
run in two terminals (use the names from step 1):

```sh person
MASH_ENDPOINT=acme.demo.bob claude
```

```sh person
MASH_ENDPOINT=acme.demo.alice claude
```

Mail arrives at the session's next prompt or tool call. An idle session that
nobody prompts is not woken: see the optional section below.

Say the setup is done only when one session has received mail from the other
(`mash inbox` in the second terminal shows it as verified). Do not claim mail
is broken because your own session started before the plugin was installed:
the plugin loads in the next session.

### Wake an idle session (optional)

Claude Code has its own flag, `--dangerously-load-development-channels`. It
lets a development plugin push a channel notification that wakes an idle
session. Claude Code asks the person to confirm it at each launch. It is
optional and it is the person's choice: you never add it on your own word.
Explain it, ask whether they want it, and only if they say yes give them the
line with it:

```sh person
MASH_ENDPOINT=acme.demo.bob claude --dangerously-load-development-channels plugin:mash@mash
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

   To delete the server folder too, which holds every key and all stored
   mail, the person runs `--remove` (day-2 skill, "Remove the whole local
   server"): it asks them to type the folder path.
2. The plugin: `claude plugin uninstall mash@mash`.
3. The programs this setup installed, only the named files. Say which ones
   you installed in step 2: you only install a missing `nats-server` or
   `nats`, and a package or Homebrew install is not removed by this step.
   Never remove a program that was there before. `mash` and `mashctl` come
   from the install in step 1. Name only the installed files in `rm`, for
   example when this setup installed all four:

   ```sh
   BIN="$HOME/.local/bin"
   rm "$BIN/mash" "$BIN/mashctl" "$BIN/nats-server" "$BIN/nats"
   ```

   If the person chose another install folder in step 1, fill in that path.
4. The download folder, `~/mash-setup-dl`: only that one folder. If the
   person chose another folder in step 1, fill in that path:

   ```sh
   DL="$HOME/mash-setup-dl"
   rm -r "$DL"
   ```

5. mash's config and state folders. They hold credentials: deleting them
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
- Do not use `sudo` or any system package; everything is installed as you.
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
| `error: missing: nats-server` while it is installed | the install folder is not on the PATH | run the script with `PATH="$BIN:$PATH"` in front of `bash` |
| `error: ... is writable by its group or other users: not installing there` | a folder made with umask 002 is group-writable | `chmod go-w "$BIN"` (you may run it on the folder you made, after a yes) |
| `sha256sum: ... FAILED` | the download is not the release's file | stop; do not run it; ask |
| the script used another port, such as 4223 | 4222 is taken by a server this guide did not make | fine: leave that server alone, mash uses the other port |
| `not ready: the server did not answer: <url>` (from `mash status`) | the server is not running | `mash-local-nats-setup.sh --start` (day-2 skill) |
| `error: the server did not come up` on a Mac | another user may hold the port: macOS shows you only your own listeners | ask the person to try another folder or free the port; do not stop it |
| `mash: command not found` after install | the install folder is not on the PATH | use the full path from the install line; the person adds it |
| mail does not arrive in a session | the session started before the plugin was installed | restart that session |
