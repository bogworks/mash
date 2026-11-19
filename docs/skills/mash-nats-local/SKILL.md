---
name: mash-nats-local
description: >-
  Set up a NATS server on this machine for mash, the default way: two
  questions, one script, a checked server. Use when a person or an agent
  needs a mash server and has none. mashctl does not install or start
  NATS; this does. For anything not the default, see WHAT-MASH-NEEDS.md
  beside this file.
---

# Set up a NATS server for mash (one machine, the default)

DRAFT 8 (0.5.6). For a person, or for an agent following it. Debian or
Ubuntu, 64-bit Intel or AMD. This guide is the default path only: one
machine, one user. Another port or folder, other machines and your own
NATS server are in `WHAT-MASH-NEEDS.md`; go there only if the person says
"not the default".

mash gives each AI agent session (one running Claude Code window, say)
an inbox that keeps its mail, so agents can message each other. The inbox
lives on a NATS server (an open-source message server). mash does not
install or start NATS; this guide does. You will: answer two questions,
check the prerequisites, and run one script that makes the server and
checks it. The README quick start takes it from there.

Every command block runs on its own in a fresh shell; nothing depends on
a variable or directory from an earlier block. Run them as written. A
block that fails prints a message and nothing worse. Blocks marked
`sh person` are run by the person themselves.

The sample outputs below were made by a user whose login is `alice`:
`/home/alice` is just that user's home folder. Yours will show your own
login.

If you are an agent: you may propose each answer, but the person decides.
Ask every question before you run anything that changes anything, show
the person what each block printed, and say whether it matches. A typed
confirmation (the `sh person` blocks) cannot show that a human is
present, because a program in the same session can type it. So never run
those: write the exact command out, say what it does, ask the person to
run it in their own terminal outside your session, and then check the
result with the read-only commands. Run only the helper script the guide
fetched and checked, never another copy.

## The names in this guide

Addresses are written with dots: owner.group.agent, such as
acme.demo.alice. Only the owner is chosen here. Replace `acme` in every
block with the person's owner name.

| In this guide | Meaning | Default |
|---|---|---|
| `acme` | owner: the first part of every address | your login name |
| `$HOME/mash-nats` | the server's directory | fixed here |
| `4222` | the port, this machine only | fixed here |

## What you say yes to

Never implied by "set it up". Each is asked when its step comes: using
sudo (part 2, only if a prerequisite is missing); installing software
(each source and version is shown). The script never opens a firewall
port. Never pipe a download into a shell.

## Stop and ask the person when

- a command prints FAILED, FAIL, an error, or a line you do not
  understand: look it up in "If something is wrong"
- a hash differs from the one printed here
- a path, port or program already exists and this guide did not make it
- you are asked to use root, turn off a check or skip a yes
- the machine is not Debian or Ubuntu on 64-bit Intel or AMD

If you are the person and nobody is there to ask: stop, change nothing
more, and read "If something is wrong".

## Words you will meet

- Server directory: `$HOME/mash-nats`, where the script puts everything.
- Operator, account, user: NATS's three levels of trust. The operator
  here is a key the script makes, not a person; it signs accounts. An
  account is one separate space; a user is one identity in it. The script
  makes two accounts: SYS, the server's own admin space (the system
  account), and MASH, the one mash uses.
- nsc: NATS's own tool for making operators, accounts and users. The
  script installs it in the server directory and runs it for you.
- JWT: a small signed file that says what an account or user may do.
- TLS: encryption of every connection. The server proves who it is with a
  certificate; a SAN is a name or address listed in that certificate (the
  script adds `localhost`, `127.0.0.1` and `::1`).
- CA: the certificate authority, made here by the script, that signs the
  server's certificate. Clients trust the CA.
- Fingerprint: a short code that identifies the CA file.
- Seed: a private key (starts with S). Never print, mail or paste one.
- Credentials (a `.creds` file): a user's identity plus its seed.
  Whoever holds the file is that user.
- JetStream: the part of NATS that stores messages on disk.
- Owner: the first part of every address (`acme` in `acme.demo.alice`).
- mashctl: mash's admin tool. It makes and revokes agents' mailboxes.
- Adopt: `mashctl adopt` hands a finished server to mash: it checks the
  server and copies what mashctl needs into its own folder.
