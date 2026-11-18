---
name: nats-local
description: Set up a local NATS server for mash on one machine, either with `mashctl init --local` or by hand, then create two endpoints and send mail between them. Use when a person or an agent needs a working mash server and has none, when an existing nats-server must be checked against what mash needs, or when a local mash server must be stopped and removed.
---

# A local NATS server for mash

This guide sets up a NATS server that mash can use, on one machine, and shows that mail moves through it. Follow it in order, or point an agent at it. The commands were run against mash 0.5.5 and nats-server 2.15.1; the output shown is what they printed, with the config directory replaced by `<config dir>` and secrets left out.

The examples use the owner `guide`, the group `dev`, the instances `design` and `review`, and port 20000. Replace them with your answers to [Questions to ask first](#questions-to-ask-first).

Where the detail lives, this guide links to it: [operations.md](../operations.md), [tls-and-clusters.md](../tls-and-clusters.md), [mashctl.md](../mashctl.md), [install.md](../install.md).

## What this sets up

- One `nats-server` process, listening on loopback, with TLS only.
- Operator mode: one operator, a system account and one account for mash, named `MASH`.
- JetStream on file storage, with the limits the `MASH` account needs.
- A local certificate authority and a server certificate it signs.
- The credentials and signing keys `mashctl` needs, in a config directory.
- The directory and lease buckets that mash keeps in the account.
- Two endpoints, and mail between them.

It does not set up a server that other machines can reach, a cluster, or a second `mashctl` machine. See [Limits](#limits).

## What mash needs from a NATS server

Use this as a checklist for a server you did not set up with `mashctl init --local`. `init --local` produces all of it.

| Need | Why |
|---|---|
| JetStream on, with a file store directory (`jetstream { store_dir: ... }`) | Every inbox is a JetStream stream and every directory, lease and record bucket is a key-value bucket. mash keeps all of it on file storage and uses no memory store. |
| Operator mode with a full account resolver (`operator`, `resolver { type: full ... }`, `resolver_preload`) | An endpoint signs with the key of its NATS user, so mash accepts only JWT user credentials. `mashctl` writes revocations into the account JWT through the resolver, which must be a full one. Token, user/password and plain nkey login are not supported. |
| A `MASH` account whose JWT gives JetStream disk, streams and consumers (default 2G, 64, 128) | One stream per endpoint plus buckets, one consumer per endpoint. An account with disk 0 has no JetStream: `adopt` refuses it. See [Account limits](../operations.md#account-limits) for the sizing rule. |
| TLS on the client port (`tls { cert_file, key_file }`), a certificate that lists every host name and address clients use, and a CA the clients trust (`tls_ca`) | Every mash connection requires TLS, to any host, loopback included. A plain `nats://` URL is refused before any connection is made. Clients must use `tls://` URLs. |
| A system account (`system_account`) and its credentials (`sys.creds`) | Revocations ask every server for the account JWT and push the update through the system account. `mashctl issued check` also uses it. |
| Admin credentials for the `MASH` account (`mashadmin.creds`) | `mashctl` creates buckets, endpoints and inboxes with them. They have no permission limits inside the account. |
| `max_payload` of at least 1 MiB (the nats-server default is 1 MiB) | mash sends messages of up to 1 MiB including headers. It reads `max_payload` when it connects and refuses a larger send locally, so a smaller value only lowers the limit. |

The file `mashctl init --local` writes is the model for a hand-managed server: see [Path B](#path-b-an-existing-or-hand-managed-nats-server).

## Questions to ask first

Ask these before running anything. Each answer changes a step.

1. **Owner name.** The owner is the first part of every address (`owner.group.instance`) and the fence for mail: an endpoint can mail every endpoint of its own owner and no other owner's. Rules: lower-case letters, digits and single hyphens, starting with a letter, 1 to 32 characters; the whole address (three names and two dots) is at most 64 characters. One owner per server. It is passed as `--owner` and cannot be changed afterwards without making a new server.
2. **Server directory and config directory.** The config directory is `~/.config/mash` unless `MASH_CONFIG_DIR` is set. The server directory defaults to `<config dir>/server`; `--dir` changes it. `init --local` refuses, and changes nothing, if the server directory already holds any file it would write. Use a fresh config directory for a trial, and set `MASH_CONFIG_DIR` in every command and shell that uses it.
3. **Port.** The default is 4222. Pick another when 4222 is taken or reserved on this machine (`--port`). The port must be free: with `--start`, `init --local` stops with a message when it is not (see [Troubleshooting](#troubleshooting)).
4. **One machine or several.** One machine: continue with this guide. Several machines that all run endpoints against one server, or a second machine that runs `mashctl`: this guide covers only the first server; the rest is in [tls-and-clusters.md](../tls-and-clusters.md) and was not run here (see [Not run](#not-run)).
5. **Must the server be reachable from other machines?** If no, path A is enough: it listens on `127.0.0.1` and its certificate lists `localhost`, `127.0.0.1` and `::1` only. If yes, the server needs a certificate that lists every host name and address the clients use, `listen` on an address they can reach, and `tls_ca` on every client. `init --local` does not do this; see [TLS on one server](../tls-and-clusters.md#1-tls-on-one-server) and use path B.
6. **Is a nats-server already running that mash must use?** If yes, use path B and check it against the [checklist](#what-mash-needs-from-a-nats-server). If no, use path A.
7. **Storage path and size.** The JetStream store is `<server dir>/jetstream`, on the disk that holds the config directory. The `MASH` account is limited to 2G of disk; each inbox keeps at most 16 MiB or 30 days, and the buckets take a few MiB more. nats-server also gives JetStream only a share of the free disk by default, so the disk under the store directory needs more free space than the account limit. To use another disk, pass `--dir` on a path on that disk.

You also need `nats-server` on your `PATH` (a current 2.x release with JetStream; mash is built and tested against 2.15), and `mash` and `mashctl` ([install.md](../install.md#1-the-binary); `mashctl` comes from `go install`, it is not in the release assets).

## Path A: one machine, `mashctl init --local`

### 1. Use a clean environment

If `MASH_*` variables are set in your shell, they point at some other server and override the files written below. Run every command in this guide with a clean environment, and a config directory of your own:

```sh
export MASH_CONFIG_DIR=$HOME/mash-local-config     # a directory that does not exist yet, or is empty; mashctl makes it
env | grep '^MASH_'                                  # nothing should print except MASH_CONFIG_DIR
```

If anything else prints, run the commands as `env -u MASH_URL -u MASH_OWNER -u MASH_ADMIN_CREDS -u MASH_TLS_CA mashctl ...` or from a shell outside the session. See [Which server does mashctl change?](../operations.md#which-server-does-mashctl-change).

### 2. Make and start the server

```sh
mashctl init --local --owner guide --port 20000 --start
```

Expected output (exit 0; the pid differs):

```
config dir: <config dir> (set MASH_CONFIG_DIR to change)
mashctl: recorded the operator JWT in <config dir>/keys/operator.jwt (mashctl accepts account JWTs issued by the keys it declares)
mashctl: local NATS server configured in <config dir>/server
  server config   <config dir>/server/server.conf (listens on 127.0.0.1:20000, loopback only, TLS)
  server cert     <config dir>/server/server.pem, signed by <config dir>/server/ca.pem (valid 10 years)
  admin creds     <config dir>/server/mashadmin.creds
  system creds    <config dir>/server/sys.creds
  signing keys    <config dir>/keys (operator.nk, system.nk, account.nk; never copy them off this machine)
  config          <config dir>/config.json
  server          started, pid 2586063 (log <config dir>/server/server.log)
mashctl: directory and lease buckets ready

Ready: the local server is up at tls://127.0.0.1:20000. Create an endpoint with: mashctl endpoint create guide/dev/design --make-key   (it writes the creds file; to make the key on the endpoint's machine instead, see: mashctl endpoint create --help)
```

Without `--start`, the command writes the files and stops. Start the server yourself and make the buckets:

```sh
nats-server -c $MASH_CONFIG_DIR/server/server.conf     # in its own terminal, or detached
mashctl init                                            # the directory and lease buckets
```

With `--start`, `mashctl` starts `nats-server` detached from `PATH`, writes its output to `server.log` and its pid to `server.pid`, waits up to 15 seconds for a connection with `mashadmin.creds`, and makes the buckets.

### 3. What it wrote

In the server directory (mode 0700):

| File | What it is | Secret |
|---|---|---|
| `server.conf` | The nats-server configuration: loopback listen, `tls`, `operator`, `system_account`, a full `resolver`, `resolver_preload`, `jetstream`. | No (it holds account JWTs, which are public) |
| `operator.jwt` | The operator's JWT, named by `operator` in `server.conf`. | No |
| `mashadmin.creds` | Full access inside the `MASH` account. For `mashctl` only. | Yes (0600). Never give it to an endpoint. |
| `sys.creds` | A user of the system account. For revocations and `issued check`. | Yes (0600) |
| `ca.pem` | The local certificate authority's certificate. Clients name it as `tls_ca`. | No |
| `ca.key` | The CA's private key. It signs the server certificate. | Yes (0600) |
| `server.pem`, `server.key` | The server certificate and its key, valid for `localhost`, `127.0.0.1` and `::1`. | `server.key` is secret (0600) |
| `resolver/`, `jetstream/` | The resolver's account JWTs, and the JetStream store (all mail). | The store holds every message |
| `server.log`, `server.pid` | The server's log, and its process id (written with `--start`). | No |

In the config directory:

| File | What it is | Secret |
|---|---|---|
| `config.json` | `url` (a `tls://` URL), `owner`, `admin_creds` and `tls_ca`. | No |
| `keys/operator.nk`, `keys/system.nk`, `keys/account.nk` | The operator, system account and `MASH` account seeds, in `mashctl`'s key store (`$MASH_KEYS_DIR` moves it). They sign users and revocations. Never printed. | Yes (0600, in a 0700 directory). Whoever holds `operator.nk` and `account.nk` can mint any user and revoke any key. |
| `keys/operator.jwt` | A copy of the operator JWT, used to tell which issuers to accept. | No |
| `<owner>.<group>.<instance>.creds` | Written later, one per endpoint. | Yes: it holds the endpoint's seed. |

`config.json` looks like this (the paths are your own):

```json
{
  "url": "tls://127.0.0.1:20000",
  "owner": "guide",
  "admin_creds": "<config dir>/server/mashadmin.creds",
  "tls_ca": "<config dir>/server/ca.pem"
}
```

`init --local` writes `config.json` only when there is none; it never touches an existing one.

### 4. Check the server

```sh
cat $MASH_CONFIG_DIR/server/server.pid
tail -3 $MASH_CONFIG_DIR/server/server.log
```

The log's start lines name the version, `Operator: "mash-local-operator"`, `Starting JetStream`, `Listening for client connections on 127.0.0.1:20000`.

## Path B: an existing or hand-managed nats-server

Use this when a nats-server is already running, or when the server must be reachable from other machines. Nothing here changes the server for you: you edit its configuration, and `mashctl adopt` then checks it and takes over the admin side.

### What `server.conf` must contain

Use the file `init --local` wrote as the model. This is that file, with the paths shortened and the account JWTs left out:

```
server_name: mash-local
listen: 127.0.0.1:20000
tls: {
  cert_file: "<server dir>/server.pem"
  key_file: "<server dir>/server.key"
}
operator: "<server dir>/operator.jwt"
system_account: <the system account's public key, starting with A>
resolver: {
  type: full
  dir: "<server dir>/resolver"
  allow_delete: false
}
resolver_preload: {
  <system account key>: "<the system account JWT>"
  <MASH account key>: "<the MASH account JWT>"
}
jetstream: {
  store_dir: "<server dir>/jetstream"
}
```

What each block is for:

- `listen` and `tls`: the client port, TLS only. For other machines, listen on an address they can reach and use a certificate that lists their host names ([TLS on one server](../tls-and-clusters.md#1-tls-on-one-server)).
- `operator`, `system_account`, `resolver` and `resolver_preload`: operator mode. `type: full` is required; the preloaded JWTs make the accounts known to a new server, and `adopt` refuses a server without a full resolver.
- `jetstream`: file storage. Leave out memory settings. A server without this block cannot hold mash's data even when the account JWT allows JetStream.

Make the `MASH` account with JetStream limits and a signing key, as in [The account](../operations.md#the-account) (`nsc add account`, `nsc edit account ... --js-disk-storage 2G --js-streams 64 --js-consumer 128`, `nsc push`). Leave `--js-mem-storage` unset. For a server with more than one node, see [A three-node cluster](../operations.md#a-three-node-cluster).

### Adopt it

`mashctl adopt` needs three files you already hold, each mode 0600: the operator signing seed (`SO...`), the `MASH` account identity seed (`SA...`) and the credentials of a system account user. It never takes a seed from an argument or an environment value. `--account` is the account's name in its JWT; its default is the owner, so pass `MASH` when the owner is another name.

Here the three files were copied from the server `init --local` made (path A), into a second, empty config directory, which is the same position as a hand-managed server where you hold the seeds. Run it first with `--dry-run`:

```sh
MASH_TLS_CA=<config dir>/server/ca.pem mashctl adopt --server tls://127.0.0.1:20000 \
  --owner guide --account MASH \
  --operator-key operator.nk --account-key account.nk --sys-creds sys.creds --mint-admin --dry-run
```

Expected output (exit 0; the key names are your own):

```
mashctl adopt: checking tls://127.0.0.1:20000 as owner "guide", account "MASH" (read-only: nothing is written until every check passes)
  1. files          ok: operator key O..., account key A..., system creds (modes 0600, seeds not printed)
  2. server         ok: refused a connection without credentials (operator mode); the system creds connect
  2. account        ok: "MASH" found on the resolver, subject = the account key, not expired. Not visible from here: whether the operator has revoked it
  3. jetstream      ok: the account JWT gives it: disk 2GiB, streams 64, consumers 128
  3. jetstream      (the JWT is what the account asks for; step 6 asks the server whether it enabled JetStream)
  4. system user    ok: the ping was answered (standalone, 1 server(s) replied) and the claims lookup worked. ...
  5. operator key   ok: it issued the account JWT (issuer O...), whose signature is valid
  6. admin creds    ok: connected (minted)
  6. jetstream      ok: the server answered for the account: 7 stream(s), 2 consumer(s), 3587B of disk in use

All checks passed. --dry-run: nothing is written. This is what adopt would do:
  would import the operator key O... into <config dir 2>/keys (operator.nk, 0600)
  ...
```

Then run the same command without `--dry-run`. It prints the same checks, then `All checks passed.` and the files it wrote (the two keys in the key store, `server/sys.creds`, `server/mashadmin.creds` and `config.json`), and exits 0. Running it again changes nothing. Delete your copies of the seed files when it has stored them.

Last, make the buckets once. `adopt` does not:

```sh
mashctl init
```

```
mashctl: target server tls://127.0.0.1:20000 (from <config dir 2>/config.json), owner guide (from <config dir 2>/config.json), admin creds <config dir 2>/server/mashadmin.creds (from <config dir 2>/config.json)
mashctl: directory and lease buckets ready
```

Here the buckets already existed (the first server had made them), so this was a no-op; on a new server it creates them. The details of `adopt` are in [Adopting an existing server](../operations.md#adopting-an-existing-server) and [TLS with `adopt`](../tls-and-clusters.md#mashctl-adopt-with-a-tls-url).

## Create two endpoints and send mail

An endpoint is `owner/group/instance` on the command line and `owner.group.instance` as an address. `--make-key` makes the endpoint's key on this host and writes its creds file, which is right only because the endpoint runs here. For an endpoint on another machine, make the key there ([An endpoint on another machine](../operations.md#an-endpoint-on-another-machine)).

```sh
mashctl endpoint create guide/dev/design --make-key
mashctl endpoint create guide/dev/review --make-key
```

Expected output of each (exit 0):

```
mashctl: target server tls://127.0.0.1:20000 (from <config dir>/config.json), owner guide (from <config dir>/config.json), admin creds <config dir>/server/mashadmin.creds (from <config dir>/config.json)
created endpoint guide/dev/design
  address  guide.dev.design
  creds    <config dir>/guide.dev.design.creds
next: start a session as this endpoint: MASH_OWNER=guide MASH_GROUP=dev MASH_NAME=design
```

```sh
mashctl endpoint list
```

```
guide/dev/design  guide.dev.design  guide_dev_design  created 2026-11-18T22:51:41Z
guide/dev/review  guide.dev.review  guide_dev_review  created 2026-11-18T22:51:41Z
2 endpoints
```

`mash` acts as the endpoint that `MASH_OWNER`, `MASH_GROUP` and `MASH_NAME` name (all three; a missing `MASH_GROUP` is `group_missing`). It reads the endpoint's creds file from the config directory, and the server from `config.json`. Send from `design` to `review`:

```sh
MASH_OWNER=guide MASH_GROUP=dev MASH_NAME=design \
  mash send guide.dev.review "Hello from design: the local server works."
```

```
sent #1 from guide.dev.design to guide.dev.review
```

Give the full address. A bare name such as `review` is refused: `mash: "review" is not an endpoint address: give the full address owner.group.instance as who shows it (the flat owner_group_instance is accepted too)`.

Read it as `review`:

```sh
MASH_OWNER=guide MASH_GROUP=dev MASH_NAME=review mash inbox
MASH_OWNER=guide MASH_GROUP=dev MASH_NAME=review mash read 1
```

```
#1	from guide.dev.design [verified]	2026-11-18T22:51:54Z	42 B	Hello from design: the local server works.	kind task	instance 2ydCnFVkmgaSsQZ0cfVmlm
```

```
--- #1 from guide.dev.design [verified] at 2026-11-18T22:51:54Z
instance: 2ydCnFVkmgaSsQZ0cfVmlm
kind: task
> Hello from design: the local server works.
```

`[verified]` means the message is signed by the key the directory holds for `guide.dev.design`. Reply the other way the same way (`MASH_NAME=review mash send guide.dev.design --kind result "Got it."`); `design`'s `mash inbox` then lists it as `#1 from guide.dev.review [verified] ... kind result`. Each endpoint has its own inbox, so each numbers from 1. `mash who` lists both endpoints as `offline` here: presence is for running sessions, and none is running.

To use these endpoints from Claude Code, install the plugin ([install.md](../install.md#2-the-plugin)) and start the session with the same three variables.

## Checks

```sh
MASH_OWNER=guide MASH_GROUP=dev MASH_NAME=design mash status
```

```
name: OK - guide.dev.design
config: OK - url tls://127.0.0.1:20000, owner guide
name rule: none set
creds: OK - <config dir>/guide.dev.design.creds exists and is readable
tls: OK - tls:// connection
connection: not checked from the CLI (a running session reports it)
...
version: mash 0.5.5 (commit e389eaf807279b58e63b05578a779ef672d93a36)
```

`mash status` from a shell reads files and does not connect. The `channel allowlist` and `delivery` lines depend on Claude Code on this machine and are not part of this check. A `tls: FAILED` or `tls: WARN` line means the URL is plain text.

The server side:

```sh
mashctl issued check
```

```
recorded but not connected (informational, 2):
  U...	endpoint	guide.dev.design
  U...	endpoint	guide.dev.review
summary: 1 connected, 0 unrecorded, 0 revoked but connected, 3 recorded (2 not connected), 1 of 1 servers answered
ok: every connected key was issued by mash and none is revoked
```

(exit 0; the first line of output, `mashctl: target server ...`, is on stderr.) `mashctl audit` prints one `record` line per endpoint with its inbox state, then `orphans: 0 stream(s), 0 lease(s), 0 creds file(s)` (exit 0). `mashctl limits --show` prints the effective inbox and bucket caps (`inbox_max_bytes 16MiB`, `inbox_max_age 30d`, and three more lines).

The log is `<config dir>/server/server.log`; `tail` it for connection and TLS errors from the server's side.

## Stopping and removing it

Stop the server by the process id in `server.pid`, and only that one:

```sh
kill $(cat $MASH_CONFIG_DIR/server/server.pid)
```

The log ends with `JetStream Shutdown` and `Server Exiting..`. While the server is down, `mash` commands fail with `mash: connect tls://127.0.0.1:20000: nats: no servers available for connection` (exit 1). Mail stays in the store: after a restart with the command below, `mash inbox` showed the same message.

```sh
nats-server -c $MASH_CONFIG_DIR/server/server.conf >> $MASH_CONFIG_DIR/server/server.log 2>&1 &
echo $! > $MASH_CONFIG_DIR/server/server.pid
```

Do not look up the process by name or pattern (`pkill`, `killall`, `pgrep -f`): other nats-servers on the machine are not yours. If `server.pid` is stale, find the process by the port it listens on and check it is yours before you kill it.

To remove it, after the server is stopped, delete what was made, by path:

- the server directory, `<config dir>/server` (the store, the resolver, the certificates, the credentials, the log);
- the key store, `<config dir>/keys` (or `$MASH_KEYS_DIR`);
- `<config dir>/config.json`;
- each endpoint's `<config dir>/<owner>.<group>.<instance>.creds` (and any `.retired-<time>` copies);
- `$MASH_STATE_DIR` (else `~/.local/state/mash`), if sessions ran as these endpoints: their spool is there;
- any seed files you copied for `adopt`, and `design.seed`-style files if you used the three-command endpoint form.

If the config directory was made for this trial and holds nothing else, removing it as a whole is the same thing. Delete the key store only when you will not use this server again: without `operator.nk` and `account.nk` nobody can revoke or mint credentials for the account.

To remove one endpoint and keep the server, use `mashctl endpoint delete OWNER/GROUP/INSTANCE`: it revokes the endpoint's user and removes its record and inbox ([Removing an endpoint](../operations.md#removing-an-endpoint)).

## Troubleshooting

These are the messages observed while writing this guide, unless marked otherwise.

| You see | Cause and fix |
|---|---|
| `mash: refusing a plain nats:// connection to "127.0.0.1": every mash connection requires TLS, to any host (loopback included); set insecure_plaintext (MASH_INSECURE_PLAINTEXT=1) to allow plain text; the fix is to use a tls:// URL with the server's certificate (tls_ca for a private CA)`, exit 2 (`mash status` shows it as `tls: FAILED - ...`) | The URL is `nats://` or `ws://`. Use `tls://HOST:PORT` and the CA as `tls_ca`. `insecure_plaintext` is for a throwaway server in a test only; it never turns TLS off for a `tls://` URL. See [Plain text](../tls-and-clusters.md#plain-text-and-mash_insecure_plaintext). |
| `nats: tls error: tls: failed to verify certificate: x509: certificate signed by unknown authority (the server's certificate is not trusted: set tls_ca or MASH_TLS_CA to the PEM file of the CA that signed it)`, exit 6 | `tls_ca` is missing, or names another CA. Point it at the `ca.pem` the server's certificate was signed by. It is read at connect time. |
| An `x509` host name error (taken from [tls-and-clusters.md](../tls-and-clusters.md#what-fails-and-what-it-says); not produced here) | The URL names a host the certificate does not list. The path A certificate lists `localhost`, `127.0.0.1` and `::1` only. |
| `nats: no servers available for connection (check MASH_URL and that at least one listed server is up)` | The server is not running, the port is wrong, or the server listens on loopback only and the URL names another address (a host name or LAN address reaches nothing). Check `server.pid` and the log. |
| `mashctl: port 20000 is already in use: something already listens on 127.0.0.1:20000, so nothing was started (pick another with --port, or stop that server)`, exit 1 | Another process holds the port. The files were written before this check and are left in place: remove them as above, or reuse them with another port by editing `listen` in `server.conf` and `url` in `config.json`. |
| `mashctl: <server dir> already holds server.conf, operator.jwt, ...: refusing to overwrite. Remove them (or choose another --dir) to start over; nothing was changed`, exit 1 | `init --local` was run twice into one server directory. |
| `mashctl: adopt: the server could not enable JetStream for the account: ... [server said: nats: API error: code=503 err_code=10076 description=jetstream not enabled]`, exit 1 | The server has no `jetstream` block, or JetStream could not start. The message names the account's disk limit as the likely cause; when the config has no `jetstream` block, add it. Seen here against a copy of `server.conf` with the block removed. |
| `mashctl: adopt: JetStream is not enabled for the account: its JWT limits give it no file storage (disk 0) ...` (taken from the source; not produced here) | The account JWT has no JetStream limits. Add them with `nsc edit account` and push. |
| `mashctl: adopt: the account JWT is named "MASH", not "guide": pass --account MASH if that is the account (--account defaults to the owner)`, exit 1 | The owner is not the account's name. Pass `--account MASH`. |
| `group_missing: mash: no group: set MASH_GROUP (the endpoint's group has no default), ...`, exit 2 | `MASH_GROUP` is not set. |
| `mash: credentials: open <config dir>/guide.dev.ghost.creds: no such file or directory`, exit 1 | No such endpoint on this machine: check `MASH_OWNER`, `MASH_GROUP` and `MASH_NAME` for a typo, and run `mashctl endpoint create`. |
| `reissue_busy` (taken from `mashctl endpoint reissue --help`; not produced here) | Another reissue of the same address holds the lock on its creds file; wait for it. The lock is dropped when that run ends or is killed. |
| `mashctl: no owner (config.json "owner" or MASH_OWNER)` | `MASH_CONFIG_DIR` is set to a directory with no `config.json`, or is not set in this shell. |

More: [Troubleshooting in operations.md](../operations.md#troubleshooting).

## Limits

- One host. The server listens on `127.0.0.1`; endpoints on other machines cannot reach it, and its certificate lists only `localhost`, `127.0.0.1` and `::1`.
- TLS only. A plain `nats://` URL is refused unless `insecure_plaintext` is set, and that is for a throwaway server in a test.
- The local CA and the server certificate are valid for 10 years. mash does not renew them. After that, make a new server or replace the certificate and key by hand.
- One server and one replica. Every inbox is a single-replica stream, and nats-server's default `sync_interval` (2 minutes) means a host crash or power cut can lose recent mail that was reported as sent; see [Durability](../operations.md#durability-and-sync_interval).
- One account (`MASH`), one owner. Every endpoint can read the whole directory.
- The seeds in `<config dir>/keys` and `mashadmin.creds` are unrestricted authority over the account, and other processes of your own user can read them. See [Where the key store lives](../operations.md#where-the-key-store-lives).
- The disk limit of 2G, 64 streams and 128 consumers caps the account at about 60 endpoints; see [Account limits](../operations.md#account-limits) to raise it.

## Not run

Run here, on one host, against mash 0.5.5 built from this tree and nats-server 2.15.1: everything above except the items below.

- A server that other machines connect to: a certificate with a host name, `listen` on a non-loopback address, `tls_ca` on a second machine. The recipes are in [tls-and-clusters.md](../tls-and-clusters.md), which says what was run for them.
- A hand-managed server. Path B ran `mashctl adopt` against the server `init --local` made, with copies of its seeds, and against a copy of its `server.conf` without the `jetstream` block. It did not run against a server someone built with `nsc`; `nsc add account` and `nsc edit account` were not run.
- A cluster, a second `mashctl` machine, and `MASH_SERVERS`.
- A Claude Code session as an endpoint (the plugin, the hooks, mail shown in a session). Mail was sent and read with `mash send`, `mash inbox` and `mash read`.
- The nats-server message for a port in use, because `mashctl` checks the port first and stops. `reissue_busy`, the `x509` host name error and the account-without-JetStream message above were not produced.
- macOS, and nats-server versions other than 2.15.1.
