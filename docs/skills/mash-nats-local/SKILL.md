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

DRAFT 7 (0.5.6). For a person, or for an agent following it. Debian or
Ubuntu, 64-bit Intel or AMD. This guide is the default path only: one
machine, one user. Another port or folder, other machines and your own
NATS server are in `WHAT-MASH-NEEDS.md`; go there only if the person says
"not the default".

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
(each source and version is shown).
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
  (a key you hold) signs accounts; an account is one separate space; a
  user is one identity in it. Here the script makes two accounts: SYS,
  the server's own admin space (the system account), and MASH, the one
  mash uses.
- nsc: NATS's own tool for making operators, accounts and users. The
  script installs it in the server directory and runs it for you.
- JWT: a small signed file that says what an account or user may do. The
  server reads the account's JWT to learn its limits.
- TLS: encryption of every connection. The server proves who it is with a
  certificate; a SAN is a name or address listed in that certificate (the
  script adds `localhost`, `127.0.0.1` and `::1`).
- Stream: where JetStream keeps one agent's mail on disk. Each endpoint
  gets one.
- Adopt: `mashctl adopt` hands a finished server to mash: it checks the
  server, copies the keys and credentials into mashctl's folder and sets
  up mash's storage.
- Owner: the first part of every address (`acme` in `acme.demo.alice`).
  It must be the same everywhere: the script, adopt and every endpoint
  use one owner name. mashctl does not refuse an address with another
  first part (`other.demo.carol` is created, with no warning), so a
  typo makes a stray endpoint rather than an error; check `mashctl
  endpoint list`. Mail between different owners was not tested here.
- Seed: a private key (starts with S). Never print, mail or paste one.
- Credentials (a `.creds` file): a user's identity plus its seed.
  Whoever holds the file is that user.
- Endpoint: one agent's mailbox, with its own `.creds` file.
- CA and certificate: the certificate is the server's ID card; the CA is
  the authority, made here by you, that signs it. Clients trust the CA.
- Fingerprint: a short code that identifies the CA file. On one machine
  nobody types it.
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
- An agent you run as yourself can read every key in your folders, so it
  can make any identity and read all mail. mash does not defend against
  that; if it matters to you, run the agent in a sandbox. The steps marked
  `sh person` are the ones that delete keys or ask for a typed
  confirmation: the person runs them.
- The script opens no other port and does not touch a firewall. It
  writes only your server directory and `$HOME/mash-setup`. mashctl
  writes its own folder; the install block writes `$HOME/.local/bin`.
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
                               make any agent; there is no backup)
  keep downloads in            $HOME/mash-setup
  install                      nats-server and nsc in the server dir only;
                               gh, mash, mashctl in $HOME/.local/bin
  sudo                         only if a prerequisite is missing
