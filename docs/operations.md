# Operating mash

mash needs a NATS server in operator mode with JetStream, and one account for mash. mash itself never mints NATS users and never holds an operator or account seed: you issue endpoint users with your own tooling (`nsc`, or any JWT issuer), using the permissions `mash admin permissions` prints, and mash creates the streams and directory entries behind them.

## Local setup with `mashctl init --local`

For one developer on one machine, `mashctl init --local` writes the whole NATS side in one command, so the manual `nsc` steps below are not needed. It needs no `config.json` and no running server:

```sh
mashctl init --local --owner alice [--dir D] [--port P] [--start]
```

It creates, in `D` (default `<config dir>/server`; mode 0700): `server.conf` (an operator-mode server listening on `127.0.0.1:P`, default 4222, with JetStream on file storage and the accounts preloaded into a full resolver), `operator.jwt`, and the credentials `mashadmin.creds` (full access within the MASH account, for `mashctl`) and `sys.creds` (system account), all 0600. The operator, system-account and MASH-account signing seeds go to the `mashctl` key store (`operator.nk`, `system.nk`, `account.nk`, 0600, in `<config dir>/keys` or `$MASH_KEYS_DIR`); they are never printed. The MASH account gets the limits [below](#limits) describe (2G of disk, 64 streams, 128 consumers). If none exists it also writes an open `policy.yaml` ([the policy file](policy-file.md), `default: allow`) and a `config.json` with `url`, `owner` and `admin_creds`; an existing `config.json` or `policy.yaml` is never touched, and for `config.json` the three settings to add are printed.

The server is loopback only and has no TLS: it is for development on this machine, not for remote endpoints. For those, follow the manual steps below with a TLS-terminated server.

Plain `nats://` to a non-loopback host is refused for admin and system connections (`mashctl` with the admin or system creds): they require TLS, and the connection fails when the server does not offer it. Local servers made by `init --local` are loopback-only and opt in explicitly: `init --local` writes `"insecure_loopback": true` into the `config.json` it creates (or prints it with the settings to add when `config.json` exists), and the same opt-in is `MASH_INSECURE_LOOPBACK=1`. It is the one minimal escape, a setting and not a flag, and may be renamed when the command-line settings are redesigned. It is honoured only when every host of the URL is `localhost` or a loopback address; with any other host the connection is refused. `init --local --start` connects only to the loopback server that run just generated and started (plain by construction), so it needs no opt-in; without `--start` nothing connects. `init --local` also records a copy of the operator JWT as `operator.jwt` in the key directory (0600, never replacing a file), which `policy apply` and `adopt` use to know which issuers to accept (see [Applying the policy](#applying-the-policy)). With `insecure_loopback` the client also dials only localhost or a loopback address, at connect and at every reconnect, and in both modes it ignores server addresses the server advertises. Endpoint connections are unchanged.

`init --local` refuses, and changes nothing, if `D` already holds any file it would create. Then:

```sh
nats-server -c ~/.config/mash/server/server.conf   # or: add --start to the command above
mashctl init                                        # the directory and lease buckets
mashctl provision <name> --user U --account A       # then add endpoints (or use `mashctl add`, see [mashctl.md](mashctl.md))
```

With `--start`, `mashctl` looks for `nats-server` on `PATH` (if it is missing it says so, and the files stay), starts it detached with its output in `D/server.log` and its pid in `D/server.pid`, waits up to 15 seconds for a connection with `mashadmin.creds`, and creates the buckets. To stop the server: `kill $(cat D/server.pid)`.

## Adopting an existing server

`mashctl init --local` stands up a server. `mashctl adopt` is its counterpart for a NATS server you already run in operator mode with a full resolver and JetStream. A URL alone proves nothing about a server: not that it is in operator mode, nor that you hold authority over its account, system account and resolver, and `mashctl` cannot stand up or reconfigure a remote server from a client URL. So adopt requires the authority to exist already, checks it, and refuses when a prerequisite is absent.

```sh
mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE \
              [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]
```

Prerequisites, all of which you supply:

- `--operator-key FILE`: the operator **signing** seed (starts with `SO`). `mashctl policy apply` signs the account JWT with it.
- `--account-key FILE`: the **identity** seed of the mash account (starts with `SA`; its public key is the account JWT's subject).
- `--sys-creds FILE`: creds of a user of the system account.
- `--server URL`, and the TLS settings of the loaded configuration (`tls_ca`, `tls_cert`, `tls_key`), as every other verb uses them. `--account NAME` is the account's name in its JWT (default: the owner), and `--owner O` defaults to the configured owner.

Seeds are read from files only, never from arguments or environment values, the files must be mode 0600, and a seed is never printed. Without `--operator-key`, `--account-key` or `--sys-creds`, adopt exits 2 and says which is missing and why.

Adopt first runs read-only checks, in this order, and writes nothing until all pass: (1) the files parse and have the right seed prefixes; (2) the server refuses a connection without credentials (a server that accepts one, or whose system account answers nothing, is refused as not looking like an operator-mode server with a full resolver), the system creds connect, and the resolver has a JWT for the account key's public key whose subject is that key, whose name is `NAME` and which is not expired; (3) the account has JetStream file storage (disk limit other than 0), and its disk, stream and consumer limits are shown, with a warning when they are below what one endpoint needs (3 streams, 1 consumer, about 36 MiB; see [Account limits](#account-limits)); (4) the system account answers the `$SYS.REQ.SERVER.PING` and the claims lookup, which is reported as standalone or as a cluster; (5) the account JWT carries a valid signature by its issuer, and the issuer is the operator key you gave; any other issuer is refused (exit 1, nothing written), naming the issuer and the accepted keys. The system and admin connections require TLS, or the `insecure_loopback` setting for a loopback URL (see [Local setup](#local-setup-with-mashctl-init---local)). The permission to push claims updates is not tested, because that would change the server: `mashctl policy apply` is the first to use it. The check also refuses a key store that already holds a different operator or account key, and a `sys.creds` that already holds other creds.

Then it writes, or with `--dry-run` prints exactly what it would write: the two keys into the key store (`operator.nk`, `account.nk`; 0600, created without replacing anything, fsynced; the same key already stored is fine, a different one is refused); the system creds as `<config dir>/server/sys.creds` (0600, where `policy apply` looks for them); with `--admin-creds FILE`, that path as `admin_creds` in `config.json` (the file is checked to connect and not copied), or with `--mint-admin` a `mashadmin` user of the account, signed by the account identity key with full access as `init --local` makes it, as `<config dir>/server/mashadmin.creds` (0600; one already there that is valid for this account and connects is kept, not replaced); `config.json` (`url`, `owner`, `admin_creds`, and `tls_ca`, `tls_cert`, `tls_key` when the loaded configuration sets them) only if there is none (otherwise the settings are printed); and the open `policy.yaml` only if there is none. Running adopt again with the same inputs changes nothing.

Adopt connect-tests whatever admin creds it ends with before it prints "All checks passed", and the summary says which it tested (`admin creds ok: connected (given)`, `(minted)` or `(kept)`). A `--mint-admin` user is made in memory and connected with in the read-only phase, so `--dry-run` reports a failure too and a failure writes nothing. An existing `mashadmin.creds` that parses but cannot connect is refused, not kept: move it aside to mint a new one. On an operator with strict signing key usage (`strict_signing_key_usage: true`), the server accepts only users signed by an account signing key, and the account identity key cannot sign users, so a minted `mashadmin` is rejected and adopt refuses with that explanation and the server's error text. mashctl does not fall back to one of its zone keys: they are scoped keys that impose a permission template, which is not admin access. The way out is `--admin-creds FILE` with a user signed by one of the account's signing keys.

The `mashadmin` creds that `--mint-admin` writes carry no permission limits on the account (when the server accepts them: see the strict-signing note above): whoever holds `mashadmin.creds` can publish and subscribe on every subject of the account, read every inbox and change the directory. Keep the file at 0600 on the machine that runs `mashctl`, and give endpoints their own creds, never these.

Adopt changes nothing on the server: it pushes no account update, creates no NATS user there (a minted `mashadmin` exists only as a local creds file, usable because the account key signed it) and no buckets. The cutover of a live account, meaning pushing zone keys and revoking the credentials issued before, is a separate step the operator runs: `mashctl init` creates the buckets, `mashctl add` mints endpoints, and `mashctl policy apply` pushes the policy. Until then, credentials minted by other tools keep working.

## Which server does mashctl change?

`mashctl` has no flag that names its server. It takes the server, the owner and the admin credentials from `config.json` in the config directory, and the endpoint's own `MASH_*` variables (`MASH_URL`, `MASH_OWNER`, `MASH_ADMIN_CREDS`, `MASH_TLS_CA`, ...) win over the file. Run from a shell inside a mash agent session, those variables are the session's, so `mashctl init`, `list` and the other verbs act on the session's own server, not the scratch one you may have meant.

Every verb that connects to a server or changes one (`init`, `limits`, `provision`, `set-meta`, `set-zone`, `suspend`, `resume`, `suspended`, `deprovision`, `list`, `add`, `retire`, `label --sync`, `policy apply`, `diff`, `drift` and `repair`) first prints, to stderr, what it is about to act on and where each setting came from:

```
mashctl: target server tls://host:4222 (from MASH_URL), owner acme (from /home/me/.config/mash/config.json), admin creds /home/me/.config/mash/admin.creds (from MASH_ADMIN_CREDS)
```

The policy verbs add the system credentials' path when it is known. Credentials files are named, never printed; the user information of a URL is shown as `***@`. When an environment variable overrides a different value in `config.json` (the url, owner, admin creds or `tls_ca`), a warning names both values, and when `MASH_NAME` or `MASH_LAUNCH_TOKEN` is set and the url came from `MASH_URL`, a second warning says the shell looks like it is inside a mash session. These lines inform; they never refuse, and the TLS and issuer rules are unchanged. The offline verbs (`permissions`, `limits --show`, `policy check`, `test`, `explain`, `show`, `init --local`, `label` without `--sync`) print nothing of this.

A flag or context to choose the target explicitly (`--server`, `--config-dir`) is deferred to the CLI redesign; the CLI is frozen until then. Until then, to use `config.json` instead of the session's variables run `env -u MASH_URL -u MASH_OWNER -u MASH_ADMIN_CREDS -u MASH_TLS_CA mashctl ...`, or run mashctl outside the session.

## Where the key store lives

The account seed in the key store (`account.nk`, with `operator.nk` and the zone keys) and `mashadmin.creds` are account-wide, unrestricted authority: whoever holds them can mint any user, change any permission and revoke any key. Their modes (0600 files in a 0700 directory) stop other users, but not other processes of the same UID, and agent sessions on the operator's host run as that UID. So run `mashctl` and keep its key store (`<config dir>/keys` or `$MASH_KEYS_DIR`) under a separate UID, or on a mount, that agent sessions cannot read, and give each host's agents only their own creds file. Never hand an agent `mashadmin.creds`, `sys.creds` or the key store.

What `mashctl` prints and reads follows from not trusting the server:

- Strings that came from a server or a directory record (server names, ack messages, scoped signing key roles, record fields in `list`, `suspended` and `policy drift`) are printed escaped: every control character (ESC, CR, newline, and the C1 range) and invalid UTF-8 shows as `\xNN` or `\uNNNN`, and a value is cut at 256 characters with `...`. Ordinary text, accented letters included, is unchanged.
- A reply from the system account is bounded: a claims reply over the account JWT limit (1 MiB) is refused with `reply of N bytes over the limit`, more than 1024 distinct server names ends the ping with `more than 1024 servers answered: refusing`, and a collection stops after 4096 replies.
- A server URL with credentials in it (`nats://user:pass@host`) is not supported. `adopt` refuses it, so it is never written to `config.json`; give the credentials as files (`--sys-creds`, `--admin-creds`). A URL that reaches `mashctl` through `config.json` or `MASH_URL` is printed with the user information replaced by `***@`.
- `config.json` is refused when it is writable by every user (`chmod go-w` it); its owner is not checked, since a config map is often a symlink owned by another user. A relative `admin_creds`, `tls_ca`, `tls_cert` or `tls_key` in `config.json` is resolved against the config directory, not the working directory.

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
export MASH_URL=tls://nats.example.com:4222 MASH_OWNER=alice MASH_ADMIN_CREDS=~/.config/mash/admin.creds
mash admin init
```

Every bucket carries a size cap, because hosted NATS accounts (Synadia Cloud's free tier, for one) refuse a stream with no max bytes. By default the directory bucket gets 16 MiB and the lease bucket 4 MiB; both hold tiny records, so that is generous (see [Limits](#limits) to change them). Running `mash admin init` again is safe: a bucket made by an older mash with no cap is bounded in place with a stream update, keeping every key; a bucket whose cap is smaller than the configured one is raised to it; a bucket with a larger cap keeps it. Init never lowers a cap and never purges anything.

## Adding an endpoint

An endpoint is one named Claude Code session. Names are lower-case letters, digits and single hyphens, up to 48 characters, unique per NATS account, not per owner: two owners who share an account cannot both have an endpoint called `review`, and every endpoint in the account can read the whole directory, other owners' records included (see [ADR 0006](decisions/0006-one-shared-nats-account-one-owner-per-deployment.md), one owner per deployment).

```sh
(
set -eu
name=design
perms=$(mash admin permissions "$name" --nsc)
[ -n "$perms" ] || { echo "no permissions printed; refusing to add the user" >&2; exit 1; }
set --
while IFS= read -r arg; do set -- "$@" "$arg"; done <<EOF
$perms
EOF
nsc add user --account MASH "$name" "$@"
user=$(nsc describe user --account MASH --name "$name" --json | jq -r .sub)
account=$(nsc describe account MASH --json | jq -r .sub)
mash admin provision "$name" --user "$user" --account "$account" --note "design session on laptop"
nsc generate creds --account "$ACCOUNT" --name "$name" > ~/.config/mash/$name.creds
chmod 600 ~/.config/mash/$name.creds
)
```

This runs in a subshell under `set -eu`, so any failing step stops it, and an empty permission list never reaches `nsc add user` (a user created with no `--allow-*` flags would carry none of the restrictions above). It is plain POSIX `sh`, so it also works pasted into zsh or macOS bash 3.2.

Deliver the credentials file to the machine that runs the session, and nowhere else.

`mash admin permissions <name>` prints the permission set as JSON; `--nsc` prints the same set as `nsc add user` flags, one argument per line. The set lets an endpoint:

- send mail to any endpoint of the owner, and send live SAP prompts and status probes to them. Mail has two publish lines, `agents.mail.*.<owner>.*` (an endpoint with the legacy subject) and `agents.mail.*.<owner>.*.*` (an endpoint in a zone, see [Zones](#zones-the-mail-subject-carries-a-zone-token)); prompt and status have one each;
- publish its own heartbeat and answer SAP discovery (it does not listen to other endpoints' heartbeats; `who` asks, see above);
- read and acknowledge its own inbox, and read its own history;
- read (never write) the directory;
- hold its own lease and nothing else.

It cannot read another endpoint's inbox, history or replies (each endpoint receives only under its own `_INBOX.<name>` prefix), take another endpoint's name, write the directory, create or delete consumers, or delete or purge any stream. It holds no response permission, because NATS grants that for any reply subject an endpoint receives; replies may only go to `_INBOX.>` subjects. The integration tests assert each of these denials against a real server.

### Directory metadata

An endpoint's directory record can carry optional fields: `--role` (what it is for), `--host` (the machine it is meant to run on) and up to 8 free-form labels, `--label key=value` (repeatable), for grouping or tagging endpoints, for example `team=acme` or `project=billing`. They are the endpoint's identity on record, set by the admin; they are not live state. A role, host or label value is up to 64 printable characters with no tab or newline. A label key is a lowercase letter followed by up to 31 of `a-z`, `0-9`, `_`, `.` and `-`.

Two more fields say what the endpoint does: `--describe TEXT`, free text of up to 256 printable characters (no tab or newline), and up to 8 capability words, `--cap C` (repeatable), each a lowercase letter followed by up to 31 of `a-z`, `0-9`, `_`, `.` and `-`, with no repeats. Capabilities are stored sorted. A label holds one value per key and there are only 8 of them; capabilities are a list of words to find an endpoint by, and the description is the longer sentence ([ADR-0050](decisions/0050-the-directory-record-says-what-an-endpoint-does-with-a-description-and-capabilities.md)).

```sh
mash admin provision "$name" --user "$user" --account "$account" --role reviewer --host laptop --label team=acme --label tier=1 --describe "Keeps the NATS accounts current" --cap nats-accounts --cap review
mash admin set-meta "$name" --role builder --host - --label tier=- --label env=prod   # change role, clear host, clear one label, set another; everything else stays
mash admin set-meta "$name" --describe "New text" --cap deploy --uncap review   # replace the description, add one capability, remove another
mash admin set-meta "$name" --describe - --cap -                              # clear the description and every capability
```

`set-meta` needs at least one flag, changes only the fields given, and writes with a compare-and-set, re-reading and retrying if another write lands first. A value of `-` clears a field, and `--label k=-` clears that one label while the other labels stay. `--describe -` clears the description. `--cap C` adds a capability, `--uncap C` removes one, and `--cap -` clears them all (added in the same command afterwards). The 8-label and 8-capability caps are checked after the merge. The confirmation line also shows the description and capabilities when set. Fields a newer mash wrote to the record are kept. Older mash versions ignore the labels, so mixed versions are safe. `mash admin list` is unchanged.

`mash who` prints the same lines as before, so tools that parse it keep working. `mash who --long` (and the `who` tool a session's model sees) appends `role R` and `host H` for the fields that are set, then `desc TEXT` and `caps a,b,c` columns when set, then one `label.K=V` column per label sorted by key, each as its own tab-separated column. For the endpoint you run it as, it also appends the live lease's `lease host` and `lease version`, and the note `lease host X differs from directory host Y` when both hosts are set and differ. Other endpoints' leases are not readable with an endpoint's permissions, so their live host is not shown. The lease is the live view; the directory metadata is only the record of intent.

### Zones: the mail subject carries a zone token

An endpoint can be put in a zone. A zoned endpoint's mail subject is `agents.mail.cc.<owner>.<zone>.<name>` (6 tokens) instead of the legacy `agents.mail.cc.<owner>.<name>`. A zone is one token of 1 to 32 lower-case letters, digits and hyphens (`default` is the conventional name, `sap.DefaultZone`). An endpoint with no zone on record keeps the legacy subject and works exactly as before; it is not in the `default` zone. Prompt, status and heartbeat subjects do not change ([ADR-0054](decisions/0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md)).

```sh
mash admin provision "$name" --user "$user" --account "$account" --zone default   # a new endpoint in a zone
mash admin set-zone "$name" default                                               # move an existing endpoint into a zone
```

A zoned endpoint's inbox stream takes the legacy subject (`agents.mail.*.<owner>.<name>`) and the exact subject of its own zone (`agents.mail.*.<owner>.<zone>.<name>`), never a wildcard zone: a sender gated into one zone cannot reach an endpoint in another by addressing it with the wrong zone. The legacy subject stays during migration, so a sender holding the legacy wildcard publish line (not a policy-compiled user, which lacks it) still reaches the endpoint. `set-zone` appends the exact zone subject to the existing stream with an update that changes nothing else and purges nothing, then rewrites the directory record's `zone` and `mail_subject` against the revision it read, keeping every other field. It is idempotent. A zone of `-` is refused, and so is moving an endpoint from one zone to another: in this version a zoned endpoint stays in its zone. `mash admin list` and `mash who --long` show the zone (`zone=Z` and `zone Z`); `who --json` has a `zone` key only for a zoned endpoint.

Roll it out in this order. First upgrade the mash binaries: they read both subjects. Then re-issue each user's permissions (`mash admin permissions <name>`) so they include the zoned publish line; a user minted before this change can only publish the 5-token mail subject, so it cannot mail a zoned endpoint. Then run `set-zone` or `provision --zone` per endpoint, in any order: senders resolve the recipient's subject from the directory, so they follow the record. A user minted with per-recipient lines (the policy compiler's form) keeps its legacy line per recipient and cannot mail a zoned endpoint until its lines are reissued.

### Applying the policy

`mashctl policy apply` compiles the policy file ([the policy file](policy-file.md)) into one scoped signing key per zone and pushes it to the MASH account. It runs `check` first and refuses a file that does not check or whose own `tests` fail (`apply` and `apply --dry-run` print the failures as `policy test` does and exit 1 before touching keys or the server; `diff` prints a `policy tests FAIL` block at the top and still shows the diff), and refuses a file that does not fit the account JWT (1 MiB) or a user JWT (3,750 bytes); a refusal changes nothing.

```sh
mashctl policy diff  [--policy FILE] [--sys-creds FILE] [--replace-zone-key ZONE]... [--remove-zone ZONE]... [--keep-zone ZONE]... [--servers N]   # what apply would change; changes nothing
mashctl policy apply --dry-run [--policy FILE] [--sys-creds FILE] [--replace-zone-key ZONE]... [--remove-zone ZONE]... [--keep-zone ZONE]... [--servers N]   # the same report, under apply's name
mashctl policy apply [--policy FILE] [--sys-creds FILE] [--replace-zone-key ZONE]... [--remove-zone ZONE]... [--keep-zone ZONE]... [--servers N]
```

The server URL and owner come from the configuration, the signing keys from the `mashctl` key store, and the push goes through the system account: `--sys-creds FILE`, else `$MASH_SYS_CREDS`, else the `sys.creds` that `mashctl init --local` wrote next to `admin_creds` (or in `<config dir>/server`). Before it uses the account JWT, apply requires it to carry a valid signature by its issuer, the subject to be the account key in the key store, and the issuer to be the operator key (`operator.nk`) or, when `<key dir>/operator.jwt` exists, the identity or a signing key of that operator JWT (which must itself carry a valid signature and list the operator key). Otherwise `apply`, `diff`, `--dry-run`, `repair` and `drift` refuse, naming the issuer and the accepted keys, and change and create nothing. The same check runs on the read-back after a push. The operator key (`operator.nk`) signs the account JWT, so apply works where `mashctl init --local` stood the server up; with an operator you manage elsewhere it stops and says the key is missing.

`diff` and `--dry-run` print, per zone, whether it is new, changed or unchanged and, for a changed or new zone, the permission lines added (`+`) and removed (`-`) under `pub allow`, `pub deny`, `sub allow` and `sub deny`; the account JWT's size now and after against the limit; the zones the server has that the file lacks; and how many endpoints carry exception tags. A dry run creates no key and pushes nothing.

`apply` creates the signing key of each new zone in the key store, looks up the account JWT, replaces or adds only the scoped signing keys of the file's zones (limits, JetStream, exports, tags, name and every other signing key stay as they are), signs it and pushes it. Before the push it pings the system account (`$SYS.REQ.SERVER.PING`) for the set of servers; every server that replied must acknowledge with code 200 within 2 seconds, and a server that is silent or answers another code makes the run `PARTIAL`, naming it. The set is trusted only when it is complete. A server that sees no peers proves nothing (an isolated cluster member reports none), so completeness needs one of two things. Either you tell apply how many servers the deployment has, with `--servers N` (or `MASH_SERVERS=N`; a whole number of 1 or more, anything else is a usage error, exit 2): the set is complete when at least N distinct servers replied (if the routes report more than N servers, that larger number wins) and no reply reports gateways. Or the server is standalone: its ping reply names no cluster, and it has no routes and no gateways, so one reply is complete. A reply that names a cluster, with no `--servers`, is `UNCONFIRMED` even when it reports zero routes ("this server is part of cluster X and the number of servers cannot be verified from here; pass --servers N"). If it is not complete after the first replies, the ping keeps listening for up to 3 seconds in total. If it is still incomplete (fewer servers replied than the cluster reports or than `--servers` says, a clustered server with no `--servers`, no server replied, or gateways are present, because super-cluster membership cannot be verified from here) the run ends `UNCONFIRMED` and exits 1: the update was pushed and the servers that replied acknowledged it, but servers not heard from may still hold the old claims; run apply again, which is idempotent. Apply then reads the JWT back and compares the signing keys and templates with what it sent. Run twice, the second apply changes nothing, but it does not trust one resolver's copy: it sends the unchanged JWT (the same token, not re-signed) again, and every known server must acknowledge it. It says `already up to date` only when the server set is complete and every server in it acknowledged; a server that is silent or refuses makes the run `PARTIAL`, an incomplete set makes it `UNCONFIRMED`, and both exit 1. That is also how a retry repairs a partial push: a server that missed the update receives it on the next apply. (The resolver offers no way to ask one server for its own copy of the account JWT, so confirmation is by acknowledgement of the push.) `diff` and `--dry-run` push nothing; they build from the newest copy any server holds, as apply does. `diff` and `--dry-run` exit 1 when they refuse (a conflict, an unknown signing key, a zone outside the policy nobody acknowledged, a replaced zone key, an issuer that does not verify, servers that cannot be told apart) and 0 when they only report differences or nothing. Servers disconnect and reconnect the users of a changed zone with the same credentials; a new zone key changes no user. When a zone key is new or a template changes and endpoints carry exception tags, apply says that user JWTs carrying changed exception tags must be re-issued with `mashctl reissue`.

Applies are serialized by an exclusive advisory lock on `<key dir>/apply.lock` (0600), taken before the account JWT is read and held until the read-back is done; a second apply waits a few seconds and then stops with a clear error. Just before pushing, apply reads the account JWT again and, if it changed since the first read, computes its update again once; a second change is an error that asks you to run it again. The lock covers appliers that share one key directory (the key holder is the authority). Two hosts that hold copies of the keys are not coordinated: do not apply from both at once. On platforms without `flock` the lock is a no-op and only the re-read protects you.

The base of an update is always the newest copy any server holds. Apply (and `mashctl revoke`, `retire` and `deprovision`, which write the same JWT) asks every known server for the account JWT, the count coming from the ping and `--servers`/`MASH_SERVERS` (when the count is unknown it waits until no new reply arrives for a short while). Every reply must pass the issuer check; one that does not is refused, not skipped. The copy with the highest issued-at (`iat`) is the base, never "the first reply". If servers hold older copies (a server that missed an earlier push, for example one made before a `--remove-zone`), apply says `warning: N of M servers hold an older copy (iat ...); building from the newest`, and the push brings every server to the newest, so a signer removed earlier does not come back from a lagging server. If two different copies share the newest `iat`, which one is current cannot be told: apply refuses (exit 1, `diff` and `--dry-run` too) and names how many distinct copies there are; make the servers agree, then run it again. If no server answers, the run fails as before. After the push, the read-back asks every server and any answering server whose copy is not the JWT that was pushed makes the run `PARTIAL` (a server that did not answer the lookup, or an incomplete server set, is not a match either).

Just before pushing, apply asks every server again and compares the newest copy with the base it built from; if it changed (a different `iat` or content), apply computes its update again once, and a second change is an error that asks you to run it again.

A limit that cannot be closed from here: the resolver offers no conditional update (no compare-and-swap on the account JWT), so a writer outside `mashctl`, such as `nsc` or another tool editing the same account, that writes between apply's final re-check and its push is still overwritten: the last writer wins at the resolver. `mashctl` serializes its own applies and re-checks right before pushing, which narrows that window but cannot close it. Only one writer should hold the operator key and use `nsc` or other tools on this account while an apply runs.

If a zone's key file is lost or replaced, apply does not quietly add the new key beside the old one: the server's old scoped signer would keep its permissions, and credentials signed by it would bypass a stricter policy. Instead it refuses, before creating anything, exits 1 (`diff` refuses the same way and exits 1), and names the zone and both public keys. Either restore the original zone key file, or run apply with `--replace-zone-key ZONE` (repeatable): the old signer under that role is removed and the new key's scope installed in the same account JWT. Apply prints, for each replaced zone, the public key being dropped and its role. Credentials signed by the old key then stop working, whether mashctl or another tool issued them, and their users must be re-issued with `mashctl add`. Only a scoped signer whose role is exactly the name of a zone of the file can be replaced this way (or removed with `--remove-zone` for a zone the file lacks); a plain signing key, or a scoped key whose role is not a zone of the file or differs in case or spelling, is refused as a signing key mashctl did not create. Unknown keys are refused when they would be carried; the explicit flags drop the named zone's key.

A zone that has a scoped signing key on the server but is not in the file would keep its key and its old permissions active: users signed by that key keep mailing outside the new policy. Apply therefore refuses (exit 1, before creating any key or pushing; `diff` prints the same as `REFUSED unless --remove-zone ZONE or --keep-zone ZONE` and exits 1, as does `apply --dry-run`) while such a zone exists and you have not said what to do with it, per zone, with two repeatable flags. `--remove-zone ZONE` removes that scoped signer from the account JWT in this apply: users signed by that key stop working and must be re-issued under a zone of the file with `mashctl add`; the local zone key file is not deleted. `--keep-zone ZONE` says the key is deliberately kept active outside the policy: apply proceeds, and its output still carries the warning `zone ZONE is outside the policy and its key stays active`. Apply adds, removes or replaces only mash's own zone keys. Its known keys are the account identity key and every zone key file in the key store. Any other signing key in the fetched JWT, scoped or plain, is unknown: `apply`, `repair` and `--dry-run` refuse (exit 1, before creating a key or pushing), naming each by public key and role, and saying that mashctl signs the account JWT with the operator key and so will not carry keys it did not create; `diff` refuses (exit 1) and `drift` reports each as a finding (exit 1). Remove such a key with the tool that added it before apply can run. `--keep-zone` and `--remove-zone` apply to a zone whose key is in the key store. A name given to either flag that is in the file, or that is not on the server, or that is given to both flags, is an error. `already up to date` is never printed while a zone outside the policy is unacknowledged.
If a zone's key file is lost or replaced, apply does not quietly add the new key beside the old one: the server's old scoped signer would keep its permissions, and credentials signed by it would bypass a stricter policy. Instead it refuses, before creating anything, exits 1 (`diff` refuses the same way and exits 1), and names the zone and both public keys. Either restore the original zone key file, or run apply with `--replace-zone-key ZONE` (repeatable): the old signer under that role is removed and the new key's scope installed in the same account JWT. Credentials signed by the old key then stop working, and their users must be re-issued with `mashctl reissue`.

A zone that has a scoped signing key on the server but is not in the file would keep its key and its old permissions active: users signed by that key keep mailing outside the new policy. Apply therefore refuses (exit 1, before creating any key or pushing; `diff` prints the same as `REFUSED unless --remove-zone ZONE or --keep-zone ZONE` and exits 1, as does `apply --dry-run`) while such a zone exists and you have not said what to do with it, per zone, with two repeatable flags. `--remove-zone ZONE` removes that scoped signer from the account JWT in this apply: users signed by that key stop working and must be re-issued with `mashctl reissue`; the local zone key file is not deleted. `--keep-zone ZONE` says the key is deliberately kept active outside the policy: apply proceeds, and its output still carries the warning `zone ZONE is outside the policy and its key stays active`. A scoped signer that mash did not create (a role that is not in the file) is treated the same way, named by its role. A name given to either flag that is in the file, or that is not on the server, or that is given to both flags, is an error. `already up to date` is never printed while a zone outside the policy is unacknowledged.

A write needs an answer from every known server. `apply` (without `--dry-run`) and `revoke` build the new account JWT from the newest copy any server holds, and a server that does not answer may hold a newer one. They therefore refuse, exit 1, before creating a key or pushing anything, when fewer servers answered the account lookup than the ping, the cluster routes or `--servers` say exist (`only M of N servers answered the account lookup`), or when the server set cannot be confirmed complete (for a clustered server, pass `--servers N`). A server that is down blocks writes until it is back, or until `--servers` is corrected. `diff`, `apply --dry-run` and `drift` do not refuse: they proceed and print the same sentence as a warning that the comparison may be stale. The claims lookup is a fan-out request whose replies carry no server id, so mashctl can count the replies but cannot tell which server sent which: the count is the best proxy the resolver offers, and a server that answers more than once could stand in for one that is silent. Treat the write gate as a strong guard, not a proof, and keep every server of the deployment reachable while you write.

Apply is not a transaction across NATS claims, JetStream resources and local files. If the push is not acknowledged by every server, or the read-back does not match, it prints `PARTIAL` with what was done (for example, zone keys created locally), what was not confirmed, and the command to retry, and exits 1. `mashctl policy apply` is idempotent: running it again pushes what still differs.

#### What apply does not do

Apply, `diff` and `--dry-run` change and compare the account claims only, and `already up to date` means exactly that: every server holds the zone keys of the policy. After a success, a diff or a dry run, when the policy has endpoints, they print a short `Not covered by apply` block with the same three points. First, user credentials issued before an apply keep the permissions of the key they were signed with and the exception tags they carry: an `allow` or `deny` tag change takes effect only when that endpoint's user JWT is re-issued with `mashctl reissue`, and apply cannot see issued tags (the block names up to five endpoints that carry exception tags). Re-issue is `mashctl reissue`'s job, not apply's. Second, credentials minted by other tools (for example `nsc`) under the account key keep their own permissions until they are revoked: no rule is enforced on them. Revocation is not something mashctl does; the cutover is a separate step; ADR 0052's cutover note already says that no rule is enforced before the old credentials are revoked. Third, apply compares the account claims and nothing else: the key store, the directory records and the creds files on disk are compared with the file by `mashctl policy drift`, and its repairs are `mashctl policy repair` (below; additive, except that it clears labels the file does not name on declared endpoints). Neither sees credentials minted by other tools.

### Checking and repairing drift

```sh
mashctl policy drift  [--policy FILE] [--sys-creds FILE] [--creds-dir DIR] [--json]
mashctl policy repair [--dry-run] [--policy FILE] [--sys-creds FILE] [--servers N] [--keep-zone ZONE]...
```

The policy file is the source of truth. `drift` compares it with four states and reports each one separately, as a block that is `ok` or a list of findings, and fixes nothing. It exits 0 only when all four match and 1 when any differs or cannot be read (`unknown`). `--json` prints the same as one stable object (`drift`, `policy`, then `account`, `keys`, `directory` and `issued`, each with `status` and `findings`, plus `not_covered`).

- **account**: the file's compiled zone keys and templates against the scoped signing keys in the account JWT on the resolver, with the code `policy diff` uses: a zone the server lacks, a template that differs (with the lines added and removed), a server key that is not the key store's, a zone on the server that the file lacks. It reads one resolver's copy; apply is what makes every server acknowledge it.
- **keys**: the zone key files in the key store against the public keys of the account JWT's scoped signers: a missing key file, a key file for a zone that is neither in the file nor in the account JWT (a leftover, for example after `--remove-zone`, which never deletes the file), a key file whose public key is not the signer's.
- **directory**: the endpoint records against the file's endpoints: a record missing, a record the file does not declare, a record with no zone where the file says one, a record in another zone, a label that differs per key (in either direction), and a mail subject that does not match the record's zone.
- **issued**: the user creds files found in `--creds-dir`, else where `mashctl add` writes them (`<config dir>/NAME.creds`), with the compare `mashctl reissue --changed` uses: the tags to add or remove, a signer that is not the zone key, another account, another name. A stale endpoint is named with what differs. An endpoint with no creds file here is a note, not drift.

The same fact can show in more than one block: a deleted zone key file is a `keys` finding, apply would refuse for it (`account`), and the creds of its endpoints cannot be checked (`issued`). Drift prints a `Not covered by drift` block: credentials minted by other tools under the account key are not seen, the account block compares one server's copy with the file (and every server's copy with each other), and only creds files on this machine are compared.

`drift` asks every server for the account JWT, not just the first one that answers. The number of servers it expects is the larger of what the system account ping found and `MASH_SERVERS` (drift has no flag for it). The `account` block then reports `servers-differ: servers differ: N of M servers answered; K distinct account JWTs (iat T1 sha H1 on a server(s), iat T2 sha H2 on b server(s))` when servers hold different JWTs, as after a split or a partition; `servers-silent: only N of M servers answered` when fewer than the known number answered (it cannot say which: a server may be down or partitioned); and `servers-unverified` when the server set cannot be established, for a cluster member without `MASH_SERVERS`, with the same reason apply gives. Only when every expected server answered with the same JWT, and it matches the file, is the block `ok`. Fix a split by running `mashctl policy apply` again: it sends the claims to every server, and says `already up to date` only when every server acknowledged the send and every server answers with that JWT.

`repair` is additive EXCEPT that it clears every label the file does not name on an endpoint the file declares (the file is the source of truth for a declared endpoint). It makes its repairs in this order and prints what each step did or `nothing to do`; the same sentence opens every run. It is idempotent, and it never revokes a credential, deletes a key file, a record or a creds file, moves an endpoint between zones, or touches an endpoint the file does not declare.

`--dry-run` performs no write of any kind: no key file is created, the account step is `mashctl policy apply --dry-run` (nothing is pushed), and the directory step makes no `set-zone` or `set-meta` call. It prints what a real run would do, and a real run prints the same change lines as it acts. Every change to a directory record is one line, in a fixed form, and no secret is ever in one: `set zone NAME ZONE`, `set label NAME key=value (was oldvalue)` (`was unset` when the record lacked it) and `clear label NAME key=oldvalue`. Read the clear lines before a real run: a label set on a declared endpoint by hand, or by another tool, and not in the file is removed.

1. Zone key files: a missing zone key file is created. This is apply's own step, so it happens inside step 2 and only after apply's refusals have passed: a key made first would stand in the way of restoring the original file of a zone the server already holds a key for.
2. The account: the same code path as `mashctl policy apply` with the same flags (`--servers`, `--keep-zone`), so every refusal, the lock and the acknowledgement rules apply unchanged, and the push is re-sent to every server (it is how a partial earlier push is repaired). If apply refuses or does not complete, repair stops there, exits 1 and runs no later step. Repair does not accept `--remove-zone` or `--replace-zone-key` (usage error, exit 2): either one makes users signed by the old key stop working, and repair never revokes. Run `mashctl policy apply` with that flag explicitly. Repair reads, checks and compiles the policy file once and uses that one snapshot for every step, so a file edited while repair runs is not seen until the next run.
3. Directory records of endpoints that are in both the file and the directory: a record with no zone gets the file's zone with `set-zone` (a record in another zone is never moved), and the record's labels are made equal to the file's with `set-meta`: a label that differs is set, and a label the record has and the file lacks is cleared, because the file is the source of truth for an endpoint it declares (this is the one removal repair makes, and it is directory metadata, not a credential; each clear is listed). A record for an endpoint the file does not declare, with its labels and zone, and an endpoint with no record, are reported and left alone.
4. Stale creds files are listed with the exact `mashctl reissue NAME...` command, which is not run. Every reissue reads the endpoint's directory record first (so a leftover creds file of a retired name is refused), and one that narrows a creds file rotates its user key and revokes the old one, which needs the system account credentials as `retire` does; the order is revoke, then record, then file ([mashctl.md](mashctl.md)).

Repair exits 0 when its steps completed, even though step 4 or the reported records leave drift: run `mashctl policy drift` afterwards to see what is left. Repair is not a transaction across NATS claims, JetStream resources and local files: a step that fails leaves the earlier ones done and running repair again is safe.

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

`--names` prints one name per line. `--json` prints an array, one object per endpoint, in name order; `--long` adds `lease`. `--json` and `--names` exclude each other. These field names are stable:

| Field | Type | Present |
|---|---|---|
| `name` | string | always |
| `owner` | string | always |
| `online` | boolean | always |
| `last_seen` | string, RFC 3339 UTC | when the endpoint last answered a presence probe (not its last heartbeat) |
| `role`, `host`, `note`, `description` | string | when set |
| `zone` | string | when the endpoint is in a zone (an optional key added by [ADR-0054](decisions/0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md); consumers must ignore keys they do not know) |
| `labels` | object, key to value | when there is at least one |
| `caps` | array of strings, sorted | when there is at least one |
| `lease` | object with `host` and `version` | with `--long`, when this endpoint's own live lease is known |

**How presence works.** Heartbeats are still published every 30 seconds by each running endpoint, and another SAP tool may listen to them, but `mash serve` no longer subscribes to them ([ADR-0066](decisions/0066-mash-serve-asks-who-is-online-when-someone-asks-instead-of-listening-to-every-heartbeat.md)). Presence is asked for when someone asks: `mash who` and the `who` tool send one request to `$SRV.INFO.agents`, which every running endpoint answers with its registration, and wait until the answers stop coming (300 ms after the last one, at most 2 s). An answer counts only when its identity signature verifies and its key is the one the directory has for that name. An endpoint is online when it answered the latest probe; `seen T ago` and `last_seen` are the time of its last answer to a probe, not of its last heartbeat. The `who` tool inside a session reuses a probe's result for 30 seconds (`endpoint.PresenceCacheTTL`) and asks again after that; one probe at a time, and after a probe that failed, or that no registration answered, the next one waits at least 5 seconds (`endpoint.PresenceRetryGap`). So the tool can be up to 30 seconds behind, and takes about a third of a second when it has to ask. `mash who` on the command line always probes. Before the first probe an endpoint with no answer shows offline.

The `who` tool takes the same filters as optional parameters: `match`, `name`, `role` (strings), `label` (an array of `"k"` or `"k=v"`), `cap` (an array of capability words), `online` (`true` online only, `false` offline only, absent both) and `format` (`text`, the default, or `json`, the same array as above including `lease`). A call with no arguments is unchanged; an unknown `format` is a tool error.

The policy file that will restrict who may mail whom (compiled by `mashctl`, not shipped yet) is described in [policy-file.md](policy-file.md). Its `mashctl policy check|test|explain|show` verbs are listed under "Using it" there.

Adding an endpoint to the policy and minting its user (`mashctl add`, `mashctl reissue`) is described in [mashctl.md](mashctl.md).

## Removing an endpoint

```sh
mashctl deprovision design       # revokes the endpoint's user in the account JWT, then removes it; add --user <public-key> to act only if its record still carries that key
mashctl retire design            # the same, plus the policy entry
```

`mashctl retire` and `mashctl deprovision` revoke the endpoint's NATS user in the same command. They need the operator and account keys in the key store and the system account credentials (`MASH_SYS_CREDS`, else the `sys.creds` next to `admin_creds` or in `<config dir>/server`; `MASH_SERVERS`; there are no flags for these two here). The revocation is added to the account JWT by user key and time, signed with the operator key, pushed to every server, and read back; a connected user is closed when the server takes the update. It happens before anything is removed, so a revocation that fails or cannot be confirmed (no key store, no system credentials, a server that did not acknowledge) refuses the command with the endpoint intact; running it again is safe. `--no-revoke` removes without revoking, and the user can then still connect: it prints a `WARNING: --no-revoke: ... is NOT revoked and can still connect` block on stderr before it starts and again after it finishes. `--no-revoke` is a temporary flag name and will be renamed with the CLI redesign. `mash admin deprovision` has no key store: it removes and leaves revoking to you (`nsc revocations add-user --account "$ACCOUNT" --name design`, then `nsc push --account "$ACCOUNT"`). A revoked key stays revoked, so a name that comes back needs a new user key. The account JWT is replaced whole and the resolver has no compare-and-set, so account changes (`policy apply`, `retire`, `deprovision`) are serialised only per key directory: two operators with separate key directories must not run them at the same moment, or the later push can erase the earlier one's change (including a revocation). Run them from one operator at a time and confirm with `mashctl policy drift`.

Deprovisioning fences the name, then removes the directory record and the inbox stream. The fence is a tombstone in the endpoint's lease key: a session still running under the name stops at its next lease refresh (within 30 seconds), and one starting up is refused, instead of either taking the name back. The tombstone expires with the lease TTL (90 seconds), and provisioning the name again clears it at once. `mash admin deprovision <name> --user <public-key>` is the guarded form, for scripts that must not remove an endpoint someone else has since provisioned under the same name: it reads the directory record, refuses (touching nothing) unless the record's user equals the key, and deletes the record only if its revision is still the one it read, so a record replaced in between is never removed. If another admin changes only the record's metadata in between, it re-reads the record and retries on the new revision (up to 5 tries), so that change does not refuse; a changed user, a vanished record or exhausted tries refuse; it then fences the lease and deletes the inbox. Provision and both forms of deprovision hold the name's lock ([ADR-0048](decisions/0048-limit-changes-take-a-per-stream-kv-lock-with-expiry.md), see [Limits](#limits)) for their whole run, so a second admin working on the same name waits for the first to finish (up to 20 s, then "try again") and cannot have its new endpoint fenced or its inbox deleted by it. With no record it refuses even if the inbox stream exists, because nothing proves who owns it; run it without `--user` to remove such a leftover. Revoking the user stops its credentials from connecting at all (`mashctl` does this for you; `mash admin deprovision` does not). Until the user is revoked, mail it sends still arrives, but labelled `[unknown-signer]`, because its key is no longer in the directory.

`mashctl retire NAME` renames `NAME.creds` to `NAME.creds.retired-<UTC timestamp>` (or deletes it with `--delete-creds`), so the name can be added again at once; the retired file still holds the old seed. An add killed mid-run can leave a record or creds file with no policy entry; `mashctl policy drift` reports it as `record-extra` with the fix, `mashctl retire NAME --force --delete-creds` then `mashctl add NAME` ([mashctl.md](mashctl.md#a-killed-add)). A leftover `policy.yaml.lock` file is harmless: the lock is released when its holder exits.

### Suspend an endpoint's inbound mail

To read the last of an endpoint's mail before removing it, stop new mail first, then drain, then deprovision ([ADR 0057](decisions/0057-an-admin-can-suspend-an-endpoints-inbound-mail-by-parking-its-inbox-stream-subjects.md)):

```sh
mashctl suspend design      # suspended design: id 9f3c..., last_seq 41, msgs 38
mashctl suspended design    # exit 0 if suspended, 3 if not, 1 on any error
# read the inbox through sequence 41 (the last_seq above); it is final while the endpoint stays suspended
mashctl deprovision design --suspension 9f3c... --drained-to 41
```

`--suspension ID --drained-to N` (both are required together) is the check that makes this safe: under the name's lock it refuses, touching nothing, unless the inbox is still suspended under that id and its last sequence is exactly N, and the directory record still belongs to the user key it had when `suspend` ran (a record that is missing, was missing at suspend time, or was replaced by another user key is refused; use plain `deprovision` for a leftover). The id, a random token `suspend` stores and prints, ties the drain to that one suspension, so an endpoint re-provisioned under the same name (which can have the same last sequence, commonly 0) cannot be removed by an old command. If another admin ran `resume` meanwhile, or mail arrived after it, the command fails instead of deleting mail you have not read. A plain `mashctl deprovision design` does not check, so use it only when you do not need the mail. `--drained-to` cannot be combined with `--user`.

`suspend` replaces the inbox stream's subjects with one subject no endpoint may publish to and saves the originals in the stream's metadata; nothing is purged and no other field changes. Mail sent while the endpoint is suspended is refused, not queued: the sender's publish finds no inbox and fails, and the sender cannot tell that from an endpoint that does not exist, so it must send again after `mashctl resume design`, which restores the saved subjects. Mail stored before the suspension stays readable, and the endpoint's own session keeps running. `limits` keeps the suspension; `set-zone` refuses on a suspended endpoint, and so does provisioning over its stream. Suspend does not revoke: it prints `new mail parked; the endpoint's credentials are still valid`, and the endpoint can still connect and send. Revocation happens in `deprovision`. Both `suspend` and `resume` can be repeated safely. `mash admin` carries the same three verbs. Suspending and resuming are admin operations: an endpoint's credentials cannot update its stream.

## Topologies

mash supports one NATS server, or a cluster, in the default JetStream domain, with the default API prefix. Inboxes are single-replica ([Durability](#durability-and-sync_interval)), so on a cluster an inbox is unavailable while its node is down.

JetStream domains, leaf-node inboxes and subject mappings are not supported. The endpoint permissions that `mash admin permissions` generates (`$JS.API.*`, `$JS.ACK.<stream>.<consumer>.>`) and the client (`jetstream.New` with no domain or API prefix) assume the default domain. On a leaf node with its own domain, or behind subject mappings, the API and ACK subjects differ: acks can be denied (endless redelivery), the inbox may be unreachable, and signatures over mapped subjects may not verify. A `js_domain` setting is not implemented.

## Inbox limits

By default each inbox keeps mail for 30 days or 16 MiB, whichever comes first, dropping the oldest (both are configurable: see [Limits](#limits)). Single messages are limited to 1 MiB including headers, or to the server's `max_payload` when that is smaller: mash reads it from the server at connect time and refuses an oversize send locally with a clear error before anything is published. Synadia Cloud's free tier caps a payload at 512 KiB, so there the limit is 512 KiB. Message deletion and purging are denied on the stream itself, so even the account admin cannot quietly rewrite history; deleting the whole stream (deprovisioning) is the only way to remove it.

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
  "url": "tls://nats.example.com:4222",
  "owner": "alice",
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
mash admin limits design     # or only the named inboxes
```

New endpoints (`mash admin provision`) get the configured limits at once. `mash admin limits` updates each existing stream in place, keeping its mail, and prints a line per endpoint saying what changed. It refuses to lower `max_bytes` or `max_age` on an endpoint, with an error naming the endpoint and field, and changes nothing for it, because a lower cap could discard stored mail. To lower an inbox's limits, run `mash admin deprovision <name>` (which removes the mail in it) and provision it again. `mash admin init` likewise never lowers a bucket. The check that runs on an existing inbox still reports any stream that differs from the configured limits. JetStream has no compare-and-set on a stream's configuration, so each change to an inbox's limits (`mash admin limits`, and the stream step of `mash admin provision`) takes a per-stream lock first: the key `limits-lock.<name>` in the lease bucket, held from the read of the stream's caps until its update is written. Two admins changing one inbox at once are serialised, so the second sees the first's caps and is refused if it would lower them. The lock lasts 30 s and expires on its own if the admin dies; a second admin waits up to 20 s for a held lock and then fails with `mash: another admin is changing the limits of <name> (lock held until <time>); try again`, which is safe to retry. Every operation under the lock now stops about 5 s before it expires and re-checks it before writes that cannot be undone, so a stalled admin fails with `try again` instead of overlapping another admin. Over many inboxes the locks are taken one at a time. Expiry is judged by each admin's own clock, so keep admins' clocks within a few seconds of each other (`mash status` prints a `clock:` line). Endpoints cannot read or write the lock. A deployment that never ran `mash admin init` for the lease bucket has it created on first use ([ADR-0048](decisions/0048-limit-changes-take-a-per-stream-kv-lock-with-expiry.md)).

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

**`delivery: REFUSED` in `status`** means the plugin's hook runs in this session but was not recognised as this session's, so mail stays spooled, and serve wakes the session a few times (then stops: see below). It happens where hooks run outside the process tree of serve's parent (serve started through a wrapper that does not `exec`, a PID namespace or sandbox, some IDE or desktop hosts, a chain deeper than 10) and the session id does not match either. The fix: start Claude Code with `MASH_LAUNCH_TOKEN` set to a fresh value for each launch (for example `MASH_LAUNCH_TOKEN=$(uuidgen) claude ...`); serve and its hooks inherit it. A value reused across launches, or one shared by two sessions with the same name, defeats the launch-token rule: a waiting session would then drain the holder's mail (serve warns on stderr when it sees this). If it still refuses, report your setup with the `hook.log` line and `MASH_HOOK_TRACE=1` output.

**Hooks that never run** (ADR-0041). Mail waits in the spool for a hook, and only a hook drains it. If no mash hook runs in the session, waking it again would cost a turn each time and show nothing, so mash watches for that. Both states hold only while no main-session hook, and no hook of the session's own subagents, has run since `serve` bound the spool; a hook run ends them.

* **Suspected:** three wakes (the first, at once, counts) and still no hook. Re-waking stops. A new waking message still rings once. Nothing is acked, and a message the spool will not take is held unacked instead of written to the channel: a channel write is not delivery when wakes are not landing (issue #40). `status` shows `delivery: SUSPECTED - the last <n> wakes started no turn (no hook since <time>); channel writes are not counted as delivery; re-waking stopped`. A session that makes no tool calls stays here: no turn is wasted, and the mail is read if the model uses the `read` tool.
* **Proven:** two mash tool calls at least 12 seconds apart (above the hook's 10 second timeout) with no hook between them. A PostToolUse hook runs after every successful tool call, so the first call's hook would have run by the second. Then one wake, and each later wake, ends "mash's hooks are not running in this session; read each message with the read tool (#seq)." The `read` tool marks the message it returns as seen once its result has been written to the client, so it is acked; the `inbox` tool never acks. The one notice in the proven state is sent for held mail of any kind, result and fyi included. `status` shows `delivery: DEGRADED - no mash hook has run since <time> although this session has made tool calls. Likely cause: ... Mail now: read each message with the read tool. Next: ...`. A hook refused as another session's (above) counts as no hook, and its refusal is named as the cause.

mash does not read Claude Code's settings files, so `status` lists the causes that leave no trace: `disableAllHooks`, `allowManagedHooksOnly`, hooks not loaded, `mash` not on the hook's PATH. The fixes: remove `disableAllHooks: true` from the user, project, local or managed settings; under `allowManagedHooksOnly` an administrator force-enables `mash@mash` in managed `enabledPlugins` (hooks of plugins force-enabled there are exempt); check that the plugin is enabled in this project; read `hook.log` for `command not found` or a refusal. Mash tool calls a subagent makes count as the session's tool calls, and a session whose only running hooks are its subagents' is never proven.

**Under the plugin** (`MASH_SPOOL_FROM_START=1`), mail is spooled from the first message and shown by the hooks; the channel event is only a one-line summary that wakes an idle session. Without that setting, `mash serve` pushes mail to the channel in full until the first hook has run in the session.

Spooled delivery works like this (without `MASH_SPOOL_FROM_START`, it starts at the first hook run):
- It writes each message to `spool/<owner>/<name>/<seq>.json` in the state directory (`$MASH_STATE_DIR`, else `$XDG_STATE_HOME/mash`, else `~/.local/state/mash`) (directories 0700, files 0600).
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
- **An SDK host must load the plugin itself.** `claude -p` loads plugins you already installed, but an Agent SDK host takes plugins through the SDK's plugin option ([Load plugins in the Agent SDK](https://code.claude.com/docs/en/agent-sdk/plugins)). Mail needs the plugin's MCP server (`mash serve`) and both hooks, PostToolUse and UserPromptSubmit (`plugin/hooks/hooks.json`); a host that loads MCP servers or hooks its own way and omits either gets no delivery, and `status` reports `delivery:` accordingly. Also set `MASH_NAME`. mash has not been run under an SDK host.

## Minimum Claude Code version

`host.MinClaudeCode` (`internal/host/claude.go`) is the oldest Claude Code a live receipt passed on. Each live receipt in `docs/evidence/` names the Claude Code version it ran on and whether it passed. The constant is the oldest version that has a passing receipt for the current delivery design (hooks plus the channel wake). It is raised when a design change retires the receipts on the oldest version, so that every version at or above it still has a passing receipt behind it, and it is never lowered: lowering it would claim a receipt that does not exist. Update it, the README and install.md Requirements line together, in the pull request that adds the receipt. `status` compares the version the client sent in `initialize` (`clientInfo`) with it and warns below it; mash does not refuse older versions, and it does not guess other clients' version schemes.

## Refused sends

`mash send` (and the `send` tool) fails at once with `refused by policy: you may not mail <name>` when the server refuses the publish because the sender's NATS user has no permission to publish to that recipient's mail subject. Nothing was stored, and retrying with the same credentials cannot help. Mash's default permission set lets an endpoint mail every endpoint of its owner, so this only happens to a sender issued a set that omits the recipient, for example one generated from a policy file or a hand-edited allow list of mail subjects. The fix is on the server side: issue the sender a permission set that includes the recipient's mail subject (`agents.mail.*.<owner>.<recipient>`).

The message adds a second sentence because the server's error does not say why the publish was denied: if `mashctl policy explain` says the policy file allows the pair (or the recipient is not in the file at all), the sender's credentials may predate the rule. Ask the admin to run `mashctl policy drift`, then `mashctl reissue`.

Mash learns of the refusal from the server's asynchronous permission-violation error, which names the subject, and matches it to the send in flight by subject. It reports a refusal only when exactly one send is waiting on that subject at that moment and no send on the subject has ended unsettled (timed out or cancelled) while others remained. After a send that ended unsettled, the next send on the subject first runs a flush barrier and a settle of at most 25 ms, so a violation delayed from the earlier send is dropped; sends on that subject wait for it. The settle narrows the window nats.go's asynchronous error handler leaves; it cannot close it. The refusal therefore ends with the send's key (`If you retry, use --id KEY (the send tool's id): it sends at most once within 2 minutes.`), which makes a retry after a false refusal harmless. With several in flight the violation cannot be attributed (it may belong to an earlier send, or to one sent before a permission change), so none of them is refused: each keeps waiting for its ack or deadline and, if it times out, the timeout error adds that an unattributed permission violation on the subject was seen. Without it a denied publish gets no reply and would only report a timeout after the full call timeout. If a server version words that error differently, mash cannot match it and the send fails as a timeout, as before.

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
| The session says it has no mash identity | `MASH_NAME` was not set when Claude Code started. |
| The session says the name is already in use | Another live session holds the lease (it expires 90 s after that session dies). The session keeps retrying every 10 s and tells Claude when mail is on; no restart is needed. |
| `--channels` says "not on the approved channels allowlist" | The managed settings allowlist is missing (see install.md, step 3). |
| Mail arrives labelled `[unknown-signer]` | The sender's key is not in the directory: it was deprovisioned, or it is not a mash endpoint. |
| `mash: endpoint X is not provisioned` | Run `mash admin provision`, or check `MASH_NAME` for a typo. |
| `x509: certificate signed by unknown authority` (or another `x509:` error) on connect | The server's certificate is not trusted. Set `tls_ca` (`MASH_TLS_CA`) to the PEM file of the CA that signed it. The files are read at connect time. |
| `remote error: tls: bad certificate` (or `certificate required`) on connect | The server requires a client certificate, or refused the one given. Set both `tls_cert` and `tls_key` (`MASH_TLS_CERT`, `MASH_TLS_KEY`) to a certificate the server's CA signed. |
| `status` says `tls: WARN` | The url is plain `nats://` to a non-loopback host. Use `tls://` with the server's certificate. |
| `refused by policy: you may not mail X` on send | The sender's NATS permissions do not include X's mail subject (a hand-edited set, or credentials older than the policy rule: `mashctl policy explain`, `mashctl policy drift`). See "Refused sends". |
| `do not match endpoint` | The credentials file belongs to a different endpoint than `MASH_NAME`. |
| Peers look offline, or mail arrives labelled `[stale]` | Clock skew. Keep every machine's clock in sync (NTP, including laptops, containers and VMs after suspend): more than 30 s of skew (`sap.LiveSkew`) makes live requests and heartbeats fail and stored mail `[stale]`. `status` prints a `clock:` line and warns past 10 s. |
| Clients take 30-60 s to reconnect after a network blip | The server rate-limits new connections per source address. Clients retry with jitter; raise the limit for trusted sources if this matters. |
