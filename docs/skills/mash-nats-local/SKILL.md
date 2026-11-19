---
name: mash-nats-local
description: >-
  Set up a NATS server on this machine for mash: questions first, then
  one helper script, hand-over to mashctl, and a two-agent test. Use when
  a person or an agent needs a mash server and has none. mashctl does not
  install or start NATS; this does.
---

# Set up a NATS server for mash

DRAFT 5 (0.5.6). For a person, or for an agent following it. Debian or
Ubuntu, 64-bit Intel or AMD only.

mash gives each AI agent session (one running AI assistant, such as one
Claude Code window) an inbox that keeps its mail, so agents can message
each other. The inbox lives on a NATS server. mashctl is mash's admin
tool: it makes agents' mailboxes on that server. mashctl does not
install or start NATS. This guide does, in six parts:

1. Answer the questions, and read the answers back.
2. Install the prerequisites.
3. Run one script that makes the server.
4. Install mash and mashctl.
5. Hand the server to mashctl, then test it with Alice and Bob.
6. Look after it: stop, start, back up, restore, upgrade, remove.

Then "What next" tells you how to use mash for real.

Every command block below runs on its own in a fresh shell. Nothing
depends on a variable or a directory set by an earlier block. Run the
blocks exactly as written; replace the names as the next section says.
A block that fails prints a message and nothing worse; it never closes
your terminal.

If you are an agent: you may propose each answer, but the person
decides. Ask every question before you run anything that changes
anything. Do not pick paths or ports for them. After every block, show
the person what it printed and say whether it matches the expected
output below it. Never keep a result to yourself.

A typed confirmation (the commands marked "person" below) cannot show
that a human is present: a program in the same session can type it. So
an agent must never run those commands. It writes the exact command out,
says what it will do, and asks the person to run it in their own
terminal, outside the agent's session, and then to say that it finished.
The agent then checks the result with the read-only commands.

Where this guide says "the test", it means the test that ships with it,
`scripts/mash-nats-local-guide_test.sh`: it runs every block of this
guide, one by one, in a fresh Debian container, and every sample output
here is from that run.

## The names in this guide

One sheet, used everywhere. Addresses are written with dots,
owner.group.agent, such as acme.demo.alice. Replace each name in every
block if the person's answers differ.

| In this guide | Meaning | Replace with |
|---|---|---|
| `acme` | owner: the first part of every address | your owner name |
| `demo` | group: agents that may mail each other | your group |
| `alice`, `bob` | the two test agents | any two names |
| `$HOME/mash-nats` | the server's directory | your directory |
| `4222` | the client port | your port |
| `$HOME/.config/mash` | mashctl's folder | your choice |

`alice` and `bob` are throwaway test agents; your real agents come in
"What next". You can keep or delete them.

## What you must say yes to, one at a time

Never implied by "set it up". Each is asked when its step comes:

- installing software (the source, version and exact command are shown)
- using sudo (part 2 only: `apt-get install` of prerequisites)
- listening on anything but this machine
- opening a firewall port (the script never does)
- a start-at-boot service

Reading is always allowed before the questions end: the checks marked
"read-only" below change nothing. Never pipe a download into a shell.

## Stop and ask the person when

- a command prints FAILED, FAIL, an error, or a line you do not
  understand: look it up in "If something is wrong" at the end first
- a hash differs from the one printed here
- a path, port or program already exists and this guide did not make it
- you are asked to use root, to turn off a check, or to skip a yes
- the machine is not Debian or Ubuntu on 64-bit Intel or AMD

## Words you will meet

- NATS: an open-source message server. mash keeps its mail on one.
- nats-server: the NATS server program. nsc: NATS's own tool for making
  keys and accounts. The script downloads both, checked, into your server
  directory only.
- JetStream: the part of NATS that stores messages on disk. Its units:
  a stream holds messages, a consumer reads one, a bucket is a small
  key-value store. mash makes them; you only meet their counts in limits.
- TLS: encryption on the connection, as in HTTPS. mash requires it.
- Certificate and CA: the server's ID card, and the authority (made here,
  by you) whose signature clients trust. Clients are given the CA file.
- Operator, account, user: NATS's three levels of trust. The operator
  signs accounts. An account is one separate space; mash uses one. A
  user is one identity in an account.
- System account: a built-in account for running the server. mashctl
  uses it to revoke a lost agent key.
- Resolver: the part of the server that looks up accounts.
- JWT: a signed token that says who someone is and what they may do.
- Seed: a private key, starting with S (a `.nk` file is a NATS key).
  Never print it, mail it or paste it into a chat.