Say yes to go ahead.
```

If the person changes the port, the directory or mashctl's folder, say the
change back in this list in plain words, then go to `WHAT-MASH-NEEDS.md`.
If they want the server reachable from other machines: this script only
makes a server for this machine (127.0.0.1). A server for other machines
is the operator's own; see `WHAT-MASH-NEEDS.md`, section 5.

## 2. Prerequisites

You need Claude Code installed (mash is a Claude Code plugin; the quick
start in "What next" uses it). The script needs curl, tar, unzip, openssl,
sha256sum and ss. Read-only check of the script's programs; it prints
`missing:` for each one that is not there:

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
mkdir -p "$HOME/mash-setup" && curl -fsSL -o "$HOME/mash-setup/nats-local-setup.sh" https://raw.githubusercontent.com/bogworks/mash/6f3dc46aa51078f386f4c6698f23696808068ab6/scripts/nats-local-setup.sh && echo "1371f18abb8de966346d60dfa0a121af75550b78d7b5ea3126db370f244954da  $HOME/mash-setup/nats-local-setup.sh" | sha256sum -c -
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

### What if something is lost

mashctl's folder (`$HOME/.config/mash`) keeps the operator signing seed,
the account seed, the system and admin credentials and every agent's
credentials, because mashctl signs every endpoint it makes. Modes: the
folder, `keys/` and `server/` are 0700, every file 0600 (the CA 0644).

There is no backup and no restore; if this machine is lost, set up again
and reissue every endpoint. (The keys are in `$HOME/.config/mash`; keep
that folder out of sync and backup tools.)

Certificates last ten years. After that clients fail with `x509:
certificate has expired`; there is no renew command, so you set up
again. `WHAT-MASH-NEEDS.md` has a table of where each secret is.

## 4. Install mash and mashctl

Check what you have (read-only). Expected: two "No such file" errors and
`checked`:

```sh
"$HOME/.local/bin/mash" version; "$HOME/.local/bin/mashctl" version; echo checked
```

If one prints a version, stop and ask. Debian and Ubuntu put
`$HOME/.local/bin` on your PATH at your next login; until then every
block here spells the full path.

The releases are signed by the project's own build, and GitHub's `gh`
checks that signature. Debian's own `gh` is too old, so fetch the pinned
one (v2.105.0, its hash is checked). Nothing is installed if the hash is
wrong:

```sh
mkdir -p "$HOME/mash-setup/dl" "$HOME/.local/bin" && cd "$HOME/mash-setup/dl" && curl -fsSLO https://github.com/cli/cli/releases/download/v2.105.0/gh_2.105.0_linux_amd64.tar.gz && echo "79e7ab5db738c48777b696e374f8b428f0647debfb424f44d2f426370407fe2f  gh_2.105.0_linux_amd64.tar.gz" | sha256sum -c - && tar -xzf gh_2.105.0_linux_amd64.tar.gz && install -m 755 gh_2.105.0_linux_amd64/bin/gh "$HOME/.local/bin/gh"
```

Expected: `gh_2.105.0_linux_amd64.tar.gz: OK`. Download the release files
into the same folder. The signed record, `mash.intoto.jsonl`, covers
every binary:

```sh
cd "$HOME/mash-setup/dl" && for f in mash-linux-amd64 mashctl-linux-amd64 mash.intoto.jsonl SHA256SUMS; do curl -fsSLO "https://github.com/bogworks/mash/releases/download/v0.5.6/$f"; done
```

(mashctl as a release file is PENDING: if the second one answers `404`,
mashctl is not a release asset yet; ask the person.) Verify each program
against the signed record. One command per file; it needs no login. No
output is printed when the output is not a terminal, so the exit code is
the answer:

```sh
cd "$HOME/mash-setup/dl" && "$HOME/.local/bin/gh" attestation verify mash-linux-amd64 -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6 && echo "verified mash"
```

```sh
cd "$HOME/mash-setup/dl" && "$HOME/.local/bin/gh" attestation verify mashctl-linux-amd64 -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6 && echo "verified mashctl"
```

Expected: `verified mash`, then `verified mashctl`. `Verification failed`,
`FAILED` or any error means stop: nothing is installed yet, and the files
stay in `$HOME/mash-setup/dl`. Check the file hashes too:

```sh
cd "$HOME/mash-setup/dl" && grep -e ' mash-linux-amd64$' -e ' mashctl-linux-amd64$' SHA256SUMS | sha256sum -c -
```

Expected: `mash-linux-amd64: OK` and `mashctl-linux-amd64: OK`. Install, only
after all four checks passed:

```sh
cd "$HOME/mash-setup/dl" && chmod +x mash-linux-amd64 mashctl-linux-amd64 && mv mash-linux-amd64 "$HOME/.local/bin/mash" && mv mashctl-linux-amd64 "$HOME/.local/bin/mashctl"
```

```sh
"$HOME/.local/bin/mashctl" version
```

Expected: one line with mashctl's version, 0.5.6.

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

This is the planned output of the 0.5.6 mashctl, to be re-checked against
the real command once it exists (0.5.5's adopt copies no CA). Expected, in
this order (the list is sorted by name): `700` for the
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

The script's `export/` folder still holds spare copies of the keys and
credentials, and the CA key and signing store stay in the server
directory. Moving those off this machine is optional and is in
`WHAT-MASH-NEEDS.md`.


## 6. Stop, start and remove

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

### Remove it

Only when you are finished with this server. **You run this one yourself**
(the script asks you to type the directory path and refuses without a
terminal). It stops the server it recorded and deletes only the directory
it made, and says how to remove mashctl's folder:

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --remove
```

