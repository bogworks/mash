---
name: day-2
description: >-
  Run mash day to day: check that it works, start or stop the local NATS
  server, add or remove an agent mailbox (endpoint), see why mail is not
  arriving. Use when the person asks to add an agent, give a session a
  mailbox, see mash status, fix mail delivery, or bring the server back after
  a reboot, and `mash version` works. If mash is not installed, use the setup
  skill instead.
---

# Run mash day to day

For a person, or for an agent following it. This covers a mash set up by the
setup skill: a NATS server in `~/mash-nats` (or the folder the person chose),
run as the person's own user. If `mash` is not installed, say so, make nothing,
and point to the setup skill. If `mashctl status` shows no server, tell a
stopped server from an unconfigured one: if the server folder exists and holds
its `mash-setup.json`, the server was set up and is stopped (after a reboot,
say), so use "The server does not answer" below; only when there is no such
folder, make nothing and point to the setup skill.

You may run every read-only command below. A command that changes something
needs the person's yes first. Blocks marked `sh person` are for the person to
run in their own terminal outside your session: write the exact command, say
what it does and what will be lost, ask them to run it, then check with the
read-only commands. A typed confirmation cannot show a human is present, so
you never run those yourself, and you never use `--yes` to skip one.

Take every path from a command's output or from the person, never from memory.
The server folder is the one the setup printed (`~/mash-nats` by default) and
the download folder is the one the person chose (`~/mash-setup-dl` by
default). The blocks below set them as `SRV` and `DL` in their first line: use
the person's absolute paths there.

## Is it working

```sh
mashctl status; mashctl doctor; MASH_ENDPOINT=acme.demo.bob mash doctor
```

`mashctl status` is one line: `<server>: ok, N endpoints, M online`.
`mashctl doctor` checks the server side (storage, endpoint records, keys) and
changes nothing. `mash doctor` checks, for one endpoint, its configuration,
credentials, the server and sign-in, says how to fix what is wrong, and changes
nothing. `mash status --endpoint
acme.demo.bob` says `acme.demo.bob: ready, offline` (or `online` while a
session holds it open). Read the `hint:` line of any error and run the command
it names.

## The server does not answer (after a reboot, say)

The server is a program running as the person. It does not come back after a
reboot. Check (`mashctl doctor` names what is wrong on the server side), then
start it:

```sh
SRV="$HOME/mash-nats"; DL="$HOME/mash-setup-dl"
bash "$DL/mash-local-nats-setup.sh" --dir "$SRV" --start
```

`--start` never creates a server: if the folder is missing it says so and
stops. Needs a yes: starting the server. If the script is no longer there,
download it again and check it as the setup skill's step 3 does.

## Stop the server

Needs a yes: stopping it ends mail delivery until it is started again.

```sh
SRV="$HOME/mash-nats"; DL="$HOME/mash-setup-dl"
bash "$DL/mash-local-nats-setup.sh" --dir "$SRV" --stop
```

It stops only the server it started, and says `not running` when there is
none. Mail already sent is kept on disk and arrives after the next start.

## Add an agent mailbox

Ask for the address first: `owner.group.name`, the owner being the one set up
(`mashctl endpoint list` shows it). Say the address back and get a yes.

```sh
mashctl endpoint create acme.demo.carol
```

Expected: `Created endpoint acme.demo.carol`. Check:
`MASH_ENDPOINT=acme.demo.carol mash status` says `ready, offline`. The
person then starts a session as that agent (`sh person`):

```sh person
MASH_ENDPOINT=acme.demo.carol claude --dangerously-load-development-channels plugin:mash@mash
```

An address that was deleted can never be used again; pick another name.

## See the endpoints

```sh
mashctl endpoint list
```

## Remove an agent mailbox

This deletes the endpoint, its stored mail and its credentials, and the
address can never be used again. The person runs it; it asks them to type the
address:

```sh person
mashctl endpoint delete acme.demo.carol
```

Then check `mashctl endpoint list` no longer shows it.

## Remove the whole local server

This stops the server and deletes its folder, which holds every key and all
stored mail. The mailboxes stop working. The script asks the person to type
the folder path and refuses without a terminal:

```sh person
SRV="$HOME/mash-nats"; DL="$HOME/mash-setup-dl"
bash "$DL/mash-local-nats-setup.sh" --dir "$SRV" --remove
```

mash keeps its own copies of the keys in its config folder; they open nothing
once the server is gone. Find the active config folder first, read-only: an
absolute `$MASH_CONFIG_DIR`, else an absolute `$XDG_CONFIG_HOME/mash`, else
`$HOME/.config/mash`.

```sh
case "${MASH_CONFIG_DIR:-}" in /*) C=$MASH_CONFIG_DIR ;; *) case "${XDG_CONFIG_HOME:-}" in /*) C=$XDG_CONFIG_HOME/mash ;; *) C=$HOME/.config/mash ;; esac ;; esac; echo "$C"; ls -d "$C"
```

To start again, the person first moves that folder aside (the path the
command printed), or `mashctl init` refuses the new server's operator key:

```sh person
C=/path/printed/above
mv "$C" "$C.old"
```

They may remove the download folder too for a fresh download. Then follow
the setup skill from step 1.

## Mail does not arrive

1. `MASH_ENDPOINT=<address> mash doctor`, for the receiver. Fix what it
   names.
2. `mash status --endpoint <address>` for the receiver. `not ready: the server
   did not answer` means start the server (above).
3. A session started before the plugin was installed has no mash tools:
   restart that session.
4. `mash inbox --endpoint <address>` shows what is waiting. Mail to an offline
   endpoint is kept and shown when its session starts.

## Upgrade

Needs a yes. Run the setup skill's step 3 (resolve one tag, download and check
the scripts) and then only the installer of step 4, with `MASH_VERSION` set to
that tag. It replaces `mash` and `mashctl`; the server and the mailboxes are
kept. Never run the server-creation command: it refuses an existing folder.

If the machine ran mash 0.5.5, its configuration folder must be migrated once
before the new programs will read it (they refuse it and say so). Download the
migration script, check it, show it to the person, and run it after a yes:

```sh
DL="$HOME/mash-setup-dl"; TAG=vX.Y.Z; cd "$DL" && U=https://github.com/bogworks/mash/releases/download/$TAG && curl -fsSL -O "$U/mash-migrate-0.5.5.sh" && grep ' mash-migrate-0.5.5.sh$' SHA256SUMS | sha256sum -c -
bash "$DL/mash-migrate-0.5.5.sh"
```

(`TAG` is the tag from step 3; run the second line only after the yes.) Then
run `claude plugin marketplace update mash` and
`claude plugin update mash@mash`, and restart the sessions.

## Do not

- Do not run a `sh person` block, `sudo`, or `--yes` yourself.
- Do not print, paste or mail a seed, a credentials file or a key.
- Do not stop, change or remove a NATS server this guide did not make.
- Do not use `pkill`, `killall` or `pgrep -f`.
- Do not delete files by hand in the server folder or mash's config folder.