- Credentials (a `.creds` file): a user's JWT plus its seed. Whoever
  holds the file is that user.
- Endpoint: one agent's mailbox on the server, with an address. Each
  has its own key and its own `.creds` file. `--make-key` makes that key
  for you.
- Attestation: a signed statement, made by the project's own build, that
  a file came from it. `gh attestation verify` checks it.
- Linger: a systemd setting that keeps your services running after you
  log out.

## What you end up with

```
  one machine
  +----------------------------------------------+
  |  nats-server: TLS, JetStream, operator mode  |
  |  keys, certificates, store, log: in YOUR dir |
  +----------------------+-----------------------+
                         |
                    mashctl adopt --from
                         |
       mash group "demo":  Alice  <---->  Bob
```

## What this opens

- The server listens on one address and port. Default: 127.0.0.1:4222,
  this machine only. In the test, `ss -ltn` showed exactly
  `127.0.0.1:4222` and nothing else.
- Other users of this machine can reach that port. They need credentials
  to get in; they cannot read mail or make agents without them.
- `--listen ::1` listens on IPv6 loopback only.
- Any other address, so other machines can connect, makes the server
  listen on every interface (the test showed `*:4303`), whatever you
  named. The script then makes you type the address at a terminal. Give
  each client the CA file, and open a firewall port yourself if you want
  one.
- The script opens no other port. It does not touch a firewall, a
  service manager (unless you ask for boot start) or any file outside
  your server directory and `$HOME/mash-setup`, plus the one file each
  of `--backup`, `--retire-keys` and `--enable-boot` writes (part 6).
  mashctl and mash write mashctl's folder; the install block writes
  `$HOME/.local/bin`.
- Do not run the script as root: it refuses.

## 1. Questions

Ask them in order. Each has a default and says what the answer changes.

1. Trial or permanent?
   - Default: trial. You can remove it with one command, and it does not
     return after a reboot.
   - Permanent: also do the key archive and backup (parts 5 and 6) and
     decide about boot start. Either way, mashctl's folder is never in a
     backup.
2. Is mash already set up on this machine?
   - Default: no. Part 2's read-only check looks for it.
   - Yes, with another server: use a different mashctl folder (question
     5) so you do not overwrite it.
3. Which directory holds the server's files?
   - Default: `$HOME/mash-nats`. It must not exist yet; the script makes
     it (mode 0700). Never `/`, never under `/tmp`, never a top-level
     directory like /demo. Needs 2 GB free; check with
     `df -h "$HOME"`.
4. Which port?
   - Default: 4222. If taken, pick another and never stop what holds it.
5. Where does mashctl keep its files?
   - Default: `$HOME/.config/mash`. Another folder: set
     `MASH_CONFIG_DIR` in every mashctl and mash command.
6. Who may connect?
   - Default: this machine only. Others: a separate yes. The person
     gives the address the server listens on (for example the machine's
     LAN address) and the name clients will type (a host name that
     reaches it, or the same IP); both go in the certificate and on the
     command line (`--listen`, `--host`).
7. Owner name?
   - Default: your login name if it is lower-case letters and digits;
     otherwise pick one. Lower-case letters, digits, single hyphens,
     starting with a letter, up to 32 characters. One per server;
     changing it means starting over.
8. Group and agent names for the test?
   - Default: group `demo`, agents `alice` and `bob`. Real ones come
     later ("What next").
9. May this guide use sudo to install prerequisites?
   - Default: only if something in part 2 is missing; ask then.

### Read the answers back

Before running anything that changes anything, say this to the person,
filled in, and wait for a yes:

```
I will:
  make a server directory at   $HOME/mash-nats   (nothing is there yet)
  listen on                    127.0.0.1:4222     (this machine only)
  use the owner name           acme
  use the group and agents     demo, alice, bob
  keep mashctl's files in      $HOME/.config/mash
  keep downloads in            $HOME/mash-setup
  this is a                    trial / permanent
  sudo                         only if a prerequisite is missing
  install                      nats-server and nsc in the server dir only;
                               gh, mash, mashctl in $HOME/.local/bin
  boot start                   no
  write                        one key archive in my home directory
                               (permanent only; I move it off this machine)
Say yes to go ahead.
```

## 2. Prerequisites

The script needs curl, tar, unzip, openssl, sha256sum and ss. Read-only
check; it prints `missing:` for each one that is not there:

```sh
for c in curl tar unzip openssl sha256sum ss; do command -v "$c" >/dev/null || echo "missing: $c"; done; echo checked
```