- Endpoint: one agent's mailbox, with its own `.creds` file.
- Stream, consumer: a stream is where JetStream keeps one endpoint's mail;
  a consumer is a reader of a stream.
- Resolver: how the server finds account records; this one keeps them on
  disk.
- `.nk` file: a seed saved as a file (the operator and account seeds).

## What you end up with

```
  one machine
  +----------------------------------------------+
  |  nats-server: TLS, JetStream, operator mode  |
  |  keys, certificates, store, log: in YOUR dir |
  +----------------------------------------------+
```

"Operator mode" means nobody connects without credentials signed by the
operator key the script makes.

## What this opens

- The server listens on 127.0.0.1:4222. Binding 127.0.0.1 keeps it off
  the network, but it does NOT limit it to you: every user on this
  machine can reach that port. They cannot connect without credentials,
  read mail or make agents.
- An agent you run as yourself can read every key in your folders, so it
  can make any identity and read all mail. mash does not defend against
  that; if it matters to you, run the agent in a sandbox. The steps marked
  `sh person` are the ones that ask for a typed confirmation: the person
  runs them.
- The script opens no other port and does not touch a firewall. It
  writes only your server directory and `$HOME/mash-setup`.
- Do not run any of this as root: the script refuses.

## 1. Questions

Ask them in order. Each has a default; the person may take it.

1. Owner name? Default: your login name if it is lower-case letters and
   digits, otherwise pick one (lower-case letters, digits, single
   hyphens, starting with a letter, up to 32 characters). It is the
   first part of every address and cannot change later without starting
   over. Everything the script prints afterwards has it filled in.
2. May this guide use sudo to install prerequisites? Default: only if
   something in part 2 is missing; ask then.

Everything else is fixed here: directory `$HOME/mash-nats` (needs 2 GB
free: check with `df -h "$HOME"`), port 4222, this machine only, a trial
you can remove. A different answer to any of these means the non-default
path in `WHAT-MASH-NEEDS.md`.

### Read the answers back

Before running anything that changes anything, say this to the person,
filled in, and wait for a yes:

```
I will:
  make a server directory at   $HOME/mash-nats   (nothing is there yet)
  listen on                    127.0.0.1:4222     (this machine only; any
                               user here can reach the port, none can
                               connect without credentials)
  use the owner name           acme
  keep downloads in            $HOME/mash-setup
  install                      nats-server and nsc in the server dir only
  sudo                         only if a prerequisite is missing
Say yes to go ahead.
```

If the person changes the port or the directory, say the change back in
this list in plain words, then go to `WHAT-MASH-NEEDS.md`. If they want
the server reachable from other machines: this script only makes a server
for this machine (127.0.0.1). A server for other machines is one you run
yourself; see `WHAT-MASH-NEEDS.md`, section 5.

## 2. Prerequisites

The server does not need Claude Code; the README quick start checks for it
later. The script needs curl, tar, unzip, openssl, sha256sum and ss.
Read-only check of the script's programs; it prints `missing:` for each
one that is not there:

```sh
for c in curl tar unzip openssl sha256sum ss; do command -v "$c" >/dev/null || echo "missing: $c"; done; echo checked
```

Expected, on a ready machine: just `checked`. If anything is missing,
this needs the sudo yes. It installs from your distribution only:

```sh
sudo apt-get update && sudo apt-get install -y curl tar unzip openssl iproute2 ca-certificates coreutils
```

If sudo is missing or not allowed, the message is `sudo: a password is
required` or `sudo: command not found`. Stop: the person installs the
programs (as root, or by giving you sudo), then run the check again; it
must print only `checked`.

Read-only: nothing this guide would create may exist already. Each line
that prints is a stop:

```sh
for p in "$HOME/mash-nats" "$HOME/mash-setup"; do [ ! -e "$p" ] || echo "exists: $p"; done; ss -ltn | grep ':4222 ' ; echo checked
```

Expected: `checked` and nothing else. A line `exists:` or one for port
4222 means: stop and ask. Do not overwrite, move or stop it.

## 3. Make the server

Fetch the script from one exact commit and check its hash. Do not run a
script you have not checked:

```sh
mkdir -p "$HOME/mash-setup" && curl -fsSL -o "$HOME/mash-setup/nats-local-setup.sh" https://raw.githubusercontent.com/bogworks/mash/f713821435cbb23dd8c0a94c3760cd54f3541aab/scripts/nats-local-setup.sh && echo "be7a07c94474d86ccffabe0ed30892ef5fcc25350cb2b4f8edc22e9772b364f5  $HOME/mash-setup/nats-local-setup.sh" | sha256sum -c -
```

Expected: one line ending `nats-local-setup.sh: OK`. If it says
`FAILED` the file is on disk but wrong: do not run it; stop. If curl
says `404`, nothing was saved: see "If something is wrong".

The script writes only under the server directory and `$HOME/mash-setup`.
It downloads nats-server v2.15.1
(`nats-server-v2.15.1-linux-amd64.tar.gz`, from
github.com/nats-io/nats-server/releases) and nsc v2.15.0
(`nsc-linux-amd64.zip`, from github.com/nats-io/nsc/releases) and installs
them into `$HOME/mash-nats/bin`, after checking each download against a
hash written in the script; of the programs on your PATH it runs only the
prerequisites. Run it:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --owner acme --start
```

It takes about a minute, asks nothing, and prints seven steps, each with
`about to:` and `did:`:

```
[1] about to: preflight (directory, port)
    did: /home/alice/mash-nats created by this script (mode 0700, yours);
         127.0.0.1:4222 is free

[2] about to: install nats-server and nsc into /home/alice/mash-nats/bin
    (pinned, checked)
    did: downloaded nats-server-v2.15.1-linux-amd64.tar.gz sha256
         bf5edc7b1ce98e885c368cb43a886eb148dc5d0551d99f3cf6141fb5e1954c70
    did: nats-server-v2.15.1-linux-amd64.tar.gz matches the pinned sha256
    did: downloaded nsc-linux-amd64.zip sha256
         7d55eda757dc9f233675a3038fcf8779bcda99753b1603f7009fc8537e126b7e
    did: nsc-linux-amd64.zip matches the pinned sha256
    did: nats-server reports 'nats-server: v2.15.1'
    did: nsc reports 'nsc version 2.15.0'
    did: installed nats-server v2.15.1 and nsc v2.15.0 in
         /home/alice/mash-nats/bin (mode 0700; only these two paths are ever
         run)

[3] about to: make a private CA and a server certificate (ECDSA P-256, 10 years)
    did: ca.pem, ca.key, server.pem, server.key written; SANs:
         DNS:localhost,IP:127.0.0.1,IP:::1

[4] about to: make the operator, accounts and users with nsc, in an isolated
    store under /home/alice/mash-nats/nsc
    did: operator mash-operator
         (OCBECHVX5L52JYSPJ5I6PKKPCLQ535Z6UNXFN7PILAMBCL7P6J57MJL2), system
         account SYS, account MASH
         (ACNB7UU6TEXKBOJLIYC4PQJQEPKZUFYDQNFURO4YG2HMNYWPGZKGXEH7)
    did: JetStream on MASH: disk 2G, 64 streams, 128 consumers (memory storage
         unset)
    did: users: mashadmin in MASH, sys in SYS; all keys are in
         /home/alice/mash-nats/nsc/keys

[5] about to: write the server configuration and test it
    did: /home/alice/mash-nats/server.conf written (self-contained; operator JWT
         in /home/alice/mash-nats/operator.jwt) and 'nats-server -t -c' passes

[6] about to: export the files mashctl adopt takes into
    /home/alice/mash-nats/export (mode 0600)
    did: operator.nk, account.nk, sys.creds, mashadmin.creds written (contents
         not shown)

[7] about to: start nats-server detached
    did: nats-server pid 3386 listens on 127.0.0.1:4222 and requires TLS (log
         /home/alice/mash-nats/server.log, filtered)
```

The seven steps: (1) check the directory and port, (2) install the two
programs, (3) make the CA and server certificate, (4) make the operator,
account and users, (5) write and test the configuration, (6) put the
files mash's admin tool will need in `export/`, and `mash-setup.json`,
(7) start the server. It ends with a "What you hold" box (shown whole at
the end of this guide) and a "Next" line.

Check it (read-only):

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --check
```

