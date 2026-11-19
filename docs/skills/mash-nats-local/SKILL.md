---
name: mash-nats-local
description: >-
  Set up a local NATS server that mash can use, then hand it to mashctl.
  Use when a person or an agent needs a mash server and has none,
  or has a nats-server that must be checked against what mash needs.
  mashctl does not install or start NATS; this skill does.
---

# Set up a local NATS server for mash

DRAFT 1 (0.5.6). Written for a person or for an agent following it.

mash needs a NATS server. mashctl does not install, configure or start
one: that is your machine and your choice. This skill asks you a few
questions, then runs one helper script. The script leaves a server and
the files mashctl needs. Then mashctl takes over.

If you are an agent: ask the person every question below before you run
anything. Do not pick paths, ports or names for them.

## What you end up with

```
  your machine ("myserver")
  +----------------------------------------------+
  |  nats-server: TLS, JetStream, operator mode  |
  |  keys, certificates, store, log: in YOUR dir |
  +----------------------+-----------------------+
                         |
          mashctl adopt, then mashctl init
                         |
     mash group "demo":  Alice  <---->  Bob
```

- One nats-server process, TLS only, mail kept on disk (JetStream).
- One operator, one system account, one account for mash.
- Your own certificate authority (CA) and a server certificate.
- Four files for mashctl, in one folder (`export/`).

## Words you will meet

- Seed: a NATS private key. It starts with S (SO for the operator, SA
  for an account, SU for a user). Treat it like a password. Never print
  it, mail it or paste it into a chat.
- Operator: owns the server's trust. Signs the accounts.
- Account: one tenant. mash uses one account, called MASH here.
- User credentials (a .creds file): a user's identity plus its seed. Each
  mash endpoint (an agent, a tool) gets its own, made later by mashctl.
- Resolver: how the server learns accounts. mash needs the full kind.
- Endpoint path: owner/group/endpoint, for example acme/demo/alice. The
  owner runs the server, the group is a set of agents that may mail each
  other, the endpoint is one agent. Its mail address is acme.demo.alice.

## Ask first

Ask these in order. Write the answers down. Each says what it changes.

1. Is a nats-server already running that mash must use?
   - Yes: go to "An existing server". Do not run the script on it.
   - No: continue.

2. Are `nats-server`, `nsc` and `openssl` installed? Check with the
   three commands in step 1 below. For each one that is missing, show
   the person where to get it and let them pick how:
   - nats-server: https://nats.io/download
   - nsc: https://github.com/nats-io/nsc/releases (or `brew install nsc`
     on macOS and Linuxbrew, or `go install github.com/nats-io/nsc/v2@latest`)
   - openssl: the system package manager
   Install only after the person says yes and says how. A later mashctl
   could do the nsc step itself; for 0.5.6 it is this skill.

3. Which directory holds the server's files (keys, certificates, mail
   store, log)? A path the person names, such as /home/alice/mash-nats.
   It must not exist yet, or be empty. Never `/`, never a top-level
   directory such as /demo. If they have no idea, suggest a folder in
   their own home and confirm it.

4. Does that disk have room? The mail store is in `<dir>/jetstream`.
   The account is limited to 2G by default. Pick a directory on a disk
   with more free space than the limit.

5. Which port? 4222 is the NATS default. If it is taken, or other NATS
   servers run on this machine, ask for another and check that it is
   free. Never stop the other server to free a port.

6. Only this machine, or other machines too?
   - This machine only: nothing to add. The server listens on 127.0.0.1.
   - Other machines: ask the name or address clients will type. Pass it
     as `--host NAME` (repeat for several) and add `--listen 0.0.0.0`.
     The certificate lists only the names you give. Copy `ca.pem` to
     every client machine. Open the port in the firewall, or ask them.

7. What is the owner name? It is the first part of every address. Lower
   case letters, digits and single hyphens, starting with a letter, up
   to 32 characters. One owner per server. Used in step 4.

8. How much mail disk, and how many endpoints?
   - Defaults (2G, 64 streams): about 59 endpoints. Nothing to add.
   - More: add `--js-disk 10G`, and tell the person the limits on
     streams and consumers are in the script's header.

9. Run it now, or as a service?
   - Now, for a trial: add `--start`.
   - Survive reboots: leave `--start` off, and write a service unit for
     their init system (ask which) that runs the line
     `nats-server -c <dir>/server.conf`.