Expected: it prints what it will remove, then `about to: stop nats-server
pid N`, `did: stopped pid N` and `did: removed <dir>`. With no terminal it
refuses and exits 4. The server is gone, so the keys in mashctl's folder
open nothing any more. **You type these yourself**, because they delete
keys: the folder and downloads, then the programs:

```sh person
rm -r "$HOME/.config/mash" "$HOME/mash-setup"
```

```sh person
rm "$HOME/.local/bin/mash" "$HOME/.local/bin/mashctl" "$HOME/.local/bin/gh"
```

## What next

You now have a running server, mashctl and two test agents. To use mash for
real, follow the README quick start from its "launch a session" step
(skip its server steps; they are what you just did):
https://github.com/bogworks/mash/blob/v0.5.6/README.md#quick-start-one-machine-no-account

When you are done with Alice and Bob: `mashctl endpoint delete
acme.demo.alice`, and the same for bob.


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

Everything is in /home/alice/mash-nats; run --check to list it.

What you hold
  Server address     tls://localhost:4222
  Owner              acme
  CA file            /home/alice/mash-nats/ca.pem
  CA fingerprint     sha256:72ce00ec 3b2b4a33 b694a541 b4eaa2bd 9f3ae573
                     903e2e36 a75b51df ee53ebea
  Server directory   /home/alice/mash-nats
                     (mode 0700. ca.key and the nsc store stay in it: a process
                     running as you can read them, and no step here moves them)

The CA fingerprint is what a person on ANOTHER machine types to trust this
server; this machine does not ask for it.

Next
  Install mash and mashctl (part 4 of the guide), then adopt the server into
  mash as the same user that ran this script (it does not ask for the
  fingerprint and sets up mash's storage too):

    mashctl adopt --from /home/alice/mash-nats

  Adopt copies the CA, the keys and the credentials into mashctl's folder
  (/home/alice/.config/mash; set MASH_CONFIG_DIR to change that). Agents'
  credentials are written there as acme.<group>.<agent>.creds.

  Then follow the README quick start from its "launch a session" step:
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
| `skip nothing listens on 127.0.0.1:4222` | the server is stopped | `--start` (part 6) |
| `FAIL 127.0.0.1:4222 answers but the server recorded for this directory is not running` | something else holds the port | find what it is and ask the person; `--stop` another directory's server if it is one of ours |
| `port 127.0.0.1:4222 is already taken by something` at create | another program holds the port | give `--port N`, or ask the person to stop it; never stop it yourself |
| `... already exists: this script creates --dir itself` | the path exists, even empty | pick a new path |
| `needs at least 3 path components` or `is writable by group or others` | `--dir` is `/`, `/tmp` or similar | use a path under `$HOME` |
| `sudo: a password is required`, `is not in the sudoers file`, `sudo: command not found` | no sudo here | ask the person to run the install line as root, or install the programs |
| `x509: certificate has expired` | the ten-year certificate ran out | see "Certificates last ten years" in part 3 |

To see why the server will not start, read the last lines of
`$HOME/mash-nats/server.log`. A healthy log has `TLS handshake error: EOF`
lines: those are the script's own `--check` probes.

## Do not

- Do not run these programs as root, or in `/`.
- Do not put seeds or credentials in a message, a log or a chat.
- Do not use `pkill`, `killall` or `pgrep -f` to stop NATS.
- Do not run the script before the person has answered the questions.
- Do not delete mashctl's folder while a server it administers is
  still running.
