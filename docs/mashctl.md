# mashctl: creating, labeling and deleting endpoints

Endpoints are made by `mashctl endpoint create`, and every endpoint carries the same kind of permission set, the owner-line set for its address `owner.group.instance` (it may mail the endpoints of its own owner and read what it needs to; the owner is the fence). `mashctl permissions ADDRESS` prints that set. A NATS user is signed by the account signing key (`account.nk` in the key directory, `<config dir>/keys` or `MASH_KEYS_DIR`) and carries its permission set in its JWT; there is no policy file and no tag. See [ADR 0052](decisions/0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md).

Every verb that names an endpoint takes its address, `owner.group.instance`, or the flat key `owner_group_instance`. A bare name is refused (exit 2).

`mashctl endpoint create`, `reissue`, `delete`, `list` and `show` are described [below](#mashctl-endpoint-create); `mash key create` and `mash credential create` make the endpoint's key and creds file. `mashctl provision` writes only a directory record, for a user you signed yourself.

## Prerequisites

Minting a user needs the account's identity key (`account.nk` in the key directory). `mashctl init` does not create that key: it only creates the directory and lease buckets on the server. The key comes from `mashctl init --local` (which makes a local server and its keys) or from `mashctl adopt` (which imports the account seed you already hold). Creds minted with that key authenticate at once: nothing has to be pushed to the account first.

## Connections and trust

`mashctl` refuses admin creds and system account creds (`--sys-creds`, `MASH_SYS_CREDS`, or `sys.creds` in the server directory) that other users can read, as mash does for endpoint creds: the file must be a regular file with no group or other permission bits (`chmod 600 FILE`). A symlink is followed and the file it points to is checked; a missing file keeps its own error.

Plain `nats://` to a non-loopback host is refused for admin and system connections: every verb that connects with the admin or system creds (and `adopt`) requires TLS, and fails when the server does not offer it. Local servers made by `init --local` are loopback-only and opt in explicitly: `init --local` writes `"insecure_loopback": true` into the `config.json` it creates (the one minimal escape, a setting and not a flag, which may be renamed when the command-line settings are redesigned; the same setting is `MASH_INSECURE_LOOPBACK=1`, honoured only for loopback URLs), and `init --local --start` connects only to the loopback server it just started, which needs no opt-in. `issued check`, `endpoint delete`, `endpoint reissue`, `deprovision` and `adopt` verify the account JWT's signature, subject and issuer (the operator key, or a key declared by `<key dir>/operator.jwt`). Admin and system connections never add servers the server advertises, and with `insecure_loopback` dial only localhost or a loopback address. See [operations](operations.md#writing-the-account-jwt-revocation).

## Which server does mashctl change?

`mashctl` takes its server, owner and admin credentials from `config.json`, and the endpoint's own `MASH_*` variables win over the file; there is no flag above them. Inside a mash agent session those variables are the session's, so `mashctl` would act on the session's own server. Before any verb that connects to a server, mashctl prints one line to stderr, `mashctl: target server <url> (from <source>), owner <owner> (from <source>), admin creds <path> (from <source>)`, and warns when an environment variable overrides a different `config.json` value or when `MASH_NAME` or `MASH_LAUNCH_TOKEN` shows the shell is inside a session. It informs and never refuses; offline verbs print nothing of it. A flag or context to choose the target is deferred to the CLI redesign (the CLI is frozen until then): use `env -u MASH_URL -u MASH_OWNER -u MASH_ADMIN_CREDS -u MASH_TLS_CA mashctl ...`, or run outside the session. See [operations](operations.md#which-server-does-mashctl-change).

## Adopting an existing server

```sh
mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]
```

`adopt` is `init --local` for a NATS server you already run in operator mode with a full resolver and JetStream. It needs authority you already hold (the operator signing seed, the mash account's identity seed, system account creds), because a URL does not prove it and `mashctl` cannot create authority on a server it did not set up. It checks the files, the system creds, the account on the resolver, JetStream and the operator key without changing anything, then keeps the keys in the key store, copies the system creds to `<config dir>/server/sys.creds`, optionally records or mints admin creds, and writes `config.json` (`url`, `owner`, `admin_creds`, and `tls_ca` when the loaded configuration sets one; `tls_cert` and `tls_key` likewise). It changes nothing on the server except that `--mint-admin` records the minted admin user in the issue record (before the creds file is written; if that fails, adopt fails and writes no creds file); the cutover of a live account is a separate step you run (`mashctl init`, then `mashctl endpoint create` for each endpoint). `adopt` connect-tests the admin creds it ends with (given, minted or kept) before it reports success, and as that account user asks the server for the account's JetStream info: step 3 only reports what the account JWT asks for (its disk, streams and consumers), and step 6 reports whether the server enabled JetStream for the account. When it did not (the account's disk limit does not fit the server's JetStream budget), adopt fails and says why; without `--admin-creds` or `--mint-admin` there is no account user, and the output says the server was not asked. On an operator with strict signing key usage the account identity key cannot sign users, so `--mint-admin` is refused (nothing written); pass `--admin-creds FILE` with a user signed by one of the account's signing keys. See [Adopting an existing server](operations.md#adopting-an-existing-server) for the checks, the files and the refusals.

## The issue record

```sh
mashctl issued list  [--json]
mashctl issued check [--json] [--sys-creds FILE] [--servers N]
```

Every user JWT `mashctl` issues is written to the issue record, the `mash-issued` bucket, under the user's public key: a user minted with the account key (a kept key adds a revision under the same key, a rotated key adds an entry for the new key and leaves the old key's), and the admin user that `mashctl init` connects with (kind `admin`; `init --local --start` and `adopt --mint-admin` followed by `mashctl init` record it). The entry is written before the creds file or the directory record, so a user that can connect is always in the record, and a record that cannot be written stops the issue. An entry holds the public key, the address (the endpoint name), the owner, the kind (`endpoint` or `admin`), the tags, the generation (always 0: mash has no policy generation yet) the issue time (RFC 3339, UTC), the purpose and, once mash has revoked the key, the revocation time. The purpose is one of `issue` (a user minted with the account key, the admin user at `init`), `rotation` (the new key of a credential that rotates the user key) and `probe` (reserved for the policy gate's probe credentials; `mashctl` does not write it); a record from before the field reads as `issue`. `revoked_at` (RFC 3339, UTC) is left out until set: `endpoint delete`, `endpoint reissue` and `deprovision` set it after the revocation succeeds, once and never cleared, and a key mash never recorded is skipped. A failure to set it prints `warning: user key U... is revoked, but the issue record was not updated with it: ...` on stderr and never fails or undoes the revocation. Nothing is removed from the record when an endpoint is deleted or its key is revoked: revocation is the account JWT's, and the record is the history of what was issued. `mashctl init` creates the bucket; a deployment from before the record gets it, hardened, the first time a user is minted and written. Users issued before the record existed are not in it until a credential is reissued under the same key.

`issued list` prints the latest entry of each key, with its purpose and, when set, `revoked TIME` (`--json`: `present`, `count`, `issued`, each entry with `purpose` and, when set, `revoked_at`).

`issued check` asks every server, through the system account (`--sys-creds FILE`, else `MASH_SYS_CREDS`, else the `sys.creds` next to `admin_creds`), which user public keys are connected in the MASH account, and compares them with the record. It reports, per server, any connected key mash did not issue (the key, the server, the account, the client's connection name, the key that signed the JWT and its tags), the recorded keys that are not connected (informational) and a count summary. No key is exempt, not even the admin user this command connects with: it must be in the record (`mashctl init` and `adopt --mint-admin` record it). The system account's users never appear in the MASH account's connection list. A connected key that the record holds as revoked is reported apart, as `REVOKED key K still connected`: the revocation did not take effect (revoke it again, and look at the account JWT). A recorded key of any purpose counts as issued by mash. Exit codes: 0 when no unrecorded key and no revoked key is connected and the scan was complete; 1 when a finding was found, even if the scan was incomplete; 2 when nothing was found but the scan was incomplete or failed: bad arguments, a connection failure, a server that did not answer, a server whose connection list changed while it was read and could not be read whole (a list that fits one page is one snapshot; a list of several pages is accepted only when two consecutive complete scans collect the same connections, and each server is scanned up to 3 times), or a server set that cannot be shown to be complete (a cluster needs `--servers N` or `MASH_SERVERS`, as `endpoint delete` does). Whenever the scan was incomplete the text says `INCOMPLETE` and names why, and the JSON has `complete: false` and `incomplete` with the reason, with or without a finding. `--json` prints `clean`, `complete`, `incomplete`, `account`, `record_present`, `servers` (`answered`, `missing`, `expected`), `unrecorded`, `revoked_connected`, `recorded_not_connected` and `summary`. Only public keys are printed. See [operations.md](operations.md#the-issue-record-and-the-drift-check) for what it can and cannot see.

## Labels

```sh
mashctl label ADDRESS k=v [k=v]... [--dry-run]
mashctl unlabel ADDRESS k [k]... [--dry-run]
```

These set and clear labels on the endpoint's directory record (what `who --long` shows). They connect with the admin credentials and need no file and no creds: the endpoint must have a directory record, and keys and values are checked as the directory checks labels. `label` prints `labeled ADDRESS: k=v ...` and `unlabel` prints `unlabeled ADDRESS: -k ...`; a change that leaves the labels as they are prints `ADDRESS: nothing to change`. `unlabel` of a label that is not set warns and exits 0. `--dry-run` says what would change and writes nothing. There are no `--policy` and `--sync` flags (both are unknown options): the record is the only place a label lives.

Labels do not change a minted user: the permissions of an endpoint do not depend on them, so setting one leaves nothing to reissue.
## Suspending an endpoint's inbound mail

- `mashctl suspend ADDRESS` refuses all inbound mail to ADDRESS's inbox and prints `suspended ADDRESS: id ID, last_seq N, msgs M (new mail parked; the endpoint's credentials are still valid)`. Suspend does not revoke: it is reversible, and the endpoint can still connect with its creds. The id is a random token naming this one suspension. Stored mail stays readable; the sequence is final while the suspension holds.
- `mashctl resume ADDRESS` restores delivery and prints `resumed ADDRESS`.
- `mashctl suspended ADDRESS` prints `suspended ADDRESS since T: id ID, last_seq N, msgs M` and exits 0, or `ADDRESS is not suspended` and exits 3. Any error exits 1.
- To remove an endpoint after reading its last mail: `mashctl suspend ADDRESS` (note `id ID` and `last_seq N`), drain through N, then `mashctl deprovision ADDRESS --suspension ID --drained-to N` (both flags are required together; either alone is a usage error). The deprovision refuses, changing nothing, unless the inbox is still suspended under that same id and at last_seq N when it runs, and the directory record still belongs to the user key it had at suspend time, so a `resume` by another admin cannot lose mail. The id ties the drain to that one suspension, so a re-provisioned endpoint of the same address cannot be removed by an old command. Example: `mashctl deprovision alice.dev.design --suspension 9f3c... --drained-to 41`. The revocation of the endpoint's user key happens in this command, only once those checks hold (see below). Plain `deprovision` does not check, and `--drained-to` cannot be combined with `--user`.

See [operations.md](operations.md#suspend-an-endpoints-inbound-mail).

## Auditing the population

`mashctl audit [--json] [--strict] [--creds-dir DIR] [--compare EARLIER.json]` lists what the server and the creds directories hold, changes nothing, and reports state, not use: it cannot see publishes, connections or endpoints with no stream. `--compare` adds what moved since an earlier `--json` report. Exit codes: 0 clean (or findings without `--strict`), 1 findings under `--strict` (orphans, unreadable items, movement found by `--compare`), 2 usage, 3 the audit could not complete (server unreachable, unreadable required input, bad `--compare` file). See [audit.md](audit.md).

## mashctl inbox inspect

```text
mashctl inbox inspect <address> [--json] [--from SEQ] [--to SEQ] [--limit N]
```

Shows one endpoint's stored messages and where its `deliver` consumer stands, so a test can prove what the transport kept and delivered. It is read only: it uses stream info, a direct get of a message by sequence and consumer info of the existing `deliver` consumer, and nothing else. It never creates, updates, binds or fetches from a consumer, never acknowledges, and writes nothing to the directory, a bucket or a file. It runs with the same admin credentials as `audit`.

- `address` is `owner.group.instance` or the flat key `owner_group_instance`. It is read from the directory record, one key; an address that names no record is refused (exit 1).
- The default is the last 20 messages, oldest first. `--limit N` caps the count. `--from SEQ` and `--to SEQ` are an inclusive stream-sequence range, clamped to the stream's first and last sequence; with `--from` the first N messages from it are shown.
- Exit codes: 0 success, 1 a refusal (an unknown endpoint, no inbox stream), 2 a usage error, 3 it could not complete (no server, no credentials).

The text form prints the stream and consumer totals, a table of messages and a `note:` line for each limit. `--json` prints one object. The schema string and the field names are a stable contract; they change only when a field is removed or changes meaning.

| field | meaning |
| --- | --- |
| `schema` | `mash-inbox-inspect@1` |
| `address` | the address as given |
| `stream` | `name`, `first_seq`, `last_seq`, `messages`, `bytes` of the inbox stream |
| `consumer` | the `deliver` consumer, or `null` when it does not exist (every message is then `undelivered`): `name`; `ack_floor` and `delivered`, each `{stream_seq, consumer_seq}`; `num_pending`; `num_redelivered`; `num_ack_pending` |
| `messages[].seq` | the stream sequence |
| `messages[].msg_id` | the `Nats-Msg-Id` header, empty when there is none |
| `messages[].sender` | the sender's address: from the directory for `verified` and `stale`, the signed address for the others, `?` when the header names none |
| `messages[].trust` | `verified`, `claimed`, `unknown-signer`, `stale`, `forged`, `malformed` or `absent`, from the stored `Agent-Sender` header (forged and malformed mail is shown, not hidden) |
| `messages[].stored_at` | the stream's store time, RFC 3339 UTC |
| `messages[].size` | payload size in bytes |
| `messages[].kind` | the `Mash-Kind` header |
| `messages[].state` | `acked`, `undelivered` or `delivered-unacked-or-acked-out-of-order` (text form: `delivered, ack not confirmed`) |
| `messages[].delivery_count`, `messages[].ack_time` | always `null` |
| `limits` | the strings below |

The state comes from the consumer's cursors alone: `acked` when the sequence is at or below `ack_floor.stream_seq`, `undelivered` when it is above `delivered.stream_seq`, otherwise `delivered-unacked-or-acked-out-of-order`, because NATS does not report acknowledgements above the ack floor. The trust label is worked out as the receiving endpoint does, except that the signer is looked up by the address it signed (one directory key read) instead of by listing the directory: a signature by a key that is not on that address's record is `unknown-signer`, and a signed header with no name is too.

Limits, also in the `limits` array: per-message delivery count and ack time are not kept by JetStream or mash; the consumer's `num_redelivered` is the aggregate. A message above the ack floor and at or below the last delivered sequence may be unacknowledged or acknowledged out of order, and the server does not say which.

See also [audit.md](audit.md) for the consumer fields (`delivered`, `ack_floor`, `num_redelivered`, `pending`, `ack_pending`) the audit reports per inbox.

## mashctl endpoint create

```sh
mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key)
                        [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]...
                        [--servers N] [--sys-creds FILE] [--dry-run] [--json]
```

Creates an endpoint: it signs the endpoint's user key with the account signing key, writes the directory record and the inbox, and records the key in the issue record. The record, the inbox and the issue record are written before the JWT or creds file, so a user that can connect is always on record.

**Naming.** Write the address one of three ways:

- `mashctl endpoint create alice/dev/design ...`: owner, group and instance in one argument. This is the usual form. The dotted form `alice.dev.design` also works.
- `mashctl endpoint create design --group dev ...`: one token is the instance. The owner comes from `--owner`, `MASH_OWNER` or `owner` in `config.json`.
- `mashctl endpoint create --owner alice --group dev --name design ...`: every token by flag.

Two tokens (`dev/design`) are refused as ambiguous. A group is always named: there is no default and no `config.json` key for it, and the environment does not supply one. An address and a flag that name different values are refused (`naming_disagrees`); the same value twice is fine. A group cannot be changed later: delete the endpoint and create a new one. The old address is never reused.

**The key.** Give exactly one of two flags.

- `--public-key FILE` signs the public key the endpoint made with `mash key create`. The JWT goes to `--jwt-out FILE`, by default `<owner>.<group>.<instance>.jwt` in the current directory, and is never overwritten. The endpoint then runs `mash credential create` to join the JWT and its seed. The seed never reaches the `mashctl` host. Use this form when the endpoint runs on another machine ([operations.md](operations.md#an-endpoint-on-another-machine)).
- `--make-key` makes the key on this host and writes `<config dir>/<address>.creds` (mode 0600). The `mashctl` host makes and holds the key while the command runs, so use it only when the endpoint runs on this host. The key is kept in `<creds file>.pending` until the creds file is written.

A key is used at one address, once. A key that was issued before, or revoked, is refused (`public_key_issued`, `key_revoked`).

**Other flags.** `--label k=v` (repeatable) sets directory labels, `--tool` adds the label `kind=tool`, `--note N` and `--describe T` set the note and the description, and `--cap C` (repeatable) adds capabilities. These are checked as `set-meta` checks them. `--servers N` and `--sys-creds FILE` name the server count and the system account creds for a cluster, as the other verbs do. `--dry-run` connects, checks everything, says what it would do and changes nothing. `--json` prints one JSON document (schema `mashctl-endpoint@1`).

**What it prints.** On success, with `--public-key`:

```text
created endpoint alice/dev/design
  address  alice.dev.design
  jwt      ./alice.dev.design.jwt
next: on the endpoint's machine, run: mash credential create --seed design.seed --jwt alice.dev.design.jwt
```

With `--make-key` the third line is `  creds    <config dir>/alice.dev.design.creds` and the last line is `next: start a session as this endpoint: MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=design`. Progress, warnings and errors go to standard error.

**Exit codes.** 0 done or planned, 1 refused, 2 usage error, 6 error (a server or a file failed). A refusal and an error name a code in the message, for example `refused (address_taken)`, and the [table below](#refusal-codes) lists them.

**A killed run.** Run the same command again. With `--make-key` the key waits in `<creds file>.pending`, so the second run finishes with the same key. With `--public-key` the second run writes the JWT again (if you removed the first, or it was never written). A creds file or inbox left by a run that never completed shows as an orphan in `mashctl audit`.

## mashctl endpoint reissue

```sh
mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE]
                         [--servers N] [--sys-creds FILE] [--dry-run] [--json]
