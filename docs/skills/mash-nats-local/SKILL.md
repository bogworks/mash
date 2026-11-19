---
name: mash-nats-local
description: >-
  Set up a NATS server on this machine for mash, the default way: three
  questions, one script, adopt, a two-agent test. Use when a person or an
  agent needs a mash server and has none. mashctl does not install or
  start NATS; this does. For anything not the default, see
  WHAT-MASH-NEEDS.md beside this file.
---

# Set up a NATS server for mash (one machine, the default)

DRAFT 6 (0.5.6). For a person, or for an agent following it. Debian or
Ubuntu, 64-bit Intel or AMD. This guide is the default path only: one
machine, one user. Another port, other machines, your own NATS server,
backup and loss are in `WHAT-MASH-NEEDS.md`; go there only if the person
says "not the default".

mash gives each AI agent session (one running Claude Code window, say)
an inbox that keeps its mail, so agents can message each other. The inbox
lives on a NATS server (an open-source message server). mashctl is mash's
admin tool: it makes the agents' mailboxes. mashctl does not install or
start NATS; this guide does. You will: answer three questions, install
the prerequisites, run one script that makes the server, install mash and
mashctl, adopt the server, and send a test message from Alice to Bob.

Every command block runs on its own in a fresh shell; nothing depends on
a variable or directory from an earlier block. Run them as written,
replacing the names in the next section. A block that fails prints a
message and nothing worse. Blocks marked `sh person` are run by the
person themselves.

The sample outputs below were made by a user whose login is `alice`. That
is a login name, not the test agent Alice: `/home/alice` is just the home
folder in the samples. Yours will show your own login.

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
acme.demo.alice. Replace the names in every block if the answers differ.

| In this guide | Meaning | Default |
|---|---|---|
| `acme` | owner: the first part of every address | your login name |
| `demo` | group: agents that may mail each other | `demo` |
| `alice`, `bob` | two throwaway test agents | any two names |
| `$HOME/mash-nats` | the server's directory | fixed here |
| `4222` | the port, this machine only | fixed here |
| `$HOME/.config/mash` | mashctl's folder | fixed here |

## What you say yes to

Never implied by "set it up". Each is asked when its step comes: using
sudo (part 2, only if a prerequisite is missing); installing software
(each source and version is shown); a start-at-boot service (part 6).
The script never opens a firewall port. Never pipe a download into a
shell.

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
  (a key you hold) signs accounts; an account is one separate space,
  mash uses one; a user is one identity in it.
- Seed: a private key (starts with S). Never print, mail or paste one.
- Credentials (a `.creds` file): a user's identity plus its seed.
  Whoever holds the file is that user.
- Endpoint: one agent's mailbox, with its own `.creds` file.
- CA and certificate: the certificate is the server's ID card; the CA is
  the authority, made here by you, that signs it. Clients trust the CA.
- Fingerprint: a short code that identifies the CA file.
- JetStream: the part of NATS that stores messages on disk.
- Attestation: a signed statement, made by the project's own build, that
  a file came from it.

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

"Operator mode" means nobody connects without credentials signed by the
operator key the script makes.

## What this opens

- The server listens on 127.0.0.1:4222. Binding 127.0.0.1 keeps it off
  the network, but it does NOT limit it to you: every user on this
  machine can reach that port. They cannot connect without credentials,
  read mail or make agents.
- The script opens no other port and does not touch a firewall. It
  writes only your server directory and `$HOME/mash-setup`, plus the one
  file each of `--backup`, `--retire-keys` and `--enable-boot` writes
  (part 6). mashctl writes its own folder; the install block writes
  `$HOME/.local/bin`.
- Do not run any of this as root: the script refuses.

## 1. Questions

Ask them in order. Each has a default; the person may take it.

1. Owner name? Default: your login name if it is lower-case letters and
   digits, otherwise pick one (lower-case letters, digits, single
   hyphens, starting with a letter, up to 32 characters). It is the
   first part of every address and cannot change later without starting
   over.
2. Group and agent names for the test? Default: group `demo`, agents
   `alice` and `bob`. Your real agents come later ("What next").
