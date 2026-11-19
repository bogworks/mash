---
name: mash-nats-local
description: Set up a local NATS server that mash can use, then hand it to mashctl. Use when a person or an agent needs a mash server and has none, or has a nats-server that must be checked against what mash needs. mashctl does not install or start NATS; this skill does.
---

# Set up a local NATS server for mash

DRAFT (0.5.6 design, not yet verified end to end by a person).

mash needs a NATS server. mashctl does not install, configure or start
one: that is your machine, your choices. This skill does it, step by
step, with plain tools (`nats-server`, `nsc`, `openssl`) and one helper
script, `scripts/nats-local-setup.sh`. When the server is up, you give
mashctl its keys with `mashctl adopt` and it takes over from there.

Read the whole skill first. Ask every question in "Ask first" before you
run anything. If you are an agent, ask the person; do not choose paths,
ports or names for them.

## What you end up with

```
  your machine
  +--------------------------------------------------+
  |  nats-server  (TLS, JetStream, operator mode)    |
  |     keys, certificates, log: in YOUR directory   |
  +-----------------------+--------------------------+
                          |
              mashctl adopt, then mashctl init
                          |
         mash's buckets, then endpoints (Alice, Bob)
```

- One `nats-server` process, TLS only, JetStream on file storage.
- Operator mode: one operator, a system account, one account for mash.
- A certificate authority of your own and a server certificate it signs.
- Four files for mashctl, in `<dir>/export/`: the operator seed,
  the account seed, system-user credentials, admin-user credentials.

## Why each piece (so you can explain it to the person)

| Piece | Why mash needs it |
|---|---|
| TLS | mash refuses a plain `nats://` URL. Every connection is TLS. |
| Operator + account + resolver | Each endpoint is a NATS *user* that signs its mail. mash accepts only JWT user credentials, and mashctl writes revocations into the account through a full resolver. |
| System account | mashctl revokes a key by pushing an update through it. |
| JetStream (file) | Inboxes and directory buckets are JetStream streams and key-value buckets. |
| Admin user | mashctl creates the buckets and endpoints as this user. |
| Seed | A NATS private key, printed as `SO...` (operator), `SA...` (account), `SU...` (user). Treat it as a password. Never print or mail it. |

## Ask first

Ask these, in this order. Write the answers down; every later command
uses them. Each row says what the answer changes.

| # | Question | If the answer is... | Then |
|---|---|---|---|
| 1 | Is a nats-server already running that mash must use? | Yes | Skip to "An existing server". Do not run the script against it. |
| | | No | Continue. |
| 2 | Is `nats-server` installed (`nats-server --version`, 2.10 or newer)? | No | Show the person the official install page, https://docs.nats.io/running-a-nats-service/introduction/installation, and the command for their system (package manager, release binary, or `go install`). Install only after they say which. |
| 3 | Are `nsc` and `openssl` installed? | No | Same: show options, let them choose. `nsc` is at https://github.com/nats-io/nsc/releases. |
| 4 | Which directory holds the server's files (keys, certificates, store, log)? | A path they name | Use it. It must not exist yet or be empty. Never `/`, never a top-level directory such as `/demo`. |
| | | "I don't know" | Offer `$HOME/mash-nats` (their own home). Confirm it. |
| 5 | Which disk? | The directory is on a small disk | The JetStream store is in `<dir>/jetstream`. Pick a directory on the disk that has room (the account limit is 2G by default; see question 9). |
| 6 | Which port? | 4222 free | Use it. |
| | | 4222 taken, or other NATS servers run here | Ask for another port. Check it is free (`ss -ltn`). On a shared machine, reserve it with whatever tool they use. |
| 7 | Only this machine, or other machines too? | This machine only | `--listen 127.0.0.1` (default). The certificate covers `localhost`, `127.0.0.1`, `::1`. |
| | | Other machines | Ask the address or DNS name clients will use. Pass it as `--host NAME` (repeat for several) and `--listen 0.0.0.0` (or a specific address). The certificate must list every name clients type. Tell them the CA file, `<dir>/ca.pem`, must be copied to every client machine. Open the firewall port yourself or ask them to. |
| 8 | Owner name (the first part of every address)? | e.g. `acme` | Used later in `mashctl adopt --owner`. Lower-case letters, digits, single hyphens, starts with a letter, 1 to 32 characters. One owner per server. |
| 9 | How much mail disk? | Default is fine | `--js-disk 2G` (the default). It also limits the account to 64 streams and 128 consumers: 5 buckets plus one inbox per endpoint, so about 59 endpoints. |
| | | More endpoints or more mail | Raise `--js-disk` and edit the stream and consumer limits in the script; make sure the disk has more free space than the limit. |
| 10 | Start it now, and how should it run? | Now, for a trial | `--start` (detached, pid and log in `<dir>`). |
| | | It must survive reboots | Do not use `--start`. Write a service unit for their init system (systemd, launchd) that runs `nats-server -c <dir>/server.conf`. Ask which one. |

## Steps

Run these yourself, or print them for the person to run. Substitute
your answers for `<dir>`, `<port>`, `<owner>`.

### 1. Check the tools

```sh
nats-server --version
nsc --version
openssl version
```

