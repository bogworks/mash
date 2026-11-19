---
name: mash-nats-local
description: >-
  Set up a NATS server on this machine for mash: questions first,
  then installs, one helper script, hand-over to mashctl, and a
  two-agent test. Use when a person or an agent needs a mash server
  and has none. mashctl does not install or start NATS; this does.
---

# Set up a NATS server for mash

DRAFT 2 (0.5.6). For a person, or for an agent following it.

mash gives each AI agent session an inbox that keeps its mail, so agents
can message each other. The inbox lives on a NATS server. mashctl is
mash's admin tool: it creates agents' mailboxes on that server. mashctl
does not install or start NATS. This guide does, in five parts:

1. Answer the questions.
2. Install the four programs.
3. Run one script that makes the server.
4. Hand the server to mashctl.
5. Test it with two agents, Alice and Bob.

If you are an agent: you may propose each answer, but the person
decides. Ask every question before you run anything. Do not pick paths
or ports for them. Do not run commands with sudo unless they said you may.

## What you end up with

```
  one machine ("myserver")
  +----------------------------------------------+
  |  nats-server: TLS, JetStream, operator mode  |
  |  keys, certificates, store, log: in YOUR dir |
  +----------------------+-----------------------+
                         |
            mashctl adopt, then mashctl init
                         |
       mash group "demo":  Alice  <---->  Bob
```

An agent's address has three parts, owner.group.agent, for example
acme.demo.alice. On the command line it is written acme/demo/alice. The
owner runs the server, the group is a set of agents that may mail each
other, the agent is one session or tool.

## Words you will meet

- NATS: an open-source message server. mash keeps its mail on one.
- nats-server: the NATS server program.
- nsc: NATS's own tool for making keys and accounts. The script uses it.
- JetStream: the part of NATS that stores messages on disk.
- Stream, consumer, bucket: JetStream's storage units. A stream holds
  messages, a consumer reads one, a bucket is a small key-value store.
  mash makes them for you; you only meet the counts in the limits.
- TLS: encryption on the connection, as in HTTPS. mash requires it.
- Certificate and CA: the server's ID card, and the authority (made here,
  by you) whose signature clients trust. Clients are given the CA file.
- Operator, account, user: NATS's three levels of trust. The operator
  signs accounts. An account is one separate space; mash uses one,
  called MASH. A user is one identity in an account; each agent is one.
- System account: a built-in account for running the server. mashctl
  uses it to revoke a lost agent key.
- JWT: a signed token that says who someone is and what they may do.
- Seed: a private key, starting with S (SO operator, SA account, SU
  user). Never print it, mail it or paste it into a chat.
- Credentials (a .creds file): a user's JWT plus its seed. Whoever holds
  the file is that user.

## 1. Questions

Ask them in order. Each has a default and says what the answer changes.
Write the answers down.

1. Trial or permanent?
   - Default: trial. Reason: you can remove it with one command.
   - Trial: start the server from the script, stop it when finished. It
     does not return after a reboot.
   - Permanent: also set up the restart unit in part 6, and keep the
     files somewhere backed up.

2. Is mash already installed and set up on this machine?
   - Default: no. Check with the `mash version` and `mashctl version`
     commands in part 2.
   - Yes, and it works with another server: use a new, separate
     MASH_CONFIG_DIR (question 6) so you do not overwrite it.

3. May this guide install software, and may it use sudo?
   - Default: install in your own home, no sudo. Reason: nothing outside
     your home changes. It uses `~/.local/bin`.
   - With sudo: you may use `apt install` for the nats-server package.
   - Say what is allowed before part 2. Never use sudo unasked.

4. Which directory holds the server's files (keys, certificates, mail,
   log)?
   - Default: `~/mash-nats` (for example /home/alice/mash-nats).
   - It must not exist, or be empty. Never `/`, never a top-level
     directory like /demo. It needs free disk: 2 GB is the default mail
     limit.

5. Which port?
   - Default: 4222, the NATS standard.
   - If it is taken (another NATS runs here), pick another. Check:
     `ss -ltn | grep :4222`. Never stop the other server.

6. Where does mashctl keep its settings and key copies?
   - Default: `~/.config/mash`.
   - To keep it apart from an existing mash, choose another folder, for
     example `~/mash-config`, and set MASH_CONFIG_DIR to it (part 4).

7. Who may connect: this machine only, or others too?
   - Default: this machine only. The server listens on 127.0.0.1.
   - Others: give the name or address they will type. Add `--host NAME`
     (repeat for several) and `--listen 0.0.0.0` in part 3. See "Who can
     connect" below. Copy `ca.pem` to every client machine.