3. May this guide use sudo to install prerequisites? Default: only if
   something in part 2 is missing; ask then.

Everything else is fixed here: directory `$HOME/mash-nats` (needs 2 GB
free: check with `df -h "$HOME"`), port 4222, this machine only,
mashctl's folder `$HOME/.config/mash`, a trial you can remove. A
different answer to any of these means the non-default path in
`WHAT-MASH-NEEDS.md`.

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
  make the test agents         acme.demo.alice, acme.demo.bob
  keep mashctl's files in      $HOME/.config/mash (holds keys that can
                               make any agent: never copied or backed up)
  keep downloads in            $HOME/mash-setup
  install                      nats-server and nsc in the server dir only;
                               gh, mash, mashctl in $HOME/.local/bin
  sudo                         only if a prerequisite is missing
  start at boot                no
Say yes to go ahead.
```

If the person changes the port, the directory, mashctl's folder or the
listen address, say the change back in this list in plain words. If they
name an address other than this machine, say: "that makes the server
listen on every network interface of this machine, whatever address you
named; only a firewall you set up yourself limits it". Then go to
`WHAT-MASH-NEEDS.md`.

## 2. Prerequisites

The script needs curl, tar, unzip, openssl, sha256sum and ss. Read-only
check; it prints `missing:` for each one that is not there:

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
for p in "$HOME/mash-nats" "$HOME/mash-setup" "$HOME/.config/mash" "$HOME/.local/bin/mash" "$HOME/.local/bin/mashctl" "$HOME/.local/bin/gh"; do [ ! -e "$p" ] || echo "exists: $p"; done; ss -ltn | grep ':4222 ' ; echo checked
```

Expected: `checked` and nothing else. A line `exists:` or one for port
4222 means: stop and ask. Do not overwrite, move or stop it. If
`$HOME/.config/mash` exists, mash is already set up here.

## 3. Make the server

Fetch the script from one exact commit and check its hash. Do not run a
script you have not checked:

```sh
mkdir -p "$HOME/mash-setup" && curl -fsSL -o "$HOME/mash-setup/nats-local-setup.sh" https://raw.githubusercontent.com/bogworks/mash/8ac4d0b0ee09240fe72c1ac5637444b44d25570f/scripts/nats-local-setup.sh && echo "900f4fcb5b327cc0204954e27536d51b5aecd674817e306b895d11ef989692b0  $HOME/mash-setup/nats-local-setup.sh" | sha256sum -c -
```

Expected: one line ending `nats-local-setup.sh: OK`. If it says
`FAILED` the file is on disk but wrong: do not run it; stop. If curl
says `404`, nothing was saved: the URL is wrong, copy the block again.

The script writes only under the server directory and `$HOME/mash-setup`.
It installs nats-server v2.15.1 and nsc v2.15.0 into
`$HOME/mash-nats/bin`, after checking each download against a hash
written in the script; of the programs on your PATH it runs only the
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
files mashctl needs in `export/` and `mash-setup.json`, (7) start the
server. It ends with a file list, a "What you hold" box (shown whole at
the end of this guide) and a "Next" list.

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

### Where every secret is, and what you lose

| What | After the script | After adopt | After `--retire-keys` | If it is gone |
|---|---|---|---|---|
| `ca.key` (CA private key) | server dir | server dir | archive only | no new server certificate; existing ones keep working |
| `nsc/` (key store) | server dir | server dir | archive only | no new accounts or users by hand; mashctl and the server keep working |
| `export/` (copies of the four files) | server dir | server dir, spare | archive only | nothing: mashctl holds its own copies |
| mashctl's folder: operator seed, account seed, system and admin credentials, agent creds | does not exist | mashctl's folder | mashctl's folder | mashctl cannot make, revoke or administer agents; set up again and reissue every endpoint |
| `server.key` | server dir | server dir | server dir | the server cannot start; set up again |
| `jetstream/` (all mail) | server dir | server dir | server dir | every stored message |
| the archive | none | none | where you put it | you can still run the server; you have no copy of `ca.key` or `nsc/` |