### 2. Make the server

```sh
scripts/nats-local-setup.sh --dir <dir> --port <port> --start
```

Add `--host NAME` and `--listen ADDR` for other machines (question 7)
and `--js-disk SIZE` (question 9). The script prints one line per step.
It creates everything under `<dir>`, touches nothing outside it, never
prints a secret, and does not change `~/.nsc` or any NATS config of
yours. Read the script before you run it; it is short.

What it makes:

| Path (under `<dir>`) | What it is | Secret |
|---|---|---|
| `ca.pem` | Your CA's certificate. Clients trust this. | No |
| `ca.key` | The CA's private key. | Yes |
| `server.pem`, `server.key` | The server certificate and its key. | `server.key` |
| `server.conf` | nats-server configuration: TLS, operator, full resolver, JetStream. | No |
| `jetstream/`, `resolver/` | The mail store; the account records. | The store holds mail |
| `export/operator.nk` | Operator seed. Lets mashctl revoke keys. | Yes |
| `export/account.nk` | Account seed. Lets mashctl create endpoints. | Yes |
| `export/sys.creds` | System-user credentials. | Yes |
| `export/mashadmin.creds` | Admin-user credentials for the mash account. | Yes |
| `server.log`, `server.pid` | Log; process id (with `--start`). | No |

### 3. Check it

```sh
scripts/nats-local-setup.sh --dir <dir> --check
```

It tests the configuration, the file modes (secrets are 0600), and, if
the server is running, that the port answers with TLS. Stop if it
reports a problem; fix that before going on.

### 4. Give the keys to mashctl

Use a configuration directory of your own (ask; do not let mashctl pick
`~/.config/mash` silently if the person has another mash setup):

```sh
export MASH_CONFIG_DIR=<config dir>
export MASH_TLS_CA=<dir>/ca.pem
mashctl adopt --server tls://localhost:<port> --owner <owner> \
  --account MASH \
  --operator-key <dir>/export/operator.nk \
  --account-key  <dir>/export/account.nk \
  --sys-creds    <dir>/export/sys.creds \
  --admin-creds  <dir>/export/mashadmin.creds --dry-run
```

`--dry-run` only checks. Run it again without `--dry-run` when every
check says ok. adopt copies the keys into mashctl's key store, so after
it finishes you may move `<dir>/export/` somewhere safe (a password
manager, an encrypted volume) or delete it; the server does not need it.

Then make mash's buckets, once:

```sh
mashctl init
```

### 5. Try it: Alice and Bob

```sh
mashctl endpoint create <owner>/demo/alice --make-key
mashctl endpoint create <owner>/demo/bob --make-key
mashctl endpoint list
```

`<owner>/demo/alice` is owner, group, endpoint. The owner is who runs
the server; the group is a set of agents that may mail each other; the
endpoint is one agent. Its mail address is `<owner>.demo.alice`.

```sh
MASH_OWNER=<owner> MASH_GROUP=demo MASH_NAME=alice \
  mash send <owner>.demo.bob "Hello Bob"
MASH_OWNER=<owner> MASH_GROUP=demo MASH_NAME=bob mash inbox
```

If Bob's inbox shows the message as `[verified]`, mash works.

## An existing server

If a nats-server already runs, do not change it with this skill. Check
it against this list, with the person who runs it. Anything missing is
theirs to add.

- TLS on the client port, with a certificate that lists the names
  clients use, and a CA file clients can be given.
- Operator mode with a full resolver (`resolver { type: full ... }`).
- A system account, and credentials of a system user.
- An account for mash, with JetStream limits in its JWT (disk, 64
  streams, 128 consumers) and JetStream on in the server configuration
  (`jetstream { store_dir: ... }`, file storage).
- The operator seed and the account identity seed, as files
  (mode 0600), and user credentials for the account.

Then go to step 4. `mashctl adopt` checks all of this read-only first
and says what is wrong.

## Stop and remove

Stop only the process this script started:

```sh
scripts/nats-local-setup.sh --dir <dir> --stop
```

It reads `<dir>/server.pid` and refuses unless that process is
`nats-server -c <dir>/server.conf`. Never stop NATS by process name.
To remove the server, delete `<dir>` by that exact path, after you have
stopped it. Mail is in `<dir>/jetstream`.

## If something is wrong

| You see | Cause |
|---|---|
| `port <n> is in use` | Another process holds it. Ask for another port. Do not stop the other process. |
| adopt: `refused a connection without credentials` is missing | The server is not in operator mode; the configuration is not the one the script wrote. |
| `x509: certificate signed by unknown authority` | `MASH_TLS_CA` is not `<dir>/ca.pem`. |
| `x509` host name error | The name in the URL is not in the certificate. Remake with `--host`. |
| adopt: account JWT is named `MASH`, not `<owner>` | Pass `--account MASH`. |
| adopt: JetStream not enabled | The server configuration has no `jetstream` block, or the disk limit is 0. |

## Do not

- Do not run `nats-server`, `nsc` or the script as root, or in `/`.
- Do not put seeds or credentials in a message, a log, or a chat.
- Do not use `pkill`, `killall` or `pgrep -f` for NATS.
- Do not set up the server for the person without their answers to the
  questions above.