Expected, on a fresh machine: some `missing:` lines, then `checked`. On a
ready machine: just `checked`. If anything is missing, this needs the
sudo yes. It installs from your distribution only:

```sh
sudo apt-get update && sudo apt-get install -y curl tar unzip openssl iproute2 ca-certificates coreutils
```

If sudo is missing or not allowed for you, the message is
`sudo: a password is required` (no rights) or `sudo: command not found`
(not installed): stop and ask the person to install the programs.

Run the check above again: it must print only `checked`.

Read-only: nothing must exist already that this guide would create.
Each line that prints is a stop:

```sh
for p in "$HOME/mash-nats" "$HOME/mash-setup" "$HOME/.config/mash" "$HOME/.local/bin/mash" "$HOME/.local/bin/mashctl" "$HOME/.local/bin/gh"; do [ ! -e "$p" ] || echo "exists: $p"; done; ss -ltn | grep ':4222 ' ; echo checked
```

Expected: `checked` and nothing else. A line `exists:` or a line for
port 4222 means: stop and ask. Do not overwrite, move or stop it. If
`$HOME/.config/mash` exists, mash is already set up here (question 2).

## 3. Make the server

Fetch the script from one exact commit and check its hash. Do not run a
script you have not checked:

```sh
mkdir -p "$HOME/mash-setup" && curl -fsSL -o "$HOME/mash-setup/nats-local-setup.sh" https://raw.githubusercontent.com/bogworks/mash/3a43a4ad8f22dd48a73df109ee8496a95a444162/scripts/nats-local-setup.sh && echo "f945bffe21b6238210260a1a5108dd1c598179cf6b756bdedda503822c0d8fd4  $HOME/mash-setup/nats-local-setup.sh" | sha256sum -c -
```

Expected: one line ending `nats-local-setup.sh: OK`. If it says
`FAILED`, or curl says `404`, stop. A `FAILED` leaves the downloaded file
on disk: do not run it; move it to `$HOME/archive/` and ask the person. A
`404` saved nothing: the URL or commit is wrong; copy the block again.

The script only writes under the directory you give it, plus
`$HOME/mash-setup`. It installs two programs, nats-server v2.15.1 and
nsc v2.15.0, into `$HOME/mash-nats/bin`, after checking each download
against a hash written in the script. Of the programs on your PATH it
runs only the ones in the prerequisites list; it runs none of the
NATS tools it finds there.