A passing check:

```
checking the setup in /home/alice/mash-nats (creates nothing)
  ok    /home/alice/mash-nats is yours, mode 700
  ok    /home/alice/mash-nats/.mash-nats-setup is yours, mode 600
  ok    /home/alice/mash-nats/export is yours, mode 700
  ok    /home/alice/mash-nats/ca.key is yours, mode 600
  ok    /home/alice/mash-nats/export/operator.nk is yours, mode 600
  ok    /home/alice/mash-nats/export/account.nk is yours, mode 600
  ok    /home/alice/mash-nats/export/sys.creds is yours, mode 600
  ok    /home/alice/mash-nats/export/mashadmin.creds is yours, mode 600
  ok    /home/alice/mash-nats/nsc exists
  ok    /home/alice/mash-nats/server.key is yours, mode 600
  ok    /home/alice/mash-nats/ca.pem exists
  ok    /home/alice/mash-nats/server.pem exists
  ok    /home/alice/mash-nats/server.conf exists
  ok    /home/alice/mash-nats/operator.jwt exists
  ok    /home/alice/mash-nats/resolver exists
  ok    /home/alice/mash-nats/jetstream exists
  ok    server.pem verifies against ca.pem for TLS server use
  ok    /home/alice/mash-nats/bin: nats-server and nsc match their recorded
        sha256, yours, mode 700, nothing else there
  ok    tool versions are current (v2.15.1, v2.15.0)
  ok    nats-server -t -c: nats-server: configuration file
        /home/alice/mash-nats/server.conf is valid
  ok    server pid 3386 runs exactly nats-server -c server.conf
  ok    127.0.0.1:4222 answers and requires TLS
  ok    pid 3386 listens on 127.0.0.1:4222 (the client port)
check passed
```

Every line starts `ok` and the last is `check passed`. A `skip` line
means the server is stopped (not a failure). A `FAIL` line is a problem:
find it in "If something is wrong".

### Where the keys are, and what a leak exposes

The signing keys are in one place: the server directory,
`$HOME/mash-nats`. Whoever can read a file there can do what its line says
(they are all mode 0600 in a directory only you can open):

- `ca.key`: pass as the server to any client that trusts this CA.
- `nsc/` (the key store, with the operator and account signing keys): make
  any account or user, so any identity.
- `export/operator.nk`, `export/account.nk`: the same power, as seeds.
- `export/mashadmin.creds`: read all mail and make streams in the mash
  account.
- `export/sys.creds`: administer the server's accounts.
- `server.key`: pass as the server (with its certificate).
- `jetstream/`: every stored message.

They are in `$HOME/mash-nats` now. The README quick start's first step
(`mashctl adopt --from`) copies what mashctl needs into `$HOME/.config/mash`;
after that there are two places, and both hold keys. Copying either to a
backup is your call: a copy holds every key above, so keep it as private as
the original. mash has no backup or restore command and does not test
restoring from a copy. If this machine is lost: run the helper again and
reissue every endpoint. Certificates last ten years; after that clients
fail with `x509: certificate has expired` and there is no renew command, so
you run the helper again. `WHAT-MASH-NEEDS.md` has a table of what losing
each file costs.

## 4. Stop, start and remove

Three script commands, with the same `--dir`. Stop acts only on the
process it recorded.

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --stop
```

Expected: `about to: stop nats-server pid N` and `did: stopped pid N`. Run
again, it says the pid file is stale and leaves it alone, and exits 0.

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --start
```

Expected: one `did:` line, `nats-server pid N listens on 127.0.0.1:4222 and
requires TLS (log ... filtered)`. Then `--check` (part 3) must pass. Each
start keeps the last two log files, and the log is kept at most 7 days.

The server survives logging out of SSH or closing the terminal: it ignores
the hangup, and its output goes only to `server.log`. It does not survive
a reboot; run `--start` again after one (there is no start-at-boot
here).