mashctl endpoint reissue (--all | --changed) --make-key [--yes] [--servers N] [--sys-creds FILE] [--dry-run] [--json]
```

Gives an existing endpoint a new key. It revokes the old key on every server, then makes the directory record name the new key. The inbox and its unread mail stay. A session that runs as the endpoint must restart to read the new creds. `--public-key FILE`, `--make-key`, `--jwt-out`, `--dry-run` and `--json` work as for `create`. With `--make-key` the new creds replace `<config dir>/<address>.creds`.

`--public-key` naming the key the endpoint already has is refused (`key_unchanged`): nothing would be rotated. Make a new key with `mash key create --out NAME.seed`. The one exception is a rerun after a killed `--public-key` reissue: the command leaves `<jwt-out>.pending` while the key changes, and a rerun with the same key and `--jwt-out` finishes the JWT and removes the file.

Before it revokes, `reissue` refuses when the old key's issue record shows a JWT issued in the future (`future_issued`: fix the clock), because the revocation would not cover that JWT. With `--make-key` it also checks the existing creds file first: it must name this endpoint, hold a seed and a JWT for the same key, match the directory's key and the account that signs (`creds_mismatch`; an unreadable file is `creds_unreadable`). Move an unrelated file aside and run again.

**Bulk.** `--all` reissues every endpoint that has a creds file on this host. `--changed` reissues the ones whose creds file carries a permission set that differs from the current one, and also those with a rotation left half done (`<creds file>.pending` exists) or whose current key is revoked. If it cannot check whether a key is revoked, it reports that endpoint as an error (`revocation_unknown`) and does not call it unchanged. Both take `--make-key` only, because many public keys cannot arrive in one command. An endpoint with no creds file on this host is skipped (`no_local_creds`), never rotated. Without `--yes`, or with `--dry-run`, the command lists what it would reissue and changes nothing. With `--yes` it reissues one endpoint at a time in address order. A refusal or error on one endpoint does not stop the rest. The JSON schema is `mashctl-endpoint-bulk@1`.

Two `mashctl` machines must not reissue the same endpoint at the same moment. The command checks the directory key after it writes and reports `superseded` if another run replaced it; it cannot stop the race, and the older key stays revoked either way.

**Exit codes.** 0 done, planned or unchanged, 1 refused, 2 usage error, 5 partial (a bulk run in which some endpoints were not reissued), 6 error. A killed run is finished by running the same command again; with `--make-key` the new key waits in `<creds file>.pending`.

## mashctl endpoint delete

```sh
mashctl endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json]
                        [--servers N] [--sys-creds FILE]
