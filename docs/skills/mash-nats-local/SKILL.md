---
name: mash-nats-local
description: >-
  Set up a NATS server on this machine for mash: questions first, then
  one helper script, hand-over to mashctl, and a two-agent test. Use when
  a person or an agent needs a mash server and has none. mashctl does not
  install or start NATS; this does.
---

# Set up a NATS server for mash

DRAFT 4 (0.5.6). For a person, or for an agent following it. Debian or
Ubuntu, 64-bit Intel or AMD only.

mash gives each AI agent session an inbox that keeps its mail, so agents
can message each other. The inbox lives on a NATS server. mashctl is
mash's admin tool: it makes agents' mailboxes on that server. mashctl
does not install or start NATS. This guide does, in six parts:

1. Answer the questions, and read the answers back.
2. Install the prerequisites.
3. Run one script that makes the server.
4. Install mash and mashctl.
5. Hand the server to mashctl, then test it with Alice and Bob.
6. Look after it: stop, start, back up, upgrade, remove.

Every command block below runs on its own in a fresh shell. Nothing
depends on a variable or a directory set by an earlier block. Run the
blocks exactly as they are written; replace the names as the next
section says.

If you are an agent: you may propose each answer, but the person
decides. Ask every question before you run anything that changes
anything. Do not pick paths or ports for them.

## The names in this guide

One sheet, used everywhere. Replace each name in every block if the
person's answers differ.

| In this guide | Meaning | Replace with |
|---|---|---|
| `acme` | owner: the first part of every address | your owner name |
| `demo` | group of agents that may mail each other | your group |
| `alice`, `bob` | the two test agents | your agents |
| `$HOME/mash-nats` | the server's directory | your directory |
| `4222` | the client port | your port |
| `$HOME/.config/mash` | mashctl's folder | your choice |

An agent's address is owner.group.agent, for example acme.demo.alice. On
a command line it is written acme/demo/alice.

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

- a command prints FAILED, an error, or a line you do not understand
- a hash differs from the one printed here
- a path, port or program already exists and this guide did not make it
- you are asked to use root, to turn off a check, or to skip a yes
- the machine is not Debian or Ubuntu on 64-bit Intel or AMD

## What you end up with

```
  one machine
  +----------------------------------------------+
  |  nats-server: TLS, JetStream, operator mode  |
  |  keys, certificates, store, log: in YOUR dir |
  +----------------------+-----------------------+
                         |
            mashctl adopt, then mashctl init
                         |
       mash group "demo":  Alice  <---->  Bob
```

## Words you will meet

- NATS: an open-source message server. mash keeps its mail on one.
- nats-server: the NATS server program. nsc: NATS's own tool for making
  keys and accounts. The script downloads both, checked, into your server
  directory only.
- JetStream: the part of NATS that stores messages on disk.
- TLS: encryption on the connection, as in HTTPS. mash requires it.
- Certificate and CA: the server's ID card, and the authority (made here,
  by you) whose signature clients trust. Clients are given the CA file.
- Operator, account, user: NATS's three levels of trust. The operator
  signs accounts. An account is one separate space; mash uses one,
  called MASH. A user is one identity in an account; each agent is one.
- System account: a built-in account for running the server. mashctl
  uses it to revoke a lost agent key.
- JWT: a signed token that says who someone is and what they may do.
- Seed: a private key, starting with S. Never print it, mail it or paste
  it into a chat.
- Credentials (a .creds file): a user's JWT plus its seed. Whoever holds
  the file is that user.

## What mash needs from NATS

mash needs seven things from the server. The script writes a
`server.conf` that gives them. These are the real lines it wrote in the
test (the JWTs, long, are shown as `<JWT>`):

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

In plain words:

1. `listen`: where the server accepts connections (this machine only,
   unless you said otherwise).
2. `tls`: every connection is encrypted; mash refuses a plain one.
3. `jetstream`: mail is kept on disk in `store_dir`. The account may use
   2 GB, 64 streams (one per agent, plus 5 mash uses itself) and 128
   consumers.
4. `operator`: operator mode: nobody connects without credentials signed
   by this operator.
5. `system_account`: the account mashctl uses to revoke a lost key.
6. `resolver { type: full }`: the server keeps account records on disk,
   so mashctl can add and revoke without restarting the server.
7. `resolver_preload`: the two accounts (system, and MASH) the server
   starts knowing.

If you already run a NATS server, it must give these seven, and you use
`mashctl adopt` against it (part 5) instead of the script.

## What this opens

- The server listens on one address and port. Default: 127.0.0.1:4222,
  this machine only. In the test, `ss -ltn` showed exactly
  `127.0.0.1:4222` and nothing else.