mashctl's folder is the one to understand. It keeps the operator signing
seed, the account seed and the system and admin credentials, because
mashctl signs every agent it makes. Modes: the folder, `keys/` and
`server/` are 0700, every file 0600 (the CA 0644). Whoever holds that
folder can make any identity in the account and read every agent's mail.
On a single-user machine that is acceptable. It is left out of backups
and sync by name. On a shared host, or with mash across machines, the
signing keys must live elsewhere; `WHAT-MASH-NEEDS.md` says more.

Certificates last ten years. After that clients fail with `x509:
certificate has expired`; there is no renew command, so you set up again.

## 4. Install mash and mashctl

Check what you have (read-only). Expected: two "No such file" errors and
`checked`:

```sh
"$HOME/.local/bin/mash" version; "$HOME/.local/bin/mashctl" version; echo checked
```

If one prints a version, stop and ask. Debian and Ubuntu put
`$HOME/.local/bin` on your PATH at your next login; until then every
block here spells the full path.

The releases are signed by the project's own build; `gh`, GitHub's
tool, checks that. Debian's own `gh` is too old, so the block uses the
pinned one (v2.105.0). It works in one new private folder: downloads
`gh` and checks its hash; downloads mash, mashctl and the signed record;
verifies both programs against the record (no login needed: the record
is a file); installs; verifies the installed copies again, so a swap
between steps is caught. If the second check fails it moves the files to
`$HOME/mash-setup/rejected`. It stops at once if `gh`, `mash` or
`mashctl` is already installed. This block is the interim route; the
project's attested install script will replace it. If you do not get
`installed and verified`, do not use the programs:

```sh
(
set -eu
H="$HOME/mash-setup"; B="$HOME/.local/bin"; V=v0.5.6; G=gh_2.105.0_linux_amd64
A="-R bogworks/mash --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/$V"
mkdir -p "$H" "$B"
for x in gh mash mashctl; do
  [ ! -e "$B/$x" ] || { echo "already there: $B/$x"; false; }
done
w="$H/install-$$"; mkdir -m 0700 "$w"; cd "$w"
curl -fsSLO "https://github.com/cli/cli/releases/download/v2.105.0/$G.tar.gz"
echo "79e7ab5db738c48777b696e374f8b428f0647debfb424f44d2f426370407fe2f  $G.tar.gz" | sha256sum -c -
tar -xzf "$G.tar.gz"
for f in mash-linux-amd64 mashctl-linux-amd64 mash.intoto.jsonl; do
  curl -fsSLO "https://github.com/bogworks/mash/releases/download/$V/$f"
done
for f in mash-linux-amd64 mashctl-linux-amd64; do
  "$G/bin/gh" attestation verify "$f" --bundle mash.intoto.jsonl $A
done
install -m 755 "$G/bin/gh" "$B/gh"
install -m 755 mash-linux-amd64 "$B/mash"
install -m 755 mashctl-linux-amd64 "$B/mashctl"
for p in mash mashctl; do
  "$G/bin/gh" attestation verify "$B/$p" --bundle mash.intoto.jsonl $A || {
    mkdir -p "$H/rejected"; mv "$B/gh" "$B/mash" "$B/mashctl" "$H/rejected/"; false; }
done
echo "installed and verified in $B"
); [ $? -eq 0 ] || { echo "NOT installed, or NOT verified: do not use these programs"; false; }
```

Expected: `gh_2.105.0_linux_amd64.tar.gz: OK`, then nothing (in a pipe) or a
"Verification succeeded" report (in a terminal) for each of four
checks, then `installed and verified in` your `.local/bin`. A `FAILED`
hash, a `Verification failed` or `404` line, or `NOT installed` means
stop and show the person. Check the result:

```sh
"$HOME/.local/bin/mashctl" version
```

Expected: one line with mashctl's version, 0.5.6. `go install` and the
Claude Code plugin also install the programs, but nothing here verifies
them: developer routes only.

## 5. Adopt the server, then test

You run this as the same user that ran the script, on the same machine,
so mashctl is not asked to type the CA fingerprint: the files are
already yours. (A person on another machine does type it; see
`WHAT-MASH-NEEDS.md`.) First the read-only check, which changes nothing:

```sh
"$HOME/.local/bin/mashctl" adopt --from "$HOME/mash-nats" --dry-run
```

