# Operating mash

mash needs a NATS server in operator mode with JetStream, and one account for mash. the `mash` binary never mints NATS users and never holds an operator or account seed. `mashctl` holds the account signing key. It signs each endpoint's user from the endpoint's own public key (`mashctl endpoint create`), with the permissions `mashctl permissions` prints, and it writes the directory record and the inbox behind it.

## Local setup with `mashctl init --local`

For one developer on one machine, `mashctl init --local` writes the whole NATS side in one command, so the manual `nsc` steps below are not needed. It needs no `config.json` and no running server:

```sh
mashctl init --local --owner alice [--dir D] [--port P] [--start]
```

You need `nats-server` on your `PATH`, a current 2.x release with JetStream (get it from the [nats-server releases](https://github.com/nats-io/nats-server/releases); this repository builds and tests against 2.15). `init --local` prints the config directory it writes to (`~/.config/mash` by default, or `$MASH_CONFIG_DIR`).

It creates, in `D` (default `<config dir>/server`; mode 0700): `server.conf` (an operator-mode server listening on `127.0.0.1:P`, default 4222, TLS only, with JetStream on file storage and the accounts preloaded into a full resolver), `operator.jwt`, a local certificate authority (`ca.pem` and `ca.key`) and a server certificate it signs (`server.pem` and `server.key`; valid 10 years, for `localhost`, `127.0.0.1` and `::1`), and the credentials `mashadmin.creds` (full access within the MASH account, for `mashctl`) and `sys.creds` (system account), all 0600. The operator, system-account and MASH-account signing seeds go to the `mashctl` key store (`operator.nk`, `system.nk`, `account.nk`, 0600, in `<config dir>/keys` or `$MASH_KEYS_DIR`); they are never printed. The MASH account gets the limits [below](#limits) describe (2G of disk, 64 streams, 128 consumers). If none exists it also writes a `config.json` with `servers` (an array holding a `tls://` URL), `admin_creds` and `ca` (the path of `ca.pem`); an existing `config.json` is never touched, and the four settings to add are printed. No policy file is written: endpoints are made by `mashctl endpoint create`.

The server listens on loopback only and accepts TLS connections only, using that certificate. It is for development on this machine, not for remote endpoints. `mashctl` and endpoints on this host need no extra setting: the URL is `tls://127.0.0.1:P` and `ca` in `config.json` names `ca.pem`. For remote endpoints, follow the manual steps below with a server whose certificate they can verify.

Every mash connection requires TLS, to any host, loopback included, so a plain `nats://` URL is refused for admin and system connections (`mashctl` with the admin or system creds) and for endpoint connections, with exit code 2. Local servers made by `init --local` use TLS, so they need no override. `init --local --start` connects over TLS to the loopback server that run just generated and started, and trusts only the `ca.pem` that run made; without `--start` nothing connects. `init --local` also records a copy of the operator JWT as `operator.jwt` in the key directory (0600, never replacing a file), which the commands that read the account JWT (a revocation, `issued check`, `adopt`) use to know which issuers to accept (see [Writing the account JWT](#writing-the-account-jwt-revocation)). The one override is `insecure_plaintext` (`MASH_INSECURE_PLAINTEXT=1`), for a throwaway server in a test; with it set each command prints a warning, because plain text lets anyone on the network read mail and obtain signatures. Do not set it on a network you do not control. Admin and system connections never add servers the server advertises. See [TLS](tls-and-clusters.md#plain-text-and-mash_insecure_plaintext).

`init --local` refuses, and changes nothing, if `D` already holds any file it would create. Then:

```sh
nats-server -c ~/.config/mash/server/server.conf   # or: add --start to the command above
mashctl init                                        # the directory and lease buckets
mashctl endpoint create alice/dev/design --make-key   # one per endpoint, see "Adding an endpoint"
```

With `--start`, `mashctl` looks for `nats-server` on `PATH` (if it is missing it says so, and the files stay), starts it detached with its output in `D/server.log` and its pid in `D/server.pid`, waits up to 15 seconds for a connection with `mashadmin.creds`, and creates the buckets. To stop the server: `kill $(cat D/server.pid)`.

## Adopting an existing server

`mashctl init --local` stands up a server. `mashctl adopt` is its counterpart for a NATS server you already run in operator mode with a full resolver and JetStream. A URL alone proves nothing about a server: not that it is in operator mode, nor that you hold authority over its account, system account and resolver, and `mashctl` cannot stand up or reconfigure a remote server from a client URL. So adopt requires the authority to exist already, checks it, and refuses when a prerequisite is absent.

```sh
mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE \
              [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]
```

Prerequisites, all of which you supply:

- `--operator-key FILE`: the operator **signing** seed (starts with `SO`). a revocation (`endpoint delete`, `endpoint reissue`, `deprovision`) signs the account JWT with it.
- `--account-key FILE`: the **identity** seed of the mash account (starts with `SA`; its public key is the account JWT's subject).
- `--sys-creds FILE`: creds of a user of the system account.
- `--server URL`, and the TLS settings of the loaded configuration (`ca`, `tls_cert`, `tls_key`), as every other verb uses them. `--account NAME` is the account's name in its JWT (default: the owner), and `--owner O` defaults to the configured owner.

Seeds are read from files only, never from arguments or environment values, the files must be mode 0600, and a seed is never printed. Without `--operator-key`, `--account-key` or `--sys-creds`, adopt exits 2 and says which is missing and why.

Adopt first runs read-only checks, in this order, and writes nothing until all pass: (1) the files parse and have the right seed prefixes; (2) the server refuses a connection without credentials (a server that accepts one, or whose system account answers nothing, is refused as not looking like an operator-mode server with a full resolver), the system creds connect, and the resolver has a JWT for the account key's public key whose subject is that key, whose name is `NAME` and which is not expired; (3) the account has JetStream file storage (disk limit other than 0), and its disk, stream and consumer limits are shown, with a warning when they are below what one endpoint needs (3 streams, 1 consumer, about 36 MiB; see [Account limits](#account-limits)); (4) the system account answers the `$SYS.REQ.SERVER.PING` and the claims lookup, which is reported as standalone or as a cluster; (5) the account JWT carries a valid signature by its issuer, and the issuer is the operator key you gave; any other issuer is refused (exit 1, nothing written), naming the issuer and the accepted keys. The system and admin connections require TLS, or the `insecure_plaintext` setting for a throwaway server (see [Local setup](#local-setup-with-mashctl-init---local)). The permission to push claims updates is not tested, because that would change the server: `mashctl endpoint delete` is the first to use it. The check also refuses a key store that already holds a different operator or account key, and a `sys.creds` that already holds other creds.

Then it writes, or with `--dry-run` prints exactly what it would write: the two keys into the key store (`operator.nk`, `account.nk`; 0600, created without replacing anything, fsynced; the same key already stored is fine, a different one is refused); the system creds as `<config dir>/server/sys.creds` (0600, where a revocation looks for them); with `--admin-creds FILE`, that path as `admin_creds` in `config.json` (the file is checked to connect and not copied), or with `--mint-admin` a `mashadmin` user of the account, signed by the account identity key with full access as `init --local` makes it, as `<config dir>/server/mashadmin.creds` (0600; one already there that is valid for this account and connects is kept, not replaced); `config.json` (`servers`, `admin_creds`, and `ca`, `tls_cert`, `tls_key` when the loaded configuration sets them) only if there is none (otherwise the settings are printed); and the open `policy.yaml` only if there is none. Running adopt again with the same inputs changes nothing.

Adopt connect-tests whatever admin creds it ends with before it prints "All checks passed", and the summary says which it tested (`admin creds ok: connected (given)`, `(minted)` or `(kept)`). A `--mint-admin` user is made in memory and connected with in the read-only phase, so `--dry-run` reports a failure too and a failure writes nothing. An existing `mashadmin.creds` that parses but cannot connect is refused, not kept: move it aside to mint a new one. On an operator with strict signing key usage (`strict_signing_key_usage: true`), the server accepts only users signed by an account signing key, and the account identity key cannot sign users, so a minted `mashadmin` is rejected and adopt refuses with that explanation and the server's error text. mashctl does not fall back to another signing key. The way out is `--admin-creds FILE` with a user signed by one of the account's signing keys.

The `mashadmin` creds that `--mint-admin` writes carry no permission limits on the account (when the server accepts them: see the strict-signing note above): whoever holds `mashadmin.creds` can publish and subscribe on every subject of the account, read every inbox and change the directory. Keep the file at 0600 on the machine that runs `mashctl`, and give endpoints their own creds, never these.

Adopt changes nothing on the server except one thing: with `--mint-admin` it records the minted `mashadmin` user in the issue record (creating the `mash-issued` bucket if the account has none), before it writes the creds file, so the credential it hands out is never unrecorded; if that write fails, adopt fails, says the admin credentials were not recorded and writes no creds file. Otherwise it pushes no account update and creates no NATS user there (a minted `mashadmin` exists only as a local creds file, usable because the account key signed it) and no other bucket. The cutover of a live account, meaning revoking the credentials issued before, is a separate step the operator runs: `mashctl init` creates the buckets and `mashctl endpoint create` makes endpoints. Until then, credentials minted by other tools keep working.

## Which server does mashctl change?

`mashctl` takes the server, the owner and the admin credentials from `config.json` in the config directory; the endpoint's own `MASH_*` variables (`MASH_SERVER`, `MASH_ENDPOINT`, `MASH_ADMIN_CREDS`, `MASH_CA`, ...) win over the file, and the global flags (`--server`, `--ca`, `--creds`, `--endpoint`) win over both. The owner is the owner part of `MASH_ENDPOINT` or `--endpoint`. Run from a shell inside a mash agent session, those variables are the session's, so `mashctl init`, `list` and the other verbs act on the session's own server, not the scratch one you may have meant.

Every verb that connects to a server or changes one (`init`, `limits`, `provision`, `set-meta`, `suspend`, `resume`, `suspended`, `deprovision`, `list`, `endpoint create`, `endpoint reissue`, `endpoint delete`, `endpoint list`, `endpoint show`, `label` and `unlabel`) first prints, to stderr, what it is about to act on and where each setting came from:

```
mashctl: target server tls://host:4222 (from MASH_SERVER), owner acme (from MASH_ENDPOINT), admin creds /home/me/.config/mash/admin.creds (from MASH_ADMIN_CREDS)
```

The verbs that revoke add the system credentials' path when it is known. Credentials files are named, never printed; the user information of a URL is shown as `***@`. When an environment variable overrides a different value in `config.json` (the servers, endpoint, admin creds or `ca`), a warning names both values, and when `MASH_ENDPOINT` or `MASH_LAUNCH_TOKEN` is set and the server came from `MASH_SERVER`, a second warning says the shell looks like it is inside a mash session. These lines inform; they never refuse, and the TLS and issuer rules are unchanged. The offline verbs (`permissions`, `limits --show`, `policy`, `init --local`) print nothing of this.

To choose the target explicitly, pass `--server`, `--creds`, `--endpoint` or `--config-dir`; or, to use `config.json` instead of the session's variables, run `env -u MASH_SERVER -u MASH_ENDPOINT -u MASH_ADMIN_CREDS -u MASH_CA mashctl ...`, or run mashctl outside the session.

## Where the key store lives

The account seed in the key store (`account.nk`, with `operator.nk` and `system.nk`) and `mashadmin.creds` are account-wide, unrestricted authority: whoever holds them can mint any user, change any permission and revoke any key. Their modes (0600 files in a 0700 directory) stop other users, but not other processes of the same UID, and agent sessions on the operator's host run as that UID. So run `mashctl` and keep its key store (`<config dir>/keys` or `$MASH_KEYS_DIR`) under a separate UID, or on a mount, that agent sessions cannot read, and give each host's agents only their own creds file. Never hand an agent `mashadmin.creds`, `sys.creds` or the key store.

What `mashctl` prints and reads follows from not trusting the server:

- Strings that came from a server or a directory record (server names, ack messages, scoped signing key roles, record fields in `list` and `suspended`) are printed escaped: every control character (ESC, CR, newline, and the C1 range) and invalid UTF-8 shows as `\xNN` or `\uNNNN`, and a value is cut at 256 characters with `...`. Ordinary text, accented letters included, is unchanged.
- A reply from the system account is bounded: a claims reply over the account JWT limit (1 MiB) is refused with `reply of N bytes over the limit`, more than 1024 distinct server names ends the ping with `more than 1024 servers answered: refusing`, and a collection stops after 4096 replies.
- A server URL with credentials in it (`nats://user:pass@host`) is not supported. `adopt` refuses it, so it is never written to `config.json`; give the credentials as files (`--sys-creds`, `--admin-creds`). A URL that reaches `mashctl` through `config.json` or `MASH_SERVER` is printed with the user information replaced by `***@`.
- `config.json` is refused when it is writable by every user (`chmod go-w` it); its owner is not checked, since a config map is often a symlink owned by another user. A relative `admin_creds`, `ca`, `tls_cert` or `tls_key` in `config.json` is resolved against the config directory, not the working directory.

## The account

Create an account with JetStream limits sized for your endpoints: one stream per endpoint plus two KV buckets, and one consumer per endpoint. The account name is yours to choose; `MASH` is only the default in these examples (set `ACCOUNT` to use another). The limits default to 2G of disk, 64 streams and 128 consumers; see [Limits](#limits) for the sizing rule.

```sh
ACCOUNT=${ACCOUNT:-MASH}
JS_DISK=${JS_DISK:-2G} JS_STREAMS=${JS_STREAMS:-64} JS_CONSUMERS=${JS_CONSUMERS:-128}
nsc add account "$ACCOUNT"
nsc edit account "$ACCOUNT" --sk generate --js-disk-storage "$JS_DISK" --js-streams "$JS_STREAMS" --js-consumer "$JS_CONSUMERS"
nsc push --account "$ACCOUNT"
```

mash stores everything on file storage; it needs no memory store. A server with no memory store configured refuses an account that asks for one, so leave `--js-mem-storage` unset.

`mashctl` is the admin tool: every `mash admin <verb>` below is also `mashctl <verb>`, and `mash admin` remains as a deprecated alias that prints a one-line notice to stderr.

Add an admin user for mash's admin commands (full access to the account), and keep its credentials only where you run `mash admin`:

```sh
nsc add user --account "$ACCOUNT" mashadmin
nsc generate creds --account "$ACCOUNT" --name mashadmin > ~/.config/mash/admin.creds
chmod 600 ~/.config/mash/admin.creds
```

Then create the directory and lease buckets once:

```sh
export MASH_SERVER=tls://nats.example.com:4222 MASH_ENDPOINT=alice.dev.admin MASH_ADMIN_CREDS=~/.config/mash/admin.creds
mash admin init
```

Every bucket carries a size cap, because hosted NATS accounts (Synadia Cloud's free tier, for one) refuse a stream with no max bytes. By default the directory bucket gets 16 MiB and the lease bucket 4 MiB; both hold tiny records, so that is generous (see [Limits](#limits) to change them). Running `mash admin init` again is safe: a bucket made by an older mash with no cap is bounded in place with a stream update, keeping every key; a bucket whose cap is smaller than the configured one is raised to it; a bucket with a larger cap keeps it. Init never lowers a cap and never purges anything.

After upgrading mash, re-run `mash admin init`: it hardens the lease bucket, so that it no longer allows rollup writes (a deployment that has not re-run it keeps its old bucket until an admin lock runs). `mashctl audit` reports a lease bucket that has not been hardened as a problem, with the text `lease bucket allows rollup writes; run "mash admin init" to harden it`, and `--strict` then exits 1. An endpoint also warns once at start (and `mash status` in a session shows a `lease bucket:` line) when the bucket still allows rollup writes; the warning never stops it from starting.

## Adding an endpoint

An endpoint is one Claude Code session, addressed `owner.group.instance` (for example `alice.dev.design`). Each token is lower-case letters, digits and single hyphens, starting with a letter, 1 to 32 characters, and the three with their two dots total at most 64. The whole address is set as `MASH_ENDPOINT` (for example `MASH_ENDPOINT=alice.dev.design`); a value that is not three dotted parts is a configuration error naming `MASH_ENDPOINT`, and with none set mash uses the one endpoint whose creds file sits directly in the config directory. An address is unique in the NATS account, and every endpoint in the account can read the whole directory, other owners' records included (see [ADR 0006](decisions/0006-one-shared-nats-account-one-owner-per-deployment.md), one owner per deployment).

`mashctl endpoint create` makes an endpoint. It signs the endpoint's public key as a NATS user with the account signing key, writes the endpoint's directory record and inbox, and gives the signed JWT back. The user key is the endpoint's own: either the endpoint makes it (the three-command form) or `mashctl` makes it on its own host (`--make-key`). Both forms are described in [mashctl.md](mashctl.md#mashctl-endpoint-create).

On the machine that runs the endpoint, make the key. On the `mashctl` host, sign it. On the endpoint's machine again, join the JWT and the seed into a creds file:

```sh
mash key create --out design.seed                                        # writes design.seed and design.pub
mashctl endpoint create alice/dev/design --public-key design.pub         # writes alice.dev.design.jwt
mash credential create --seed design.seed --jwt alice.dev.design.jwt     # writes alice.dev.design.creds in the config directory
```

When the endpoint and `mashctl` are on one machine, one command does all three:

```sh
mashctl endpoint create alice/dev/design --make-key
```

`--make-key` makes the key on the `mashctl` host and holds it while the command runs. Use it only when the endpoint runs on that host. A creds file written this way is `<config dir>/alice.dev.design.creds`.

The creds file is named `<owner>.<group>.<instance>.creds`. It holds the seed. Keep it on the machine that runs the session, and nowhere else. The three-command form never moves the seed: see [An endpoint on another machine](#an-endpoint-on-another-machine).

Before a command changes anything, `--dry-run` says what it would do: `mashctl endpoint create alice/dev/design --public-key design.pub --dry-run`.

`mashctl permissions <owner.group.instance>` prints the permission set as JSON; `--nsc` prints the same set as `nsc add user` flags, one argument per line. The set lets an endpoint:

- publish mail, live SAP prompts and status probes to any endpoint of its owner: `agents.mail.<owner>.*.*`, `agents.prompt.<owner>.*.*` and `agents.status.<owner>.*.*`, so a credential is open to the whole owner by default and reaches no other owner;
- publish its own heartbeat, `agents.hb.<owner>.<group>.<instance>`, and answer SAP discovery;
- subscribe to its own `agents.prompt` and `agents.status` subjects, to `$SRV.>` and to the owner's heartbeats, `agents.hb.<owner>.*.*`;
- read and acknowledge its own inbox, `inbox-<owner>_<group>_<instance>`, and read its own history;
- read (never write) the directory;
- hold its own lease and nothing else.

It cannot read another endpoint's inbox, history or replies (each endpoint receives only under its own `_INBOX.<owner>.<flat key>` prefix, for example `_INBOX.alice.alice_dev_design`), take another endpoint's address, write the directory, create or delete consumers, or delete or purge any stream. It holds no response permission, because NATS grants that for any reply subject an endpoint receives; replies may only go to `_INBOX.<owner>.>` subjects, the reply inboxes of its own owner, so an endpoint of another owner can neither answer your discovery or presence probes nor forge into them. The integration tests assert each of these denials against a real server.

### An endpoint on another machine

The endpoint's seed is its private key. In this form it is made on the endpoint's machine and never leaves it. Only the public key goes to the `mashctl` host, and only the signed JWT comes back.

1. On the endpoint's machine, make the key: `mash key create --out design.seed`. It writes `design.seed` (mode 0600) and `design.pub`.
2. Copy only `design.pub` to the host that runs `mashctl`. Do not copy `design.seed`.
3. On the `mashctl` host, sign it: `mashctl endpoint create alice/dev/design --public-key design.pub`. It writes the endpoint's record and `alice.dev.design.jwt` (in the current directory, or where `--jwt-out FILE` says).
4. Copy only `alice.dev.design.jwt` back to the endpoint's machine. A JWT holds no secret.
5. On the endpoint's machine, join them: `mash credential create --seed design.seed --jwt alice.dev.design.jwt`. It writes `alice.dev.design.creds` in the config directory (mode 0600) and checks that the JWT names the seed's key. It refuses a JWT issued for another key (`key_mismatch`) and does not check the issuing account or the expiry.

If the command in step 3 is killed, run the same command again with the same arguments. If step 3 refuses because the JWT file exists, name another file with `--jwt-out` or remove the old one.

Multi-server and TLS use, and a second `mashctl` machine, are described in [TLS, clusters and a second mashctl machine](tls-and-clusters.md). The 0.5.5 acceptance run (AT-005) tested all three by hand, in rootless containers on one host: three NATS servers of one cluster with TLS on the client and route connections, endpoints on separate nodes, and two `mashctl` machines. It did not test real separate hosts or a physical network, clock drift between hosts, or certificate expiry and renewal; the [Not tested in 0.5.5](tls-and-clusters.md#not-tested-in-055) box lists everything that was not run.

### Directory metadata

An endpoint's directory record can carry optional fields: `--role` (what it is for), `--host` (the machine it is meant to run on) and up to 64 free-form labels, `--label key=value` (repeatable), for grouping or tagging endpoints, for example `team=acme` or `project=billing`. `endpoint create` sets labels (`--label`), the description (`--describe`), capabilities (`--cap`) and a note (`--note`); `set-meta` sets the role and host and changes the rest later. They are the endpoint's identity on record, set by the admin; they are not live state. A role, host or label value is up to 64 printable characters with no tab or newline. A label key is a lowercase letter followed by up to 31 of `a-z`, `0-9`, `_`, `.` and `-`.

Two more fields say what the endpoint does: `--describe TEXT`, free text of up to 256 printable characters (no tab or newline), and up to 8 capability words, `--cap C` (repeatable), each a lowercase letter followed by up to 31 of `a-z`, `0-9`, `_`, `.` and `-`, with no repeats. Capabilities are stored sorted. A label holds one value per key and there are only 8 of them; capabilities are a list of words to find an endpoint by, and the description is the longer sentence ([ADR-0050](decisions/0050-the-directory-record-says-what-an-endpoint-does-with-a-description-and-capabilities.md)).

```sh
mashctl endpoint create alice/dev/design --public-key design.pub --label team=acme --label tier=1 --describe "Keeps the NATS accounts current" --cap nats-accounts --cap review --note "design session on laptop"
mashctl set-meta alice.dev.design --role reviewer --host laptop     # role and host are set after create
mashctl set-meta alice.dev.design --role builder --host - --label tier=- --label env=prod   # change role, clear host, clear one label, set another; everything else stays
mashctl set-meta alice.dev.design --describe "New text" --cap deploy --uncap review   # replace the description, add one capability, remove another
mashctl set-meta alice.dev.design --describe - --cap -                              # clear the description and every capability
```

`set-meta` needs at least one flag, changes only the fields given, and writes with a compare-and-set, re-reading and retrying if another write lands first. A value of `-` clears a field, and `--label k=-` clears that one label while the other labels stay. `--describe -` clears the description. `--cap C` adds a capability, `--uncap C` removes one, and `--cap -` clears them all (added in the same command afterwards). The 8-label and 8-capability caps are checked after the merge. The confirmation line also shows the description and capabilities when set. Fields a newer mash wrote to the record are kept. Older mash versions ignore the labels, so mixed versions are safe. 

The first column of `mash who` is the endpoint's address `owner.group.instance` (for example `alice.dev.design`), not its flat directory key; a script that needs the key uses `--names` or `--json`'s `name`. `mash who --long` (and the `who` tool a session's model sees) appends `role R` and `host H` for the fields that are set, then `desc TEXT` and `caps a,b,c` columns when set, then one `label.K=V` column per label sorted by key, each as its own tab-separated column. For the endpoint you run it as, it also appends the live lease's `lease host` and `lease version`, and the note `lease host X differs from directory host Y` when both hosts are set and differ. Other endpoints' leases are not readable with an endpoint's permissions, so their live host is not shown. The lease is the live view; the directory metadata is only the record of intent.

### Writing the account JWT: revocation

There is no policy file: every endpoint's permission set is the owner-line set for its address and its user is signed by the account signing key, so nothing is pushed to the account to make an endpoint work. The one thing `mashctl` still writes to the account JWT is a revocation, by `endpoint delete`, `endpoint reissue` and `deprovision`. They share one procedure.

The base of an update is always the newest copy any server holds. The command asks every known server for the account JWT, the count coming from the system account ping and `MASH_SERVERS` (when the count is unknown it waits until no new reply arrives for a short while). Every reply must pass the issuer check (the operator key, or a key declared by `<key dir>/operator.jwt`); one that does not is refused, not skipped. The copy with the highest issued-at (`iat`) is the base, never "the first reply". If servers hold older copies, the command says `warning: N of M servers hold an older copy (iat ...); building from the newest`, and the push brings every server to the newest. If two different copies share the newest `iat`, which one is current cannot be told: the command refuses (exit 1) and names how many distinct copies there are; make the servers agree, then run it again. A signing key in the account JWT that mashctl did not create is not carried: the command refuses, naming it by public key and role, because `mashctl` signs the account JWT with the operator key; remove such a key with the tool that added it first.

A write needs an answer from every known server, and the set of servers must be known to be complete. A server that sees no peers proves nothing (an isolated cluster member reports none), so completeness needs one of two things. Either you tell the command how many servers the deployment has, with `MASH_SERVERS=N` (a whole number of 1 or more, anything else is a usage error, exit 2): the set is complete when at least N distinct servers replied (if the routes report more than N servers, that larger number wins) and no reply reports gateways. Or the server is standalone: its ping reply names no cluster, and it has no routes and no gateways, so one reply is complete. The command refuses, exit 1, before pushing anything, when fewer servers answered the account lookup than the ping, the cluster routes or `MASH_SERVERS` say exist (`only M of N servers answered the account lookup`), or when the server set cannot be confirmed complete (for a clustered server, set `MASH_SERVERS`). A server that is down blocks writes until it is back, or until the count is corrected. The claims lookup is a fan-out request whose replies carry no server id, so mashctl can count the replies but cannot tell which server sent which: treat the write gate as a strong guard, not a proof, and keep every server of the deployment reachable while you write.

Before the push, the command pings the system account (`$SYS.REQ.SERVER.PING`) for the set of servers; every server that replied must acknowledge the update with code 200 within 2 seconds, and a server that is silent or answers another code makes the run `PARTIAL`, naming it. The JWT is then read back from every server; any answering server whose copy is not the JWT that was pushed makes the run `PARTIAL` too, and a set that is still incomplete after up to 3 seconds of listening ends `UNCONFIRMED`. Both exit 1: running the command again re-sends the update to every server. Just before pushing, the command asks every server again and compares the newest copy with the base it built from; if it changed, it computes its update again once, and a second change is an error that asks you to run it again.

Writers are serialized by an exclusive advisory lock on `<key dir>/apply.lock` (0600), taken before the account JWT is read and held until the read-back is done; a second writer waits a few seconds and then stops with a clear error. The lock covers writers that share one key directory (the key holder is the authority). Two hosts that hold copies of the keys are not coordinated: do not write from both at once. On platforms without `flock` the lock is a no-op and only the re-read protects you.

A limit that cannot be closed from here: the resolver offers no conditional update (no compare-and-swap on the account JWT), so a writer outside `mashctl`, such as `nsc` or another tool editing the same account, that writes between the final re-check and the push is still overwritten: the last writer wins at the resolver. Only one writer should hold the operator key and use `nsc` or other tools on this account while a revocation runs.

A revocation is not a transaction across NATS claims, JetStream resources and local files. If the push is not acknowledged by every server, or the read-back does not match, the command prints what was done, what was not confirmed and the command to retry, and exits 1.

What a revocation does not do: credentials minted by other tools (for example `nsc`) under the account key keep their own permissions until they are revoked. `mashctl issued check` finds a connected key that mash did not issue.

### Finding endpoints: `who` filters and `--json`

```
mash who [--long] [--json] [--names] [--match TEXT] [--name SUB] [--owner O] [--role SUB] [--label K[=V]]... [--cap C]... [--online|--offline]
```

Filters all have to match. `--name`, `--role` and `--match` are case-insensitive substrings; `--owner` is exact; `--label K` keeps endpoints that have label `K`, and `--label K=V` those whose value is exactly `V` (case-sensitive; repeat the flag to require several). `--cap C` keeps endpoints that have capability `C` (exact; repeat the flag to require several, and an invalid word is an error). `--match TEXT` looks in the name, role, note, description, capabilities and label keys and values at once. `--online` and `--offline` exclude each other. A flag takes its value as the next argument or as `--flag=value`. With no match the command exits 0 with empty output (`[]` for `--json`); a bad flag or value exits 2 with the usage on stderr. `mash who` with no flags prints what it always did, and `no endpoints` only when the directory itself is empty.

```sh
mash who --match nats                       # replaces: mash who | grep -i nats
mash who --role reviewer --online --names   # names only, one per line, for scripts
mash who --label team=acme --json           # machine-readable
mash who --cap nats-accounts --online --names   # who can do this right now, for scripts
```

`--names` prints one name per line: the directory key, the flat key `owner_group_instance`, which `send` and `prompt` accept (so does `owner.group.instance`). `--json` prints an array, one object per endpoint, in name order; `--long` adds `lease`. `--json` and `--names` exclude each other. These field names are stable:

| Field | Type | Present |
|---|---|---|
| `name` | string | always |
| `identity` | string | always: the address `owner.group.instance`, beside the flat key in `name` |
| `owner`, `group`, `instance` | string | always |
| `agent` | string | when the record names it: the harness that runs the endpoint, for example `claude-code` |
| `online` | boolean | always |
| `last_seen` | string, RFC 3339 UTC | when the endpoint last answered a presence probe (not its last heartbeat) |
| `role`, `host`, `note`, `description` | string | when set |
| `labels` | object, key to value | when there is at least one |
| `caps` | array of strings, sorted | when there is at least one |
| `lease` | object with `host` and `version` | with `--long`, when this endpoint's own live lease is known |

**How presence works.** Heartbeats are still published every 30 seconds by each running endpoint, and another SAP tool may listen to them, but `mash serve` no longer subscribes to them ([ADR-0066](decisions/0066-mash-serve-asks-who-is-online-when-someone-asks-instead-of-listening-to-every-heartbeat.md)). Presence is asked for when someone asks: `mash who` and the `who` tool send one request to `$SRV.INFO.agents`, which every running endpoint answers with its registration, and wait until the answers stop coming (300 ms after the last one, at most 2 s). An answer counts only when its identity signature verifies and its key is the one the directory has for that name. An endpoint is online when it answered the latest probe; `seen T ago` and `last_seen` are the time of its last answer to a probe, not of its last heartbeat. The `who` tool inside a session reuses a probe's result for 30 seconds (`endpoint.PresenceCacheTTL`) and asks again after that; one probe at a time, and after a probe that failed, or that no registration answered, the next one waits at least 5 seconds (`endpoint.PresenceRetryGap`). So the tool can be up to 30 seconds behind, and takes about a third of a second when it has to ask. `mash who` on the command line always probes. Before the first probe an endpoint with no answer shows offline.

The `who` tool takes the same filters as optional parameters: `match`, `name`, `role` (strings), `label` (an array of `"k"` or `"k=v"`), `cap` (an array of capability words), `online` (`true` online only, `false` offline only, absent both) and `format` (`text`, the default, or `json`, the same array as above including `lease`). A call with no arguments is unchanged; an unknown `format` is a tool error.

There is no policy file; see [mashctl.md](mashctl.md).

## The issue record and the check of it

`mashctl` keeps a record of every user JWT it issues in the `mash-issued` KV bucket (key: the user's public key; value: public key, address, owner, kind, tags, generation, issue time, purpose, and the revocation time once mash has revoked the key). It exists so a user JWT signed outside `mashctl`, for example with a copy of the account signing key, can be found. The commands are in [mashctl.md](mashctl.md#the-issue-record).

- **Who writes it.** Only the admin connection `mashctl` holds. Endpoint users have no permission on `$KV.mash-issued.>` or on the stream's `$JS.API` subjects, not even to read, and a test asserts that no permission list mash makes grants anything on it. The stream is created by `mashctl init` (or on first use, over the admin connection) with file storage, no TTL, a history of 64 revisions per key (the most a KV bucket allows: the last 64 revisions of a key are kept, and a key is re-recorded only when a credential is reissued under it; a bucket from the first version, with 8, is accepted and `mashctl init` raises it to 64 without losing a record), a size cap of 8 MiB (one more stream, and 8 MiB more disk, in the account's [limits](#account-limits)), discard-new when full (a full bucket refuses new issues instead of losing a record), and rollup, message delete and purge refused. mash verifies these on use and refuses to write to a bucket that was made otherwise (`recreate it as mash admin init makes it, copying its records`). A holder of the admin creds can still write the bucket, so the record protects against signing-key copies, not against the admin creds ([Who holds which key](security/threat-model.md#10-who-holds-which-key)).
- **What the check sees.** `mashctl issued check` sends the system account's `$SYS.REQ.SERVER.PING.CONNZ` request with `auth` on and the MASH account as filter. For a JWT user the server reports the user's public key as `authorized_user`, plus the connection's name, the key that signed its JWT (`issuer_key`) and its tags; the connection's JWT also comes back and is never read or printed. A request asks for up to 8192 connections, so a typical scan is one reply; the rest of a larger server's connections are read with further requests to that server's own subject (offset and limit). Pages are separate snapshots, so a connection closing or opening between two pages can shift the list: the scan of a server is accepted only when the connections collected (by connection id) match the total the server reports on its first and last page with none repeated, is made again up to 3 times otherwise, and a server that still does not reconcile counts as one that did not answer (`INCOMPLETE`). No key is exempt from the check: the admin user must be in the record (`mashctl init` and `adopt --mint-admin` record it), and the system account's users never appear in the MASH account's connection list. Connections of other accounts are not asked for.
- **What it does not see.** Only connections open when the check runs: a user that connected and left between checks is not seen, so run it on a schedule and after a key leak. It does not see a user that has not connected. It cannot tell a server that is down from one that has no connections except by the server set: give `--servers N` (or `MASH_SERVERS`) the real server count, or the run is `INCOMPLETE` and exits 2.
- **Complete history.** The stream's metadata key `mash_issued_history` says whether a key's retained revisions can be trusted to be all of them: `full` for a bucket created at 64, `raised:N:S` written once by `mashctl init` when it raises an older bucket from N (S is the stream's last sequence, read after the limit is raised; if the marker write fails, `init` reports the error, the limit stays raised and the bucket has no marker, so the legacy rule applies until `init` is run again), none for a bucket from before the marker. `deprovision` and `endpoint delete` refuse to revoke a key whose history is not shown complete (8 or more revisions with no marker; N or more at or below S after a raise), and say to revoke it by hand in the account JWT only after checking where it was issued. Run `mashctl init` on an older deployment so a later raise is marked; a raise already done without a marker cannot be marked afterwards.
- **Revoked keys.** `endpoint delete`, `endpoint reissue` and `deprovision` set `revoked_at` on the key's entry after the revocation succeeds (best effort: a failure is a warning on stderr, and the revocation stands). A key that is recorded as revoked and still connected is reported as `revoked key still connected` and exits 1, because the revocation did not take effect: check that every server holds the account JWT with the revocation, and revoke the key again. Exit codes of `mashctl issued check`: 0 clean; 1 an unrecorded key or a revoked key is connected; 2 the check could not be made. A key revoked outside `mashctl` (nsc) is not marked, so it is not reported this way.
- **After a finding.** The finding names the key and the signer. If the signer is a key that should not sign users, remove it from the account (with the tool that added it), replace the credentials of the endpoints it signed, and revoke the unknown key in the account JWT. A key that is yours but predates the record is recorded when its credential is next reissued (`mashctl endpoint reissue`).

## Removing an endpoint

```sh
mashctl endpoint delete alice/dev/design    # revokes the endpoint's user, removes the record and inbox, burns the address, sets the creds file aside
mashctl endpoint delete alice/dev/design --dry-run   # say what would happen; change nothing
mashctl deprovision alice.dev.design        # the older verb: revokes the user, then removes it; add --user <public-key> to act only if its record still carries that key
```

`mashctl endpoint delete` and `mashctl deprovision` revoke the endpoint's NATS user in the same command. They need the operator and account keys in the key store and the system account credentials (`MASH_SYS_CREDS`, else the `sys.creds` next to `admin_creds` or in `<config dir>/server`; `MASH_SERVERS`; `endpoint delete` takes `--servers N` and `--sys-creds FILE`, which override them; `deprovision` has the same two flags). The revocation is added to the account JWT by user key and time, signed with the operator key, pushed to every server, and read back; a connected user is closed when the server takes the update. It happens before anything is removed, so a revocation that fails or cannot be confirmed (no key store, no system credentials, a server that did not acknowledge) refuses the command with the endpoint intact; running it again is safe. `--no-revoke` removes without revoking, and the user can then still connect: it prints a `WARNING: --no-revoke: ... is NOT revoked and can still connect` block on stderr before it starts and again after it finishes. `mash admin deprovision` has no key store: it removes and leaves revoking to you (`nsc revocations add-user --account "$ACCOUNT" --name alice.dev.design`, then `nsc push --account "$ACCOUNT"`). A revoked key stays revoked, so a name that comes back needs a new user key. The account JWT is replaced whole and the resolver has no compare-and-set, so account changes (`endpoint delete`, `endpoint reissue`, `deprovision`) are serialised only per key directory: two operators with separate key directories must not run them at the same moment, or the later push can erase the earlier one's change (including a revocation). Run them from one operator at a time.

Deprovisioning fences the name, then removes the directory record and the inbox stream. The fence is a tombstone in the endpoint's lease key: a session still running under the name stops at its next lease refresh (within 30 seconds), and one starting up is refused, instead of either taking the name back. The tombstone expires with the lease TTL (90 seconds), and provisioning the name again clears it at once. `mashctl deprovision <owner.group.instance> --user <public-key>` is the guarded form, for scripts that must not remove an endpoint someone else has since provisioned under the same name: it reads the directory record, refuses (touching nothing) unless the record's user equals the key, and deletes the record only if its revision is still the one it read, so a record replaced in between is never removed. If another admin changes only the record's metadata in between, it re-reads the record and retries on the new revision (up to 5 tries), so that change does not refuse; a changed user, a vanished record or exhausted tries refuse; it then fences the lease and deletes the inbox. Provision and both forms of deprovision hold the name's lock ([ADR-0048](decisions/0048-limit-changes-take-a-per-stream-kv-lock-with-expiry.md), see [Limits](#limits)) for their whole run, so a second admin working on the same name waits for the first to finish (up to 20 s, then "try again") and cannot have its new endpoint fenced or its inbox deleted by it. With no record it refuses even if the inbox stream exists, because nothing proves who owns it; run it without `--user` to remove such a leftover. Revoking the user stops its credentials from connecting at all (`mashctl` does this for you; `mash admin deprovision` does not). Until the user is revoked, mail it sends still arrives, but labelled `[unknown-signer]`, because its key is no longer in the directory.

`mashctl endpoint delete ADDRESS` renames `NAME.creds` to `NAME.creds.retired-<UTC timestamp>` (or deletes it with `--delete-creds`), so a creds file is never in the way of a new one; the retired file still holds the old seed. A creation killed mid-run can leave a creds file or an inbox with no complete record; `mashctl audit` lists them as orphans, and `mashctl endpoint delete ADDRESS --force --delete-creds` clears the creds file. A killed `endpoint delete` finishes when you run the same command again; a killed `endpoint create` finishes the same way.

### Provisioning and deprovisioning

`mashctl endpoint create` and `mashctl endpoint delete` do this work for an endpoint, including the NATS user and the creds file. `provision` writes only the directory record, for a user you signed yourself.

`provision` and `deprovision` (in `mashctl` and `mash admin`) take the address, `owner.group.instance`, or the flat key `owner_group_instance`. All three tokens are required: a bare name is refused (exit 2).

```bash
mashctl provision alice.dev.recv --user "$user" --account "$account"
mashctl deprovision alice.dev.recv      # the flat alice_dev_recv works too
```

Provisioning prints `provisioned alice.dev.recv: ...`. A bad token is a usage error (exit 2); a refusal (the name rule, an address already provisioned, a retired address) exits 1 with the reason, nothing changed. A retired address is refused with `endpoint alice.dev.recv is retired ... never reassigned (INV-002)`. `set-meta`, `suspend`, `resume`, `suspended`, `label` and `endpoint delete` take the same address.

### A retired address is never reassigned

Deprovisioning an endpoint also burns a retired marker for its address: one key, the flat key `owner_group_instance`, in the `mash-retired` bucket. The marker has two states, and both deprovision paths (`deprovision NAME` and the user-key-guarded `--user`) use the same order:

1. The marker is written as `pending` first, before the endpoint's user is revoked and before anything is purged. A bucket that is full or failing makes the command fail at this point, with nothing revoked and nothing removed.
2. The user is revoked, the lease is fenced, the directory record is purged (for `--user`, with the record's revision, so a changed or replaced record is never removed).
3. After the purge the marker is made `final` (an update with a revision check; the first retired time is kept).

A `pending` marker blocks provisioning exactly like a `final` one: `INV-002`, and the message says the retirement is pending and unfinished. The command deletes a pending marker only when the retirement definitively did not happen: a check that refuses the removal before anything is purged (a record that belongs to another user key, a revocation that could not be confirmed), or the server's refusal of the revision-guarded purge (the record changed or was re-keyed after the user was revoked; the revoked user stays revoked). Deleting the marker after the revocation is right because a refusal means the record changed, so no retirement of this name happened; the live record keeps its name. It deletes it with a revision check, and only while its own name lock is still held: a call that has lost its lock leaves the marker pending and says so. A purge that fails with an answer that does not say whether the server did it (a timeout, a lost connection) is not a refusal: the marker stays `pending`, the error says the retirement is unfinished, and running the same command again resolves it. A command that stops part-way likewise leaves the marker `pending`, so the name is never reused in between. Running the same command again resolves it: with the record gone and the marker present it makes the marker final (writing it final if it vanished while it was being made final) and finishes the lease fence and the inbox delete; with the record gone and no marker, the `--user` form refuses and changes nothing (no marker, no fence, no inbox delete), because only a marker shows a removal began, so a mistyped name is never burned and a partial provision's mail is never deleted (the plain form without `--user` still removes a record-less leftover, and writes no marker); with the record present for the marker's user key it revokes if needed, purges, and makes it final. If a live record carries a different user key than a `pending` marker names, that old retirement was superseded: the command deletes the stale marker (with a revision check) and carries on as if there were none. If the marker is `final` and a live record carries a different user key, the command refuses with `INV-002` naming the burned name and removes nothing. A marker with no `state` (written before the two states existed) counts as `final`. The value holds the address's three tokens, the retired user public key, the time and the state, never a seed. Provisioning an address checks the marker while it holds the directory-wide admission lock and refuses with `INV-002` before writing anything.

The bucket is checked wherever it is opened (`init`, provisioning, both deprovisions) and the command refuses when it has a TTL (a TTL would expire markers and let a retired name come back) or a history other than the configured one, or when its stream evicts messages (a discard policy other than new, a message-age limit or a message-count cap), since a full bucket must refuse a write rather than drop a marker; the message says to fix or recreate the bucket, and mash never changes an existing bucket for this. Provisioning also refuses when the bucket has no room left for one more marker, because the later deprovision could not retire the name.

What an auditor sees: a `final` marker is a retired name; a `pending` marker is a retirement that was started and not finished (the record may still be present, with its user revoked or about to be). A pending marker that stays pending is a removal to finish by running `deprovision` again. `mashctl audit` lists every marker (flat key, state, retired time) with the counts of final and pending, the bucket's bytes against its cap and the percent used; `--strict` fails for a pending marker (rerun `deprovision` for it) and raises the alarm when the bucket is at or over 50% of its 64 MiB cap, and `audit --compare EARLIER.json` fails when a final marker has gone ([audit.md](audit.md#retired)). No bucket means no retirement was ever recorded.

Unlike the lease tombstone above, the marker has no TTL, so the refusal does not lapse after 90 seconds. Nothing in mash deletes a marker except a pending one whose retirement did not happen, and there is no command to release one: a release would need a ruling from the contract and an audit record, neither of which exists yet. An address with no directory record is not burned, so a mistyped `deprovision` retires nothing. Endpoint users have no permission on the bucket.

### Suspend an endpoint's inbound mail

To read the last of an endpoint's mail before removing it, stop new mail first, then drain, then deprovision ([ADR 0057](decisions/0057-an-admin-can-suspend-an-endpoints-inbound-mail-by-parking-its-inbox-stream-subjects.md)):

```sh
mashctl suspend design      # suspended design: id 9f3c..., last_seq 41, msgs 38
mashctl suspended design    # exit 0 if suspended, 3 if not, 1 on any error
# read the inbox through sequence 41 (the last_seq above); it is final while the endpoint stays suspended (suspend waits for a publish accepted just before the park to land, so it is already final when printed)
mashctl deprovision design --suspension 9f3c... --drained-to 41
```

`--suspension ID --drained-to N` (both are required together) is the check that makes this safe: under the name's lock it refuses, touching nothing, unless the inbox is still suspended under that id and its last sequence is exactly N, and the directory record still belongs to the user key it had when `suspend` ran (a record that is missing, was missing at suspend time, or was replaced by another user key is refused; use plain `deprovision` for a leftover). The id, a random token `suspend` stores and prints, ties the drain to that one suspension, so an endpoint re-provisioned under the same name (which can have the same last sequence, commonly 0) cannot be removed by an old command. If another admin ran `resume` meanwhile, or mail arrived after it, the command fails instead of deleting mail you have not read. A plain `mashctl deprovision design` does not check, so use it only when you do not need the mail. `--drained-to` cannot be combined with `--user`.

`suspend` replaces the inbox stream's subjects with one subject no endpoint may publish to and saves the originals in the stream's metadata; nothing is purged and no other field changes. Mail sent while the endpoint is suspended is refused, not queued: the sender's publish finds no inbox and fails, and the sender cannot tell that from an endpoint that does not exist, so it must send again after `mashctl resume design`, which restores the saved subjects. Mail stored before the suspension stays readable, and the endpoint's own session keeps running. `limits` keeps the suspension; provisioning over a suspended endpoint's stream refuses. Suspend does not revoke: it prints `new mail parked; the endpoint's credentials are still valid`, and the endpoint can still connect and send. Revocation happens in `deprovision`. Both `suspend` and `resume` can be repeated safely. `mash admin` carries the same three verbs. Suspending and resuming are admin operations: an endpoint's credentials cannot update its stream.

## Topologies

mash supports one NATS server, or a cluster, in the default JetStream domain, with the default API prefix. Inboxes are single-replica ([Durability](#durability-and-sync_interval)), so on a cluster an inbox is unavailable while its node is down.

JetStream domains, leaf-node inboxes and subject mappings are not supported. The endpoint permissions that `mashctl permissions` generates (`$JS.API.*`, `$JS.ACK.<stream>.<consumer>.>`) and the client (`jetstream.New` with no domain or API prefix) assume the default domain. On a leaf node with its own domain, or behind subject mappings, the API and ACK subjects differ: acks can be denied (endless redelivery), the inbox may be unreachable, and signatures over mapped subjects may not verify. A `js_domain` setting is not implemented.

## Several servers

This section covers `mashctl` run from one central host against a NATS cluster of two or more servers. The recipe below was run live with three routed servers; a server behind TLS, gateways and two central hosts were not tried.

### One central host

Keep the admin credentials, the system account credentials and the key store (`operator.nk`, `account.nk`) on one host and run `mashctl` there. It reaches the servers only through their URLs and opens no server file. The endpoint hosts need only their own creds file.

- `MASH_SERVER` lists every server, separated by commas (or `servers` in `config.json`), so a connection still succeeds when one of them is down.
- `MASH_SERVERS=N` or `--servers N` is how many servers the cluster has. `endpoint create`, `endpoint reissue`, `endpoint delete` and `deprovision` take `--servers N`, which overrides `MASH_SERVERS`. A server count that is not a whole number of 1 or more is refused. Without it, a command that writes the account JWT waits for a quiet period instead of for N replies, and a cluster member that sees no peers is refused as an unverifiable server set.
- `--sys-creds FILE` or `MASH_SYS_CREDS` names the system account creds. The same verbs take `--sys-creds FILE`, which overrides `MASH_SYS_CREDS`. Without either, `mashctl` looks for `sys.creds` next to the admin creds, then in `<config dir>/server/` (where `adopt` puts it).

```sh
mashctl deprovision alice.dev.design --servers 3 --sys-creds /secure/sys.creds
mashctl endpoint delete alice/dev/design --servers 3 --sys-creds /secure/sys.creds
```

A creds file holds the user seed. The seed never leaves the endpoint's machine: make the key there with `mash key create` and send only the public key file to the `mashctl` host. Only with `--make-key`, which is for an endpoint on the `mashctl` host itself, does `mashctl` write a creds file, and that file stays on that host.

### One server down

- Each bucket and each inbox has one replica, on one node. Any one node down therefore blocks the writing commands (`endpoint create`, `endpoint reissue`, `endpoint delete`, `provision`, `deprovision`), because each of them writes to a bucket or inbox that node may hold. `endpoint list` can still work when the directory bucket is on a live node. This stays so until the buckets get replicas.
- `endpoint delete` and `deprovision` also refuse when fewer servers answer than the count says: `refusing: only N of M servers answered: a server may be down (bring it back and run the command again), or the server count is wrong (pass --servers N or set MASH_SERVERS)`. It does not name the silent server, because the lookup does not say which one it was. Bring the server back first. Lower the count only when it was wrong.
- When the server that holds a stream or bucket is down, the command says `the server holding bucket X is down: bring it back and run the command again` (`audit` and `issued` say `the server holding stream X is down`, or `a stream or bucket` when the error does not name it), followed by the raw NATS text. `mashctl init` does not help, because the bucket exists.
- `endpoint delete` and `deprovision` check the server set before they change the directory record, the lease or the inbox. When it is short they print `nothing was revoked; the key is still valid` and leave the record as it was. Run the command again when the server is back. If the revoke failed after the update was pushed, the text says the revocation may already be in force on some servers and that the directory record was not changed; run the same command again (revoking again is safe). If the revocation completed and a later step failed, the PARTIAL message says the key was revoked, with the number of servers that acknowledged, and that the removal of the record, the lease and the inbox is still pending. Running the same command again finishes it.
- `MASH_SERVER` (or `servers` in config.json: a JSON array, or one string with commas; or `--server`, repeated) takes a list, so a connection still succeeds when the first server is down. If every listed server is down, the error is `no servers available for connection`, with a hint to check `MASH_SERVER` and that at least one listed server is up.

### A three-node cluster

`mashctl init --local` builds one node only: loopback, TLS only, `server_name: mash-local`, no cluster block. For a cluster:

1. On node 1 run `mashctl init --local` (without `--start`). It writes `server.conf`, `operator.jwt`, `mashadmin.creds` and `sys.creds` in the server directory, and the signing keys in the key store on that host. Do not start node 1 yet.
2. Edit node 1's own `server.conf`: give it a new `server_name` (it is `mash-local`) and add a `cluster` block. Then copy `operator.jwt` and `server.conf` to the other nodes as a template. The `tls` block names node 1's certificate and key by absolute path, and that certificate lists only `localhost`, `127.0.0.1` and `::1`, so it works for no other node: for each node make a certificate and key that list the node's own host names and addresses, signed by a CA the clients trust (see [TLS, clusters and a second mashctl machine](tls-and-clusters.md)), set `cert_file` and `key_file` in that node's `tls` block, and give clients `ca`. In each copy set a different `server_name` and `listen` address, a different `dir` in the `resolver` block, a different `store_dir` in the `jetstream` block, and a different cluster `listen`. Keep the same `resolver_preload` (the system and account JWTs), so every node starts with the same accounts. The `operator` line is the path of `operator.jwt` on that node. The `cluster` block of node 1 in a three-node cluster on one test host looks like this; `routes` lists the other nodes' cluster addresses, and `name` is the same on every node:

```
server_name: mash-n1
listen: 127.0.0.1:4222
resolver: {
  type: full
  dir: "/var/lib/mash/n1/resolver"
  allow_delete: false
  interval: "5s"
}
jetstream: {
  store_dir: "/var/lib/mash/n1/jetstream"
}
cluster: {
  name: mashc
  listen: 127.0.0.1:6222
  routes: [nats-route://127.0.0.1:6223, nats-route://127.0.0.1:6224]
}
```

   Listen on addresses the central host and the other nodes can reach, and use TLS for anything beyond a test (the template has none).

   Set `interval` in every node's `resolver` block. The full resolver syncs from a timer only, one interval after the node starts (default one minute), and nothing holds clients back until it has. A node that was down while a key was revoked therefore accepts the revoked credential until its first sync; `interval: "5s"` shortens that to the interval plus about 0.03 to 0.1 s on a loopback cluster (measured: restart to refusal took 5.03 to 5.11 s with `interval: "5s"`); a real network may add route formation time. To close it completely, keep the returning node's client port closed (a firewall rule, or listen on a private address first) until `mashctl audit` shows no `revocation_pending`.
3. Start the nodes. Check that the cluster formed with `mashctl issued check --servers 3` once step 5 is done (it needs the admin creds and the issue record): its last line, `summary: ... 3 of 3 servers answered`, counts the servers that answered the system account, and it exits 2 with `INCOMPLETE` when fewer did. `adopt` also prints `3 server(s) replied`.
4. On the central host, copy the operator signing seed, the account identity seed and `sys.creds` from node 1, and run `adopt`. It creates the central host's config.json, imports the operator and account keys into its key store, copies `sys.creds` to `<config dir>/server/` and, with `--mint-admin`, writes `mashadmin.creds` there. A fresh central host has no configured owner, so pass `--owner`:

```sh
mashctl adopt --server tls://node1:4222 --owner O --account MASH \
  --operator-key operator.nk --account-key account.nk --sys-creds sys.creds --mint-admin
```

5. On the central host run `mashctl init` (no `--local`). `adopt` does not create the directory and lease buckets; this does, over the cluster, with the admin creds `adopt` wrote. `init --local` is not run on the central host: it would make a second, local server and a second set of keys.
6. Put every server in config.json's `servers`, for example `"servers": ["tls://node1:4222", "tls://node2:4222", "tls://node3:4222"]`, and set `MASH_SERVERS=3` (or pass `--servers 3`).

`--account` must be the account name `init --local` chose, `MASH` by default; the default for `--account` is the owner, which fails when the owner is another name. `adopt` takes one `--server` URL and writes it to config.json. For a test cluster without TLS, run `adopt` as `MASH_INSECURE_PLAINTEXT=1 mashctl adopt --server nats://127.0.0.1:4222 ...`: it prints the plain-text warning, and writes `insecure_plaintext` into config.json, so the later commands do not need the variable. Never do this on a network you do not control. The seed files must be mode 0600. Delete the copies you made when `adopt` has stored the keys.

## Inbox limits

By default each inbox keeps mail for 30 days or 16 MiB, whichever comes first, dropping the oldest (both are configurable: see [Limits](#limits)). Single messages are limited to 1 MiB including headers, or to the server's `max_payload` when that is smaller: mash reads it from the server at connect time and refuses an oversize send locally with a clear error before anything is published. Synadia Cloud's free tier caps a payload at 512 KiB, so there the limit is 512 KiB. Message deletion and purging are denied on the stream itself, so even the account admin cannot quietly rewrite history; deleting the whole stream (deprovisioning) is the only way to remove it.

A full inbox drops its oldest mail, read or not, and the server keeps no counter for it. mash derives the loss from the stream: a message whose sequence is below the stream's first no longer exists (deleting and purging are denied, so a limit dropped it), and a message the endpoint's consumer never received was lost unread. `mashctl audit` reports each endpoint whose inbox lost mail unread as a problem (`inbox <key>: at least N unread message(s) lost: evicted from this inbox ...`). An inbox at least 90% full that has lost nothing unread is a note (`note: inbox <key> is N% full ...`, in the JSON `notes`): operational state, not a misconfiguration, so it does not fail `--strict` or change the exit code. The JSON report gives every inbox `bytes`, `max_bytes`, `fill_percent`, `evicted` and `evicted_unread`. The running session's `status` tool shows the same for its own inbox as an `inbox: WARN` line (nothing from a shell, and nothing when the server will not show the stream). Read mail sooner, or find the sender that floods it with `mashctl audit`; deprovisioning and provisioning an endpoint again starts the count over. `evicted` also counts mail that aged out (`inbox_max_age`), and `evicted_unread` is a lower bound, which is why every message says "at least". The count comes from the consumer's sequences, which count deliveries, not what was shown to the user: mail that was fetched but never shown, or that was redelivered, can make it undercount, so a clean audit does not prove that nothing was lost. `--strict` fails whenever the lower bound is above 0.

## Limits

The caps on inboxes and buckets are configurable. The defaults are what mash has always used:

| Key (`limits` in config.json) | Default | Applies to |
|---|---|---|
| `inbox_max_bytes` | `16MiB` | each inbox stream; the oldest mail is dropped past it (minimum 2 MiB) |
| `inbox_max_age` | `30d` | each inbox stream; older mail is dropped (minimum `1h`) |
| `directory_max_bytes` | `16MiB` | the directory bucket (minimum 1 MiB) |
| `lease_max_bytes` | `4MiB` | the lease bucket (minimum 1 MiB) |
| `inbox_max_consumers` | `-1` | consumers per inbox stream (-1 is unlimited, else at least 1) |

An optional `limits` object in `~/.config/mash/config.json` changes them; leave out a key to keep its default. Sizes are integers (bytes) or strings with a `B`, `KiB`, `MiB` or `GiB` suffix; ages are Go durations (`720h`) or whole days (`30d`). An unknown key or an invalid value is an error naming it. There are no environment overrides.

```json
{
  "servers": ["tls://nats.example.com:4222"],
  "endpoint": "alice.dev.design",
  "limits": {
    "inbox_max_bytes": "16MiB",
    "inbox_max_age": "30d",
    "directory_max_bytes": "16MiB",
    "lease_max_bytes": "4MiB",
    "inbox_max_consumers": -1
  }
}
```

`mash admin limits --show` prints the effective limits, one `key value` per line.

### Changing limits later

Limits only go up. Edit config.json, then apply it:

```sh
mash admin init              # raises the two buckets (a larger existing cap is kept)
mash admin limits            # raises every inbox's max_bytes and max_age, and sets max_consumers
mash admin limits alice.dev.design   # or only the named inboxes
```

New endpoints (`mash admin provision`) get the configured limits at once. `mash admin limits` updates each existing stream in place, keeping its mail, and prints a line per endpoint saying what changed. It refuses to lower `max_bytes` or `max_age` on an endpoint, with an error naming the endpoint and field, and changes nothing for it, because a lower cap could discard stored mail. To lower an inbox's limits, run `mash admin deprovision <name>` (which removes the mail in it) and provision it again. `mash admin init` likewise never lowers a bucket. The check that runs on an existing inbox still reports any stream that differs from the configured limits. JetStream has no compare-and-set on a stream's configuration, so each change to an inbox's limits (`mash admin limits`, and the stream step of `mash admin provision`) takes a per-stream lock first: the key `limits-lock.<name>` in the lease bucket, held from the read of the stream's caps until its update is written. Two admins changing one inbox at once are serialised, so the second sees the first's caps and is refused if it would lower them. The lock lasts 30 s and expires on its own if the admin dies; a second admin waits up to 20 s for a held lock and then fails with `mash: another admin is changing the limits of <name> (lock held until <time>); try again`, which is safe to retry. Every operation under the lock now stops about 5 s before it expires and re-checks it before writes that cannot be undone, so a stalled admin fails with `try again` instead of overlapping another admin. Over many inboxes the locks are taken one at a time. Expiry is judged by each admin's own clock, so keep admins' clocks within a few seconds of each other (`mash status` prints a `clock:` line); with clocks 45 s apart, one admin can take a lock the other still holds, and the one that lost it is refused with `the lock of <name> was lost or is about to expire ... try again` (two admins creating one endpoint still never both succeed, because the directory record is created only once). Endpoints cannot read or write the lock. A deployment that never ran `mash admin init` for the lease bucket has it created on first use ([ADR-0048](decisions/0048-limit-changes-take-a-per-stream-kv-lock-with-expiry.md)).

### Account limits

The NATS account has limits of its own: streams, consumers and disk. Size them from the endpoint count: one stream per endpoint plus two for the buckets, and one consumer per endpoint. The disk must hold the sum of the stream caps (`inbox_max_bytes` per endpoint plus the two bucket caps), so raising the inbox cap raises the disk you need.

```sh
ENDPOINTS=20
JS_STREAMS=$((ENDPOINTS + 2))
JS_CONSUMERS=$ENDPOINTS
JS_DISK_MIB=$((ENDPOINTS * 16 + 16 + 4))   # inbox_max_bytes in MiB per endpoint, plus the two buckets
```

The examples above default to `JS_DISK=2G`, `JS_STREAMS=64` and `JS_CONSUMERS=128`, which leave room to grow; change the values and re-run `nsc edit account "$ACCOUNT" ... --js-disk-storage "$JS_DISK" --js-streams "$JS_STREAMS" --js-consumer "$JS_CONSUMERS"` and `nsc push`. Leave `--js-mem-storage` unset.

## Durability and sync_interval

Every mash inbox is a JetStream stream with one replica (R1). nats-server's default `sync_interval` is 2 minutes: the server acknowledges a write before it has fsynced it. So a host crash or power cut can lose up to about 2 minutes of mail that mash already reported as `sent #N`. Restarting or killing mash, or the nats-server process, is fine: the data is in the operating system's page cache and survives. See [Jepsen's analysis of NATS 2.12.1](https://jepsen.io/analyses/nats-2.12.1) and [Synadia's response](https://synadia.com/blog/jepsen-nats-2-12-1), which recommends `sync_interval: always` for a single server.

- **Self-hosted server:** set it in `nats-server.conf`, then restart the server:

  ```
  jetstream { sync_interval: always }
  ```

  I could not confirm the block from the NATS configuration pages, so check the documentation for your server version. Replicated streams (R3) can survive the loss of one server when the replicas sit on independent hosts, but replication alone does not guarantee that acknowledged writes survive a crash (see the NATS JetStream documentation); mash does not provision replicated streams today and Synadia's free tier does not offer them.
- **Cost:** `always` fsyncs on every write, so write latency and throughput suffer. A mail mesh sends little, so this is usually acceptable.
- **Synadia Cloud:** its policy for this is not documented and unknown to us (we have asked). Treat a host crash there as able to lose recent mail.
- **Visibility:** mash cannot read the server's setting today, so `mash status` does not warn about it.

## Hook delivery

The plugin ships two Claude Code hooks, PostToolUse and UserPromptSubmit, which run `mash hook` (`plugin/hooks/hooks.json`).

The hook command always exits 0 (a mash too old to have `hook` would otherwise exit 2, which Claude Code treats as blocking) and appends the hook's stderr to `hook.log` in the state directory (`$MASH_STATE_DIR`, else `$XDG_STATE_HOME/mash`, else `~/.local/state/mash`), beside the spool. That is where a failing or too-old hook is found; a diagnostic tool can surface it.

**Subagents have no mailbox.** Claude Code runs hooks for a subagent's tool calls too, and mash ignores them (a hook input carrying `agent_id`): they show no mail and drain nothing. Mail waits for the main session's next tool call or prompt, or for the idle wake. A session busy in a long subagent run therefore sees no mail until then. Such a hook only counts as proof that hooks run (see "Hooks that never run").

**Which hooks may drain the spool.** `mash serve` records its owner in `<spool>/.owner` as JSON: its parent pid, its `CLAUDE_CODE_SESSION_ID` and its `MASH_LAUNCH_TOKEN`. A serve that waits for the lease records itself in `<spool>/.waiting.<pid>` (its parent Claude pid and session id) and removes the record when it binds or exits. A hook that descends from such a waiting serve and not from the owner is refused as another launch, with no launch token needed (broken process layouts fall back to the rules below). Otherwise a hook drains the spool if any one of these holds: its session id (the `session_id` on its stdin, else `CLAUDE_CODE_SESSION_ID` from its environment) equals serve's (unless both sides have a non-empty `MASH_LAUNCH_TOKEN` and they differ: that is another launch of the same session, such as a `claude --resume` of a running one, [ADR-0043](decisions/0043-second-launch-of-a-running-session-is-refused-and-told-once.md)); both sides have the same non-empty `MASH_LAUNCH_TOKEN` (this survives `/clear`); or serve's parent pid is among the hook's ancestors. A hook that matches none is refused: it prints nothing, leaves the mail spooled, writes one line to `hook.log` (`mash hook: not this session's spool (...); mail left for its owner`) and records `<spool>/.refused` (at most once a minute). For the other-launch case the hook also prints a one-time notice into that session's context. This keeps a second session with the same `MASH_NAME` from draining this session's mail. `<spool>/.session` keeps the legacy text `pid:N`, so a hook from an older mash still enforces the process-ancestry rule (and nothing else) when serve is newer; a damaged `.owner` makes every hook refuse. Keep serve and hook on the same mash version for the session-id and launch-token rules. `MASH_HOOK_TRACE=1` makes every hook run log its pid chain, both session ids and the rule that matched.

### Same-session takeover

When Claude Code restarts its MCP server inside one session (an account switch with `/login`, for one), the new `mash serve` can start while the old one is alive and still holds the endpoint lease. The lease records the serving process's parent pid and that parent's start time (`ppid`, `pstart`) and `CLAUDE_CODE_SESSION_ID` (`session`), each only when known. A new serve takes the lease over, by the same compare-and-set as for a dead holder, when the holder is on the same machine and has the same non-empty session id; when either side has no session id, the same parent pid with the same parent start time does instead. Two different non-empty session ids never match, whatever parent they share. The log line is `lease: took over from the same session's earlier serve <instance> (pid P)`. It refuses a lease of another session, another machine, one that lacks those fields, one started under another `MASH_LAUNCH_TOKEN`, and a taker whose own parent pid is 1 or less. What this does and does not protect: the session id and the parent identity are claims the holder writes into the lease. A process on the same machine that holds the endpoint's credentials is already that endpoint (it can publish and consume as it), so a same-session takeover gives it no authority it did not have; the refusals above keep one session from taking another's endpoint by accident or by a differing identity. The earlier serve reads its lease key every second, so it finds out within about a second (it no longer waits for its next refresh, up to 30 seconds): from that moment it neither delivers nor acknowledges any mail, nak's what it held (removing its waiting spool entries) so the new serve is redelivered it at once, stops consuming and refreshing, and becomes inactive. Its tools answer that it was superseded by a newer serve of the same session (naming the pid) and that mail is handled there, `mash status` shows `connection: SUPERSEDED` with the new holder, and it exits when its stdin closes or its parent changes. If the takeover loses its compare-and-set to a write made since, the new serve retries after 1 s, up to 5 times, instead of waiting the usual 10 s.

**Exit on stdin EOF (guarantee).** When its stdin reaches EOF, `mash serve` stops consuming, releases the endpoint lease and exits with code 0 within 5 s (the lease release is bounded at 3 s); another serve can take the name at once. If EOF arrives during the first open, serve exits as soon as that open returns, which is bounded by the open timeout (`open_timeout` in the config, `MASH_OPEN_TIMEOUT` in the environment; default 20 s). A serve that has lost its lease, or been superseded from it, never renews it again, and exiting leaves the new holder's lease alone.

While a serve waits for a lease that another session holds, it records the holder (pid, host, since) and its own pid and instance in `<spool>/.lease-wait`, rewritten on every retry and removed when it takes the lease or exits, but only while the file still names that serve (several serves can wait at once: the last writer's state stands, and another serve's removal never clears it). The next run of `mash hook` in that session prints one line, `mash: this session does not hold endpoint <name> (held by pid P on H); mail is off until it recovers`, and again at most every 10 minutes while the wait persists (the rate limit is kept in `<spool>/.lease-wait-told`). A state whose serve process is gone prints nothing, and neither does the hook of the session that holds the lease. `mash status`, the tools and the channel notice name the holder found by the latest retry ([ADR-0092](decisions/0092-a-new-serve-of-the-same-session-takes-over-the-endpoint-lease.md)).

**`delivery: REFUSED` in `status`** means the plugin's hook runs in this session but was not recognised as this session's, so mail stays spooled, and serve wakes the session a few times (then stops: see below). It happens where hooks run outside the process tree of serve's parent (serve started through a wrapper that does not `exec`, a PID namespace or sandbox, some IDE or desktop hosts, a chain deeper than 10) and the session id does not match either. The fix: start Claude Code with `MASH_LAUNCH_TOKEN` set to a fresh value for each launch (for example `MASH_LAUNCH_TOKEN=$(uuidgen) claude ...`); serve and its hooks inherit it. A value reused across launches, or one shared by two sessions with the same name, defeats the launch-token rule: a waiting session would then drain the holder's mail (serve warns on stderr when it sees this). If it still refuses, report your setup with the `hook.log` line and `MASH_HOOK_TRACE=1` output.

**Hooks that never run** (ADR-0041). Mail waits in the spool for a hook, and only a hook drains it. If no mash hook runs in the session, waking it again would cost a turn each time and show nothing, so mash watches for that. Both states hold only while no main-session hook, and no hook of the session's own subagents, has run since `serve` bound the spool; a hook run ends them.

* **Suspected:** three wakes (the first, at once, counts) and still no hook. Re-waking stops. A new waking message still rings once. Nothing is acked, and a message the spool will not take is held unacked instead of written to the channel: a channel write is not delivery when wakes are not landing (issue #40). `status` shows `delivery: SUSPECTED - the last <n> wakes started no turn (no hook since <time>); channel writes are not counted as delivery; re-waking stopped`. A session that makes no tool calls stays here: no turn is wasted, and the mail is read if the model uses the `read` tool.
* **Proven:** two mash tool calls at least 12 seconds apart (above the hook's 10 second timeout) with no hook between them. A PostToolUse hook runs after every successful tool call, so the first call's hook would have run by the second. Then one wake, and each later wake, ends "mash's hooks are not running in this session; read each message with the read tool (#seq)." The `read` tool marks the message it returns as seen once its result has been written to the client, so it is acked; the `inbox` tool never acks. The one notice in the proven state is sent for held mail of any kind, result and fyi included. `status` shows `delivery: DEGRADED - no mash hook has run since <time> although this session has made tool calls. Likely cause: ... Mail now: read each message with the read tool. Next: ...`. A hook refused as another session's (above) counts as no hook, and its refusal is named as the cause.

mash does not read Claude Code's settings files, so `status` lists the causes that leave no trace: `disableAllHooks`, `allowManagedHooksOnly`, hooks not loaded, `mash` not on the hook's PATH. The fixes: remove `disableAllHooks: true` from the user, project, local or managed settings; under `allowManagedHooksOnly` an administrator force-enables `mash@mash` in managed `enabledPlugins` (hooks of plugins force-enabled there are exempt); check that the plugin is enabled in this project; read `hook.log` for `command not found` or a refusal. Mash tool calls a subagent makes count as the session's tool calls, and a session whose only running hooks are its subagents' is never proven.

**Under the plugin** (`MASH_SPOOL_FROM_START=1`), mail is spooled from the first message and shown by the hooks; the channel event is only a one-line summary that wakes an idle session. Without that setting, `mash serve` pushes mail to the channel in full until the first hook has run in the session.

Spooled delivery works like this (without `MASH_SPOOL_FROM_START`, it starts at the first hook run):
- It writes each message to `spool/<owner>/<flat key>/<seq>.json` in the state directory (`$MASH_STATE_DIR`, else `$XDG_STATE_HOME/mash`, else `~/.local/state/mash`) (directories 0700, files 0600).
- It holds the message unacked and sends the channel nothing: a hook runs after every tool call and with every prompt, so a busy session sees the mail without a turn of its own.
- If spooled mail that wakes waits 45 seconds by default with no hook draining it (`MASH_IDLE_WAKE` or `idle_wake` in config.json, 5s to 30m) (an idle session), serve sends one channel event, the summary line alone (`1 message from lead (question)`). That starts a turn, and its UserPromptSubmit hook shows the mail. If that wake starts no turn, serve wakes it again while the mail is still held: twice the idle wake later (90 seconds by default), then doubling each time, to every 30 minutes. A hook drain resets the backoff. If no hook has run in the session at all, the wakes stop after the third (see "Hooks that never run"). Under `MASH_SPOOL_FROM_START` (the Claude plugin sets it), mail that wakes arriving before the first hook has run in the session wakes it at once, with no idle wait, each message once, and that ring does not start the backoff, so the idle wake still follows if it starts no turn; result and fyi still wait, and once a hook has run the idle wake applies as above.
- Which mail wakes is its kind (`mash send --kind K`, or the send tool's `kind`; the `Mash-Kind` header): `blocker`, `question` and `task` wake an idle session; `result` and `fyi` (opt-in; a send with no kind is `task`) never do (mail with no `Mash-Kind` header, from older mash, or with an unknown kind is `task`, and wakes), so they wait for the session's next hook run and cost no turn, except that a backlog of half the consumer's limit (500 messages) or a quarter of the inbox's 16 MiB byte limit wakes the session at once to drain, so waking mail behind it can still arrive. While only result or fyi mail is held there is no idle wake and no re-wake; once a waking message is held, the wake's summary covers all held mail and the drain shows it all. The kind is the sender's claim and is not signed. A reply (`in_reply_to`) sent as `question` with no `?` and at most 280 characters goes as `fyi`, and the send tool says so; a `task` or `blocker` is never downgraded. Every message shows its `kind:`.
- Urgent mail (`mash send --urgent`, or the send tool's `urgent`) wakes the session at once, of any kind, with a summary led by `urgent,`. Each urgent message wakes it once. The mark is the sender's signature over the message (the `Mash-Urgent` header) and counts only for a [verified] sender; anyone else's urgent mail is normal mail.
- A message may show `answers #N` (the send tool's `in_reply_to`) and `relayed from <origin> #N` (its optional `via`: a #seq in the sender's own inbox the message relays or quotes). Both are signed by the sender and shown only when the signature verifies for that message and the sender is `verified` (or `stale`); they never change a trust label or wake anything, and a `via` proves only that the sender declared the relation, not that the content matches. A sender on an older mash sends `in_reply_to` unsigned, and it is not shown.
- The next hook run shows the session the spooled mail as hook context, framed with the trust notes, at most `MASH_BATCH_MSGS` messages per run.
- A large batch is shown as a digest: one line per message with a preview of its first line, when the bodies of the messages in a run (at most `MASH_BATCH_MSGS`, 10 by default) total more than `MASH_BATCH_BYTES` (16384 by default), or when the full text would pass the 9,500-unit cap mash keeps under Claude Code's 10,000-character hook limit. Messages that do not fit even as a digest wait for the next run and are not marked. **A digest line is not delivery.** The hook marks each message it listed `listed/<seq>`, not drained, and the digest says "Read each with the read tool (#seq); they stay unread until you do." serve does not ack a listed message. It is acked when the model calls `read` for its #seq (once the result is written, in every mode; `inbox` never acks), or when a later hook run shows it in full (new mail that fits in full, together with the listed mail, if that whole batch fits in full). A run with nothing new shows nothing, and a listed message is never listed again, so a message the model does not read stays unacked and is not repeated. Listed mail never wakes the session and does not count in the lease's `waking_pending`; new mail wakes as usual and the wake's summary does not name the listed mail. After a restart of serve, unacked listed mail is redelivered and listed again once, in one digest ([ADR-0046](decisions/0046-a-digest-lists-mail-it-does-not-ack-it.md)). Senders should still put the action in the first line.
- The hook marks each message `drained/<seq>`. serve then acks it (confirmed by the server) and removes the tombstone. Until the ack is confirmed the tombstone stays, so a redelivery is acked rather than shown again.
- Delivery is **at-least-once**. The hook records `shown/<seq>`, prints the batch, then marks it drained. A hook killed between the print and the mark (a timeout, a crash) shows the batch again on the next run, each message flagged `re-delivered after an interrupted hook` with its #seq, so the model does not act on it twice. No message is lost.
- If the spool cannot be written (disk full, permissions), a message is retried 3 times and then, once a hook has run in the session, delivered by the channel instead (before that, held unacked in the inbox, readable with the inbox and read tools); the failure is logged once. A held message whose spool file goes missing or is damaged is rewritten from memory.

**Fail closed: `MASH_SPOOL_REQUIRED`.** By default a spool that cannot be written falls back to the channel, as above. With `MASH_SPOOL_REQUIRED=1` (or `spool_required` in config.json) mash never takes that fallback, for any host: mail is spooled from the first message and acked only once a hook has drained it, and mail the spool cannot take stays unacked in the inbox. The failure is reported, not routed around: one line on stderr, the channel notice, and a `spool: FAILING - mail is being held unacked in the inbox because the spool is failing: <error>; ...` line in the `status` tool, which goes when a later write succeeds. A spool that cannot be made at start is retried every 5 seconds and bound when it works; the held mail is then redelivered and spooled. The `mash status` command does not connect, so it never shows the line: look in the session's `status` tool and on stderr. While the line stands the held mail is read with the inbox and read tools. A spool that stays broken means mail waits unseen: it is not lost, but nothing shows it to the session until the spool recovers or the model reads it by hand.

What this means for operators:
- **Unread means not yet shown by a hook, and not listed only.** The inbox consumer's unread count (pending plus ack-pending) counts a spooled message until a hook has printed it in full, or the model has read it with `read` after a digest listed it, and serve has acked it. A message a digest only listed stays unread. It is still not proof the model read a message shown in full: a hook's output may be on its way into the session.
- **The spool is bounded.** The consumer's MaxAckPending (1000) caps the count, and `SpoolMaxBytes` (32 MiB) caps the bodies held. Past either cap, mail stays in NATS, still unread. The byte cap is tested; the count cap rests on NATS's MaxAckPending and is not exercised by a test.
- **A restart re-spools.** Unacked messages are redelivered and spooled again, except one a hook already showed: its tombstone gets it acked instead. A Claude restart starts a new serve, which takes over the spool and delivers what the old session had not shown.
- **A hook without serve does nothing.** It never creates a spool, so it is silent until serve has made one.
- **Test failpoints.** `MASH_HOOK_FAILPOINT` (`before-print`, `after-print`, `after-first-drained`) makes `mash hook` print `mash hook: failpoint <name>` on stderr and block, and `MASH_SERVE_FAILPOINT=before-ack` makes `mash serve` print `mash serve: failpoint before-ack #<seq>` and block just before it acks a message a hook has drained (it never acks it), so a test can SIGKILL the process there. Both are for the crash tests only; never set them outside those.

## Relay tracing

`mash serve` traces relay by asking: a session that receives a request from one endpoint and soon asks a third endpoint the same thing, passing a message along. It only traces ([ADR 0064](decisions/0064-relay-by-asking-is-traced-not-blocked-content-free-trace-lines-and-counters.md)). It never blocks, delays or changes a message, and it never reads a message body: the trace comes from signed headers (the sender's name, the signed `Mash-Via` of [ADR 0063](decisions/0063-in-reply-to-is-signed-and-a-sender-can-declare-a-relay-with-a-signed-mash-via-both-are-shown-only-when-they-verify.md)) and from the time each mail arrived and each send was made. There is no pattern matching and no operator-supplied rule. There is no flag; the lines go to serve's stderr.

What is recorded, per `mash serve` run:

- An inbound mail from a `verified` or `stale` sender is remembered by its sequence, the sender's name and the time it arrived (the last 256 within the window; never its text). If it carries a `Mash-Via` that verifies, serve logs `trace relay: mail #<seq> from <from> declares a relay of #<n> from <origin>` and counts it.
- A send that was published with a `via` logs `trace relay: sent to <to> declaring a relay of #<via> from <origin>` and is counted as a declared relay.
- A send to a third party within the window of another endpoint's mail logs `trace relay: sent to <to> <N> s after mail #<seq> from <from> (declared: yes|no)`, naming the most recent such mail, and is counted as a relay candidate (once per send). A third party is an endpoint other than the sender of that mail, other than this endpoint. A reply to the sender of the recent mail is not a candidate, and a send to oneself is ignored. `declared: yes` means the send carried a `via`.

The window is `relay_window` in `config.json` (or `MASH_RELAY_WINDOW`): a Go duration from `1s` to `1h`, default `60s`. An invalid value fails with a message naming the key.

`mash status` shows the counters:

```
relay trace: inbound with via 0, sends within 60s of another sender's mail 1, declared relays 1
```

Inside a session (the MCP `status` tool) the line is the running endpoint's. From a shell, `mash status` is another process and reads `relay-counters.json`, which serve writes (0600, atomically, at most once a second after a change, by a background worker that never holds up a receipt or a send; shutdown waits at most 2 s for the last write) in the endpoint's state directory next to its spool (`<state dir>/spool/<owner>/<name>/`). It then adds `(since <time>)`, the start of the run that wrote it: the counters are per `mash serve` run and restart at zero, and the shell cannot tell whether that run is still going, so a file from an earlier run reads as that run's. With no file, or an unreadable one, the line is `relay trace: none recorded`.

The limits. A candidate is time adjacency: the endpoint sent to someone soon after someone else's mail arrived. It does not show that the text relates, and a busy endpoint is a candidate on most sends. A relay that waits longer than the window, is retyped or paraphrased, or is sent through another endpoint, is not seen. `Mash-Via` is an honesty mechanism: a sender that does not declare is not caught by it. Use the lines to see how often it happens, then decide with the data whether any rule is worth making; none is made here.

## Headless and SDK sessions

mash's live receipts use persistent `claude -p` stream-json sessions. Other headless shapes behave differently.

- **Persistent stream-json works.** A `claude -p` process that stays open between turns, with the plugin's hooks, delivers mail as in an interactive session. The receipt is [2026-10-29-default-kind-live-v0.2.6](evidence/2026-10-29-default-kind-live-v0.2.6/RECEIPT.md) (Claude Code 2.1.312, hooks injected with `--settings`).
- **A one-shot run holds the name only for its turn.** `mash serve` exits when its MCP input stream ends (`internal/mcp/server.go`, `Run`), which ends the session's lease. Mail that arrives while the run is in its turn is shown by the hooks at its next tool call. Mail sent between runs waits in the inbox, and the next run's hooks show it once its serve has received it. This one-shot behaviour follows from the code and is not covered by a live receipt.
- **The development flag does nothing here, so the allowlist is required.** With `-p` or the Agent SDK, Claude Code ignores `--dangerously-load-development-channels` and the channel does not register ([Claude Code channels reference](https://code.claude.com/docs/en/channels-reference#test-during-the-research-preview)). Allowlist mash in managed settings as in [install.md](install.md#3-allow-the-channel-once-per-machine) for any channel wake; hooks deliver mail without a channel, but nothing wakes an idle session. A headless run that is already in a turn needs no wake.
- **An SDK host must load the plugin itself.** `claude -p` loads plugins you already installed, but an Agent SDK host takes plugins through the SDK's plugin option ([Load plugins in the Agent SDK](https://code.claude.com/docs/en/agent-sdk/plugins)). Mail needs the plugin's MCP server (`mash serve`) and both hooks, PostToolUse and UserPromptSubmit (`plugin/hooks/hooks.json`); a host that loads MCP servers or hooks its own way and omits either gets no delivery, and `status` reports `delivery:` accordingly. Also set `MASH_ENDPOINT`. mash has not been run under an SDK host.

## Minimum Claude Code version

`host.MinClaudeCode` (`internal/host/claude.go`) is the oldest Claude Code a live receipt passed on. Each live receipt in `docs/evidence/` names the Claude Code version it ran on and whether it passed. The constant is the oldest version that has a passing receipt for the current delivery design (hooks plus the channel wake). It is raised when a design change retires the receipts on the oldest version, so that every version at or above it still has a passing receipt behind it, and it is never lowered: lowering it would claim a receipt that does not exist. Update it, the README and install.md Requirements line together, in the pull request that adds the receipt. `status` compares the version the client sent in `initialize` (`clientInfo`) with it and warns below it; mash does not refuse older versions, and it does not guess other clients' version schemes.

## Live prompt size

A live prompt carries at most 32768 bytes of text (`endpoint.MaxPromptBytes`). The receiving endpoint refuses a longer one before it acknowledges it: the caller gets the error `413 prompt_too_large: a live prompt is at most 32768 bytes; send the longer text as mail`, the session is never told, and no request id is opened, so nobody answers a cut-off request. `mash prompt` checks the size first and prints the same text without sending. The session's notice shows the text whole, with control characters escaped; send anything longer as mail.

## Refused sends

`mash send` (and the `send` tool) fails at once with `refused by policy: you may not mail <address>` when the server refuses the publish because the sender's NATS user has no permission to publish to that recipient's mail subject. Nothing was stored, and retrying with the same credentials cannot help. Mash's default permission set lets an endpoint mail every endpoint of its owner, so this only happens to a sender issued a set that omits the recipient, for example a hand-edited allow list of mail subjects. The fix is on the server side: issue the sender a permission set that includes the recipient's mail subject (`agents.mail.<owner>.<group>.<instance>` of the recipient).

The message adds a second sentence because the server's error does not say why the publish was denied: if the recipient is an endpoint of the sender's owner, the credentials may predate the permissions mash issues now or the permissions were narrowed, and the admin should check them.

Mash learns of the refusal from the server's asynchronous permission-violation error, which names the subject, and matches it to the send in flight by subject. It reports a refusal only when exactly one send is waiting on that subject at that moment and no send on the subject has ended unsettled (timed out or cancelled) while others remained. After a send that ended unsettled, the next send on the subject first runs a flush barrier and a settle of at most 25 ms, so a violation delayed from the earlier send is dropped; sends on that subject wait for it. The settle narrows the window nats.go's asynchronous error handler leaves; it cannot close it. The refusal therefore ends with the send's key (`If you retry, use --id KEY (the send tool's id): it sends at most once within 2 minutes.`), which makes a retry after a false refusal harmless. With several in flight the violation cannot be attributed (it may belong to an earlier send, or to one sent before a permission change), so none of them is refused: each keeps waiting for its ack or deadline and, if it times out, the timeout error adds that an unattributed permission violation on the subject was seen. Without it a denied publish gets no reply and would only report a timeout after the full call timeout. If a server version words that error differently, mash cannot match it and the send fails as a timeout, as before.

## The name rule

A deployment can refuse addresses that mash's own syntax accepts, such as a reserved group, with a name-rule file. mash ships no default: with no file named, no rule applies and nothing changes. The file's format, the exact conditions under which it refuses start, and its compatibility rule are in [name-rule.md](name-rule.md); the deployment owns the content.

Name the file with `MASH_NAME_RULE_FILE` or the key `name_rule_file` in `config.json` (the environment wins; a relative path in `config.json` is taken from the config directory, a relative `MASH_NAME_RULE_FILE` from the current directory; either way mash resolves it to an absolute path at start and shows that path in `mash status` and in every error, so two endpoints that show the same path loaded the same file). `mash` and `mashctl` read it through the same configuration, once, at start: a change takes a restart. A path that is set but empty is an error, not "no rule", and so is a file that cannot be read or does not follow the format; the error names the file and the problem, and mash does not start.

To make sure a restart without the file cannot reopen what the rule closed, set `MASH_NAME_RULE_REQUIRED=true` (or `"name_rule_required": true`). mash then refuses to start unless a rule file was loaded. Only `true` and `false` are accepted.

`mash status` shows the rule in force on one line:

```text
name rule: /etc/mash/name-rule.json sha256:0123456789ab (3 rules)
name rule: none set
```

The digest is the first 12 hex digits of the SHA-256 of the file's bytes, so a check can confirm every endpoint loaded the same file. A refusal names the rule and carries its message, for example `mash: send: the recipient: name rule "no-scratch" refuses acme.scratch.peer: the group scratch is not for real work`. It is returned to whoever asked, at provision, start, send, reply, prompt and lookup, and always before the directory is read. A receiving endpoint applies the rule again to a prompt's asker, answering `403` with the same text and not showing the prompt, because a peer on an older client may not have checked. A relay declaration (`relayed from`) whose origin the rule refuses is not honoured: the mail is still delivered, shown without the declaration, and the serve log says why.

The tests named `TestConfigA...` (in `internal/endpoint`, `internal/admin`, `cmd/mashctl` and `cmd/mash`) pin that the old rule keys `agent` and `name` are refused and that a deployment that requires a rule and supplies none does not start. They run in CI permanently and must not be skipped or weakened.

## Fixed protocol constants

These timings are not configurable: peers and the server must agree on them, so one operator's value would break another's session or weaken a check.

| Name | Value | Where | Why fixed |
|---|---|---|---|
| Heartbeat interval | 30 s | `endpoint.HeartbeatInterval` (`internal/endpoint/service.go`) | Endpoints publish presence at this rate; the online window below assumes it. |
| Presence cache / retry gap | 30 s / 5 s | `endpoint.PresenceCacheTTL`, `endpoint.PresenceRetryGap` (`presence.go`) | How long the `who` tool inside a session reuses a probe's answers, and the shortest wait after a failed probe ([ADR-0066](decisions/0066-mash-serve-asks-who-is-online-when-someone-asks-instead-of-listening-to-every-heartbeat.md)). |
| Online window | 90 s (3 heartbeats) | `endpoint.OnlineWindow` (`directory.go`) | Only before any presence probe has run (an endpoint seen within it is online). Once a probe has run, online means answered the latest probe. |
| Lease TTL | 90 s | `store.LeaseTTL` (`internal/store/store.go`) | The lease bucket's expiry: how long a crashed holder blocks its name. It is set on the bucket, so every session shares it. |
| Lease refresh | 30 s | `endpoint.LeaseRefresh` (`endpoint.go`) | A third of the TTL, so two missed refreshes do not lose the lease. |
| Directory poll | 15 s, each wait jittered plus or minus 25 percent; first poll at a random offset | `endpoint.DirectoryPoll` (`directory.go`) | A running endpoint asks the directory stream for its state (one unfiltered stream info, a few hundred bytes) and re-reads the directory (a filtered stream info plus one direct get per record) only when the stream's creation time, message count, last sequence or last time changed. It needs no consumer, which endpoints may not create ([ADR 0065](decisions/0065-a-directory-poll-reloads-only-when-the-directory-stream-moved-and-is-jittered.md)). Idle cost is linear in the number of endpoints. |
| Directory forced reload | 20 min, due time jittered plus or minus 25 percent per endpoint | `endpoint.DirectoryFullReload` (`dirpoll.go`) | The longest an endpoint goes without a full reload even when the stream state looks unchanged, so a stale view cannot outlive it. A change is seen at the next poll, within about 15 s. |
| Limits lock | 30 s expiry, 20 s wait, 200 ms poll | `lockTTL`, `lockWait`, `lockPoll` (`internal/admin/lock.go`) | Admins serialise a stream's limit change; the expiry is compared on each admin's clock, so every admin must use the same one. |
| Live request signature skew | 30 s | `sap.LiveSkew` (`internal/sap/sender.go`) | How far a signed live request's time may differ from the receiver's clock; `mash status` warns at 10 s. |
| Signed message age | 5 min | `endpoint.MaxSignedAge` (`mail.go`) | A message signed longer than this before it was stored is labelled `[stale]`: the replay defence. |
| MCP ping | every 30 s, 10 s to answer | `mcpPingEvery`, `mcpPingWait` (`cmd/mash/serve.go`) | Serve's liveness check of its MCP client, recorded in the lease. |
| Channel grace and fallback | 500 ms, 3 s | `mcp.ChannelGrace`, `mcp.ChannelFallback` (`internal/mcp/server.go`) | Claude Code drops channel events sent before it registers the channel; these bound that wait. |

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| The session says it has no mash identity | `MASH_ENDPOINT` was not set when Claude Code started (and no single installed endpoint was found). |
| The session stops with `more than one endpoint is installed here: a, b` | No `MASH_ENDPOINT` was set and several creds files are installed. Name one in the environment when Claude Code starts. |
| The session says the address is already in use | Another live session holds the lease (it expires 90 s after that session dies). The session keeps retrying every 10 s and tells Claude when mail is on; no restart is needed. |
| After `/login` (or any restart inside one session) `mash status` shows the old serve | A new `mash serve` of the same session takes the lease from the earlier one at once, so mail is on in the new one; the earlier serve says it was superseded (pid of the new one) and exits when its client closes it or its parent changes. See [Same-session takeover](#same-session-takeover). |
| `--channels` says "not on the approved channels allowlist" | The managed settings allowlist is missing (see install.md, step 3). |
| Mail arrives labelled `[unknown-signer]` | The sender's key is not in the directory: it was deprovisioned, or it is not a mash endpoint. |
| `mash: endpoint X is not provisioned` | Run `mashctl endpoint create`, or check `MASH_ENDPOINT` for a typo. |
| `x509: certificate signed by unknown authority` (or another `x509:` error) on connect | The server's certificate is not trusted. Set `ca` (`MASH_CA`) to the PEM file of the CA that signed it. The files are read at connect time. |
| `remote error: tls: bad certificate` (or `certificate required`) on connect | The server requires a client certificate, or refused the one given. Set both `tls_cert` and `tls_key` (`MASH_TLS_CERT`, `MASH_TLS_KEY`) to a certificate the server's CA signed. |
| `status` says `tls: WARN` | `MASH_INSECURE_PLAINTEXT` is set and the server URL is plain text. Use `tls://` with the server's certificate and unset it. |
| `status` says `tls: FAILED` | The url is plain text and `MASH_INSECURE_PLAINTEXT` is not set: mash refused it before connecting, loopback included. Use `tls://` with the server's certificate (`ca` for a private CA). |
| `refused by policy: you may not mail X` on send | The sender's NATS permissions do not include X's mail subject (a hand-edited set, or credentials issued before the permissions changed). See "Refused sends". |
| `do not match endpoint` | The credentials file belongs to a different address than `MASH_ENDPOINT` names. |
| Peers look offline, or mail arrives labelled `[stale]` | Clock skew. Keep every machine's clock in sync (NTP, including laptops, containers and VMs after suspend): more than 30 s of skew (`sap.LiveSkew`) makes live requests and heartbeats fail and stored mail `[stale]`. `status` prints a `clock:` line and warns past 10 s. |
| Clients take 30-60 s to reconnect after a network blip | The server rate-limits new connections per source address. Clients retry with jitter; raise the limit for trusted sources if this matters. |