- Other users of this machine can reach that port. They need credentials
  to get in; they cannot read mail or make agents without them.
- `--listen ::1` listens on IPv6 loopback only (`[::1]:4304` in the
  test).
- Any other address (so other machines can connect) listens on all
  interfaces named by it (the test showed `*:4303`). The script then
  makes you type the address at a terminal. You must also give each
  client the CA file, and open a firewall port yourself if you want one.
- The script opens no other port. It does not touch a firewall, a
  service manager (unless you ask for boot start) or any file outside
  your server directory and mashctl's folder.

## 1. Questions

Ask them in order. Each has a default and says what the answer changes.

1. Trial or permanent?
   - Default: trial. You can remove it with one command, and it does not
     return after a reboot.
   - Permanent: also back it up (part 6) and decide about boot start.
2. Is mash already set up on this machine?
   - Default: no. Part 4 checks.
   - Yes, with another server: use a different mashctl folder (question
     5) so you do not overwrite it.
3. Which directory holds the server's files?
   - Default: `$HOME/mash-nats`. It must not exist yet; the script makes
     it (mode 0700). Never `/`, never under `/tmp`, never a top-level
     directory like /demo. Needs 2 GB of disk free.
4. Which port?
   - Default: 4222. If taken, pick another and never stop what holds it.
5. Where does mashctl keep its files?
   - Default: `$HOME/.config/mash` (mode set by mashctl; part 5 shows
     it). Another folder: set `MASH_CONFIG_DIR` in every mashctl and mash
     command.