Expected: numbered checks, each `ok` (the files; the server refuses a
connection without credentials and accepts the system credentials; the
account exists; storage is given; the system user works; the operator
key signed the account; the admin credentials connect), then `All checks
passed.` and a list of what adopt would do. A failed check says what was
wrong and that nothing was changed. Now the real one:

```sh
"$HOME/.local/bin/mashctl" adopt --from "$HOME/mash-nats"
```

Expected: the same checks, `All checks passed.`, one `copied` line per
file (the CA, the two keys, the two credentials, with modes), a
`config.json` line, and a line that mash's storage is ready. mashctl
now has its own copies in `$HOME/.config/mash`, so `export/` is spare.

The modes of mashctl's folder (it holds keys, so it must not be open):

```sh
stat -c '%a %n' "$HOME/.config/mash" "$HOME/.config/mash"/*
```

Expected, in this order (the list is sorted by name): `700` for the
folder, `644` for `ca.pem`, `600` for `config.json`, `700` for `keys` and
`700` for `server`. The CA is meant to be 0644: do not change it. If any
other number differs, `chmod` it to the value above and tell the person.

### Alice and Bob

The group `demo` comes into being when the first agent uses it.
`--make-key` makes the agent's own key on this machine and signs it;
nothing is asked, because the signing keys are already yours:

```sh
"$HOME/.local/bin/mashctl" endpoint create acme.demo.alice --make-key --creds-out "$HOME/.config/mash/acme.demo.alice.creds"
```

Expected: a line saying the endpoint was created, its address
`acme.demo.alice`, and the path of its `.creds` file. Bob the same way:

```sh
"$HOME/.local/bin/mashctl" endpoint create acme.demo.bob --make-key --creds-out "$HOME/.config/mash/acme.demo.bob.creds"
```

```sh
"$HOME/.local/bin/mashctl" endpoint list
```

Expected: two lines, `acme.demo.alice` and `acme.demo.bob`, each with
its creation time, then `2 endpoints`. `mash` finds the server and the
agent's credentials from mashctl's folder and the address you name.
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

### Move the spare keys off this machine

`ca.key`, `nsc/` and `export/` are not needed to run mash. Prove
mashctl has its own copies first (each line must print `same`; `differ`
or an error means stop):

```sh
for p in keys/operator.nk keys/account.nk server/sys.creds server/mashadmin.creds; do f="$HOME/mash-nats/export/$(basename "$p")"; [ -s "$f" ] && [ "$(cat "$f")" = "$(cat "$HOME/.config/mash/$p")" ] && echo "same $p" || echo "differ $p"; done
```

Then retire them. For a trial you may skip this, but `--remove` later
needs it. **You run this one yourself:** it asks you to type the server
directory's full path, writes one archive (mode 0600), checks that it
lists back and matches, and only then removes `ca.key`, `nsc/` and
`export/` from the server directory. The server keeps running:

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --retire-keys "$HOME/mash-nats-keys.tar.gz"
```

The archive holds the only copy of the CA key and the keys that make new
accounts. Move it off this machine (an encrypted disk, a password
manager's file store; not a plain backup, not a chat). It stays in your
home directory until you move it; to put it aside there:

```sh
mkdir -p "$HOME/archive" && mv -i "$HOME/mash-nats-keys.tar.gz" "$HOME/archive/"
```

Check the result (read-only); all `ok`, and no mention of `ca.key`,
`nsc/` or `export/` as missing:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --check
```

## 6. Look after it

Each is one script command with the same `--dir`. Stop acts only on the
pid it recorded. `--backup` and `--retire-keys` each write one file at
exactly the path you give (it must not exist; mode 0600); `--enable-boot`
writes `$HOME/.config/systemd/user/mash-nats.service`. Nothing else is
written outside the server directory.

Stop, then start (after a reboot, say):

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --stop
```

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --start
```

Each start keeps the last two log files, and the log is kept at most
7 days.

