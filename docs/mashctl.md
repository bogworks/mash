# mashctl: creating, labeling and deleting endpoints

Endpoints are made by `mashctl endpoint create`, and every endpoint carries the same kind of permission set, the owner-line set for its address `owner.group.instance` (it may mail the endpoints of its own owner and read what it needs to; the owner is the fence). `mashctl endpoint permissions ADDRESS` prints that set. A NATS user is signed by the account signing key (`account.nk` in the key directory, `<config dir>/keys` or `MASH_KEYS_DIR`) and carries its permission set in its JWT; there is no policy file and no tag. See [ADR 0052](decisions/0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md).

`endpoint create`, `reissue`, `delete`, `list`, `show`, `set`, `unset`, `suspend`, `resume`, `inbox` and `permissions` take an endpoint as `owner.group.name`: dotted, three parts. A bare name, the flat key `owner_group_name` and the slash form are refused (exit 2).

`mashctl endpoint create`, `reissue`, `delete`, `list` and `show` are described [below](#mashctl-endpoint-create); `mash key create` makes the endpoint's own key, and `mash join` installs its creds file from the join file.

## Prerequisites

Minting a user needs the account's identity key (`account.nk` in the key directory). `mashctl init --config` does not create that key: it imports the account seed you already hold (`account_key` in the setup file) and makes mash's storage on the server. Creds minted with that key authenticate at once: nothing has to be pushed to the account first.

## Connections and trust

`mashctl` refuses admin creds and system account creds (`system_creds` in `config.json`) that other users can read, as mash does for endpoint creds: the file must be a regular file with no group or other permission bits (`chmod 600 FILE`). A symlink is followed and the file it points to is checked; a missing file keeps its own error.

Every connection requires TLS, to any host, loopback included: every verb that connects with the admin or system creds (and `init`) refuses a plain `nats://` URL with exit code 2, naming `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`, which allows plain text and makes every command print a warning on stderr (see [Turning encryption off](install.md#turning-encryption-off)); use it only for a throwaway server in a test. A `tls://` connection fails when the server does not offer TLS. `doctor`, `endpoint delete`, `endpoint reissue` and `init` verify the account JWT's signature, subject and issuer (the operator key, or a key declared by `<key dir>/operator.jwt`). Admin and system connections never add servers the server advertises. See [operations](operations.md#writing-the-account-jwt-revocation).

## Which server does mashctl change?

`mashctl` takes its server, owner and admin credentials from `config.json`; the endpoint's own `MASH_*` variables win over the file, and the global flags `--server`, `--ca`, `--creds` and `--endpoint` win over both. Its owner is the owner part of an explicit endpoint (`--endpoint`, `MASH_ENDPOINT` or `endpoint` in `config.json`; any group and name will do), else the `owner` key of `config.json`, which `init` writes. It never comes from an installed endpoint's creds file, so installed endpoints do not change who `mashctl` acts as. Inside a mash agent session those variables are the session's, so `mashctl` would act on the session's own server. Before any verb that connects to a server, mashctl prints one line to stderr, `mashctl: target server <url> (from <source>), owner <owner> (from <source>), admin creds <path> (from <source>)`, for example `mashctl: target server tls://host:4222 (from MASH_SERVER), owner acme (from MASH_ENDPOINT), admin creds ... (from ...)` (a flag source reads `flag:--server`), and warns when an environment variable overrides a different `config.json` value or when `MASH_LAUNCH_TOKEN` shows the shell is inside a session. It informs and never refuses; `init`, `status` and the offline verbs print nothing of it. To choose the target explicitly, pass `--server`, `--creds` and `--endpoint`, or use `env -u MASH_SERVER -u MASH_ENDPOINT -u MASH_ADMIN_CREDS -u MASH_CA mashctl ...`, or run outside the session. See [operations](operations.md#which-server-does-mashctl-change).

## Setting up: `mashctl init`

```sh
mashctl init --config FILE [--dry-run]
```

`init` sets mash up on a NATS server you already run in operator mode with a full resolver and JetStream. `mashctl` does not install or start NATS. The setup file is YAML with the keys `servers`, `ca`, `owner`, `admin_creds`, `system_creds`, `operator_key` and `account_key`: paths and addresses, never secrets (see [Setting mash up](operations.md#setting-mash-up-with-mashctl-init---config) for what each one is). It needs authority you already hold (the operator signing seed, the mash account's identity seed, system account creds, admin creds), because a server address does not prove it. It checks the files, the system creds, the account on the resolver, JetStream, the operator key and the admin creds without changing anything, then keeps the keys in the key store, copies the system creds to `<config dir>/server/sys.creds`, makes the buckets, records the admin user in the issue record and writes `config.json` (`servers`, `ca`, `owner`, `admin_creds`, `system_creds`). It prints `Initialized mash on <server> for owner <owner>`, or `Already initialized on <server>; nothing changed` when there was nothing to do; `--dry-run` prints `Would initialize mash on <server> for owner <owner>` and changes nothing. It changes nothing on the server except mash's own storage and the admin user's entry in the issue record; the cutover of a live account is a separate step you run (`mashctl endpoint create` for each endpoint). On an operator with strict signing key usage the account identity key cannot sign users: give `admin_creds` a user signed by one of the account's signing keys.

## Status and limits

```sh
mashctl status
mashctl limits show
mashctl limits apply [prefix] [--dry-run]
```

`status` prints one line per configured server: `tls://host:4222: ok, 3 endpoints, 1 online`, or `tls://host:4222: not ok: the server did not answer`. It exits 1 when any server is not ok, and then prints `hint: mashctl doctor` on stderr. `--json` prints `{"server", "ok", "endpoints", "online", "reason"}` for one server, and `{"servers": [...]}` for several.

`limits show` prints the inbox size and age limits in force (`inbox size:  16 MiB`, `inbox age:   30 days`; `--json`: `{"inbox_bytes", "inbox_age"}`). `limits apply` raises existing inboxes to the limits in `config.json` and never lowers one: it prints `Raised 2 inboxes` or `Nothing to raise`, and `--detail` adds a line for each inbox. Its prefix is an owner (`acme`), an owner and group (`acme.demo`), or one address; the default is every inbox. See [Changing limits later](operations.md#changing-limits-later).

## The issue record

Every user JWT `mashctl` issues is written to the issue record, the `mash-issued` bucket, under the user's public key: a user minted with the account key (a kept key adds a revision under the same key, a rotated key adds an entry for the new key and leaves the old key's), and the admin user that `mashctl init` connects with (kind `admin`; `mashctl init --config` records it). The entry is written before the creds file or the directory record, so a user that can connect is always in the record, and a record that cannot be written stops the issue. An entry holds the public key, the address (the endpoint name), the owner, the kind (`endpoint` or `admin`), the tags, the generation (always 0: mash has no policy generation yet) the issue time (RFC 3339, UTC), the purpose and, once mash has revoked the key, the revocation time. The purpose is one of `issue` (a user minted with the account key, the admin user at `init`), `rotation` (the new key of a credential that rotates the user key) and `probe` (reserved for the policy gate's probe credentials; `mashctl` does not write it); a record from before the field reads as `issue`. `revoked_at` (RFC 3339, UTC) is left out until set: `endpoint delete` and `endpoint reissue` set it after the revocation succeeds, once and never cleared, and a key mash never recorded is skipped. A failure to set it prints `warning: user key U... is revoked, but the issue record was not updated with it: ...` on stderr and never fails or undoes the revocation. Nothing is removed from the record when an endpoint is deleted or its key is revoked: revocation is the account JWT's, and the record is the history of what was issued. `mashctl init --config` creates the bucket; a deployment from before the record gets it, hardened, the first time a user is minted and written. Users issued before the record existed are not in it until a credential is reissued under the same key.

`mashctl doctor` compares the user keys connected to every server with this record (its `keys` check, see [doctor.md](doctor.md#keys)): a connected key that mash did not issue, or a key recorded as revoked that is still connected, is a problem. No key is exempt, not even the admin user the command connects with: `mashctl init` records it. The check needs `system_creds` in `config.json` and the number of servers is the count under `servers`, as for `endpoint delete`. There is no command that lists the record; one endpoint's own records are `mashctl endpoint show ADDRESS`. See [operations.md](operations.md#the-issue-record-and-the-drift-check) for what it can and cannot see.

## Setting and removing values on an endpoint

```sh
mashctl endpoint set ADDRESS key=value... [--dry-run] [--json]
mashctl endpoint unset ADDRESS key... [--dry-run] [--json]
```

These change the endpoint's directory record (what `who --long` shows). They connect with the admin credentials and need no file and no creds: the endpoint must have a directory record. The keys are `role`, `description`, `host`, `note`, `capability` and `label.NAME`.

- `set` gives each key its value and leaves every other field as it is. `capability=a,b` takes a list with commas and sets the whole list, replacing what was there. `label.team=blue` sets the label `team`. A label name is a letter, then up to 31 of `a-z 0-9 _ . -`; keys and values are checked as the directory checks them.
- `unset` clears each key. `unset capability` removes every capability; to remove one, set the list again without it. `unset label.team` removes the label `team`; a label that is not set gives a warning and exit 0.
- Both print `Updated endpoint ADDRESS`. A command that leaves the record as it is prints `Nothing to change on endpoint ADDRESS`.
- `--dry-run` prints `Would update endpoint ADDRESS: what` and `Nothing was changed.`, and changes nothing.
- `--json` prints `{"address": ..., "set": {...}}` or `{"address": ..., "unset": [...]}`, with `"dry_run": true` for a dry run.
- A key that cannot be set, a missing value, a key given twice and an unknown option exit 2 and are not echoed back. An endpoint with no directory record exits 1.

Labels do not change a minted user: the permissions of an endpoint do not depend on them, so setting one leaves nothing to reissue.

## Suspending an endpoint's inbound mail

- `mashctl endpoint suspend ADDRESS` refuses all inbound mail to ADDRESS's inbox and prints `Suspended endpoint ADDRESS`. Suspend does not revoke: it is reversible, and the endpoint can still connect with its creds. Stored mail stays readable and is not purged.
- `mashctl endpoint resume ADDRESS` restores delivery and prints `Resumed endpoint ADDRESS`.
- `mashctl endpoint show ADDRESS` shows `state: suspended` for a suspended endpoint, and `endpoint list` shows `suspended` in its STATE column.
- Both take `--json` (`{"address": ..., "state": ...}`). An endpoint with no inbox stream exits 1.

See [operations.md](operations.md#suspend-an-endpoints-inbound-mail).

## Checking the deployment

`mashctl doctor [--strict] [--json]` checks the server, mash's storage on it and every endpoint's records, changes nothing, and says how to fix what is wrong. It always prints every check (`ok`, `warn`, `FAIL`, or `skip` for one that could not run), then the count and one `hint:` line when the first problem has a fix. It reports state, not use: it cannot see publishes, connections or endpoints with no stream. Exit codes: 0 no problem (and no warning with `--strict`), 1 a problem, 2 usage. See [doctor.md](doctor.md).

## mashctl endpoint inbox

```text
mashctl endpoint inbox ADDRESS [--after NUMBER] [--limit n] [--json]
```

Lists the messages stored in one endpoint's inbox and whether each was delivered, so a test can prove what the transport kept and delivered. It never shows a message's text. It is read only: it uses stream info, a direct get of a message by sequence and consumer info of the existing `deliver` consumer, and nothing else. It never creates, updates, binds or fetches from a consumer, never acknowledges, and writes nothing to the directory, a bucket or a file. It runs with the same admin credentials as `doctor`.

- ADDRESS is read from the directory record, one key; an address that names no record is refused (exit 1).
- The default is the last 20 messages, oldest first. `--limit n` caps the count. `--after NUMBER` shows only the messages after that message number, as `mash inbox --after` does; with it the first n messages after the number are shown. A value that is not a whole number exits 2 with `error: --after must be a whole number`.
- Exit codes: 0 success, 1 a refusal (an unknown endpoint, no inbox stream) or it could not complete (no server, no credentials), 2 a usage error.

The text form prints a line with the count, then a table:

```text
2 messages for acme.demo.bob, 1 not delivered.

NUMBER  FROM             WHEN    TRUST     DELIVERED  KIND  SIZE
7       alice            2m ago  verified  no         task  48 B
8       acme.lab.mallet  1m ago  absent    yes        task  61 B
```

TRUST says whether the claimed sender was proven: `verified`, `unknown-signer`, `claimed`, `absent`, `stale` or `forged` (malformed mail shows as `forged`); `-` when there is no label. DELIVERED is `yes` or `no`, and means only that the endpoint took the message off the server. Whether the endpoint then showed it or refused it is decided on the endpoint's own machine and mashctl cannot see it: by default an endpoint refuses everything that is not verified, so a `yes` beside any other trust label most likely means it was refused.

FROM is the sender's name when the sender is in the endpoint's owner and group, else its whole address, and `?` when the message names none. `--json` prints one object:

| field | meaning |
| --- | --- |
| `address` | the endpoint |
| `total` | the messages in the inbox stream |
| `undelivered` | the messages the `deliver` consumer has not delivered; all of them when the consumer does not exist |
| `messages[].number` | the stream sequence |
| `messages[].from` | the sender's address |
| `messages[].trust` | the trust label, as in the text form |
| `messages[].delivered` | `true` when the sequence is at or below the consumer's last delivered sequence |
| `messages[].kind` | the `Mash-Kind` header |
| `messages[].size` | payload size in bytes |

Delivered comes from the consumer's cursors alone. NATS does not report acknowledgements above the ack floor, so a delivered message may be unacknowledged or acknowledged out of order, and the server does not say which. A per-message delivery count and ack time are not kept by JetStream or mash.

See also [doctor.md](doctor.md) for what `mashctl doctor` says about an inbox that lost mail unread or is nearly full.

## mashctl endpoint create

```sh
mashctl endpoint create ADDRESS [--out PATH] [--public-key PATH] [--dry-run] [--json]
```

Creates an endpoint: it signs the endpoint's user key with the account signing key, writes the directory record and the inbox, and records the key in the issue record. The record, the inbox and the issue record are written before the creds file or join file, so a user that can connect is always on record.

**The address** is `owner.group.name`: three tokens joined by dots. Other forms, such as `alice/dev/design` or a name alone, are a usage error. A group cannot be changed later: delete the endpoint and create a new one. The old address is never reused. Role, host, labels and the other directory fields are set after the endpoint exists, with `mashctl endpoint set`.

**With no option** the key pair is made on this host and `<config dir>/<address>.creds` is written (mode 0600, never over an existing file). The endpoint can be used on this machine at once. The `mashctl` host holds the key while the command runs. The key is kept in `<creds file>.pending` until the creds file is written.

**With `--out PATH`** the key pair is made, the user JWT is signed, and nothing is installed on this machine. The command writes a join file at `PATH` for another machine. Take that file to the endpoint's machine and run `mash join` there. The join file holds the seed: move it as you would a creds file.

**With `--public-key PATH`** (which needs `--out`) the command signs a public key that the endpoint made with `mash key create`. No seed is made, and the join file holds the user JWT and no seed. On the endpoint's machine, `mash join` takes the seed with `--key`. The seed never reaches the `mashctl` host.

A key is used at one address, once. A key that was issued before, or revoked, is refused.

**The join file** is JSON, mode 0600, written to a temporary file in the same folder and then renamed. A path that exists is refused, and so is a folder that other users can write in (this holds for `--out` on every form, and for the `<PATH>.pending` file beside it). The CA file may hold certificates only: only its `CERTIFICATE` blocks are copied, and a file with a private key or any other block in it is refused with `error: FILE must hold only certificates`. It has these fields and no others:

| Field | Holds |
| --- | --- |
| `mash_join` | `1`, the version of the format |
| `address` | the endpoint, as `owner.group.name` |
| `servers` | the server addresses from the configuration |
| `ca` | the certificate authority certificates as PEM text, read from the configured CA file, or `null` when none is configured (`mash join` then trusts the system roots) |
| `creds` | the whole text of the NATS creds file, user JWT and seed (without `--public-key`) |
| `user_jwt` | the user JWT alone (with `--public-key`) |

**Other options.** `--dry-run` connects, checks everything, says what it would do and changes nothing. `--json` prints one JSON object.

**What it prints.** On success:

```text
Created endpoint alice.dev.design
```

With `--out` a second line names the file:

```text
Created endpoint alice.dev.design
Join file: alice.dev.design.join
```

With `--detail` the lines `servers: ...`, `credentials file: PATH` (or `join file: PATH`) and `public key: KEY` follow. With `--dry-run` it prints one line, and with `--json` the same object plus `"dry_run": true`:

```text
Would create endpoint alice.dev.design
```

With `--json`, `{"address": "alice.dev.design", "installed_here": true, "creds": "~/.config/mash/alice.dev.design.creds", "join_file": null}`, where `creds` is the installed credentials file. With `--out`, `installed_here` is `false`, `creds` is `null` and `join_file` is the path. With `--dry-run` the same keys report the planned paths, plus `"dry_run": true`. Neither the seed nor the JWT is printed.

**Errors** are one line on standard error, `error: WHAT`, and at most one line `hint: COMMAND`; with `--json` they are `{"error": ..., "hint": ...}` on standard output. Exit codes: 0 done or planned, 1 failed, 2 the command line was wrong. For example:

```text
error: alice.dev.design already exists
error: alice.dev.design was deleted and its address cannot be used again
error: --public-key needs --out, the join file to write
hint: mashctl endpoint create --help
```

**A killed run.** Run the same command again. The key waits in `<creds file>.pending` (or `<PATH>.pending` with `--out`), so the second run finishes with the same key and removes the file. With `--public-key` the second run signs again and writes the join file. A creds file or inbox left by a run that never completed shows in `mashctl doctor` as an inbox with no endpoint record.

## mashctl endpoint reissue

```sh
mashctl endpoint reissue ADDRESS [--out PATH] [--public-key PATH] [--dry-run] [--json]
```

Gives an existing endpoint new credentials and cancels the old ones. It revokes the old key on every server, then makes the directory record name the new key. The inbox and its unread mail stay. A session that runs as the endpoint must restart to read the new creds. The options work as for `create`.

With no option the new key pair is made on this host and `<config dir>/<address>.creds` is replaced. With `--out PATH` the command writes a join file for another machine and installs nothing here. With `--out PATH --public-key PATH` it signs the endpoint's new public key and the join file holds the user JWT. `--public-key` needs `--out`.

On success it prints `Reissued credentials for alice.dev.design`, then `Join file: PATH` with `--out`. With `--detail` it adds the line `servers acknowledged: N`. With `--dry-run` it prints `Would reissue credentials for alice.dev.design`, and with `--json` the same object plus `"dry_run": true`. If the join file cannot be written after the key changed, the hint is `remove PATH or choose another --out`. With `--json` it prints `{"address": "alice.dev.design", "join_file": null}`; with `--out` `join_file` is the path. There is no bulk form: reissue takes one address.

`--public-key` naming the key the endpoint already has is refused: nothing would be rotated. Make a new key with `mash key create --out NAME.seed`. The one exception is a rerun after a killed `--public-key` reissue: the command leaves `<PATH>.pending` while the key changes, and a rerun with the same key and `--out` finishes the join file and removes the file.

Before it revokes, `reissue` refuses when the old key's issue record shows a JWT issued in the future, because the revocation would not cover that JWT; fix the clock. With no `--out` it also checks the existing creds file first: it must name this endpoint, hold a seed and a JWT for the same key, and match the directory's key and the account that signs. Move an unrelated file aside, as the hint says, and run again.

Two `mashctl` machines must not reissue the same endpoint at the same moment. The command checks the directory key after it writes and reports that another reissue changed the key if so; it cannot stop the race, and the older key stays revoked either way. On one host, a lock on the creds file's `.lock` file keeps two reissues of an address apart; the operating system releases it when the run ends or is killed. The lock works on a local filesystem, not across hosts or on a network filesystem that does not honour `flock`.

**A killed run** is finished by running the same command again. The new key waits in `<creds file>.pending` (or `<PATH>.pending` with `--out`). A reissue that stopped after the new credentials were written, or could not read the directory record back to confirm them, is finished the same way: it keeps the new key, confirms it and removes the `.pending` file, and does not revoke the new key.

## mashctl endpoint delete

```sh
mashctl endpoint delete ADDRESS [--yes] [--dry-run] [--json]
```

Deletes an endpoint, its inbox and its credentials. The address can never be used again. Without `--yes` it prints `This deletes <address> and the <N> messages in its inbox. It cannot be undone.` and asks you to type the address; any other text deletes nothing and exits 1. That needs a terminal: with no terminal and no `--yes` it prints `error: deleting needs a terminal to confirm, or --yes` and exits 2. The question comes after the endpoint is found and its messages counted, and before anything changes. In order, it:

1. revokes the endpoint's user key in the account JWT, on every server (see [Revocation](#revocation)); a revocation that some server did not confirm stops the command with nothing removed (`error: ADDRESS was not deleted: K of N servers did not confirm its key was cancelled`, with the same command as the hint);
2. removes the directory record and the inbox, so unread mail is lost;
3. burns the address: it is never assigned again, to any key;
4. renames the creds file, if one is in the config directories, to `<address>.creds.deleted` (`.deleted.1`, `.deleted.2` and so on if that name is taken; nothing is overwritten). The file is no longer counted as an installed endpoint.

On success it prints `Deleted endpoint <address>`; `--detail` adds `retired marker: written`, `servers acknowledged: N` and `credentials file: set aside as <path>`. `--dry-run` prints `Would delete endpoint <address> and the N messages in its inbox`, asks nothing and changes nothing. `--json` prints `{"address", "deleted", "messages_deleted"}`, and with `--dry-run` the same object plus `"dry_run": true`; a failure prints `{"error", "hint"}`. An address that does not exist is `error: <address> does not exist`, exit 1. A killed run finishes when you run the same command again. Exit codes: 0 deleted, 1 refused, partial or error, 2 usage error. After a partial delete the message is `error: deleted <address>, but <what is left>` with `hint: mashctl endpoint delete <address>`; running the same command again is safe and finishes it. The options `--force`, `--delete-creds` and `--no-revoke` no longer exist.

## mashctl endpoint list and show

```sh
mashctl endpoint list [PREFIX] [--wide] [--json]
mashctl endpoint show ADDRESS [--json]
```

`list` prints a table of the endpoints in the directory, sorted by address. PREFIX is an owner (`acme`) or an owner and group (`acme.demo`); only endpoints under it are listed. An owner or group that repeats the row above is left blank. STATE is `online` (a session holds the endpoint's lease), `offline`, or `suspended`. `--wide` adds CREATED (UTC), WAITING (messages not yet delivered) and LABELS (`-` when there are none). With no endpoint it prints `No endpoints.` It exits 0, also when there are none.

```text
OWNER  GROUP  NAME   STATE
acme   demo   alice  online
              bob    offline
       ops    carol  suspended
```

`show` prints one endpoint:

```text
address:    acme.demo.bob
state:      offline
created:    2026-11-19 09:40 UTC
waiting:    1 message
labels:     team=blue
public key: UDXU4RCSV3GJRXWQ4PNDKYEL7ZBSMSGGTJYNDQGHFM4QWHBRD3ZTQ4LX
```

`role`, `host`, `description`, `capability` and `note` lines appear when the record has them. It exits 1 when the endpoint does not exist. Both change nothing.

`--json` prints, for `list`, `{"endpoints": [{"address", "owner", "group", "name", "state"}]}`, and for `show`, `address`, `state`, `created` (RFC 3339), `waiting`, `labels`, `public_key` and the optional fields above.

## mashctl version

`mashctl version` prints `mashctl vVERSION (COMMIT)`, where COMMIT is the first seven characters of the commit the binary was built from (`unknown` when the build has none). `--json` prints `{"version": "VERSION", "commit": "COMMIT"}` with the whole commit, all 40 characters (`unknown` when the build has none).

## mash key create and mash join

These two run on the endpoint's machine and need no server and no admin credentials.

```sh
mash key create [--out FILE]
mash join FILE [--key SEED] [--replace] [--dry-run]
```

`mash key create` makes a user key pair. The seed goes to `FILE` (mode 0600; default `mash.seed` in the current folder), and the public key beside it, with `.pub` added to the whole seed path: `bob.seed` and `bob.seed.pub`. It makes a missing parent directory (mode 0700), refuses to overwrite either file (`error: bob.seed already exists; nothing was written`, exit 1), and prints `Created bob.seed and bob.seed.pub.` The key itself is never printed. `--json` prints `{"seed": "bob.seed", "public_key": "bob.seed.pub"}`, the two paths.

`mash join` installs the endpoint from a join file, the one text file `mashctl endpoint create --out` writes. It copies the servers and the certificate authority's certificate into the configuration folder (`config.json` `servers`, and `ca.pem` named by `ca`) and writes `<address>.creds` there (mode 0600). A join file made for a public key you supplied holds no seed: give your seed file with `--key`. One configuration folder serves one mesh, so a join file for other servers is refused (`--config-dir` names another folder). A join file for another certificate authority (compared with the file `config.json`'s `ca` names, or `ca.pem`), or none where the folder has one, or one where the folder has none and already holds an endpoint, is refused too, with or without `--replace`; the first join into an empty folder sets it. An endpoint already installed is refused unless `--replace` is given, which replaces its creds and nothing else. `--dry-run` says what it would do and writes nothing. It prints `Joined as alice.dev.design.`; with `--detail` the lines `servers:` and `creds:` follow; `--json` prints `{"endpoint"}` and `--detail --json` adds `servers` and `creds` (the creds file's absolute path). The join file's format is described in [the specification](../spec/mash.json) under `join`: JSON with `mash_join` (1), `address`, `servers`, `ca` (PEM text or null), and `creds` (the whole creds file) or `user_jwt`.

## Errors of create and reissue

Every failure is `error: WHAT` on standard error and at most one `hint: COMMAND`. A wrong command line exits 2 with the hint `mashctl endpoint create --help` (or `reissue`). Any other failure exits 1. A command that did part of its work exits 1, says what is left, and is safe to run again: the hint is the command to run.

| Error | Exit | Meaning |
| --- | --- | --- |
| `unknown option --x` | 2 | An option that does not exist. `--make-key`, `--jwt-out`, `--creds-out`, `--label`, `--owner`, `--group`, `--name` and the other metadata options are gone. |
| `not an endpoint address` | 2 | The address is not `owner.group.name`. |
| `--public-key needs --out, the join file to write` | 2 | A public key is signed for a machine that makes its own seed. |
| `ADDRESS already exists` | 1 | create: the endpoint exists. Use `endpoint reissue`. |
| `ADDRESS was deleted and its address cannot be used again` | 1 | create or reissue: the address was deleted. |
| `ADDRESS does not exist` | 1 | reissue: no such endpoint. |
| `PATH already exists` | 1 | The join file path is taken; nothing is written over it. |
| `no server is configured` | 1 | Set a server with `--server` or `MASH_SERVER`. Nothing was written. |
| `cannot read the CA file` | 1 | The configured CA file cannot be read. |
| `FILE must hold only certificates` | 1 | The configured CA file holds a private key, another block or text that is not PEM. Nothing is written. |
| `the folder DIR is not usable` | 1 | The `--out` folder can be written by other users. Nothing is written. |
| `no config folder to write the credentials file in` | 1 | Set `MASH_CONFIG_DIR`, or use `--out`. |
| `no account signing key` | 1 | Run `mashctl init --config FILE` first. |
| `the public key ... was already issued to another address`, `... was revoked` | 1 | A key is used at one address, once: make a new key with `mash key create`. A supplied key is checked before it is signed. |
| `the public key is already this endpoint's key` | 1 | reissue: nothing would be rotated. |
| `the staged key ... was revoked` | 1 | The key staged in the `.pending` file was revoked: remove the file and run again. |
| `another reissue of ADDRESS holds the lock` | 1 | The detail names its pid and host when readable. Run again later. A `.lock` directory made by an earlier mash is reported the same way; if no reissue is running, remove it once by hand. |
| `the creds file ... is not this endpoint's current creds`, `... exists but ...` | 1 | reissue: the creds file here is not this endpoint's. Move it aside, as the hint says. |
| `another reissue changed ADDRESS's key while this one ran` | 1 | The file this run wrote was removed. Run the command again. |
| `... issued in the future` | 1 | reissue: the old key's JWT was issued after now. Fix the clock. Nothing was changed. |
| `only N of M servers answered` | 1 | Fewer servers answered than the count says. Nothing was revoked. |
| `... cannot tell whether the public key ... is revoked` | 1 | A server did not answer. Nothing was changed. |
| server unreachable, directory not writable | 1 | Nothing was changed. |

`endpoint show` exits 1 when there is no such endpoint. `endpoint delete` refuses with a plain message and exit 1 when the address does not exist, too few servers answered the account lookup, the user key's issue time is in the future (fix the clock), the revocation could not be confirmed, or the record belongs to another user key. Nothing is changed in any of these cases.

## Removed commands

Three verbs from 0.5.0 are gone. Each exits 2 and prints its replacement.

| Removed | Use |
| --- | --- |
| `mashctl add` | `mashctl endpoint create alice.dev.design` |
| `mashctl reissue` | `mashctl endpoint reissue alice.dev.design` |
| `mashctl retire` | `mashctl endpoint delete alice.dev.design` |

Endpoints made by 0.5.0 do not work with 0.5.5. Make new ones with `endpoint create` and remove the old ones. Mail unread in an old endpoint is not carried over.

## Revocation

`endpoint delete` and `endpoint reissue` revoke the endpoint's NATS user in the same command (`mashctl endpoint suspend` does not). The revocation is an entry in the account JWT, by user public key and time: NATS refuses a user JWT for that key issued at or before the revocation time, and a connected user is closed when the server takes the account update, so credentials stop working everywhere the update reached. mashctl signs the update with the operator key, pushes it through the system account to every known server, requires every one to acknowledge it, and reads it back; if that cannot be shown it exits non-zero with `not confirmed` and running the command again is safe. An existing revocation is never lowered or removed.

`endpoint delete ADDRESS` revokes the user of ADDRESS's directory record. With no record there is nothing to revoke. There is no option to skip the revocation. The system account credentials come from `system_creds` in `config.json`, and the number of servers a revocation must be confirmed on is the number listed under `servers`.

Before a revocation is written, `endpoint delete` reads the issue record of each key and refuses, changing nothing, when any retained revision of it has an issue time after the revocation time: NATS honours a revocation only for credentials issued at or before it, so a credential dated in the future would stay valid. Every revision counts, not only the latest, because a credential reissued under the same key writes a new revision and, after a clock rollback, the newer revision can carry an earlier time than an older credential that is still held. The record keeps 64 revisions of a key; a key at that cap may have lost its oldest ones, so it is refused too (`the key has been re-recorded 64 times or more; its oldest issue times are no longer kept; cannot show that no earlier credential is dated in the future`) unless a retained revision is already future-dated, which gets the usual refusal. To proceed, fix the clock, or run again later once the clock is known to be right. A key with no record warns and the revocation goes on; a record that cannot be read, or has a revision whose `issued_at` is missing or malformed, refuses.

`endpoint delete` makes the same check, before the revocation and with the same clock reading, and refuses the same way with nothing changed. The check also refuses a key whose recorded history cannot be shown to be complete: the issue-record stream carries a `mash_issued_history` marker (`full` for a bucket made at 64 revisions, `raised:N:S` when `mashctl init --config` raised one from N), and a key of a bucket with no marker that has 8 or more revisions, or of a raised bucket that had N or more up to the raise, may have lost a future-dated one. The refusal says its issued history cannot be shown to be complete; revoke that key by hand in the account JWT, only after checking where it was issued ([ADR 0115](decisions/0115-a-revocation-needs-the-issue-record-to-show-a-keys-whole-history-and-the-bucket-says-so-in-its-stream-metadata.md)).

Because revocation is by key and time, a new credential for an endpoint needs a new user key (a new nkey), and a user JWT issued in the same second as the revocation is revoked too.

## Exit codes and arguments

mashctl exits with 0, 1 or 2 only: 0 it worked, 1 it failed (one line on standard error says why), 2 the command line was wrong or the command cannot run here, and nothing was attempted. Every verb exits 0 on success, 2 for a usage error (a wrong or missing argument, an unknown flag, a stray argument, an argument that fails validation) and 1 for any failure; `doctor` exits 1 for a problem, and for a warning too with `--strict` (see [doctor.md](doctor.md#exit-codes)). An argument that fails validation is named in the message but never printed back, so a secret pasted into the wrong place is not echoed. `mashctl endpoint permissions ADDRESS` prints the permission set of the endpoint's NATS user as JSON, and nothing else, on stdout; it reads no server. The keys are `pub` and `sub`, each with `allow` and `deny`; `deny` is always printed, as `[]` when it is empty. Errors are `error: WHAT` on standard error, then an optional `hint: COMMAND`; with `--json` the commands of this form print `{"error": ..., "hint": ...}` on standard output instead.

## Before you push with nsc

If you also edit the account with `nsc`, these steps keep it from undoing mashctl.

- Run `nsc pull -A` before any `nsc push`, so a push from a stale local copy does not overwrite a revocation mashctl made. `mashctl doctor` shows keys connected to a server that mash did not issue.
- `nsc push` rejects an account server URL that starts with `tls://`; give nsc a `nats://` URL. For a server with a private CA, nsc needs `SSL_CERT_FILE` set to the CA's PEM file (mashctl takes `ca` instead).
- `nsc revocations add-user` followed by an `nsc push` revokes a user; `mashctl endpoint delete` revokes the user of an endpoint itself.
- To cut off old creds and keep the endpoint, run `mashctl endpoint reissue ADDRESS` (or `--out PATH --public-key FILE`): it revokes the old key and names a new one. `mashctl endpoint delete ADDRESS` revokes the user and removes the endpoint for good.
- A suspended endpoint's stored mail stays readable. `mash send` to a suspended endpoint fails with "no response from stream" and does not say the endpoint is suspended; this is a known limitation.