## Steps

Replace `<dir>`, `<port>` and `<owner>` with the answers.

### 1. Check the tools

```sh
nats-server --version
```

```sh
nsc --version
```

```sh
openssl version
```

### 2. Make the server (one command)

```sh
scripts/nats-local-setup.sh --dir <dir> --port <port> --start
```

Add `--host`, `--listen` and `--js-disk` from the answers above. The
script prints one line per step. It writes only under `<dir>`, changes
nothing in your home, and never prints a secret. Read it first; it is
short.

When it finishes it prints the files it left and where they are:

- `ca.pem`: the CA certificate. Give it to every client.
- `server.conf`: the nats-server configuration.
- `export/mashadmin.creds`: admin credentials for mashctl. Secret.
- `export/sys.creds`: system-user credentials. Secret.
- `export/operator.nk`: the operator seed. Secret.
- `export/account.nk`: the account seed. Secret.
- `server.log` and `server.pid`: only with `--start`.

### 3. Check it

```sh
scripts/nats-local-setup.sh --dir <dir> --check
```

It tests the configuration and the file modes, and, if the server is
running, that the port answers with TLS. Fix anything it reports before
you go on.

### 4. Give the keys to mashctl

Choose a mashctl configuration directory of your own. Ask the person if
they already use mash; do not overwrite theirs.

```sh
export MASH_CONFIG_DIR=<config dir>
```

```sh
export MASH_TLS_CA=<dir>/ca.pem
```

```sh
cd <dir>/export
```

Check first. `--dry-run` writes nothing:

```sh
mashctl adopt --server tls://localhost:<port> --owner <owner> \
  --account MASH --operator-key operator.nk --account-key account.nk \
  --sys-creds sys.creds --admin-creds mashadmin.creds --dry-run
```

When every check says ok, run the same command again without
`--dry-run`. adopt copies the keys into mashctl's key store. Afterwards
move `export/` somewhere safe (a password manager, an encrypted volume)
or delete it. The server does not need it.

Then make mash's buckets, once:

```sh
mashctl init
```

### 5. Try it: Alice and Bob

```sh
mashctl endpoint create <owner>/demo/alice --make-key
```

```sh
mashctl endpoint create <owner>/demo/bob --make-key
```

```sh
mashctl endpoint list
```

Alice sends, Bob reads:

```sh
export MASH_OWNER=<owner> MASH_GROUP=demo
```

```sh
MASH_NAME=alice mash send <owner>.demo.bob "Hello Bob"
```

```sh
MASH_NAME=bob mash inbox
```

If Bob's inbox shows the message as `[verified]`, mash works.

## An existing server

Do not change a running server with this skill. Check it with the
person who runs it. Anything missing is theirs to add:

- TLS on the client port, a certificate that lists the names clients
  use, and a CA file clients can be given.
- Operator mode with a full resolver (`resolver { type: full ... }`).
- A system account, and credentials of a system user.
- An account for mash with JetStream limits in its JWT (disk, 64
  streams, 128 consumers), and a `jetstream { store_dir: ... }` block in
  the server configuration, on file storage.
- The operator seed and the account seed as files (mode 0600), and user
  credentials for the account.

Then go to step 4. adopt checks all of this read-only first and says
what is wrong.

## Stop and remove

Stop only the process the script started:

```sh
scripts/nats-local-setup.sh --dir <dir> --stop
```

It reads `<dir>/server.pid` and refuses unless that process is
`nats-server -c <dir>/server.conf`. To remove the server, stop it, then
delete `<dir>` by that exact path. The mail is in `<dir>/jetstream`.

## If something is wrong

- "port is in use": another process holds it. Ask for another port.
- `x509: certificate signed by unknown authority`: `MASH_TLS_CA` is not
  `<dir>/ca.pem`.
- `x509` host name error: the name in the URL is not in the
  certificate. Run the script again in a new directory with `--host`.
- adopt says the account is named `MASH`, not the owner: pass
  `--account MASH`.
- adopt says JetStream is not enabled: the configuration has no
  `jetstream` block, or the disk limit is 0.

## Do not

- Do not run nats-server, nsc or the script as root, or in `/`.
- Do not put seeds or credentials in a message, a log or a chat.
- Do not use `pkill`, `killall` or `pgrep -f` to stop NATS.
- Do not run the script before the person has answered the questions.