6. Who may connect?
   - Default: this machine only. Others: a separate yes (see "What this
     opens"). The person names the address and the host name.
7. Owner name?
   - Default: your login name. Lower-case letters, digits, single
     hyphens, starting with a letter, up to 32 characters. One per
     server; changing it means starting over.
8. May this guide use sudo to install prerequisites?
   - Default: only if something in part 2 is missing; ask then.

### Read the answers back

Before running anything that changes anything, say this to the person,
filled in, and wait for a yes:

```
I will:
  make a server directory at   $HOME/mash-nats   (nothing is there yet)
  listen on                    127.0.0.1:4222     (this machine only)
  use the owner name           acme
  keep mashctl's files in      $HOME/.config/mash
  install                      prerequisites with sudo: only if missing
                               nats-server, nsc (in the server dir only)
                               gh, mash, mashctl in $HOME/.local/bin
Say yes to go ahead.
```

## 2. Prerequisites

The script needs curl, tar, unzip, openssl, sha256sum and ss. Read-only
check; it prints `missing:` for each one that is not there:

```sh
for c in curl tar unzip openssl sha256sum ss; do command -v "$c" >/dev/null || echo "missing: $c"; done; echo checked
```

If anything is missing, this needs the sudo yes. It installs from your
distribution only:

```sh
sudo apt-get install -y curl tar unzip openssl iproute2 ca-certificates coreutils
```

Read-only: nothing must exist already that this guide would create. Each
line that prints is a stop:

```sh
for p in "$HOME/mash-nats" "$HOME/.local/bin/mash" "$HOME/.local/bin/mashctl" "$HOME/.local/bin/gh"; do [ ! -e "$p" ] || echo "exists: $p"; done; ss -ltn | grep ':4222 ' ; echo checked
```

A line `exists:` or a line for port 4222 means: stop and ask. Do not
overwrite, move or stop it.

## 3. Make the server

Fetch the script from one exact commit and check its hash. Do not run a
script you have not checked:

```sh
mkdir -p "$HOME/mash-setup" && curl -fsSL -o "$HOME/mash-setup/nats-local-setup.sh" https://raw.githubusercontent.com/bogworks/mash/aab2c1b44faadfcd0657cf72b178a9756306e5c4/scripts/nats-local-setup.sh && echo "0377924074b2d3220e35231cabe2128959090b94646cd62203122c68d4108378  $HOME/mash-setup/nats-local-setup.sh" | sha256sum -c -
```

The last line must say `OK`. The script only writes under the directory
you give it, plus `$HOME/mash-setup`. It makes nothing outside, and it
installs two programs, nats-server v2.15.1 and nsc v2.15.0, into
`$HOME/mash-nats/bin`, after checking each download against a hash that
is written in the script. It runs no program found on your PATH.

Run it with your answers:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --owner acme --start
```

Leave out `--start` to start the server yourself (part 6). For question
4 add `--port N`; for question 6 add `--host NAME` and `--listen ADDRESS`
(the script asks the person to type the address). It prints seven steps,
then what it left, in the output shown in "What you hold" below.

Check it (changes nothing):

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --check
```

If a line starts with FAIL, fix that first.

### Every file, and what it exposes if it leaks

Everything is under `$HOME/mash-nats` (mode 0700). Nobody else keeps a
copy: if it is lost, it is lost. "Lost (every copy)" means this file and
any backup you made.

- `ca.key`: the CA's private key. **If it leaks:** someone can make a
  certificate the clients trust, and pose as your server. **Lost (every
  copy):** you cannot make a new server certificate with this CA.
- `nsc/`: every key and JWT the script made. **If it leaks:** someone can
  make accounts and users, and so agents. **Lost (every copy):** you can
  no longer make accounts or users by hand; the running server and
  mashctl keep working.
- `export/operator.nk`: the operator seed. **If it leaks:** someone can
  sign anything for this server. **Lost (every copy):** mashctl cannot
  revoke a lost agent key. mashctl keeps its own copy.
- `export/account.nk`: the MASH account seed. **If it leaks:** someone
  can make agents and change what they may do. **Lost (every copy):**
  mashctl cannot create agents. mashctl keeps its own copy.
- `export/mashadmin.creds`: the admin user. **If it leaks:** someone can
  administer the server's mail space and read every agent's mail.
  **Lost (every copy):** mashctl cannot administer it; make new ones from
  `nsc/`. mashctl keeps its own copy.
- `export/sys.creds`: the system user. **If it leaks:** someone can
  revoke keys and see the server's state. **Lost (every copy):** mashctl
  cannot revoke keys. mashctl keeps its own copy.
- Agents' creds files, `acme.<group>.<agent>.creds`, in mashctl's folder
  (not here): **if one leaks:** someone can send and read as that agent.
  **Lost:** `mashctl endpoint reissue` makes a new one.
- `server.key`: the server's private key. **If it leaks:** someone can
  pose as your server. **Lost:** the server cannot start; make a new
  server directory.
- `server.pem`, `ca.pem`, `server.conf`, `operator.jwt`, `resolver/`:
  not secret. `ca.pem` is what clients need; give it to them. **Lost:**
  the server cannot start, or clients cannot trust it.
- `jetstream/`: all the mail. Not a key, but private. **Lost:** every
  stored message.
- `server.log`, `server.pid`: the log (lines that look secret are held
  back) and the process id. Not secret.
- `bin/`: the two programs the script installed. **Lost:** run
  `--upgrade` to get them again.

Certificates last ten years: the CA and the server certificate expire
about ten years after the script ran. When they do, clients stop
connecting with an `x509: certificate has expired` error. There is no
renew command in 0.5.6: you make a new server directory (the same
steps), or make a new server certificate by hand from `ca.key`, which is
why you keep the archive in part 5. Accounts and users carry no expiry.

## 4. Install mash and mashctl

Check what you have (read-only):

```sh
"$HOME/.local/bin/mash" version; "$HOME/.local/bin/mashctl" version; echo checked
```

Both must print "No such file" or nothing useful; if one works, stop and
ask (question 2). Debian and Ubuntu put `$HOME/.local/bin` on your PATH
at your next login, when the folder exists; until then every block here
spells the full path.

The releases are signed by the project's own build. You check that with
`gh`, GitHub's tool. Debian's own `gh` is too old for this, so install
the pinned one (v2.105.0, from the GitHub CLI project) into
`$HOME/.local/bin`. This needs the install yes; it overwrites nothing
(it stops if `gh` is there):

```sh
mkdir -p "$HOME/mash-setup/dl" "$HOME/.local/bin" && cd "$HOME/mash-setup/dl" && curl -fsSLO https://github.com/cli/cli/releases/download/v2.105.0/gh_2.105.0_linux_amd64.tar.gz && echo "79e7ab5db738c48777b696e374f8b428f0647debfb424f44d2f426370407fe2f  gh_2.105.0_linux_amd64.tar.gz" | sha256sum -c - && tar -xzf gh_2.105.0_linux_amd64.tar.gz && test ! -e "$HOME/.local/bin/gh" && install -m 755 gh_2.105.0_linux_amd64/bin/gh "$HOME/.local/bin/gh" && "$HOME/.local/bin/gh" --version
```

Download mash, mashctl and the signed record that covers them:

```sh
mkdir -p "$HOME/mash-setup/dl" && cd "$HOME/mash-setup/dl" && for f in mash-linux-amd64 mashctl-linux-amd64 mash.intoto.jsonl; do curl -fsSLO "https://github.com/bogworks/mash/releases/download/v0.5.6/$f"; done; ls -l
```

Verify both. The command needs no GitHub login because it reads the
signed record you just downloaded. On success it prints nothing unless
you run it in a terminal; the exit code is what counts, and this block
prints `verified` only when both pass:

```sh
cd "$HOME/mash-setup/dl" && for f in mash-linux-amd64 mashctl-linux-amd64; do "$HOME/.local/bin/gh" attestation verify "$f" -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6 || exit 1; done; echo verified
```

If it does not print `verified`, stop: do not install the files. Then
install them (each stops if the program is already there):

```sh
cd "$HOME/mash-setup/dl" && for f in mash mashctl; do test ! -e "$HOME/.local/bin/$f" && install -m 755 "$f-linux-amd64" "$HOME/.local/bin/$f" || exit 1; done; "$HOME/.local/bin/mashctl" version
```

## 5. Hand the server to mashctl, then test

Check first. `--dry-run` changes nothing. Every check must say ok:

```sh
MASH_TLS_CA="$HOME/mash-nats/ca.pem" "$HOME/.local/bin/mashctl" adopt --server tls://localhost:4222 --owner acme --account MASH --operator-key "$HOME/mash-nats/export/operator.nk" --account-key "$HOME/mash-nats/export/account.nk" --sys-creds "$HOME/mash-nats/export/sys.creds" --admin-creds "$HOME/mash-nats/export/mashadmin.creds" --dry-run
```

Then the same command without `--dry-run`. It writes mashctl's folder
(default `$HOME/.config/mash`) with its copies of the keys and the admin
and system credentials:

```sh
MASH_TLS_CA="$HOME/mash-nats/ca.pem" "$HOME/.local/bin/mashctl" adopt --server tls://localhost:4222 --owner acme --account MASH --operator-key "$HOME/mash-nats/export/operator.nk" --account-key "$HOME/mash-nats/export/account.nk" --sys-creds "$HOME/mash-nats/export/sys.creds" --admin-creds "$HOME/mash-nats/export/mashadmin.creds"
```

Make mash's storage on the server, once:

```sh
MASH_TLS_CA="$HOME/mash-nats/ca.pem" "$HOME/.local/bin/mashctl" init
```

The mode of mashctl's folder: it holds keys, so it must not be open to
others:

```sh
stat -c '%a %n' "$HOME/.config/mash" "$HOME/.config/mash"/*
```

The first number must be `700`. If it is not, run
`chmod 700 "$HOME/.config/mash"` and tell the person.

### Alice and Bob

```sh
MASH_TLS_CA="$HOME/mash-nats/ca.pem" "$HOME/.local/bin/mashctl" endpoint create acme/demo/alice --make-key
```

```sh
MASH_TLS_CA="$HOME/mash-nats/ca.pem" "$HOME/.local/bin/mashctl" endpoint create acme/demo/bob --make-key
```

```sh
MASH_TLS_CA="$HOME/mash-nats/ca.pem" "$HOME/.local/bin/mashctl" endpoint list
```

Alice sends, Bob reads:

```sh
MASH_TLS_CA="$HOME/mash-nats/ca.pem" MASH_OWNER=acme MASH_GROUP=demo MASH_NAME=alice "$HOME/.local/bin/mash" send acme.demo.bob "Hello Bob"
```

```sh
MASH_TLS_CA="$HOME/mash-nats/ca.pem" MASH_OWNER=acme MASH_GROUP=demo MASH_NAME=bob "$HOME/.local/bin/mash" inbox
```

`[verified]` beside Alice's name means the message is signed by her key.
It works.

### Move the keys off this machine

The files in `export/`, plus `ca.key` and `nsc/`, can sign anything for
this server. The server does not need them to run. After adopt they
belong off this machine. First prove mashctl has its own copies (each
line must print `same`; a `differ` or an error means: do not go on):

```sh
for p in keys/operator.nk keys/account.nk server/sys.creds server/mashadmin.creds; do f="$HOME/mash-nats/export/$(basename "$p")"; [ -s "$f" ] && [ "$(cat "$f")" = "$(cat "$HOME/.config/mash/$p")" ] && echo "same $p" || echo "differ $p"; done
```

Then retire them. This command asks the person to type the server
directory at a terminal, so an agent cannot run it; the person does. It
writes one archive (mode 0600), checks that it lists back, and only then
removes `ca.key`, `nsc/` and `export/` from the server directory. The
server keeps running, and keeps `server.pem`, `server.key`, `ca.pem`:

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --retire-keys "$HOME/mash-nats-keys.tar.gz"
```

Then move `mash-nats-keys.tar.gz` off this machine: an encrypted disk or
a password manager's file store, not a plain backup, not a chat. Keep it:
it is the only copy of the CA key and the keys that make new accounts.
Check the result (changes nothing):

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --check
```

## 6. Look after it

Each is one script command, with the same `--dir`. Stop acts only on
the pid it recorded, and only if that pid is this server.

Stop:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --stop
```

Start (after a reboot, for example):

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --start
```

Back up (the archive holds secrets: keep it private and off this
machine). It stops the server for a moment and restarts it:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --backup "$HOME/mash-nats-backup.tar.gz"
```

Upgrade nats-server and nsc to the versions the script pins:

```sh
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --upgrade
```

Start at boot is a separate yes: it keeps a network server running
after you log out. The person types the confirmation, so an agent
cannot run these. `--linger` also runs `loginctl enable-linger`, which
may say "Access denied" for an ordinary user; the script then prints the
command for you to run:

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --enable-boot
```

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --disable-boot
```

Remove it all. This deletes `$HOME/mash-nats` with everything in it: the
mail, and the keys if you have not retired them. The person types the
directory name to confirm. It leaves mashctl's folder and says so:

```sh person
bash "$HOME/mash-setup/nats-local-setup.sh" --dir "$HOME/mash-nats" --remove
```

## What you hold

The script ends by printing this. It is the real output of the test
(after `Done.`); your paths differ. Its commands say `mashctl`; until
your next login, when `$HOME/.local/bin` joins your PATH, use the full
path `$HOME/.local/bin/mashctl`, as the blocks above do. Read it back to
the person:

<!-- closing-begin -->
```
Done. The setup is in /home/alice/mash-nats. Nothing above printed a seed or a
credential, and nothing below does.

Other users on this machine can still reach 127.0.0.1:4222; they cannot connect
without credentials.

Files (all under /home/alice/mash-nats):
  bin/nats-server          server, v2.15.1 (sha256 checked)
  bin/nsc                  nsc, v2.15.0 (sha256 checked)
  ca.pem                   CA certificate: give this to clients
  server.conf              nats-server configuration (no seeds)
  export/mashadmin.creds   admin credentials (secret)
  export/sys.creds         system account credentials (secret)
  export/operator.nk       operator seed (secret)
  export/account.nk        account seed (secret)
  ca.key                   CA private key (secret)
  nsc/                     nsc store with all signing keys (secret)
  server.log               server log (secret-looking lines withheld)
  server.pid               server process id (--stop reads it)

What you hold
  Server address     tls://localhost:4222
  CA file            /home/alice/mash-nats/ca.pem
  Server directory   /home/alice/mash-nats
                     (mode 0700; holds the CA key and the nsc keys until you run
                     --retire-keys)

Next
  1. Adopt the server into mash. One command; run it as it is:

    MASH_TLS_CA=/home/alice/mash-nats/ca.pem mashctl adopt \
    --server tls://localhost:4222 \
    --owner acme \
    --account MASH \
    --operator-key /home/alice/mash-nats/export/operator.nk \
    --account-key /home/alice/mash-nats/export/account.nk \
    --sys-creds /home/alice/mash-nats/export/sys.creds \
    --admin-creds /home/alice/mash-nats/export/mashadmin.creds

  2. Create the directory and lease buckets:

    mashctl init

  mashctl keeps its files in /home/alice/.config/mash (set MASH_CONFIG_DIR to
  change that). Agents' credentials are written there as
  acme.<group>.<agent>.creds.

  To use mash in Claude Code, run these two lines in Claude Code, then see the
  guide:

    claude plugin marketplace add bogworks/mash
    claude plugin install mash@mash

  https://github.com/bogworks/mash/blob/v0.5.6/docs/install.md

  3. Looking after the server later (each takes --dir):

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

  remove it all
    /home/alice/mash-setup/nats-local-setup.sh --dir /home/alice/mash-nats \
        --remove
```
<!-- closing-end -->

## If something is wrong

- "port is in use": another process holds it. Pick another port.
- `x509: certificate signed by unknown authority`: MASH_TLS_CA is not
  the `ca.pem` of this server.
- `x509` host name error: the name in the URL is not in the certificate.
  Make a new server directory with `--host`.
- adopt says JetStream is not enabled: the configuration has no
  JetStream block, or the disk limit is 0.
- The script refuses a directory: read its message; it says which
  component is wrong. Do not work around it.

## Do not

- Do not run these programs as root, or in `/`.
- Do not put seeds or credentials in a message, a log or a chat.
- Do not use `pkill`, `killall` or `pgrep -f` to stop NATS.
- Do not run the script before the person has answered the questions.
- Do not delete `export/`, `nsc/` or `ca.key` by hand: use
  `--retire-keys`, which keeps the only copy.