Back up the server (stops it a moment, restarts it, writes one archive
of the server directory without `bin/`; it holds secrets, keep it off
this machine):

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --backup "$HOME/mash-nats-backup.tar.gz"
```

Restore means: put the backup in an empty place with the same name, run
`--upgrade` (it makes `bin/`), then `--start` and `--check`. It brings
back the server, its mail and its accounts. It does not bring back
mashctl's folder, which is never in a backup: if that is gone too, there
is no restore; set up again and reissue every endpoint.

```sh
if [ -e "$HOME/mash-nats" ]; then echo "not restored: $HOME/mash-nats already exists"; else mkdir -m 0700 "$HOME/mash-nats" && tar -xzf "$HOME/mash-nats-backup.tar.gz" -C "$HOME/mash-nats" && echo restored || { echo "not restored: a step failed"; false; }; fi
```

`--upgrade` installs the versions pinned in the script you hold and does
nothing if they are already there:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --upgrade
```

Start at boot is a separate yes: it keeps a network server running after
you log out. You run these yourself. `--linger` also runs `loginctl
enable-linger` (it may say "Access denied" for an ordinary user, and
then the script prints the command to run):

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --enable-boot --linger
```

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --disable-boot
```

`--disable-boot` does not undo linger. To undo it, run `loginctl
disable-linger "$USER"` yourself.

### Remove it completely

Only when you are finished with this server. The order matters, and no
step deletes keys that are still live: the script's `--remove` refuses
until `--retire-keys` has archived the CA key and signing keys (part 5).

1. Retire the keys (part 5), if you have not. You run it yourself.
2. Delete the server directory and its boot unit. You run it yourself;
   you type the directory path. It leaves mashctl's folder and says so:

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --remove
```

3. The server is gone, so the keys in mashctl's folder open nothing any
   more. Delete the folder, then the downloads, then the programs:

```sh
rm -r "$HOME/.config/mash" "$HOME/mash-setup"
```

```sh
rm "$HOME/.local/bin/mash" "$HOME/.local/bin/mashctl" "$HOME/.local/bin/gh"
```

4. Delete the archives once you no longer want the CA key and signing
   keys (this cannot be undone): `$HOME/archive/mash-nats-keys.tar.gz`
   (or `$HOME/mash-nats-keys.tar.gz`), `$HOME/mash-nats-backup.tar.gz` and
   the copy you moved off this machine.

```sh
rm -f "$HOME/archive/mash-nats-keys.tar.gz" "$HOME/mash-nats-keys.tar.gz" "$HOME/mash-nats-backup.tar.gz"
```

## What next

You have a server, mashctl and two test agents, and you hold: the server
directory (`$HOME/mash-nats`, serving the mail), mashctl's folder (the
keys, kept private), and the archive of the spare keys. To use mash for
real, make your own agent, then follow the README quick start from its
"launch a session" step:

```sh
"$HOME/.local/bin/mashctl" endpoint create acme.demo.design --make-key --creds-out "$HOME/.config/mash/acme.demo.design.creds"
```

Install the plugin by running these two lines in Claude Code, then start
a session as that agent: `MASH_ENDPOINT=acme.demo.design claude
--channels plugin:mash@mash`.

```
claude plugin marketplace add bogworks/mash
claude plugin install mash@mash
```

The quick start, with the same commands (skip its server steps):
https://github.com/bogworks/mash/blob/v0.5.6/README.md#quick-start-one-machine-no-account

Delete Alice and Bob when you are done:
`mashctl endpoint delete acme.demo.alice`, and the same for bob.

## What you hold

The script ends by printing this (the real output of the test, after
`Done.`; your paths differ). Until your next login, when
`$HOME/.local/bin` joins your PATH, use the full path to `mashctl`, as
the blocks above do. Read it back to the person:

<!-- closing-begin -->
```
Done. The setup is in /home/alice/mash-nats. Nothing above printed a seed or a
credential, and nothing below does.

Binding 127.0.0.1 keeps the server off the network but does not limit it to you:
every user on this machine can reach port 4222. They cannot connect without
credentials, read mail or make agents.

A process running as you can still swap a path between the recheck and the
write; this script does not defend against that.