Run it with your answers:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --owner acme --start
```

Leave out `--start` to start the server yourself (part 6). For question
4 add `--port N`; for question 6 add `--host NAME` and `--listen ADDRESS`
(the script asks the person to type the address). It takes about a
minute, asks nothing for the default, and prints seven steps. Each step
says `about to:` and then `did:`:

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
accounts and users, (5) write and test the configuration, (6) put the
files mashctl needs in `export/` and `mash-setup.json`, (7) start the
server. It ends with a
list of files, a "What you hold" box and a "Next" list, shown whole at
the end of this guide. The "Next" list tells you to run `mashctl adopt --from`:
that is part 5, after part 4 installs mashctl.

Check it (changes nothing):

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

Every line starts with `ok`. A line that starts with `FAIL` is a
problem: find its text under "If something is wrong". A stopped server
shows as a `skip` line, not a `FAIL`: start it (part 6). The last line
of a failing check is `nats-local-setup: check: N problem(s)`.

### Every file, and what it exposes if it leaks

Everything in the server directory is under `$HOME/mash-nats` (mode
0700). "Lost (every copy)" means this file and every backup you made.

- `ca.key`: the CA's private key. **If it leaks:** someone can make a
  certificate the clients trust, and pose as your server. **Lost (every
  copy):** you cannot make a new server certificate with this CA.
- `nsc/`: every key and JWT the script made, including the operator's
  and the account's. **If it leaks:** someone can make accounts and
  users, and so agents, without mashctl. **Lost (every copy):** you can
  no longer make accounts or users by hand; the running server and
  mashctl keep working.
- `export/`: copies of four files for mashctl to adopt:
  `operator.nk` (the operator signing seed), `account.nk` (the MASH
  account's seed), `mashadmin.creds` (the admin user) and `sys.creds`
  (the system user), and the CA file. After part 5, mashctl has its own
  copies (next entry), so `export/` is spare. **If they leak:** see the
  next entry; they are the same keys.
- **mashctl's folder, `$HOME/.config/mash`** (made in part 5): the
  **custody truth**. mashctl signs every agent it makes, so this folder
  keeps the operator signing seed, the account seed and the system and
  admin credentials, in `keys/operator.nk`, `keys/account.nk`,
  `server/sys.creds` and `server/mashadmin.creds`, plus the CA,
  `config.json` and one `.creds` file per agent. Modes: the folder,
  `keys/` and `server/` are 0700; every file is 0600 (the CA 0644).
  Seeds are never printed. These stay for as long as you use mash.
  **If this folder leaks:** whoever holds it can mint any identity in the
  account, read every agent's mail, and revoke or create keys. It is as
  serious as a leak of the whole server directory's keys. It is left out
  of backups and sync by name. **Lost (every copy):** mashctl cannot
  create, revoke or administer agents; the server and the agents you
  already have keep working. On a single-user machine this is acceptable.
  On a shared host, or with mash across machines, the signing keys must
  live somewhere else; this guide does not cover that.
- Agents' creds files, `acme.<group>.<agent>.creds`, in mashctl's folder:
  **if one leaks:** someone can send and read as that agent. **Lost:**
  `mashctl endpoint reissue` makes a new one.
- `server.key`: the server's private key. **If it leaks:** someone can
  pose as your server. **Lost:** the server cannot start; you make a new
  server directory, and with it a new account, so you lose the agents.
- `server.pem`, `ca.pem`, `server.conf`, `operator.jwt`, `resolver/`:
  not secret. `ca.pem` is what clients need; give it to them. **Lost:**
  the server cannot start, or clients cannot trust it.
- `jetstream/`: all the mail. Not a key, but private. **Lost:** every
  stored message.
- `server.log`, `server.pid`: the log (lines that look secret are held
  back) and the process id. Not secret. `server.log` is where to look
  when the server does not start.
- `bin/`: the two programs the script installed. **Lost:** run
  `--upgrade` to get them again.

Certificates last ten years: the CA and the server certificate expire
about ten years after the script ran. When they do, clients stop
connecting with an `x509: certificate has expired` error. There is no
renew command in 0.5.6: you make a new server directory (the same
steps), or make a new server certificate by hand from `ca.key`, which is
why you keep the archive in part 5. Accounts and users carry no expiry.

## 4. Install mash and mashctl

Check what you have (read-only). Expected: two "No such file" errors and
`checked`:

```sh
"$HOME/.local/bin/mash" version; "$HOME/.local/bin/mashctl" version; echo checked
```

If one prints a version, stop and ask (question 2). Debian and Ubuntu put
`$HOME/.local/bin` on your PATH at your next login, when the folder
exists; until then every block here spells the full path.

The releases are signed by the project's own build, and you check that
with `gh`, GitHub's tool. Debian's own `gh` is too old, so this block
uses the pinned one (v2.105.0, from the GitHub CLI project). One block
does everything, in one new private folder (mode 0700): it downloads
`gh` and checks its hash, downloads mash, mashctl and the signed record,
verifies both programs against the record (no GitHub login is needed:
the record is a file), installs them, then verifies the installed copies
again, so a swap between steps is caught. If the second check fails, it
moves the installed files to `$HOME/mash-setup/rejected`. It stops at
once if `gh`, `mash` or `mashctl` is already in `$HOME/.local/bin`. It
needs the install yes. If you do not get `installed and verified`, do
not use the programs:

```sh
( set -eu; H="$HOME/mash-setup"; B="$HOME/.local/bin"; V=v0.5.6; G=gh_2.105.0_linux_amd64; A="-R bogworks/mash --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/$V"; mkdir -p "$H" "$B"; for x in gh mash mashctl; do [ ! -e "$B/$x" ] || { echo "already there: $B/$x"; false; }; done; w="$H/install-$$"; mkdir -m 0700 "$w"; cd "$w"; curl -fsSLO "https://github.com/cli/cli/releases/download/v2.105.0/$G.tar.gz"; echo "79e7ab5db738c48777b696e374f8b428f0647debfb424f44d2f426370407fe2f  $G.tar.gz" | sha256sum -c -; tar -xzf "$G.tar.gz"; for f in mash-linux-amd64 mashctl-linux-amd64 mash.intoto.jsonl; do curl -fsSLO "https://github.com/bogworks/mash/releases/download/$V/$f"; done; for f in mash-linux-amd64 mashctl-linux-amd64; do "$G/bin/gh" attestation verify "$f" --bundle mash.intoto.jsonl $A; done; install -m 755 "$G/bin/gh" "$B/gh"; install -m 755 mash-linux-amd64 "$B/mash"; install -m 755 mashctl-linux-amd64 "$B/mashctl"; for p in mash mashctl; do "$G/bin/gh" attestation verify "$B/$p" --bundle mash.intoto.jsonl $A || { mkdir -p "$H/rejected"; mv "$B/gh" "$B/mash" "$B/mashctl" "$H/rejected/"; false; }; done; echo "installed and verified in $B" ) || { echo "NOT installed, or NOT verified: do not use these programs"; false; }
```

Expected: `gh_2.105.0_linux_amd64.tar.gz: OK`, then, for each of the four
verifications, nothing (in a pipe) or a "Verification succeeded" report
(in a terminal), then `installed and verified in` your `.local/bin`. A
`FAILED` hash, a `Verification failed` or `404` line, or the last
`NOT installed` line means: stop, show the person. Check the result:

```sh
"$HOME/.local/bin/mashctl" version
```

Expected: one line with mashctl's version, 0.5.6.

Two other ways to get the programs, `go install` and the Claude Code
plugin, are not checked by this block. They are for developers; they
work, but nothing here verifies them.

## 5. Hand the server to mashctl, then test

The script wrote `mash-setup.json` in the server directory (the server
address, owner and account; no secrets). `mashctl adopt --from` reads it
and the files it names. First the read-only check; it changes nothing,
does not prompt, and an agent may run it. Every check must say `ok`:

```sh
"$HOME/.local/bin/mashctl" adopt --from "$HOME/mash-nats" --dry-run
```

Expected: numbered checks, each `ok` (files; the server refuses a
connection without credentials and accepts the system credentials; the
account exists; JetStream is given; the system user works; the operator
key signed the account; the admin creds connect), then `All checks
passed.` and a list of what adopt would do, and `Would ask you to type
the CA fingerprint`. A failed check says what was wrong and that nothing
was changed.

Now the real one. This one is the person's. adopt makes the person type
the CA fingerprint at a terminal, to prove they know which server they
trust. An agent in its own session cannot honestly type it for them, and
adopt refuses without a terminal. So the agent does not run this block.
It shows the person the exact command and the "CA fingerprint" line from
the script's "What you hold" (or prints it with `openssl x509 -in
"$HOME/mash-nats/ca.pem" -noout -fingerprint -sha256`), asks them to run
the command in their own terminal, compare the fingerprint, type it,
press Enter, and say when it finished:

```sh person
"$HOME/.local/bin/mashctl" adopt --from "$HOME/mash-nats"
```

Expected: the same checks, then a `matches` line for the fingerprint,
`All checks passed.`, one `copied` line per file (the CA, the two keys,
the two credentials, with modes), a `config.json` line, and a line that
mash's storage is ready. adopt copies everything into mashctl's folder
(default `$HOME/.config/mash`; files 0600, the CA 0644), so `export/` is
no longer needed. A server you manage by hand, with no `mash-setup.json`,
uses the long form instead: `mashctl adopt --server tls://HOST:PORT
--owner NAME --account NAME --ca FILE --operator-key FILE --account-key
FILE --system-credentials FILE --admin-credentials FILE`. The two forms
cannot be mixed.

The modes of mashctl's folder: it holds keys, so it must not be open to
others:

```sh
stat -c '%a %n' "$HOME/.config/mash" "$HOME/.config/mash"/*
```

Expected: `700` for the folder, `keys` and `server`, and `600` for
`config.json`. If a number is wrong, run `chmod 700` (or `600`) on that
path and tell the person.

### Alice and Bob

The group `demo` comes into being when the first agent uses it. `--make-key`
makes the agent's own key on this machine and signs it:

```sh
"$HOME/.local/bin/mashctl" endpoint create acme.demo.alice --make-key --creds-out "$HOME/.config/mash/acme.demo.alice.creds"
```

Expected: a line saying the endpoint was created, its address
`acme.demo.alice`, and the path of its `.creds` file in mashctl's
folder. Use the address shown with the dots.

```sh
"$HOME/.local/bin/mashctl" endpoint create acme.demo.bob --make-key --creds-out "$HOME/.config/mash/acme.demo.bob.creds"
```

```sh
"$HOME/.local/bin/mashctl" endpoint list
```

Expected: two lines, `acme.demo.alice` and `acme.demo.bob`, each with
its creation time, then `2 endpoints`.

Alice sends, Bob reads:

```sh
MASH_ENDPOINT=acme.demo.alice "$HOME/.local/bin/mash" send acme.demo.bob "Hello Bob"
```

Expected: `sent #1 from acme.demo.alice to acme.demo.bob`.

```sh
MASH_ENDPOINT=acme.demo.bob "$HOME/.local/bin/mash" inbox
```

Expected: one line that starts `#1`, then `from acme.demo.alice
[verified]`, the time, `9 B` and `Hello Bob`. `[verified]` means the
message is signed by Alice's key. It works.

### Which keys stay on this machine

Be plain with the person about this. Which keys are where, now:

- mashctl's folder, `$HOME/.config/mash`, **stays**. It holds the
  operator signing seed, the account seed, the system credentials, the
  admin credentials and every agent's creds. mashctl needs them to sign
  each agent it makes and to administer the server. Anyone who holds that
  folder can mint credentials for the whole account and read every
  agent's mail. Treat it like a password manager's vault: mode 0700, not
  in a shared or synced folder, not in any backup.
- The server directory holds, until you move them, `ca.key`, `nsc/` and
  `export/`.

Only material that mashctl does not need can leave. That is `ca.key`
(forging server certificates) and `nsc/` (the store that makes
accounts and users without mashctl). `export/` is a spare copy of what
mashctl now holds, so it goes with them. After the next step this
machine can no longer make a new server certificate or a new account;
it can still mint agents' credentials, through mashctl, as above.

### Move the spare keys off this machine

First prove mashctl has its own copies (each line must print `same`; a
`differ` or an error means: do not go on):

```sh
for p in keys/operator.nk keys/account.nk server/sys.creds server/mashadmin.creds; do f="$HOME/mash-nats/export/$(basename "$p")"; [ -s "$f" ] && [ "$(cat "$f")" = "$(cat "$HOME/.config/mash/$p")" ] && echo "same $p" || echo "differ $p"; done
```

Then retire them. For a trial you can skip this and `--remove` later. This
command asks the person to type the server directory as a full path
(shown on screen) at a terminal, so an agent cannot run it; the person
does. It writes one archive (mode 0600), checks that it lists back, and
only then removes `ca.key`, `nsc/` and `export/` from the server
directory. The server keeps running, and keeps `server.pem`, `server.key`
and `ca.pem`:

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --retire-keys "$HOME/mash-nats-keys.tar.gz"
```

Then move `mash-nats-keys.tar.gz` off this machine (an encrypted disk or
a password manager's file store, not a plain backup, not a chat), and
then remove the copy in your home directory. Keep the one off the
machine: it is the only copy of the CA key and the keys that make new
accounts. To remove the home copy after you have the other safe:

```sh
mkdir -p "$HOME/archive" && mv -i "$HOME/mash-nats-keys.tar.gz" "$HOME/archive/"
```

That moves it to `$HOME/archive`: empty that folder yourself when you
are sure.

Check the result (changes nothing):

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --check
```

Expected: all `ok` lines, and no mention of `ca.key`, `nsc/` or
`export/` as missing.

## 6. Look after it

Each is one script command, with the same `--dir`. Stop acts only on the
pid it recorded, and only if that pid is this server.

Stop:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --stop
```

Start (after a reboot, for example):

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --start
```

Where each command writes: `--backup DEST` and `--retire-keys DEST`
each write one file, at the exact path you give (it must not exist; the
script makes it mode 0600 and stops if its folder is shared or
writable by others); `--enable-boot` writes one file,
`$HOME/.config/systemd/user/mash-nats.service`, and only with the
typed yes. Nothing else is written outside the server directory.

Back up. The server directory's backup stops the server for a moment,
restarts it, and writes one archive (mode 0600). It does not include
mashctl's folder (see below), and it does not include `bin/`; once you
have retired the keys, it holds only the server's files. The archive
holds secrets: keep it private and off this machine:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --backup "$HOME/mash-nats-backup.tar.gz"
```

Do not back up mashctl's folder with this or with a file backup or a
sync tool: it holds the keys that can mint credentials for the whole
account (see "Which keys stay on this machine"). If you lose it, you
make a new server directory and new agents; the mail is not lost with
it. Keep it out of backups on purpose.

Restore means: stop the server, put the files back, start it. The
server directory goes back into an empty place with the same name; its
`bin/` comes from `--upgrade`. This block restores the backup into an
empty location and stops if anything already exists there:

```sh
if [ -e "$HOME/mash-nats" ]; then echo "not restored: $HOME/mash-nats already exists"; else mkdir -m 0700 "$HOME/mash-nats" && tar -xzf "$HOME/mash-nats-backup.tar.gz" -C "$HOME/mash-nats" && echo restored || { echo "not restored: a step failed"; false; }; fi
```

Then run `--upgrade` (it installs the two programs the backup left out),
then `--start`, then `--check`. mashctl's folder is not in the backup:
it stays where it is (or is gone with the machine; see above).

Upgrade installs the versions pinned in the script you hold (nats-server
v2.15.1, nsc v2.15.0), and does nothing if they are already there. To
get newer versions, fetch a newer script from a newer commit (part 3)
and run this with it:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --upgrade
```

Start at boot is a separate yes: it keeps a network server running
after you log out. The person types the confirmation, so an agent
cannot run these. Add `--linger` to also run `loginctl enable-linger`,
so it runs without you logged in; that may say "Access denied" for an
ordinary user, and then the script prints the command for you to run:

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --enable-boot --linger
```

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --disable-boot
```

### Remove it completely

Five things, in this order, and a sixth if you used boot start (the first step removes its unit). Ask first: the second and fourth cannot be
undone. The person types the directory name to confirm the first.

1. Delete the server directory, with the mail and any keys you did not
   retire; this also stops the server and removes the boot unit if the
   script made one. It leaves mashctl's folder and says so:

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --remove
```

2. Delete mashctl's folder: its keys, copies and every agent's creds.
   Do this only when you will not use this server again:

```sh
rm -r "$HOME/.config/mash"
```

3. Delete the downloads and the script:

```sh
rm -r "$HOME/mash-setup"
```

4. Delete the three programs from `$HOME/.local/bin`:

```sh
rm "$HOME/.local/bin/mash" "$HOME/.local/bin/mashctl" "$HOME/.local/bin/gh"
```

5. Delete the archives you made, once you do not need them:
   `$HOME/archive/mash-nats-keys.tar.gz` (or `$HOME/mash-nats-keys.tar.gz`
   if you did not move it), `$HOME/mash-nats-backup.tar.gz`, and the copy
   you moved off this machine.

```sh
rm -f "$HOME/archive/mash-nats-keys.tar.gz" "$HOME/mash-nats-keys.tar.gz" "$HOME/mash-nats-backup.tar.gz"
```

## What next

You have a server, mashctl and two test agents. To use mash for real:

1. Make your own agents, one per Claude Code session you want to run
   (use your owner and group):

```sh
"$HOME/.local/bin/mashctl" endpoint create acme.demo.design --make-key --creds-out "$HOME/.config/mash/acme.demo.design.creds"
```

2. Install the plugin, by running these two lines in Claude Code:

```
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
```

3. Start a session as that agent, with its address:
   `MASH_ENDPOINT=acme.demo.design claude --channels plugin:mash@mash`.
   The rest of the launch steps are in
   https://github.com/bogworks/mash/blob/v0.5.6/docs/install.md
   (sections 3 to 5), and a quick start, which uses the same commands, is
   https://github.com/bogworks/mash/blob/v0.5.6/README.md#quick-start-one-machine-no-account
   (skip its server steps: you have a server).
4. Another machine that wants to use this server needs `ca.pem`, an
   agent `.creds` file made on this machine, and an address for the
   server that machine can reach, which must be in the certificate
   (question 6). A credential carries no address: `localhost` works only
   on this machine. Do not copy mashctl's folder to another machine.

Delete Alice and Bob when you are done with them:
`mashctl endpoint delete acme.demo.alice` and the same for bob.

## What you hold

The script ends by printing this. It is the real output of the test
(after `Done.`); your paths differ. The "Next" list is the same as this
guide's parts 5 and 6, with the bare command `mashctl`; until your next
login, when `$HOME/.local/bin` joins your PATH, use the full path
`$HOME/.local/bin/mashctl`, as the blocks above do. Read it back to the
person:

<!-- closing-begin -->
<!-- closing-end -->

## If something is wrong

Find the line you saw. Every fix below was run, and the check passed
afterwards. A fix that deletes anything is not offered: move it to
`$HOME/archive/` instead.

| You see | Cause | Fix |
|---|---|---|
| `FAIL ... mode 755, want 700` (the server directory) | others can read the directory | `chmod 700 "$HOME/mash-nats"`, then `--check` |
| `FAIL ... mode 644, want 600` on a file in `export/` | a secret readable by others | `chmod 600` that file; tell the person |
| `FAIL missing .../server.pem` and a `nats-server -t -c` FAIL | a file the config names is gone | put it back; if lost, restore from a backup or make a new server directory |
| `skip nothing listens on 127.0.0.1:4222` | the server is stopped | `--start` (part 6) |
| `FAIL 127.0.0.1:4222 answers but the server recorded for this directory is not running` | something else holds the port | find what it is and ask the person; `--stop` another directory's server if it is one of ours |
| `port 127.0.0.1:4222 is already taken by something` at create | another program holds the port | give `--port N`, or ask the person to stop it; never stop it yourself |
| `... already exists: this script creates --dir itself` | the path exists, even empty | pick a new path |
| `needs at least 3 path components` or `is writable by group or others` | `--dir` is `/`, `/tmp` or similar | use a path under `$HOME` |
| `sudo: a password is required`, `is not in the sudoers file`, `sudo: command not found` | no sudo here | ask the person to run the install line as root, or install the programs |
| `... tls error ... certificate signed by unknown authority` (adopt) | the CA file was not given, or is the wrong one | give adopt `--ca "$HOME/mash-nats/ca.pem"` |
| `... no servers available for connection` (adopt) | the server is not running | `--start`, then adopt again |
| `x509: certificate has expired` | the ten-year certificate ran out | see "Certificates last ten years" in part 3 |

To see why the server will not start, read the last lines of
`$HOME/mash-nats/server.log`. A healthy log has `TLS handshake error: EOF`
lines: those are the script's own `--check` probes.

## What mash needs from NATS

Read this if you already run a NATS server and want mash to use it,
or want to know what the script's `server.conf` gives. mash needs seven
things from the server, and `mashctl adopt` checks them. These are the
real lines the script wrote in the test (account ids and JWTs, long,
are shortened):

```
listen: "127.0.0.1:4222"
tls {
  cert_file: "/home/alice/mash-nats/server.pem"
  key_file: "/home/alice/mash-nats/server.key"
}
jetstream {
  store_dir: "/home/alice/mash-nats/jetstream"
}
operator: "/home/alice/mash-nats/operator.jwt"
system_account: "ABOJ...A3QQ"
resolver {
  type: full
  dir: "/home/alice/mash-nats/resolver"
  allow_delete: true
  interval: "2m"
  timeout: "1.9s"
}
resolver_preload: {
  ABOJ...A3QQ: "<JWT>"
  ABZ3...7ZIK: "<JWT>"
}
```

1. `listen`: where the server accepts connections (this machine only,
   unless you said otherwise).
2. `tls`: every connection is encrypted; mash refuses a plain one. Give
   adopt the CA that signed `cert_file` (`--ca`); it keeps a copy.
3. `jetstream`: mail is kept on disk in `store_dir`.
4. `operator`: operator mode: nobody connects without credentials signed
   by this operator.
5. `system_account`: the account mashctl uses to revoke a lost key.
6. `resolver { type: full }`: the server keeps account records on disk,
   so mashctl can revoke without restarting the server.
7. `resolver_preload`: the accounts (system, and the mash account) the
   server starts knowing.

Where the limits are set. The 2 GB of disk, 64 streams and 128 consumers
are not in `server.conf`. They are in the mash account's own JWT, which
the operator signs (the script sets them with `nsc edit account
--js-disk-storage 2G --js-streams 64 --js-consumer 128`, and leaves
memory storage unset). The server reads that JWT from the resolver.
`mashctl adopt` reads the limits from the JWT and then asks the server
whether it really gave JetStream to the account. Streams needed: one per
agent endpoint, plus 5 for mash's own buckets; so 64 streams allows
about 59 agents.

The account's name. mash does not require `MASH`. `mashctl adopt` takes
`--account NAME` and checks it against the name inside the account JWT;
if you omit it, it uses the owner name. They must agree, or adopt
refuses and says which name the JWT has.

The two users `mashctl adopt` is given:

- The admin user (`--admin-credentials`) must belong to the mash account and
  be allowed everything in it: create JetStream streams and key-value
  buckets, publish and subscribe on any subject. mash uses it to make
  the five buckets and each agent's stream. The script's `mashadmin` has
  no permission limits. If the operator uses strict signing keys, the user
  must be signed by an account signing key (adopt says so).
- The system user (`--system-credentials`) must belong to the system account and be
  allowed to ping the server (`$SYS.REQ.SERVER.PING`), look up accounts
  and push an account update (`$SYS.REQ.CLAIMS.UPDATE`); adopt checks the
  first two, and `mashctl endpoint delete` uses the third. The script's
  `sys` user has no permission limits.

Also needed: the operator signing seed (`--operator-key`, starts `SO`)
and the account identity seed (`--account-key`, starts `SA`), each as a
0600 file. mashctl keeps both (see part 3, the file list).

## Do not

- Do not run these programs as root, or in `/`.
- Do not put seeds or credentials in a message, a log or a chat.
- Do not use `pkill`, `killall` or `pgrep -f` to stop NATS.
- Do not run the script before the person has answered the questions.
- Do not delete `export/`, `nsc/` or `ca.key` by hand: use
  `--retire-keys`, which keeps the only copy.