```

Removes an endpoint. In order, it:

1. revokes the endpoint's user key in the account JWT, on every server (see [Revocation](#revocation)); a revocation that cannot be confirmed stops the command with nothing removed;
2. removes the directory record and the inbox, so unread mail is lost;
3. burns the address: it is never assigned again, to any key;
4. renames the creds file, if one is in the config directories, to `<address>.creds.retired-<UTC time>`, or removes it with `--delete-creds`.

`--force` also handles the creds file when the server has no record. `--no-revoke` skips the revocation, and the user can still connect; the command warns. `--dry-run` says what would happen and changes nothing. `--json` uses schema `mashctl-endpoint@1`. A killed run finishes when you run the same command again. Exit codes: 0 deleted or unchanged, 1 refused, 2 usage error, 5 partial, 6 error.

## mashctl endpoint list and show

```sh
mashctl endpoint list [--json]
mashctl endpoint show ADDRESS [--json]
```

`list` prints one line per endpoint in the directory: `owner/group/instance`, `owner.group.instance`, the flat key and the creation time, then a count. It exits 0, also when there are none. `show` prints one endpoint's whole directory record, the path of its creds file in the config directories, and its issue record entry (when issued and whether revoked). It exits 1 when the endpoint does not exist. Both change nothing. The JSON schemas are `mashctl-endpoint-list@1` and `mashctl-endpoint@1`.

## mash key create and mash credential create

These two run on the endpoint's machine and need no server and no admin credentials.

```sh
mash key create --out FILE [--pub-out FILE] [--json] [--dry-run]
mash credential create --seed FILE --jwt FILE [--out CREDS] [--force] [--json] [--dry-run]
```

`mash key create` makes a user key pair. The seed goes to `FILE` (mode 0600), and the public key to the same name with `.seed` replaced by `.pub` (or to `--pub-out`). It makes a missing parent directory (mode 0700), refuses to overwrite either file, and prints the public key and both paths, never the seed.

`mash credential create` joins the seed and the JWT that `mashctl endpoint create` wrote into a creds file (mode 0600). By default the file is `<config dir>/<JWT name>.creds`; `--out` names another. It checks the structure of the JWT, its self-signature, that it is a user JWT, and that it names the seed's key (`key_mismatch` if not). It does not check the issuing account or the expiry. It refuses to replace an existing file unless `--force` is given, and it prints the path and the public key, never the seed or the JWT.

## Refusal codes

A refusal exits 1, a usage error exits 2 and an error exits 6. The message names the code. Every code below is tested.

| Code | Exit | Command | Meaning |
| --- | --- | --- | --- |
| `name_invalid` | 1 | create | A token is empty or has a character mash does not allow. |
| `name_rule` | 1 | create | The name rule refuses the name. |
| `address_ambiguous` | 2 | create | Two tokens: write `alice/dev/design` or `--group dev design`. |
| `naming_disagrees` | 2 | create | The address and a flag name different values. |
| `group_missing` | 2 | create | No group: write `--group dev` or `alice/dev/design`. |
| `owner_missing` | 2 | create | No owner: write `--owner`, the three-token form, or set `MASH_OWNER`. |
| `name_missing` | 2 | create | No instance name. |
| `address_missing`, `address_invalid` | 2 | reissue, delete, show | No address, or one that is not an address. |
| `key_both`, `key_missing` | 2 | create, reissue | Give exactly one of `--public-key` and `--make-key`. |
| `unknown_flag`, `usage` | 2 | every verb | An unknown flag, or arguments the verb does not take. With `--json` the document has outcome `error` and the code. |
| `bulk_needs_make_key` | 2 | reissue | `--all` and `--changed` take `--make-key` only. |
| `staged_key_revoked` | 1 | create, reissue | The key staged in `<creds file>.pending` was revoked: remove the file and run again. |
| `labels_invalid` | 2 | create | A label key or value is not allowed. |
| `servers_invalid` | 2 | create, reissue, delete | `--servers` or `MASH_SERVERS` is not a whole number of 1 or more. |
| `no_config_dir` | 1 | create, reissue | No config directory for the creds file: set `MASH_CONFIG_DIR`. |
| `public_key_unreadable` | 1 | create, reissue | The `--public-key` file cannot be read. |
| `public_key_invalid` | 1 | create | The file does not hold a NATS user public key. |
| `file_exists` | 1 | create, reissue | The JWT or creds file exists and is never overwritten: name another with `--jwt-out`. |
| `creds_exists` | 1 | create | A creds file for another key exists: move it aside. |
| `no_account_key` | 1 | create, reissue, bulk | No account signing key: run `mashctl init` first. |
| `address_taken` | 1 | create | The endpoint exists: use `endpoint reissue`. |
| `key_retired` | 1 | create, reissue | The address was deleted and is never reassigned. |
| `public_key_issued` | 1 | create, reissue | The key was issued to another address: make a new key. |
| `key_revoked` | 1 | create, reissue | The key was revoked in the account JWT, or was issued and revoked before: make a new key. A supplied key is checked before it is signed. |
| `key_unchanged` | 1 | reissue | The key is already the endpoint's key. |
| `endpoint_missing` | 1 | reissue | No such endpoint. |
| `future_issued` | 1 | reissue, bulk | The old key's JWT was issued after now: fix the clock. Nothing was changed. |
| `creds_mismatch` | 1 | reissue, bulk | The creds file at the creds path is not this endpoint's current creds. Move it aside. |
| `reissue_busy` | 1 | reissue, bulk | Another reissue of the endpoint holds the lock; the detail names its pid and host when readable. A lock left by a killed run on this host is reclaimed automatically; a lock whose owner is on another host, or that names no owner, is not reclaimed; if that run is gone, remove the lock directory the detail names. |
| `server` | 6 | create, reissue, bulk | The server could not be reached or answered with an error. Nothing was changed. |
| `superseded` | 6 | reissue, bulk | Another reissue changed the endpoint's key while this one ran, so the file this run wrote was removed. Run the command again. |
| `servers` | 6 | reissue, bulk | Fewer servers answered than the count says. Nothing was revoked. |
| `write` | 6 | create, reissue | A directory is writable by every user or cannot be used. Nothing was changed. |
| `creds_unreadable` | 1 (6 in bulk) | reissue, bulk | A creds file on this host cannot be read or parsed. |
| `revocation_unknown` | 6 | create, reissue, bulk | Whether a key is revoked could not be checked (a server did not answer). Nothing was changed. |
| `no_local_creds` | 0 | bulk | Skipped: no creds file on this host. |

`endpoint show` exits 1 when there is no such endpoint. `endpoint delete` refuses with a plain message and exit 1 when there is no directory record on the server (`--force` still handles the creds file), too few servers answered the account lookup, the user key's issue time is in the future (fix the clock), the revocation could not be confirmed, or the record belongs to another user key. Nothing is changed in any of these cases.

## Removed commands

Three verbs from 0.5.0 are gone. Each exits 2 and prints its replacement.

| Removed | Use |
| --- | --- |
| `mashctl add` | `mashctl endpoint create alice/dev/design --public-key design.pub` |
| `mashctl reissue` | `mashctl endpoint reissue alice/dev/design --public-key FILE` |
| `mashctl retire` | `mashctl endpoint delete alice/dev/design` |

Endpoints made by 0.5.0 do not work with 0.5.5. Make new ones with `endpoint create` and remove the old ones. Mail unread in an old endpoint is not carried over.

## Revocation

`endpoint delete`, `endpoint reissue` and `deprovision` revoke the endpoint's NATS user in the same command (`mashctl suspend` does not). The revocation is an entry in the account JWT, by user public key and time: NATS refuses a user JWT for that key issued at or before the revocation time, and a connected user is closed when the server takes the account update, so credentials stop working everywhere the update reached. mashctl signs the update with the operator key, pushes it through the system account to every known server, requires every one to acknowledge it, and reads it back; if that cannot be shown it exits non-zero with `not confirmed` and running the command again is safe. An existing revocation is never lowered or removed.

`deprovision ADDRESS` revokes the user of ADDRESS's directory record, `deprovision ADDRESS --user U` revokes U (unless the record names another user: then it refuses and revokes nothing), and `--suspension ID --drained-to N` revokes only when its checks pass. With no record there is nothing to revoke. `--no-revoke` (same warning, same temporary name) is the one mashctl option for it, and the system credentials and server count come from `MASH_SYS_CREDS` and `MASH_SERVERS`; `mash admin deprovision` has no key store and still only advises revoking with your operator tooling.

Before a revocation is written, `deprovision` reads the issue record of each key and refuses, changing nothing, when any retained revision of it has an issue time after the revocation time: NATS honours a revocation only for credentials issued at or before it, so a credential dated in the future would stay valid. Every revision counts, not only the latest, because a credential reissued under the same key writes a new revision and, after a clock rollback, the newer revision can carry an earlier time than an older credential that is still held. The record keeps 64 revisions of a key; a key at that cap may have lost its oldest ones, so it is refused too (`the key has been re-recorded 64 times or more; its oldest issue times are no longer kept; cannot show that no earlier credential is dated in the future`) unless a retained revision is already future-dated, which gets the usual refusal. To proceed, fix the clock, or run again later once the clock is known to be right. A key with no record warns and the revocation goes on; a record that cannot be read, or has a revision whose `issued_at` is missing or malformed, refuses.

`endpoint delete` makes the same check, before the revocation and with the same clock reading, and refuses the same way with nothing changed. The check also refuses a key whose recorded history cannot be shown to be complete: the issue-record stream carries a `mash_issued_history` marker (`full` for a bucket made at 64 revisions, `raised:N:S` when `mash admin init` raised one from N), and a key of a bucket with no marker that has 8 or more revisions, or of a raised bucket that had N or more up to the raise, may have lost a future-dated one. The refusal says its issued history cannot be shown to be complete; revoke that key by hand in the account JWT, only after checking where it was issued ([ADR 0115](decisions/0115-a-revocation-needs-the-issue-record-to-show-a-keys-whole-history-and-the-bucket-says-so-in-its-stream-metadata.md)).

Because revocation is by key and time, a new credential for an endpoint needs a new user key (a new nkey), and a user JWT issued in the same second as the revocation is revoked too.

## Exit codes and arguments

The `endpoint` verbs also use 5 (partial) and 6 (error), as described above. Every other verb exits 0 on success, 2 for a usage error (a wrong or missing argument, an unknown flag, a stray argument, an argument that fails validation) and 1 for any failure; `suspended` also uses 3 for "not suspended"; `audit` uses 1 only for `--strict` findings and 3 when it could not complete (see [audit.md](audit.md#exit-codes)). An argument that fails validation is named in the message but never printed back, so a secret pasted into the wrong place is not echoed. `mashctl deprovision ADDRESS` for an address with no record and no inbox prints `no endpoint named ADDRESS` and exits 1; the admin operation itself stays idempotent, only the verb looks first. `mashctl permissions ADDRESS` prints the permission set of the endpoint's NATS user (stdout stays the JSON or the `--nsc` flags).

## Before you push with nsc

If you also edit the account with `nsc`, these steps keep it from undoing mashctl.

- Run `nsc pull -A` before any `nsc push`, so a push from a stale local copy does not overwrite a revocation mashctl made. `mashctl issued check` shows keys connected to a server that mash did not issue.
- `nsc push` rejects an account server URL that starts with `tls://`; give nsc a `nats://` URL. For a server with a private CA, nsc needs `SSL_CERT_FILE` set to the CA's PEM file (mashctl takes `tls_ca` instead).
- `nsc revocations add-user` followed by an `nsc push` revokes a user; `mashctl endpoint delete` and `mashctl deprovision` revoke the user of an endpoint themselves.
- To cut off old creds and keep the endpoint, run `mashctl endpoint reissue ADDRESS`: it revokes the old key and names a new one. `mashctl endpoint delete ADDRESS` revokes the user and removes the endpoint for good.
- A suspended endpoint's stored mail stays readable. `mash send` to a suspended endpoint fails with "no response from stream" and does not say the endpoint is suspended; this is a known limitation.