8. Where should `export/` end up after hand-over?
   - Default: leave it in place. Reason: mashctl keeps a pointer to
     `export/mashadmin.creds`, not a copy.
   - You may move `operator.nk`, `account.nk` and `sys.creds` somewhere
     safe after part 4 (a password manager, an encrypted disk): mashctl
     has its own copies. Keep `mashadmin.creds` where it is.

9. Owner name (the first part of every address)?
   - Default: your login name, for example alice. Lower-case letters,
     digits, single hyphens, starting with a letter, up to 32 characters.
   - One owner per server. It can be changed only by starting over.

The rest are defaults you need not ask about: 2 GB of mail disk, up to 64
streams (about 59 agents), the account named MASH.

## Who can connect

By default only programs on this machine: the server listens on
127.0.0.1, which other machines cannot reach. Every connection uses TLS,
and a client must also hold credentials for the MASH account, or the
server refuses it before it says anything (mashctl adopt checks this).
With `--listen 0.0.0.0` the port is open to your whole network: anyone who
can reach it can start a TLS handshake, but only a holder of valid
credentials gets in, and only to the MASH account. The certificate names
only the hosts you list with `--host`. Use a firewall as well.

## 2. Install the four programs

Check what you already have:

```sh
nats-server --version
```

```sh
nsc --version
```

```sh
mash version
```

```sh
mashctl version
```

The versions this guide was tested with, and the minimum:

- nats-server 2.15.1 (minimum: 2.15)
- nsc 2.15.0 (minimum: 2.15)
- mash and mashctl from the same release, 0.5.5 or newer
- openssl 3 (Debian already has it: `openssl version`)

Install what is missing (Debian or Ubuntu, 64-bit Intel or AMD; for an
Arm machine, use arm64 in the names). Make a working folder first:

```sh
mkdir -p ~/mash-setup ~/.local/bin
```

```sh
cd ~/mash-setup
```

nats-server, with sudo (the Debian package):

```sh
NV=v2.15.3
```

```sh
GH=https://github.com/nats-io/nats-server/releases/download
```

```sh
curl -fsSLO $GH/$NV/nats-server-$NV-amd64.deb
```

```sh
sudo dpkg -i nats-server-$NV-amd64.deb
```

Without sudo, download `nats-server-$NV-linux-amd64.tar.gz` from the same
place, unpack it, and copy the `nats-server` file to `~/.local/bin`.

nsc:

```sh
NS=https://github.com/nats-io/nsc/releases/download/v2.15.0
```

```sh
curl -fsSLO $NS/nsc-linux-amd64.zip
```

```sh
unzip -o nsc-linux-amd64.zip nsc -d ~/.local/bin
```

mash, from the latest release (the SHA256SUMS file lists the right hash):

```sh
MR=https://github.com/bogworks/mash/releases/latest/download
```

```sh
curl -fsSLO $MR/mash-linux-amd64
```

```sh
curl -fsSLO $MR/SHA256SUMS
```

```sh
grep ' mash-linux-amd64$' SHA256SUMS | sha256sum -c
```

```sh
install -m 755 mash-linux-amd64 ~/.local/bin/mash
```