Files (all under /home/alice/mash-nats):
  bin/nats-server          server, v2.15.1 (sha256 checked)
  bin/nsc                  nsc, v2.15.0 (sha256 checked)
  ca.pem                   CA certificate: give this to clients
  mash-setup.json          what adopt --from reads (no secrets)
  server.conf              nats-server configuration (no seeds)
  export/mashadmin.creds   admin credentials (secret)
  export/sys.creds         system account credentials (secret)
  export/operator.nk       operator seed (secret)
  export/account.nk        account seed (secret)
  ca.key                   CA private key (secret)
  nsc/                     nsc store with all signing keys (secret)
  server.log               server log (the log is kept at most 7 days)
  server.pid               server process id (--stop reads it)

What you hold
  Server address     tls://localhost:4222
  CA file            /home/alice/mash-nats/ca.pem
  CA fingerprint     sha256:2704956b 8f136f27 53863bff 9ea5ccba b183ded9
                     49ac7f8d 3c312596 0590be80
  Server directory   /home/alice/mash-nats
                     (mode 0700; holds the CA key and the nsc keys until you run
                     --retire-keys)

The CA fingerprint is what a person on ANOTHER machine types to trust this
server; this machine does not ask for it.

Next
  Adopt the server into mash, as the same user that ran this script (it does not
  ask for the fingerprint, and it sets up mash's storage too, so there is no
  separate init):

    mashctl adopt --from /home/alice/mash-nats

  Adopt copies the CA, the keys and the credentials into mashctl's folder
  (/home/alice/.config/mash; set MASH_CONFIG_DIR to change that) and prints one
  'copied' line per file, so export/ is no longer needed. Agents' credentials
  are written there as acme.<group>.<agent>.creds.

  Hand-managed server instead? see the long form in the guide (mashctl adopt
  --server ... --ca ...).

  Or, after adopt succeeds, to retire the setup's keys: --retire-keys (item 2
  below). --retire-keys archives the keys first; nothing here asks you to delete
  the server directory by hand.

  Another machine that wants to use this server needs the CA file and an address
  for the server that machine can reach (the one in the certificate); a
  credential carries no address.

  To use mash in Claude Code, run these two lines in Claude Code, then see the
  guide:

    claude plugin marketplace add bogworks/mash
    claude plugin install mash@mash

  https://github.com/bogworks/mash/blob/v0.5.6/docs/install.md

  2. Looking after the server later (each takes --dir):

  start the server
    /home/alice/mash-setup/nats-local-setup.sh --dir /home/alice/mash-nats \
        --start

  stop it
    /home/alice/mash-setup/nats-local-setup.sh --dir /home/alice/mash-nats \
        --stop

  check it
    /home/alice/mash-setup/nats-local-setup.sh --dir /home/alice/mash-nats \
        --check

  back it up (the archive holds secrets)
    /home/alice/mash-setup/nats-local-setup.sh --dir /home/alice/mash-nats \
        --backup /home/alice/mash-nats-backup.tar.gz

  after adopt, move the offline-only secrets off this machine
    /home/alice/mash-setup/nats-local-setup.sh --dir /home/alice/mash-nats \
        --retire-keys /home/alice/mash-nats-keys.tar.gz

  upgrade nats-server and nsc to the pinned versions
    /home/alice/mash-setup/nats-local-setup.sh --dir /home/alice/mash-nats \
        --upgrade

  start it at boot (systemd user unit)
    /home/alice/mash-setup/nats-local-setup.sh --dir /home/alice/mash-nats \
        --enable-boot

  stop starting it at boot
    /home/alice/mash-setup/nats-local-setup.sh --dir /home/alice/mash-nats \
        --disable-boot

  remove it all (after --retire-keys)
    /home/alice/mash-setup/nats-local-setup.sh --dir /home/alice/mash-nats \
        --remove
```
<!-- closing-end -->

## If something is wrong

Find the line you saw. Every fix below was run, and the check passed
afterwards.

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

## Do not

- Do not run these programs as root, or in `/`.
- Do not put seeds or credentials in a message, a log or a chat.
- Do not use `pkill`, `killall` or `pgrep -f` to stop NATS.
- Do not run the script before the person has answered the questions.
- Do not delete `export/`, `nsc/` or `ca.key` by hand: use
  `--retire-keys`, which archives them first.
