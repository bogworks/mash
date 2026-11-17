# mashctl: provisioning, labeling and retiring endpoints

Endpoints are declared in the directory by `mashctl provision`, and every endpoint carries the same kind of permission set, the owner-line set for its address `owner.group.instance` (it may mail the endpoints of its own owner and read what it needs to; the owner is the fence). `mashctl permissions ADDRESS` prints that set. A NATS user is signed by the account signing key (`account.nk` in the key directory, `<config dir>/keys` or `MASH_KEYS_DIR`) and carries its permission set in its JWT; there is no policy file and no tag. See [ADR 0052](decisions/0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md).

Every verb that names an endpoint takes its address, `owner.group.instance`, or the flat key `owner_group_instance`. A bare name is refused (exit 2).

Creating an endpoint (a user, its creds file and its directory record in one step) is `mashctl endpoint create`, which is coming. Until it lands, an endpoint is made with `mashctl provision` plus a credential minted by the setup script or by hand ([operations.md](operations.md#adding-an-endpoint)); `mashctl endpoint reissue`, `delete`, `list` and `show` arrive with it.

## Prerequisites

Minting a user needs the account's identity key (`account.nk` in the key directory). `mashctl init` does not create that key: it only creates the directory and lease buckets on the server. The key comes from `mashctl init --local` (which makes a local server and its keys) or from `mashctl adopt` (which imports the account seed you already hold). Creds minted with that key authenticate at once: nothing has to be pushed to the account first.

## Connections and trust

`mashctl` refuses admin creds and system account creds (`--sys-creds`, `MASH_SYS_CREDS`, or `sys.creds` in the server directory) that other users can read, as mash does for endpoint creds: the file must be a regular file with no group or other permission bits (`chmod 600 FILE`). A symlink is followed and the file it points to is checked; a missing file keeps its own error.

Plain `nats://` to a non-loopback host is refused for admin and system connections: every verb that connects with the admin or system creds (and `adopt`) requires TLS, and fails when the server does not offer it. Local servers made by `init --local` are loopback-only and opt in explicitly: `init --local` writes `"insecure_loopback": true` into the `config.json` it creates (the one minimal escape, a setting and not a flag, which may be renamed when the command-line settings are redesigned; the same setting is `MASH_INSECURE_LOOPBACK=1`, honoured only for loopback URLs), and `init --local --start` connects only to the loopback server it just started, which needs no opt-in. `issued check`, `retire`, `deprovision` and `adopt` verify the account JWT's signature, subject and issuer (the operator key, or a key declared by `<key dir>/operator.jwt`). Admin and system connections never add servers the server advertises, and with `insecure_loopback` dial only localhost or a loopback address. See [operations](operations.md#writing-the-account-jwt-revocation).

## Which server does mashctl change?

`mashctl` takes its server, owner and admin credentials from `config.json`, and the endpoint's own `MASH_*` variables win over the file; there is no flag above them. Inside a mash agent session those variables are the session's, so `mashctl` would act on the session's own server. Before any verb that connects to a server, mashctl prints one line to stderr, `mashctl: target server <url> (from <source>), owner <owner> (from <source>), admin creds <path> (from <source>)`, and warns when an environment variable overrides a different `config.json` value or when `MASH_NAME` or `MASH_LAUNCH_TOKEN` shows the shell is inside a session. It informs and never refuses; offline verbs print nothing of it. A flag or context to choose the target is deferred to the CLI redesign (the CLI is frozen until then): use `env -u MASH_URL -u MASH_OWNER -u MASH_ADMIN_CREDS -u MASH_TLS_CA mashctl ...`, or run outside the session. See [operations](operations.md#which-server-does-mashctl-change).

## Adopting an existing server

```sh
mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]
```

`adopt` is `init --local` for a NATS server you already run in operator mode with a full resolver and JetStream. It needs authority you already hold (the operator signing seed, the mash account's identity seed, system account creds), because a URL does not prove it and `mashctl` cannot create authority on a server it did not set up. It checks the files, the system creds, the account on the resolver, JetStream and the operator key without changing anything, then keeps the keys in the key store, copies the system creds to `<config dir>/server/sys.creds`, optionally records or mints admin creds, and writes `config.json` (`url`, `owner`, `admin_creds`, and `tls_ca` when the loaded configuration sets one; `tls_cert` and `tls_key` likewise). It changes nothing on the server except that `--mint-admin` records the minted admin user in the issue record (before the creds file is written; if that fails, adopt fails and writes no creds file); the cutover of a live account is a separate step you run (`mashctl init`, then `mashctl provision` for each endpoint). `adopt` connect-tests the admin creds it ends with (given, minted or kept) before it reports success, and as that account user asks the server for the account's JetStream info: step 3 only reports what the account JWT asks for (its disk, streams and consumers), and step 6 reports whether the server enabled JetStream for the account. When it did not (the account's disk limit does not fit the server's JetStream budget), adopt fails and says why; without `--admin-creds` or `--mint-admin` there is no account user, and the output says the server was not asked. On an operator with strict signing key usage the account identity key cannot sign users, so `--mint-admin` is refused (nothing written); pass `--admin-creds FILE` with a user signed by one of the account's signing keys. See [Adopting an existing server](operations.md#adopting-an-existing-server) for the checks, the files and the refusals.

## The issue record

```sh
mashctl issued list  [--json]
mashctl issued check [--json] [--sys-creds FILE] [--servers N]
```

Every user JWT `mashctl` issues is written to the issue record, the `mash-issued` bucket, under the user's public key: a user minted with the account key (a kept key adds a revision under the same key, a rotated key adds an entry for the new key and leaves the old key's), and the admin user that `mashctl init` connects with (kind `admin`; `init --local --start` and `adopt --mint-admin` followed by `mashctl init` record it). The entry is written before the creds file or the directory record, so a user that can connect is always in the record, and a record that cannot be written stops the issue. An entry holds the public key, the address (the endpoint name), the owner, the kind (`endpoint` or `admin`), the tags, the generation (always 0: mash has no policy generation yet) the issue time (RFC 3339, UTC), the purpose and, once mash has revoked the key, the revocation time. The purpose is one of `issue` (a user minted with the account key, the admin user at `init`), `rotation` (the new key of a credential that rotates the user key) and `probe` (reserved for the policy gate's probe credentials; `mashctl` does not write it); a record from before the field reads as `issue`. `revoked_at` (RFC 3339, UTC) is left out until set: `retire` and `deprovision` set it after the revocation succeeds, once and never cleared, and a key mash never recorded is skipped. A failure to set it prints `warning: user key U... is revoked, but the issue record was not updated with it: ...` on stderr and never fails or undoes the revocation. Nothing is removed from the record when an endpoint is retired or its key is revoked: revocation is the account JWT's, and the record is the history of what was issued. `mashctl init` creates the bucket; a deployment from before the record gets it, hardened, the first time a user is minted and written. Users issued before the record existed are not in it until a credential is reissued under the same key.

`issued list` prints the latest entry of each key, with its purpose and, when set, `revoked TIME` (`--json`: `present`, `count`, `issued`, each entry with `purpose` and, when set, `revoked_at`).

`issued check` asks every server, through the system account (`--sys-creds FILE`, else `MASH_SYS_CREDS`, else the `sys.creds` next to `admin_creds`), which user public keys are connected in the MASH account, and compares them with the record. It reports, per server, any connected key mash did not issue (the key, the server, the account, the client's connection name, the key that signed the JWT and its tags), the recorded keys that are not connected (informational) and a count summary. No key is exempt, not even the admin user this command connects with: it must be in the record (`mashctl init` and `adopt --mint-admin` record it). The system account's users never appear in the MASH account's connection list. A connected key that the record holds as revoked is reported apart, as `REVOKED key K still connected`: the revocation did not take effect (revoke it again, and look at the account JWT). A recorded key of any purpose counts as issued by mash. Exit codes: 0 when no unrecorded key and no revoked key is connected and the scan was complete; 1 when a finding was found, even if the scan was incomplete; 2 when nothing was found but the scan was incomplete or failed: bad arguments, a connection failure, a server that did not answer, a server whose connection list changed while it was read and could not be read whole (a list that fits one page is one snapshot; a list of several pages is accepted only when two consecutive complete scans collect the same connections, and each server is scanned up to 3 times), or a server set that cannot be shown to be complete (a cluster needs `--servers N` or `MASH_SERVERS`, as `retire` does). Whenever the scan was incomplete the text says `INCOMPLETE` and names why, and the JSON has `complete: false` and `incomplete` with the reason, with or without a finding. `--json` prints `clean`, `complete`, `incomplete`, `account`, `record_present`, `servers` (`answered`, `missing`, `expected`), `unrecorded`, `revoked_connected`, `recorded_not_connected` and `summary`. Only public keys are printed. See [operations.md](operations.md#the-issue-record-and-the-drift-check) for what it can and cannot see.

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

## Retiring an endpoint

```sh
mashctl retire ADDRESS [--delete-creds] [--force] [--no-revoke]
```

Retire acts on the directory record. Without `--force`, an ADDRESS with no record refuses (`no directory record on the server`); `--force` then handles the creds file only (nothing is revoked, because no user key is known). There is no `--policy` flag.

Needs the server and admin credentials, and, to revoke the user, the operator and account keys in the key store and the system account credentials (`MASH_SYS_CREDS`, or the `sys.creds` that `mashctl init --local` wrote; `MASH_SERVERS` for a cluster; revocation has no flags for these). Order of operations (a failure leaves the earlier, safer state):

1. Read ADDRESS's user key from the directory record and **revoke it** (see Revocation below); if the revocation fails the command stops here, with nothing removed, and says why and that `--no-revoke` skips it (`--no-revoke` is a temporary flag name, to be renamed with the CLI redesign). Then delete the record and inbox only if the record still holds that key (`DeprovisionIfUser`, under the address's lock). A record someone replaced meanwhile is left in place, and a failure here changes nothing. With no record the command refuses (and says to finish a partly removed endpoint with `mashctl deprovision ADDRESS`); with `--force` it says so and goes on to the creds file.

   A refusal prints `nothing was changed`. Any other error from this step is reported as `PARTIAL`: the record may already be gone but the lease fence or the inbox delete did not finish, and the creds file is untouched. A retry of `mashctl retire ADDRESS` now refuses (no record), so finish with `mashctl deprovision ADDRESS` (removes what is left); `mashctl retire ADDRESS --force` then handles the creds file as in steps 2 and 3.
2. Creds file: renamed to `ADDRESS.creds.retired-<UTC yyyymmddThhmmssZ>` in the same directory (mode kept), so a creds file is never in the way of a new one, and the rename is reported. `--delete-creds` removes `ADDRESS.creds` instead. Only a regular file is touched (a symlink or other file is left and reported); a missing file is fine and nothing is printed. The retired file still holds the user's seed: delete it when you no longer need it. `retire` does not revoke the user: anyone holding the old creds keeps access until the user is revoked (`retire` revokes it unless `--no-revoke`), same as after `--delete-creds`.
3. Print `retired ADDRESS` and the revocation line (`user key U... revoked in the account JWT`, or the `--no-revoke` warning).

### Revocation

`retire` and `deprovision` revoke the endpoint's NATS user in the same command (`mashctl suspend` does not). The revocation is an entry in the account JWT, by user public key and time: NATS refuses a user JWT for that key issued at or before the revocation time, and a connected user is closed when the server takes the account update, so credentials stop working everywhere the update reached. mashctl signs the update with the operator key, pushes it through the system account to every known server, requires every one to acknowledge it, and reads it back; if that cannot be shown it exits non-zero with `not confirmed` and running the command again is safe. An existing revocation is never lowered or removed.

`deprovision ADDRESS` revokes the user of ADDRESS's directory record, `deprovision ADDRESS --user U` revokes U (unless the record names another user: then it refuses and revokes nothing), and `--suspension ID --drained-to N` revokes only when its checks pass. With no record there is nothing to revoke. `--no-revoke` (same warning, same temporary name) is the one mashctl option for it, and the system credentials and server count come from `MASH_SYS_CREDS` and `MASH_SERVERS`; `mash admin deprovision` has no key store and still only advises revoking with your operator tooling.

Before a revocation is written, `deprovision` reads the issue record of each key and refuses, changing nothing, when any retained revision of it has an issue time after the revocation time: NATS honours a revocation only for credentials issued at or before it, so a credential dated in the future would stay valid. Every revision counts, not only the latest, because a credential reissued under the same key writes a new revision and, after a clock rollback, the newer revision can carry an earlier time than an older credential that is still held. The record keeps 64 revisions of a key; a key at that cap may have lost its oldest ones, so it is refused too (`the key has been re-recorded 64 times or more; its oldest issue times are no longer kept; cannot show that no earlier credential is dated in the future`) unless a retained revision is already future-dated, which gets the usual refusal. To proceed, fix the clock, or run again later once the clock is known to be right. A key with no record warns and the revocation goes on; a record that cannot be read, or has a revision whose `issued_at` is missing or malformed, refuses.

`retire` makes the same check, before the revocation and with the same clock reading, and refuse the same way with nothing changed. The check also refuses a key whose recorded history cannot be shown to be complete: the issue-record stream carries a `mash_issued_history` marker (`full` for a bucket made at 64 revisions, `raised:N:S` when `mash admin init` raised one from N), and a key of a bucket with no marker that has 8 or more revisions, or of a raised bucket that had N or more up to the raise, may have lost a future-dated one. The refusal says its issued history cannot be shown to be complete; revoke that key by hand in the account JWT, only after checking where it was issued ([ADR 0115](decisions/0115-a-revocation-needs-the-issue-record-to-show-a-keys-whole-history-and-the-bucket-says-so-in-its-stream-metadata.md)).

Because revocation is by key and time, a new credential for a retired address needs a new user key (a new nkey), and a user JWT issued in the same second as the revocation is revoked too.

## Exit codes and arguments

Every verb exits 0 on success, 2 for a usage error (a wrong or missing argument, an unknown flag, a stray argument, an argument that fails validation) and 1 for any failure; `suspended` also uses 3 for "not suspended"; `audit` uses 1 only for `--strict` findings and 3 when it could not complete (see [audit.md](audit.md#exit-codes)). An argument that fails validation is named in the message but never printed back, so a secret pasted into the wrong place is not echoed. `mashctl deprovision ADDRESS` for an address with no record and no inbox prints `no endpoint named ADDRESS` and exits 1; the admin operation itself stays idempotent, only the verb looks first. `mashctl permissions ADDRESS` prints the permission set of the endpoint's NATS user (stdout stays the JSON or the `--nsc` flags).

## Before you push with nsc

If you also edit the account with `nsc`, these steps keep it from undoing mashctl.

- Run `nsc pull -A` before any `nsc push`, so a push from a stale local copy does not overwrite a revocation mashctl made. `mashctl issued check` shows keys connected to a server that mash did not issue.
- `nsc push` rejects an account server URL that starts with `tls://`; give nsc a `nats://` URL. For a server with a private CA, nsc needs `SSL_CERT_FILE` set to the CA's PEM file (mashctl takes `tls_ca` instead).
- `nsc revocations add-user` followed by an `nsc push` revokes a user; `mashctl retire` and `mashctl deprovision` revoke the user of an endpoint themselves.
- To cut off old creds, `mashctl retire ADDRESS` revokes the user; mint a new user for the address afterwards.
- A suspended endpoint's stored mail stays readable. `mash send` to a suspended endpoint fails with "no response from stream" and does not say the endpoint is suspended; this is a known limitation.