To remove it: **you run this one yourself** (the script asks you to type
the directory path and refuses without a terminal). It stops the server
it recorded and deletes only the directory it made:

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --remove
```

Expected: it prints what it will remove, then `about to: stop nats-server
pid N`, `did: stopped pid N` and `did: removed <dir>`. With no terminal it
refuses and exits 4.

### Doing it again

`--remove` leaves `$HOME/mash-setup` (the script you fetched) in place, and
part 2's last check wants it absent. To start over, you run these two
yourself, then go back to part 3. If you already ran `--remove`, the first
says the directory does not exist and changes nothing: go on to the
second.

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --remove
```

```sh person
rm -r "$HOME/mash-setup"
```

## What next

You now hold: the server directory `$HOME/mash-nats` (the running
server, its keys and its mail, listed above) and the script in
`$HOME/mash-setup`. Next: the README quick start, step 1:
https://github.com/bogworks/mash/blob/v0.5.6/README.md#quick-start-one-machine-no-account

## What you hold

The script ends by printing this (the real output of the test, after
`Done.`; your paths differ). Read it back to the person:

<!-- closing-begin -->
```
Done. The setup is in /home/alice/mash-nats. Nothing above printed a seed or a
credential, and nothing below does.

Binding 127.0.0.1 keeps the server off the network but does not limit it to you:
every user on this machine can reach port 4222. They cannot connect without
credentials, read mail or make agents.

A process running as you can still change files under the script while it runs;
this script does not defend against that.

Everything is in /home/alice/mash-nats; run --check to list it.

What you hold
  Server address     tls://localhost:4222
  Owner              acme
  CA file            /home/alice/mash-nats/ca.pem
  CA fingerprint     sha256:ba59e4df f73610f0 27757e46 1fe4e76e 89ff25cd
                     c45facd4 48706b18 388e6a63
  Server directory   /home/alice/mash-nats
                     (mode 0700. ca.key and the nsc store stay in it: a process
                     running as you can read them, and no step here moves them)

The CA fingerprint is what a person on ANOTHER machine types to trust this
server; this machine does not ask for it.

Next: follow the README quick start, step 1:
https://github.com/bogworks/mash/blob/v0.5.6/README.md#quick-start-one-machine-no-account

Other commands: --stop, --start, --check, --remove, --help.
```
<!-- closing-end -->

## If something is wrong

Find the line you saw. Every fix below was run, and the check passed
afterwards.

| You see | Cause | Fix |
|---|---|---|
| `FAIL ... mode 755, want 700` (the server directory) | others can read the directory | `chmod 700 "$HOME/mash-nats"`, then `--check` |
| `FAIL ... mode 644, want 600` on a file in `export/` | a secret readable by others | `chmod 600` that file; tell the person |
| `FAIL missing .../server.pem` and a `nats-server -t -c` FAIL | a file the config names is gone | put it back; if lost, set up again |
| `skip nothing listens on 127.0.0.1:4222` | the server is stopped | `--start` (part 4) |
| `FAIL 127.0.0.1:4222 answers but the server recorded for this directory is not running` | something else holds the port | find what it is and ask the person; `--stop` another directory's server if it is one of ours |
| `port 127.0.0.1:4222 is already taken by something` at create | another program holds the port | give `--port N`, or ask the person to stop it; never stop it yourself |
| `... already exists: this script creates --dir itself` | the path exists, even empty | pick a new path |
| `needs at least 3 path components` or `is writable by group or others` | `--dir` is `/`, `/tmp` or similar | use a path under `$HOME` |
| `sudo: a password is required`, `is not in the sudoers file`, `sudo: command not found` | no sudo here | ask the person to run the install line as root, or install the programs |
| `curl: (22) ... 404` fetching the script | the commit is not at that address (typo, or it was moved) | copy the block again; if still 404, use the same URL with `v0.5.6` in place of the commit (once that tag exists): the hash line still checks it and says `FAILED` for any other file |
| `x509: certificate has expired` | the ten-year certificate ran out | see "Certificates last ten years" in part 3 |

To see why the server will not start, read the last lines of
`$HOME/mash-nats/server.log`. A healthy log has `TLS handshake error: EOF`
lines: those are the script's own `--check` probes.

## Do not

- Do not run these programs as root, or in `/`.
- Do not put seeds or credentials in a message, a log or a chat.
- Do not use `pkill`, `killall` or `pgrep -f` to stop NATS.
- Do not run the script before the person has answered the questions.
