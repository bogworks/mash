# mashctl: creating, labeling and deleting endpoints

Endpoints are made by `mashctl endpoint create`, and every endpoint carries the same kind of permission set, the owner-line set for its address `owner.group.instance` (it may mail the endpoints of its own owner and read what it needs to; the owner is the fence). `mashctl endpoint permissions ADDRESS` prints that set. A NATS user is signed by the account signing key (`account.nk` in the key directory, `<config dir>/keys` or `MASH_KEYS_DIR`) and carries its permission set in its JWT; there is no policy file and no tag. See [ADR 0052](decisions/0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md).

`endpoint create`, `reissue`, `delete`, `list`, `show`, `set`, `unset`, `suspend`, `resume`, `inbox` and `permissions` take an endpoint as `owner.group.name`: dotted, three parts. A bare name, the flat key `owner_group_name` and the slash form are refused (exit 2).

`mashctl endpoint create`, `reissue`, `delete`, `list` and `show` are described [below](#mashctl-endpoint-create); `mash key create` and `mash credential create` make the endpoint's key and creds file.

## Prerequisites

Minting a user needs the account's identity key (`account.nk` in the key directory). `mashctl init` does not create that key: it only creates the directory and lease buckets on the server. The key comes from `mashctl init --local` (which makes a local server and its keys) or from `mashctl adopt` (which imports the account seed you already hold). Creds minted with that key authenticate at once: nothing has to be pushed to the account first.

## Connections and trust

`mashctl` refuses admin creds and system account creds (`--sys-creds`, `MASH_SYS_CREDS`, or `sys.creds` in the server directory) that other users can read, as mash does for endpoint creds: the file must be a regular file with no group or other permission bits (`chmod 600 FILE`). A symlink is followed and the file it points to is checked; a missing file keeps its own error.

Every connection requires TLS, to any host, loopback included: every verb that connects with the admin or system creds (and `adopt`) refuses a plain `nats://` URL with exit code 2, naming `insecure_plaintext` (`MASH_INSECURE_PLAINTEXT=1`), which allows plain text and prints a warning; use it only for a throwaway server in a test. A `tls://` connection fails when the server does not offer TLS. Local servers made by `init --local` use TLS with a local certificate authority it makes (`ca.pem` in the server directory), so they need no override: `init --local` writes a `tls://` URL and `ca` into the `config.json` it creates, and `init --local --start` connects over TLS to the loopback server it just started. `issued check`, `endpoint delete`, `endpoint reissue` and `adopt` verify the account JWT's signature, subject and issuer (the operator key, or a key declared by `<key dir>/operator.jwt`). Admin and system connections never add servers the server advertises. See [operations](operations.md#writing-the-account-jwt-revocation).

## Which server does mashctl change?

`mashctl` takes its server, owner and admin credentials from `config.json`; the endpoint's own `MASH_*` variables win over the file, and the global flags `--server`, `--ca`, `--creds` and `--endpoint` win over both. Its owner is the owner part of an explicit endpoint (`--endpoint`, `MASH_ENDPOINT` or `endpoint` in `config.json`; any group and name will do), else the `owner` key of `config.json`, which `init --local` and `adopt` write. It never comes from an installed endpoint's creds file, so installed endpoints do not change who `mashctl` acts as. Inside a mash agent session those variables are the session's, so `mashctl` would act on the session's own server. Before any verb that connects to a server, mashctl prints one line to stderr, `mashctl: target server <url> (from <source>), owner <owner> (from <source>), admin creds <path> (from <source>)`, for example `mashctl: target server tls://host:4222 (from MASH_SERVER), owner acme (from MASH_ENDPOINT), admin creds ... (from ...)` (a flag source reads `flag:--server`), and warns when an environment variable overrides a different `config.json` value or when `MASH_LAUNCH_TOKEN` shows the shell is inside a session. It informs and never refuses; offline verbs print nothing of it. To choose the target explicitly, pass `--server`, `--creds` and `--endpoint`, or use `env -u MASH_SERVER -u MASH_ENDPOINT -u MASH_ADMIN_CREDS -u MASH_CA mashctl ...`, or run outside the session. See [operations](operations.md#which-server-does-mashctl-change).

## Adopting an existing server

```sh
mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]
```

`adopt` is `init --local` for a NATS server you already run in operator mode with a full resolver and JetStream. It needs authority you already hold (the operator signing seed, the mash account's identity seed, system account creds), because a URL does not prove it and `mashctl` cannot create authority on a server it did not set up. It checks the files, the system creds, the account on the resolver, JetStream and the operator key without changing anything, then keeps the keys in the key store, copies the system creds to `<config dir>/server/sys.creds`, optionally records or mints admin creds, and writes `config.json` (`servers` as an array, `admin_creds`, and `ca` when the loaded configuration sets one; `tls_cert` and `tls_key` likewise). It changes nothing on the server except that `--mint-admin` records the minted admin user in the issue record (before the creds file is written; if that fails, adopt fails and writes no creds file); the cutover of a live account is a separate step you run (`mashctl init`, then `mashctl endpoint create` for each endpoint). `adopt` connect-tests the admin creds it ends with (given, minted or kept) before it reports success, and as that account user asks the server for the account's JetStream info: step 3 only reports what the account JWT asks for (its disk, streams and consumers), and step 6 reports whether the server enabled JetStream for the account. When it did not (the account's disk limit does not fit the server's JetStream budget), adopt fails and says why; without `--admin-creds` or `--mint-admin` there is no account user, and the output says the server was not asked. On an operator with strict signing key usage the account identity key cannot sign users, so `--mint-admin` is refused (nothing written); pass `--admin-creds FILE` with a user signed by one of the account's signing keys. See [Adopting an existing server](operations.md#adopting-an-existing-server) for the checks, the files and the refusals.

## The issue record

```sh
mashctl issued list  [--json]
mashctl issued check [--json] [--sys-creds FILE] [--servers N]
```

Every user JWT `mashctl` issues is written to the issue record, the `mash-issued` bucket, under the user's public key: a user minted with the account key (a kept key adds a revision under the same key, a rotated key adds an entry for the new key and leaves the old key's), and the admin user that `mashctl init` connects with (kind `admin`; `init --local --start` and `adopt --mint-admin` followed by `mashctl init` record it). The entry is written before the creds file or the directory record, so a user that can connect is always in the record, and a record that cannot be written stops the issue. An entry holds the public key, the address (the endpoint name), the owner, the kind (`endpoint` or `admin`), the tags, the generation (always 0: mash has no policy generation yet) the issue time (RFC 3339, UTC), the purpose and, once mash has revoked the key, the revocation time. The purpose is one of `issue` (a user minted with the account key, the admin user at `init`), `rotation` (the new key of a credential that rotates the user key) and `probe` (reserved for the policy gate's probe credentials; `mashctl` does not write it); a record from before the field reads as `issue`. `revoked_at` (RFC 3339, UTC) is left out until set: `endpoint delete` and `endpoint reissue` set it after the revocation succeeds, once and never cleared, and a key mash never recorded is skipped. A failure to set it prints `warning: user key U... is revoked, but the issue record was not updated with it: ...` on stderr and never fails or undoes the revocation. Nothing is removed from the record when an endpoint is deleted or its key is revoked: revocation is the account JWT's, and the record is the history of what was issued. `mashctl init` creates the bucket; a deployment from before the record gets it, hardened, the first time a user is minted and written. Users issued before the record existed are not in it until a credential is reissued under the same key.

`issued list` prints the latest entry of each key, with its purpose and, when set, `revoked TIME` (`--json`: `present`, `count`, `issued`, each entry with `purpose` and, when set, `revoked_at`).

`issued check` asks every server, through the system account (`--sys-creds FILE`, else `MASH_SYS_CREDS`, else the `sys.creds` next to `admin_creds`), which user public keys are connected in the MASH account, and compares them with the record. It reports, per server, any connected key mash did not issue (the key, the server, the account, the client's connection name, the key that signed the JWT and its tags), the recorded keys that are not connected (informational) and a count summary. No key is exempt, not even the admin user this command connects with: it must be in the record (`mashctl init` and `adopt --mint-admin` record it). The system account's users never appear in the MASH account's connection list. A connected key that the record holds as revoked is reported apart, as `REVOKED key K still connected`: the revocation did not take effect (revoke it again, and look at the account JWT). A recorded key of any purpose counts as issued by mash. Exit codes: 0 when no unrecorded key and no revoked key is connected and the scan was complete; 1 when a finding was found, even if the scan was incomplete; 2 when nothing was found but the scan was incomplete or failed: bad arguments, a connection failure, a server that did not answer, a server whose connection list changed while it was read and could not be read whole (a list that fits one page is one snapshot; a list of several pages is accepted only when two consecutive complete scans collect the same connections, and each server is scanned up to 3 times), or a server set that cannot be shown to be complete (a cluster needs `--servers N` or `MASH_SERVERS`, as `endpoint delete` does). Whenever the scan was incomplete the text says `INCOMPLETE` and names why, and the JSON has `complete: false` and `incomplete` with the reason, with or without a finding. `--json` prints `clean`, `complete`, `incomplete`, `account`, `record_present`, `servers` (`answered`, `missing`, `expected`), `unrecorded`, `revoked_connected`, `recorded_not_connected` and `summary`. Only public keys are printed. See [operations.md](operations.md#the-issue-record-and-the-drift-check) for what it can and cannot see.

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
- Both take `--dry-run` (print `Would suspend endpoint ADDRESS` or `Would resume endpoint ADDRESS` and `Nothing was changed.`) and `--json` (`{"address": ..., "state": ...}`). An endpoint with no inbox stream exits 1.

See [operations.md](operations.md#suspend-an-endpoints-inbound-mail).

## Auditing the population

`mashctl audit [--json] [--strict] [--creds-dir DIR] [--compare EARLIER.json]` lists what the server and the creds directories hold, changes nothing, and reports state, not use: it cannot see publishes, connections or endpoints with no stream. `--compare` adds what moved since an earlier `--json` report. Exit codes: 0 clean (or findings without `--strict`), 1 findings under `--strict` (orphans, unreadable items, movement found by `--compare`), 2 usage. A server that cannot be reached, unreadable required input and a bad `--compare` file also exit 1. See [audit.md](audit.md).

## mashctl endpoint inbox

```text
mashctl endpoint inbox ADDRESS [--after NUMBER] [--limit n] [--json]
```

Lists the messages stored in one endpoint's inbox and whether each was delivered, so a test can prove what the transport kept and delivered. It never shows a message's text. It is read only: it uses stream info, a direct get of a message by sequence and consumer info of the existing `deliver` consumer, and nothing else. It never creates, updates, binds or fetches from a consumer, never acknowledges, and writes nothing to the directory, a bucket or a file. It runs with the same admin credentials as `audit`.

- ADDRESS is read from the directory record, one key; an address that names no record is refused (exit 1).
- The default is the last 20 messages, oldest first. `--limit n` caps the count. `--after NUMBER` shows only the messages after that message number, as `mash inbox --after` does; with it the first n messages after the number are shown. A value that is not a whole number exits 2 with `error: --after must be a whole number`.
- Exit codes: 0 success, 1 a refusal (an unknown endpoint, no inbox stream) or it could not complete (no server, no credentials), 2 a usage error.

The text form prints a line with the count, then a table:

```text
1 message for acme.demo.bob, 1 not delivered.

NUMBER  FROM   WHEN    DELIVERED  KIND  SIZE
7       alice  2m ago  no         task  48 B
```

FROM is the sender's name when the sender is in the endpoint's owner and group, else its whole address, and `?` when the message names none. `--json` prints one object:

| field | meaning |
| --- | --- |
| `address` | the endpoint |
| `total` | the messages in the inbox stream |
| `undelivered` | the messages the `deliver` consumer has not delivered; all of them when the consumer does not exist |
| `messages[].number` | the stream sequence |
| `messages[].from` | the sender's address |
| `messages[].delivered` | `true` when the sequence is at or below the consumer's last delivered sequence |
| `messages[].kind` | the `Mash-Kind` header |
| `messages[].size` | payload size in bytes |

Delivered comes from the consumer's cursors alone. NATS does not report acknowledgements above the ack floor, so a delivered message may be unacknowledged or acknowledged out of order, and the server does not say which. A per-message delivery count and ack time are not kept by JetStream or mash.

See also [audit.md](audit.md) for the consumer fields (`delivered`, `ack_floor`, `num_redelivered`, `pending`, `ack_pending`) the audit reports per inbox.

## mashctl endpoint create

```sh
mashctl endpoint create ADDRESS [--out PATH] [--public-key PATH] [--servers N] [--sys-creds FILE] [--dry-run] [--json]
```

Creates an endpoint: it signs the endpoint's user key with the account signing key, writes the directory record and the inbox, and records the key in the issue record. The record, the inbox and the issue record are written before the creds file or join file, so a user that can connect is always on record.

**The address** is `owner.group.name`: three tokens joined by dots. Other forms, such as `alice/dev/design` or a name alone, are a usage error. A group cannot be changed later: delete the endpoint and create a new one. The old address is never reused. Role, host, labels and the other directory fields are set after the endpoint exists, with `mashctl endpoint set`.

**With no option** the key pair is made on this host and `<config dir>/<address>.creds` is written (mode 0600, never over an existing file). The endpoint can be used on this machine at once. The `mashctl` host holds the key while the command runs. The key is kept in `<creds file>.pending` until the creds file is written.

**With `--out PATH`** the key pair is made, the user JWT is signed, and nothing is installed on this machine. The command writes a join file at `PATH` for another machine. Take that file to the endpoint's machine and run `mash join` there. The join file holds the seed: move it as you would a creds file.

**With `--public-key PATH`** (which needs `--out`) the command signs a public key that the endpoint made with `mash key create`. No seed is made, and the join file holds the user JWT and no seed. On the endpoint's machine, `mash join` takes the seed with `--key`. The seed never reaches the `mashctl` host.

A key is used at one address, once. A key that was issued before, or revoked, is refused.

**The join file** is JSON, mode 0600, written to a temporary file in the same folder and then renamed. A path that exists is refused. It has these fields and no others:

| Field | Holds |
| --- | --- |
| `mash_join` | `1`, the version of the format |
| `address` | the endpoint, as `owner.group.name` |
| `servers` | the server addresses from the configuration |
| `ca` | the certificate authority certificate as PEM text, read from the configured CA file, or `null` when none is configured |
| `creds` | the whole text of the NATS creds file, user JWT and seed (without `--public-key`) |
| `user_jwt` | the user JWT alone (with `--public-key`) |

**Other options.** `--servers N` and `--sys-creds FILE` name the server count and the system account creds for a cluster, as the other verbs do. `--dry-run` connects, checks everything, says what it would do and changes nothing. `--json` prints one JSON object.

**What it prints.** On success:

```text
Created endpoint alice.dev.design
```

With `--out` a second line names the file:

```text
Created endpoint alice.dev.design
Join file: alice.dev.design.join
```

With `--verbose` the lines `servers: ...`, `credentials file: PATH` (or `join file: PATH`) and `public key: KEY` follow. With `--dry-run`:

```text
Would create endpoint alice.dev.design
Nothing was changed.
```

With `--json`, `{"address": "alice.dev.design", "installed_here": true, "join_file": null}`. With `--out`, `installed_here` is `false` and `join_file` is the path. Neither the seed nor the JWT is printed.

**Errors** are one line on standard error, `error: WHAT`, and at most one line `hint: COMMAND`; with `--json` they are `{"error": ..., "hint": ...}` on standard output. Exit codes: 0 done or planned, 1 failed, 2 the command line was wrong. For example:

```text
error: alice.dev.design already exists
error: alice.dev.design was deleted and its address cannot be used again
error: --public-key needs --out, the join file to write
hint: mashctl endpoint create --help
```

**A killed run.** Run the same command again. The key waits in `<creds file>.pending` (or `<PATH>.pending` with `--out`), so the second run finishes with the same key and removes the file. With `--public-key` the second run signs again and writes the join file. A creds file or inbox left by a run that never completed shows as an orphan in `mashctl audit`.

## mashctl endpoint reissue

```sh
mashctl endpoint reissue ADDRESS [--out PATH] [--public-key PATH] [--servers N] [--sys-creds FILE] [--dry-run] [--json]
```

Gives an existing endpoint new credentials and cancels the old ones. It revokes the old key on every server, then makes the directory record name the new key. The inbox and its unread mail stay. A session that runs as the endpoint must restart to read the new creds. The options work as for `create`.

With no option the new key pair is made on this host and `<config dir>/<address>.creds` is replaced. With `--out PATH` the command writes a join file for another machine and installs nothing here. With `--out PATH --public-key PATH` it signs the endpoint's new public key and the join file holds the user JWT. `--public-key` needs `--out`.

On success it prints `Reissued credentials for alice.dev.design`, then `Join file: PATH` with `--out`. With `--verbose` it adds `servers`, `credentials file` or `join file`, `servers acknowledged` and `public key` lines. With `--json` it prints `{"address": "alice.dev.design", "join_file": null}`; with `--out` `join_file` is the path. There is no bulk form: reissue takes one address.

`--public-key` naming the key the endpoint already has is refused: nothing would be rotated. Make a new key with `mash key create --out NAME.seed`. The one exception is a rerun after a killed `--public-key` reissue: the command leaves `<PATH>.pending` while the key changes, and a rerun with the same key and `--out` finishes the join file and removes the file.

Before it revokes, `reissue` refuses when the old key's issue record shows a JWT issued in the future, because the revocation would not cover that JWT; fix the clock. With no `--out` it also checks the existing creds file first: it must name this endpoint, hold a seed and a JWT for the same key, and match the directory's key and the account that signs. Move an unrelated file aside, as the hint says, and run again.

Two `mashctl` machines must not reissue the same endpoint at the same moment. The command checks the directory key after it writes and reports that another reissue changed the key if so; it cannot stop the race, and the older key stays revoked either way. On one host, a lock on the creds file's `.lock` file keeps two reissues of an address apart; the operating system releases it when the run ends or is killed. The lock works on a local filesystem, not across hosts or on a network filesystem that does not honour `flock`.

**A killed run** is finished by running the same command again. The new key waits in `<creds file>.pending` (or `<PATH>.pending` with `--out`). A reissue that stopped after the new credentials were written, or could not read the directory record back to confirm them, is finished the same way: it keeps the new key, confirms it and removes the `.pending` file, and does not revoke the new key.

## mashctl endpoint delete

```sh
mashctl endpoint delete ADDRESS [--yes] [--dry-run] [--json] [--servers N] [--sys-creds FILE]
```

Deletes an endpoint, its inbox and its credentials. The address can never be used again. Without `--yes` it prints `This deletes <address> and the <N> messages in its inbox. It cannot be undone.` and asks you to type the address; any other text deletes nothing and exits 1. That needs a terminal: with no terminal and no `--yes` it prints `error: deleting needs a terminal to confirm, or --yes` and exits 2. The question comes after the endpoint is found and its messages counted, and before anything changes. In order, it:

1. revokes the endpoint's user key in the account JWT, on every server (see [Revocation](#revocation)); a revocation that cannot be confirmed stops the command with nothing removed;
2. removes the directory record and the inbox, so unread mail is lost;
3. burns the address: it is never assigned again, to any key;
4. renames the creds file, if one is in the config directories, to `<address>.creds.retired-<UTC time>`.

On success it prints `Deleted endpoint <address>`; `--verbose` adds `retired marker: written`, `servers acknowledged: N` and `credentials file: renamed to <path>`. `--dry-run` prints `Would delete endpoint <address> and the N messages in its inbox` and `Nothing was changed.`, asks nothing and changes nothing. `--json` prints `{"address", "deleted", "messages_deleted"}`; a failure prints `{"error", "hint"}`. An address that does not exist is `error: <address> does not exist`, exit 1. A killed run finishes when you run the same command again. Exit codes: 0 deleted, 1 refused, partial or error, 2 usage error. After a partial delete the message is `error: deleted <address>, but <what is left>` with `hint: mashctl endpoint delete <address>`; running the same command again is safe and finishes it. The options `--force`, `--delete-creds` and `--no-revoke` no longer exist.

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

`mashctl version` prints `mashctl vVERSION (COMMIT)`, where COMMIT is the first seven characters of the commit the binary was built from (`unknown` when the build has none). `--json` prints `{"version": "VERSION", "commit": "COMMIT"}`.

## mash key create and mash credential create

These two run on the endpoint's machine and need no server and no admin credentials.

```sh
mash key create --out FILE [--pub-out FILE] [--json] [--dry-run]
mash credential create --seed FILE --jwt FILE [--out CREDS] [--force] [--json] [--dry-run]
```

`mash key create` makes a user key pair. The seed goes to `FILE` (mode 0600), and the public key to the same name with `.seed` replaced by `.pub` (or to `--pub-out`). It makes a missing parent directory (mode 0700), refuses to overwrite either file, and prints the public key and both paths, never the seed.

`mash credential create` joins a seed and a JWT file into a creds file (mode 0600). By default the file is `<config dir>/<JWT name>.creds`; `--out` names another. It checks the structure of the JWT, its self-signature, that it is a user JWT, and that it names the seed's key (`key_mismatch` if not). It does not check the issuing account or the expiry. It refuses to replace an existing file unless `--force` is given, and it prints the path and the public key, never the seed or the JWT.

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
| `no config folder to write the credentials file in` | 1 | Set `MASH_CONFIG_DIR`, or use `--out`. |
| `no account signing key` | 1 | Run `mashctl init` first. |
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

`endpoint delete ADDRESS` revokes the user of ADDRESS's directory record. With no record there is nothing to revoke. There is no option to skip the revocation. The system credentials and server count come from `MASH_SYS_CREDS` and `MASH_SERVERS`.

Before a revocation is written, `endpoint delete` reads the issue record of each key and refuses, changing nothing, when any retained revision of it has an issue time after the revocation time: NATS honours a revocation only for credentials issued at or before it, so a credential dated in the future would stay valid. Every revision counts, not only the latest, because a credential reissued under the same key writes a new revision and, after a clock rollback, the newer revision can carry an earlier time than an older credential that is still held. The record keeps 64 revisions of a key; a key at that cap may have lost its oldest ones, so it is refused too (`the key has been re-recorded 64 times or more; its oldest issue times are no longer kept; cannot show that no earlier credential is dated in the future`) unless a retained revision is already future-dated, which gets the usual refusal. To proceed, fix the clock, or run again later once the clock is known to be right. A key with no record warns and the revocation goes on; a record that cannot be read, or has a revision whose `issued_at` is missing or malformed, refuses.

`endpoint delete` makes the same check, before the revocation and with the same clock reading, and refuses the same way with nothing changed. The check also refuses a key whose recorded history cannot be shown to be complete: the issue-record stream carries a `mash_issued_history` marker (`full` for a bucket made at 64 revisions, `raised:N:S` when `mashctl init` raised one from N), and a key of a bucket with no marker that has 8 or more revisions, or of a raised bucket that had N or more up to the raise, may have lost a future-dated one. The refusal says its issued history cannot be shown to be complete; revoke that key by hand in the account JWT, only after checking where it was issued ([ADR 0115](decisions/0115-a-revocation-needs-the-issue-record-to-show-a-keys-whole-history-and-the-bucket-says-so-in-its-stream-metadata.md)).

Because revocation is by key and time, a new credential for an endpoint needs a new user key (a new nkey), and a user JWT issued in the same second as the revocation is revoked too.

## Exit codes and arguments

mashctl exits with 0, 1 or 2 only: 0 it worked, 1 it failed (one line on standard error says why), 2 the command line was wrong or the command cannot run here, and nothing was attempted. Every verb exits 0 on success, 2 for a usage error (a wrong or missing argument, an unknown flag, a stray argument, an argument that fails validation) and 1 for any failure; `audit` exits 1 for `--strict` findings and when it could not complete (see [audit.md](audit.md#exit-codes)). An argument that fails validation is named in the message but never printed back, so a secret pasted into the wrong place is not echoed. `mashctl endpoint permissions ADDRESS` prints the permission set of the endpoint's NATS user as JSON, and nothing else, on stdout; it reads no server. The keys are `pub` and `sub`, each with `allow` and `deny`; `deny` is always printed, as `[]` when it is empty. Errors are `error: WHAT` on standard error, then an optional `hint: COMMAND`; with `--json` the commands of this form print `{"error": ..., "hint": ...}` on standard output instead.

## Before you push with nsc

If you also edit the account with `nsc`, these steps keep it from undoing mashctl.

- Run `nsc pull -A` before any `nsc push`, so a push from a stale local copy does not overwrite a revocation mashctl made. `mashctl issued check` shows keys connected to a server that mash did not issue.
- `nsc push` rejects an account server URL that starts with `tls://`; give nsc a `nats://` URL. For a server with a private CA, nsc needs `SSL_CERT_FILE` set to the CA's PEM file (mashctl takes `ca` instead).
- `nsc revocations add-user` followed by an `nsc push` revokes a user; `mashctl endpoint delete` revokes the user of an endpoint itself.
- To cut off old creds and keep the endpoint, run `mashctl endpoint reissue ADDRESS` (or `--out PATH --public-key FILE`): it revokes the old key and names a new one. `mashctl endpoint delete ADDRESS` revokes the user and removes the endpoint for good.
- A suspended endpoint's stored mail stays readable. `mash send` to a suspended endpoint fails with "no response from stream" and does not say the endpoint is suspended; this is a known limitation.