mashctl is not a release download yet. Until it is, build it. This needs
Go 1.27.4 or newer (https://go.dev/dl/):

```sh
go install github.com/bogworks/mash/cmd/mashctl@latest
```

Go puts it in `$(go env GOPATH)/bin`. Make sure `~/.local/bin` and that
folder are on your PATH, then run the four version commands again.

## 3. Make the server (one command)

Get the script from one exact version of the repository and check its
hash. Do not run a script you have not checked:

```sh
export MASH_REF=84cc7ffed66152af2f3ea194f7713b1aba2a4a62
```

```sh
RAW=https://raw.githubusercontent.com/bogworks/mash/$MASH_REF/scripts
```

```sh
curl -fsSLO $RAW/nats-local-setup.sh
```

```sh
sha256sum nats-local-setup.sh
```

The line must start with:

```
35eb7b81a96667f4d7fef118f54a45cd43f9af7636cae98b3b78a4bb27439746
```

Read the script; it is short and only writes under the directory you
give it. Then run it with your answers (question 4 and 5; add `--host`,
`--listen` for question 7):

```sh
bash nats-local-setup.sh --dir ~/mash-nats --port 4222 --start
```

Leave out `--start` if you want to start the server yourself (part 6).
You should see six steps, then the list of files. It looks like this (the
key names after "operator" and "account" differ on your machine):

```
[1] about to: preflight (tools, directory, port)
    did: nsc version 2.15.0; OpenSSL 3.3.7; nats-server: v2.15.1
    did: tools found; /home/alice/mash-nats is usable (mode 0700);
         127.0.0.1:4222 is free

[2] about to: make a private CA and a server certificate (ECDSA P-256,
    10 years)
    did: ca.pem, ca.key, server.pem, server.key written; SANs:
         DNS:localhost,IP:127.0.0.1,IP:::1
...
[6] about to: start nats-server detached
    did: nats-server pid 227 listens on 127.0.0.1:4222 (log
         /home/alice/mash-nats/server.log)

Done. The setup is in /home/alice/mash-nats. Nothing above printed a seed
or a credential, and nothing below does.

Files left for you (all under /home/alice/mash-nats):
  ca.pem                   CA certificate: give this to clients
  server.conf              nats-server configuration (no seeds)
  export/mashadmin.creds   admin credentials (secret)
  export/sys.creds         system account credentials (secret)
  export/operator.nk       operator seed (secret)
  export/account.nk        account seed (secret)
  server.log               server log
  server.pid               server process id (--stop reads it)
```

Check it:

```sh
bash nats-local-setup.sh --dir ~/mash-nats --check
```

```
checking the setup in /home/alice/mash-nats (creates nothing)
  ok    /home/alice/mash-nats mode 700
  ok    /home/alice/mash-nats/ca.key mode 600
...
  ok    nats-server -t -c: nats-server: configuration file
        /home/alice/mash-nats/server.conf is valid
  ok    127.0.0.1:4222 answers and requires TLS
```

If a line starts with FAIL, fix that first.

### What each file is for

Every file in the directory, what breaks if you lose it, and where
mashctl keeps its own copy. `nsc/` holds the master copy of all keys.

- `nsc/`: every key and JWT the script made.
  Lost: you can no longer make new accounts or users by hand; the running
  server and mashctl keep working. Back it up.
  mashctl copy: none.
- `ca.pem`: the CA certificate. Clients use it to trust the server.
  Lost: clients cannot connect until you give them a copy; it is not
  secret. mashctl copy: none; you set MASH_TLS_CA to its path.
- `ca.key`: the CA's private key. Secret.
  Lost: you cannot make a new server certificate with the same CA.
  mashctl copy: none, it never needs it.
- `server.pem`, `server.key`: the server's certificate and key.
  Lost: the server cannot start. `server.key` is secret.
  mashctl copy: none.
- `server.conf`, `operator.jwt`, `resolver/`: the server's configuration
  and account records. Not secret.
  Lost: the server cannot start with its old accounts.
  mashctl copy: none.
- `jetstream/`: all the mail. Not a key, but private.
  Lost: all stored messages. mashctl copy: none.
- `export/operator.nk`: operator seed. Secret.
  Lost: mashctl cannot revoke a lost agent key.
  mashctl copy: `keys/operator.nk` in MASH_CONFIG_DIR.
- `export/account.nk`: account seed. Secret.
  Lost: mashctl cannot create agents.
  mashctl copy: `keys/account.nk` in MASH_CONFIG_DIR.
- `export/sys.creds`: system user credentials. Secret.
  Lost: mashctl cannot revoke keys.
  mashctl copy: `server/sys.creds` in MASH_CONFIG_DIR.
- `export/mashadmin.creds`: admin user credentials. Secret.
  Lost: mashctl cannot administer the server.
  mashctl copy: none, config.json holds its path. Keep it.
- `server.log`, `server.pid`: log and process id. Not secret.

## 4. Hand the server to mashctl

Use the folder from question 6. Skip the first line if you chose the
default `~/.config/mash`:

```sh
export MASH_CONFIG_DIR=~/mash-config
```

```sh
export MASH_TLS_CA=~/mash-nats/ca.pem
```

```sh
cd ~/mash-nats/export
```

Check first. `--dry-run` changes nothing. Use your owner name:

```sh
mashctl adopt --server tls://localhost:4222 --owner acme \
  --account MASH --operator-key operator.nk --account-key account.nk \
  --sys-creds sys.creds --admin-creds mashadmin.creds --dry-run
```

Every check should say ok (output shortened):

```
mashctl adopt: checking tls://localhost:4222 as owner "acme", account
"MASH" (read-only: nothing is written until every check passes)
  1. files          ok: operator key O..., account key A...
  2. server         ok: refused a connection without credentials ...
  ...
  6. jetstream      ok: the server answered for the account: 0 stream(s)
All checks passed. --dry-run: nothing is written. ...
```

Then run the same command again without `--dry-run`. It prints the same
checks, then the files it wrote (shortened):

```
All checks passed.
  operator key O... imported in /home/alice/mash-config/keys
  account key A... imported in /home/alice/mash-config/keys
  system creds copied at /home/alice/mash-config/server/sys.creds
  record the admin creds path /home/alice/mash-nats/export/mashadmin.creds
    (the file is not copied)
  write /home/alice/mash-config/config.json with "url": ...
```

Then make mash's storage on the server, once:

```sh
mashctl init
```

```
mashctl: directory and lease buckets ready
```

(The real output has a first line about the target server; it is long and
not shortened here.) Now do what you decided in question 8.

## 5. Try it: Alice and Bob

```sh
mashctl endpoint create acme/demo/alice --make-key
```

```
created endpoint acme/demo/alice
  address  acme.demo.alice
  creds    /home/alice/mash-config/acme.demo.alice.creds
```

(The real output adds a first and a last line; see mashctl help.) Now Bob:

```sh
mashctl endpoint create acme/demo/bob --make-key
```

```sh
mashctl endpoint list
```

```
acme/demo/alice  acme.demo.alice  acme_demo_alice  created 2026-11-19T01:57:35Z
acme/demo/bob  acme.demo.bob  acme_demo_bob  created 2026-11-19T01:57:35Z
2 endpoints
```

Alice sends, Bob reads:

```sh
export MASH_OWNER=acme MASH_GROUP=demo
```

```sh
MASH_NAME=alice mash send acme.demo.bob "Hello Bob"
```

```
sent #1 from acme.demo.alice to acme.demo.bob
```

```sh
MASH_NAME=bob mash inbox
```

```
#1  from acme.demo.alice [verified]  2026-11-19T01:57:35Z  9 B  Hello Bob
```

`[verified]` means the message is signed by Alice's key. It works.

## 6. Stop, start again, remove

Stop the server the script started. It stops only the process whose id is
in `server.pid`, and only if it is this server:

```sh
bash nats-local-setup.sh --dir ~/mash-nats --stop
```

Your mail stays in `jetstream/`. Start it again by hand (after a reboot,
for example):

```sh
nohup nats-server -c ~/mash-nats/server.conf >> ~/mash-nats/server.log 2>&1 &
```

This does not update `server.pid`, so `--stop` will later say the pid is
stale. Stop that one with `kill` and the pid you started it with.

To start it at every boot as a systemd user service, write
`~/.config/systemd/user/mash-nats.service` with this (the paths are your
own; `command -v nats-server` shows the first one):

```
[Unit]
Description=NATS server for mash

[Service]
ExecStart=/usr/bin/nats-server -c /home/alice/mash-nats/server.conf
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user daemon-reload
```

```sh
systemctl --user enable --now mash-nats
```

```sh
loginctl enable-linger "$USER"
```

(The unit file was not run in the tests for this guide; check it with
`systemctl --user status mash-nats`.) Stop the script's own copy first,
or the port is taken.

Full removal. Stop the server, then delete these by their exact paths:

- the server directory, `~/mash-nats`
- mashctl's folder, `~/mash-config` (keys, copies, and the agents' creds)
- `~/mash-setup`, the downloads
- the unit file, if you made one

Delete mashctl's folder only when you will not use this server again:
without the keys nobody can revoke or add agents.

## You now hold

- The server address: `tls://localhost:4222`
- The CA file: `~/mash-nats/ca.pem`
- The admin credentials: `~/mash-nats/export/mashadmin.creds`
- mashctl's folder, with its copy of the operator and account keys

## Next

To use mash from Claude Code, install the plugin and start a session as
Alice or Bob, with the same three variables:

```sh
claude plugin install mash@mash
```

See docs/install.md for the rest.

## If something is wrong

- "port is in use": another process holds it. Pick another port.
- `x509: certificate signed by unknown authority`: MASH_TLS_CA is not
  the `ca.pem` of this server.
- `x509` host name error: the name in the URL is not in the certificate.
  Make a new server directory with `--host`.
- adopt says the account is named MASH, not your owner: add
  `--account MASH`.
- adopt says JetStream is not enabled: the configuration has no
  JetStream block, or the disk limit is 0.

## Do not

- Do not run these programs as root, or in `/`.
- Do not put seeds or credentials in a message, a log or a chat.
- Do not use `pkill`, `killall` or `pgrep -f` to stop NATS.
- Do not run the script before the person has answered the questions.
