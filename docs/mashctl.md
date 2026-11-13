# mashctl: adding, re-issuing, labeling and retiring endpoints

The policy file is gone: endpoints are declared in the directory, by `mashctl add`, and every endpoint carries the same kind of permission set, the owner-line set `admin.Permissions(owner, name)` (it may mail the endpoints of its own owner and read what it needs to; the address is the fence). `mashctl add` and `mashctl reissue` mint NATS users for them. A user is signed by the account signing key (`account.nk` in the key directory, `<config dir>/keys` or `MASH_KEYS_DIR`) and carries its permission set in its JWT; there are no zones, no per-zone signing keys and no tags ([ADR 0091](decisions/0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md). See [ADR 0052](decisions/0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md); [ADR 0053](decisions/0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md) is amended by ADR 0091. The `mashctl policy` group has no subcommands yet; the list-set verbs that will replace the file's rules are added to it later.

## Prerequisites

`add` needs the account's identity key (`account.nk` in the key directory). `mashctl init` does not create that key: it only creates the directory and lease buckets on the server. The key comes from `mashctl init --local` (which makes a local server and its keys) or from `mashctl adopt` (which imports the account seed you already hold). Creds minted by `add` authenticate at once: nothing has to be pushed to the account first.

## Connections and trust

`mashctl` refuses admin creds and system account creds (`--sys-creds`, `MASH_SYS_CREDS`, or `sys.creds` in the server directory) that other users can read, as mash does for endpoint creds: the file must be a regular file with no group or other permission bits (`chmod 600 FILE`). A symlink is followed and the file it points to is checked; a missing file keeps its own error.

Plain `nats://` to a non-loopback host is refused for admin and system connections: every verb that connects with the admin or system creds (and `adopt`) requires TLS, and fails when the server does not offer it. Local servers made by `init --local` are loopback-only and opt in explicitly: `init --local` writes `"insecure_loopback": true` into the `config.json` it creates (the one minimal escape, a setting and not a flag, which may be renamed when the command-line settings are redesigned; the same setting is `MASH_INSECURE_LOOPBACK=1`, honoured only for loopback URLs), and `init --local --start` connects only to the loopback server it just started, which needs no opt-in. `issued check`, a rotating `reissue`, `retire`, `deprovision`, `unzone` and `adopt` verify the account JWT's signature, subject and issuer (the operator key, or a key declared by `<key dir>/operator.jwt`). Admin and system connections never add servers the server advertises, and with `insecure_loopback` dial only localhost or a loopback address. See [operations](operations.md#writing-the-account-jwt-revocation).

## Which server does mashctl change?

`mashctl` takes its server, owner and admin credentials from `config.json`, and the endpoint's own `MASH_*` variables win over the file; there is no flag above them. Inside a mash agent session those variables are the session's, so `mashctl` would act on the session's own server. Before any verb that connects to a server, mashctl prints one line to stderr, `mashctl: target server <url> (from <source>), owner <owner> (from <source>), admin creds <path> (from <source>)`, and warns when an environment variable overrides a different `config.json` value or when `MASH_NAME` or `MASH_LAUNCH_TOKEN` shows the shell is inside a session. It informs and never refuses; offline verbs print nothing of it. A flag or context to choose the target is deferred to the CLI redesign (the CLI is frozen until then): use `env -u MASH_URL -u MASH_OWNER -u MASH_ADMIN_CREDS -u MASH_TLS_CA mashctl ...`, or run outside the session. See [operations](operations.md#which-server-does-mashctl-change).

## Adopting an existing server

```sh
mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]
```

`adopt` is `init --local` for a NATS server you already run in operator mode with a full resolver and JetStream. It needs authority you already hold (the operator signing seed, the mash account's identity seed, system account creds), because a URL does not prove it and `mashctl` cannot create authority on a server it did not set up. It checks the files, the system creds, the account on the resolver, JetStream and the operator key without changing anything, then keeps the keys in the key store, copies the system creds to `<config dir>/server/sys.creds`, optionally records or mints admin creds, and writes `config.json` (`url`, `owner`, `admin_creds`, and `tls_ca` when the loaded configuration sets one; `tls_cert` and `tls_key` likewise). It changes nothing on the server except that `--mint-admin` records the minted admin user in the issue record (before the creds file is written; if that fails, adopt fails and writes no creds file); the cutover of a live account is a separate step you run (`mashctl init`, then `mashctl add` for each endpoint). `adopt` connect-tests the admin creds it ends with (given, minted or kept) before it reports success. On an operator with strict signing key usage the account identity key cannot sign users, so `--mint-admin` is refused (nothing written); pass `--admin-creds FILE` with a user signed by one of the account's signing keys. See [Adopting an existing server](operations.md#adopting-an-existing-server) for the checks, the files and the refusals.

## Adding an endpoint

```sh
mashctl add NAME [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]...
```

`--tool` adds the label `kind=tool`; `--describe` and `--cap` set the record's description and capabilities, as `provision` does. Before it mints anything it reads the account JWT with the system account credentials (`MASH_SYS_CREDS`, or the `sys.creds` next to `admin_creds`; `MASH_SERVERS` for a cluster) and refuses, changing nothing, while a zone policy of an older release is applied (`a zone policy is applied: ...`), because the owner-wide permissions it mints would bypass that policy's mail restrictions; when the system credentials or the key store are missing, or the account JWT cannot be read or verified, it refuses with `cannot check whether a zone policy is applied ...; nothing was changed`. There is no flag that skips the check. No file is read or written but the creds file: there is no `--policy` flag (it is an unknown option). It needs the server and admin credentials (as `provision` does) and the account key. A name the directory already holds is refused (`endpoint "NAME" already exists in the directory`); to rotate an existing endpoint's user, use `reissue`.

Order of operations; a failure at any step undoes the earlier ones, so no half-made endpoint is left:

1. The name must be valid and pass the name rule, and the account key must exist; no creds file of that name may exist, and the directory must hold no record of it.
2. Mint the user, signed by the account signing key with the owner-line permission set `admin.Permissions(owner, name)`.
3. Write the user to the issue record ([below](#the-issue-record)) before anything else is created; if that fails, nothing was issued.
4. Provision the directory record (with the labels, note, description and capabilities) and inbox. A server failure here writes nothing.
5. Write `NAME.creds` (mode 0600, never over an existing file; the directory is made 0700 if missing). On failure the provision is rolled back.
6. Report `added NAME creds PATH`. Seeds, JWTs and creds contents are never printed.

### A killed add

The rollback above covers errors, not kills. If `mashctl add` is killed mid-run (SIGKILL, power loss), it can leave an orphan that nothing can undo: a directory record and inbox and/or a `NAME.creds` file that does not belong to a finished add. A leftover creds file makes `add NAME` refuse with:

```
NAME looks like an interrupted add (a creds file or an inbox exists but the directory has no complete record of NAME): run mashctl retire NAME --force --delete-creds, then mashctl add NAME again
```

A leftover directory record makes `add NAME` refuse with `already exists in the directory`; `mashctl retire NAME` removes it (and revokes its user). `mashctl audit` lists records, inboxes and creds files that do not match. That is the whole recovery; add never resumes by itself.
## Re-issuing

```sh
mashctl reissue NAME... [--widen]
mashctl reissue --all --yes
mashctl reissue --changed --yes
```

**Bulk runs list first and need `--yes`.** `--all` and `--changed` first print one line per endpoint they would reissue or rotate to stdout (`will reissue NAME: REASON`, the reason being what differs in its creds file, an interrupted rotation, a revoked key, or `re-signed as it is` for `--all`), and act only with `--yes`. Without `--yes` they print the list, change nothing, say to rerun with `--yes` and exit 2, whether or not stdin is a terminal; there is no prompt. A bulk run with nothing to reissue prints `nothing to reissue`, exits 0 and needs neither `--yes` nor the system account credentials. `--yes` is valid only with `--all` or `--changed`, and `reissue NAME...` is not affected by it.

**A reissue never widens silently.** Before it re-mints an endpoint it compares the creds file's current permissions with the owner-wide set (`admin.Permissions(owner, name)`). The new set widens when it grants a publish or subscribe subject the current allow lists do not cover (a wildcard covers what it matches), or when the current JWT has a deny entry the new set lacks; a JWT with no permissions at all is not narrower. A widening reissue is refused, changing nothing, with exit 1 and a message that the current JWT is narrower than the owner-wide set, unless the endpoint is named on the command line with `--widen`. In a bulk run such an endpoint is skipped, not failed, with `skipped NAME: its permissions are narrower than the owner-wide set; name it with --widen to widen it` on stderr, and it is not in the `--yes` list. `--widen` is valid only with explicit names.

Mints the endpoint's user JWT again, signed by the account signing key with the endpoint's own permission set, and replaces its creds file atomically (0600). Because the new JWT is signed with the seed stored in the creds file, every reissue first connects with the admin credentials and reads the endpoint's directory record, and refuses, changing nothing, when there is no record (`NAME has no directory record (retired or never added): add it again; a leftover creds file is not reissued`), when the record belongs to another account, or when the record names a different user key than the creds file's seed. The messages name only public-key prefixes. Before a reissue mints or rotates anything it makes the same applied-zone-policy check as `add` (once per run, with the system account credentials) and refuses, changing nothing, while a zone policy is applied or when the check cannot be made; there is no flag that skips it, and a `--changed` run that selects no endpoint does not need it. A reissue of a creds file that already matches (signed by the account key, with the endpoint's permissions and name, no tags) keeps the user key and leaves the record unchanged, but only after it reads the account JWT (system account credentials, as `retire`; `MASH_SYS_CREDS`, and `MASH_SERVERS` for a cluster) and finds the key is not in the revocation list of any server's copy (every server must answer and the server set must be complete, as for `retire`; a cluster needs `MASH_SERVERS`): a key found revoked is rotated as below, never re-signed, and if the account JWT cannot be read or verified the reissue refuses (`cannot tell whether the user key U... is revoked: ...; nothing was changed`). A rotation also refuses, changing nothing, when the creds file's current JWT claims it was issued in the future (`NAME's current creds JWT claims it was issued in the future (T): the revocation would not cover it; fix this machine's clock (or the machine that minted it) and run again`), because a revocation does not cover a JWT issued after it; the same applies to a revoked key. The name lock is re-checked just before the new creds are staged, and the reissue refuses if it is no longer held. A creds file issued for another account is refused, changing nothing (`NAME's creds file was issued for another account (X...); mashctl can only revoke in this account, so it cannot cut that JWT off: revoke it where it was issued, then retire and add NAME again`). The record check, the revocation check and the rewrite of the creds file run under the endpoint's name lock, so two reissues of one name never interleave (the second waits, then finds the record changed and refuses, or finds nothing to do). A reissue of a creds file that differs (its signer is not the account key, as for creds a zone key signed under an older release, its permissions or name are not the current ones, or it carries tags) rotates the user key instead, because a user JWT does not expire and the old one would otherwise stay valid. It also needs the system account credentials, as `retire` does (`MASH_SYS_CREDS`, and `MASH_SERVERS` for a cluster). The order is: make a fresh user key and stage its creds in `<creds file>.next` (0600, synced), revoke the old key (confirmed), update the directory record from the old key to the new one (a compare-and-set), and only then replace the creds file with the staged creds; it prints `reissued NAME (new user key, old key revoked)`, or `reissued NAME` when the key is kept. If the server is unreachable, the record check refuses or the revocation is not confirmed, the record and the creds file are unchanged and the old creds still work; running the same reissue again retries and reuses the staged key. If the record update or the file write fails after the revocation, the endpoint is cut off but the new seed is safe in the staged file: run `mashctl reissue NAME` again and it finishes the rotation (it re-signs the staged key, accepts a record that already names it, and replaces the creds file). Never retire the endpoint for this: retiring deletes its inbox. A staged file that is unreadable, loose, for another account or name, or whose JWT does not match its seed is refused (`remove the stale staged file X and run again`). `--all` and `--changed` do this per endpoint, for the endpoints of the directory (the legacy-form records of this owner: a three-token record or a record of another owner is named on stderr and skipped), so they also need the server and the admin credentials to read the directory. `--all` re-issues every one of them. `--changed` re-issues every one whose creds file holds a JWT that is not signed by the account key, has other permissions, tags or another name, or is for another account, and also every endpoint with a staged file `<creds file>.next` (an interrupted rotation, even if the creds file was put back since: the reissue resumes it, or refuses a stale staged file) and every endpoint whose creds file's user key the account JWT revokes (the JWT may still match what a mint issues now). Finding revoked keys reads the account JWT, so `--changed` needs the server and the system account credentials (`MASH_SYS_CREDS`, and `MASH_SERVERS` for a cluster) for each endpoint it has no other reason to select; when that cannot be read it prints `mashctl: reissue NAME: cannot tell whether its user key is revoked: ...`, goes on with the other endpoints and exits 1. It prints `nothing to reissue` only when no endpoint is selected and nothing failed; an endpoint with no creds file on this machine is skipped with a note. Naming an endpoint with no creds file is an error: retire it and add it again. The names come from the arguments, `--all` or `--changed`; there is no `--policy` flag (it is an unknown option) and no file is read.

## The issue record

```sh
mashctl issued list  [--json]
mashctl issued check [--json] [--sys-creds FILE] [--servers N]
```

Every user JWT `mashctl` issues is written to the issue record, the `mash-issued` bucket, under the user's public key: `add`, `reissue` (a kept key adds a revision under the same key, a rotated key adds an entry for the new key and leaves the old key's), and the admin user that `mashctl init` connects with (kind `admin`; `init --local --start` and `adopt --mint-admin` followed by `mashctl init` record it). The entry is written before the creds file or the directory record, so a user that can connect is always in the record, and a record that cannot be written stops the issue. An entry holds the public key, the address (the endpoint name), the owner, the kind (`endpoint` or `admin`), the tags, the generation (always 0: mash has no policy generation yet) the issue time (RFC 3339, UTC), the purpose and, once mash has revoked the key, the revocation time. The purpose is one of `issue` (`add`, a reissue that keeps the key, the admin user at `init`), `rotation` (the new key of a reissue that rotates the user key) and `probe` (reserved for the policy gate's probe credentials; `mashctl` does not write it); a record from before the field reads as `issue`. `revoked_at` (RFC 3339, UTC) is left out until set: `retire`, `deprovision` and the old key of a rotating `reissue` set it after the revocation succeeds, once and never cleared, and a key mash never recorded is skipped. A failure to set it prints `warning: user key U... is revoked, but the issue record was not updated with it: ...` on stderr and never fails or undoes the revocation. Nothing is removed from the record when an endpoint is retired or its key is revoked: revocation is the account JWT's, and the record is the history of what was issued. `mashctl init` creates the bucket; a deployment from before the record gets it, hardened, the first time `add` or `reissue` writes. Users issued before the record existed are not in it until `mashctl reissue NAME` (without a rotation, the same key is recorded).

`issued list` prints the latest entry of each key, with its purpose and, when set, `revoked TIME` (`--json`: `present`, `count`, `issued`, each entry with `purpose` and, when set, `revoked_at`).

`issued check` asks every server, through the system account (`--sys-creds FILE`, else `MASH_SYS_CREDS`, else the `sys.creds` next to `admin_creds`), which user public keys are connected in the MASH account, and compares them with the record. It reports, per server, any connected key mash did not issue (the key, the server, the account, the client's connection name, the key that signed the JWT and its tags), the recorded keys that are not connected (informational) and a count summary. No key is exempt, not even the admin user this command connects with: it must be in the record (`mashctl init` and `adopt --mint-admin` record it). The system account's users never appear in the MASH account's connection list. A connected key that the record holds as revoked is reported apart, as `REVOKED key K still connected`: the revocation did not take effect (revoke it again, and look at the account JWT). A recorded key of any purpose counts as issued by mash. Exit codes: 0 when no unrecorded key and no revoked key is connected and the scan was complete; 1 when a finding was found, even if the scan was incomplete; 2 when nothing was found but the scan was incomplete or failed: bad arguments, a connection failure, a server that did not answer, a server whose connection list changed while it was read and could not be read whole (a list that fits one page is one snapshot; a list of several pages is accepted only when two consecutive complete scans collect the same connections, and each server is scanned up to 3 times), or a server set that cannot be shown to be complete (a cluster needs `--servers N` or `MASH_SERVERS`, as `retire` does). Whenever the scan was incomplete the text says `INCOMPLETE` and names why, and the JSON has `complete: false` and `incomplete` with the reason, with or without a finding. `--json` prints `clean`, `complete`, `incomplete`, `account`, `record_present`, `servers` (`answered`, `missing`, `expected`), `unrecorded`, `revoked_connected`, `recorded_not_connected` and `summary`. Only public keys are printed. See [operations.md](operations.md#the-issue-record-and-the-drift-check) for what it can and cannot see.

## Labels

```sh
mashctl label NAME k=v [k=v]... [--dry-run]
mashctl unlabel NAME k [k]... [--dry-run]
```

These set and clear labels on the endpoint's directory record (what `who --long` shows). They connect with the admin credentials and need no file and no creds: the endpoint must have a directory record, and keys and values are checked as the directory checks labels. `label` prints `labeled NAME: k=v ...` and `unlabel` prints `unlabeled NAME: -k ...`; a change that leaves the labels as they are prints `NAME: nothing to change`. `unlabel` of a label that is not set warns and exits 0. `--dry-run` says what would change and writes nothing. There are no `--policy` and `--sync` flags (both are unknown options): the record is the only place a label lives.

Labels do not change a minted user: the permissions of an endpoint do not depend on them, so setting one leaves nothing to reissue.
## Suspending an endpoint's inbound mail

- `mashctl suspend NAME` refuses all inbound mail to NAME's inbox and prints `suspended NAME: id ID, last_seq N, msgs M (new mail parked; the endpoint's credentials are still valid)`. Suspend does not revoke: it is reversible, and the endpoint can still connect with its creds. The id is a random token naming this one suspension. Stored mail stays readable; the sequence is final while the suspension holds.
- `mashctl resume NAME` restores delivery and prints `resumed NAME`.
- `mashctl suspended NAME` prints `suspended NAME since T: id ID, last_seq N, msgs M` and exits 0, or `NAME is not suspended` and exits 3. Any error exits 1.
- To remove an endpoint after reading its last mail: `mashctl suspend NAME` (note `id ID` and `last_seq N`), drain through N, then `mashctl deprovision NAME --suspension ID --drained-to N` (both flags are required together; either alone is a usage error). The deprovision refuses, changing nothing, unless the inbox is still suspended under that same id and at last_seq N when it runs, and the directory record still belongs to the user key it had at suspend time, so a `resume` by another admin cannot lose mail. The id ties the drain to that one suspension, so a re-provisioned endpoint of the same name cannot be removed by an old command. Example: `mashctl deprovision design --suspension 9f3c... --drained-to 41`. The revocation of the endpoint's user key happens in this command, only once those checks hold (see below). Plain `deprovision` does not check, and `--drained-to` cannot be combined with `--user`.

See [operations.md](operations.md#suspend-an-endpoints-inbound-mail).

## Auditing the population

`mashctl audit [--json] [--strict] [--creds-dir DIR] [--compare EARLIER.json]` lists what the server and the creds directories hold, changes nothing, and reports state, not use: it cannot see publishes, connections or endpoints with no stream. `--compare` adds what moved since an earlier `--json` report. Exit codes: 0 clean (or findings without `--strict`), 1 findings under `--strict` (orphans, undrained legacy inboxes, unreadable items, movement found by `--compare`), 2 usage, 3 the audit could not complete (server unreachable, unreadable required input, bad `--compare` file). See [audit.md](audit.md).

## mashctl inbox inspect

```text
mashctl inbox inspect <address> [--json] [--legacy] [--from SEQ] [--to SEQ] [--limit N]
```

Shows one endpoint's stored messages and where its `deliver` consumer stands, so a test lane can prove what the transport kept and delivered. It is read only: it uses stream info, a direct get of a message by sequence and consumer info of the existing `deliver` consumer, and nothing else. It never creates, updates, binds or fetches from a consumer, never acknowledges, and writes nothing to the directory, a bucket or a file. It runs with the same admin credentials as `audit`, and also when `agent_token` is set.

- `address` is an endpoint name (a legacy endpoint), a flat key `agent_owner_name`, or `agent.owner.name` (`cc.OWNER.NAME` names a legacy endpoint). It is read from the directory record, one key; an address that names no record, or a dotted address whose owner is not the record's, is refused (exit 1).
- The default is the last 20 messages, oldest first. `--limit N` caps the count. `--from SEQ` and `--to SEQ` are an inclusive stream-sequence range, clamped to the stream's first and last sequence; with `--from` the first N messages from it are shown.
- During an alias window an endpoint has two inbox streams. The endpoint's own is shown; `--legacy` shows the alias's legacy stream, and is refused for a record with no alias.
- Exit codes: 0 success, 1 a refusal (an unknown endpoint, no inbox stream, no alias for `--legacy`), 2 a usage error, 3 it could not complete (no server, no credentials).

The text form prints the stream and consumer totals, a table of messages and a `note:` line for each limit. `--json` prints one object. The schema string and the field names are a stable contract; they change only when a field is removed or changes meaning.

| field | meaning |
| --- | --- |
| `schema` | `mash-inbox-inspect@1` |
| `address` | the address as given |
| `stream` | `name`, `first_seq`, `last_seq`, `messages`, `bytes` of the inbox stream |
| `consumer` | the `deliver` consumer, or `null` when it does not exist (every message is then `undelivered`): `name`; `ack_floor` and `delivered`, each `{stream_seq, consumer_seq}`; `num_pending`; `num_redelivered`; `num_ack_pending` |
| `messages[].seq` | the stream sequence |
| `messages[].msg_id` | the `Nats-Msg-Id` header, empty when there is none |
| `messages[].sender` | the sender's name: from the directory for `verified` and `stale`, the signed name for the others, `?` when the header names none |
| `messages[].trust` | `verified`, `claimed`, `unknown-signer`, `stale`, `forged`, `malformed` or `absent`, from the stored `Agent-Sender` header (forged and malformed mail is shown, not hidden) |
| `messages[].stored_at` | the stream's store time, RFC 3339 UTC |
| `messages[].size` | payload size in bytes |
| `messages[].kind` | the `Mash-Kind` header |
| `messages[].state` | `acked`, `undelivered` or `delivered-unacked-or-acked-out-of-order` (text form: `delivered, ack not confirmed`) |
| `messages[].delivery_count`, `messages[].ack_time` | always `null` |
| `limits` | the strings below |

The state comes from the consumer's cursors alone: `acked` when the sequence is at or below `ack_floor.stream_seq`, `undelivered` when it is above `delivered.stream_seq`, otherwise `delivered-unacked-or-acked-out-of-order`, because NATS does not report acknowledgements above the ack floor. The trust label is worked out as the receiving endpoint does, except that the signer is looked up by the name it signed (one directory key read) instead of by listing the directory: a signature by a key that is not on that name's record is `unknown-signer`, and a signed header with no name is too.

Limits, also in the `limits` array: per-message delivery count and ack time are not kept by JetStream or mash; the consumer's `num_redelivered` is the aggregate. A message above the ack floor and at or below the last delivered sequence may be unacknowledged or acknowledged out of order, and the server does not say which.

See also [audit.md](audit.md) for the consumer fields (`delivered`, `ack_floor`, `num_redelivered`, `pending`, `ack_pending`) the audit reports per inbox.

## Retiring an endpoint

```sh
mashctl retire NAME [--delete-creds] [--force] [--no-revoke]
```

Retire acts on the directory record. Without `--force`, a NAME with no record refuses (`no directory record on the server`); `--force` then handles the creds file only (nothing is revoked, because no user key is known). There is no `--policy` flag.

Needs the server and admin credentials, and, to revoke the user, the operator and account keys in the key store and the system account credentials (`MASH_SYS_CREDS`, or the `sys.creds` that `mashctl init --local` wrote; `MASH_SERVERS` for a cluster; revocation has no flags for these). Order of operations (a failure leaves the earlier, safer state):

1. Read NAME's user key from the directory record and **revoke it** (see Revocation below); if the revocation fails the command stops here, with nothing removed, and says why and that `--no-revoke` skips it (`--no-revoke` is a temporary flag name, to be renamed with the CLI redesign). Then delete the record and inbox only if the record still holds that key (`DeprovisionIfUser`, under the name's lock). A record someone replaced meanwhile is left in place, and a failure here changes nothing. With no record the command refuses (and says to finish a partly removed endpoint with `mashctl deprovision NAME`); with `--force` it says so and goes on to the creds file.

   A refusal prints `nothing was changed`. Any other error from this step is reported as `PARTIAL`: the record may already be gone but the lease fence or the inbox delete did not finish, and the creds file is untouched. A retry of `mashctl retire NAME` now refuses (no record), so finish with `mashctl deprovision NAME` (removes what is left); `mashctl retire NAME --force` then handles the creds file as in steps 2 and 3.
2. Creds file: renamed to `NAME.creds.retired-<UTC yyyymmddThhmmssZ>` in the same directory (mode kept), so `mashctl add NAME` works again at once, and the rename is reported. `--delete-creds` removes `NAME.creds` instead. Only a regular file is touched (a symlink or other file is left and reported); a missing file is fine and nothing is printed. The retired file still holds the user's seed: delete it when you no longer need it. `retire` does not revoke the user: anyone holding the old creds keeps access until the user is revoked (`retire` revokes it unless `--no-revoke`), same as after `--delete-creds`.
3. Print `retired NAME` and the revocation line (`user key U... revoked in the account JWT`, or the `--no-revoke` warning).

### Revocation

`retire` and `deprovision` revoke the endpoint's NATS user in the same command (`mashctl suspend` does not). The revocation is an entry in the account JWT, by user public key and time: NATS refuses a user JWT for that key issued at or before the revocation time, and a connected user is closed when the server takes the account update, so credentials stop working everywhere the update reached. mashctl signs the update with the operator key, pushes it through the system account to every known server, requires every one to acknowledge it, and reads it back; if that cannot be shown it exits non-zero with `not confirmed` and running the command again is safe. An existing revocation is never lowered or removed.

`deprovision NAME` revokes the user of NAME's directory record, `deprovision NAME --user U` revokes U (unless the record names another user: then it refuses and revokes nothing), and `--suspension ID --drained-to N` revokes only when its checks pass. With no record there is nothing to revoke. `--no-revoke` (same warning, same temporary name) is the one mashctl option for it, and the system credentials and server count come from `MASH_SYS_CREDS` and `MASH_SERVERS`; `mash admin deprovision` has no key store and still only advises revoking with your operator tooling.

Because revocation is by key and time, a reissue (`mashctl reissue`, `mashctl add`) for a retired name needs a new user key (a new nkey), and a user JWT issued in the same second as the revocation is revoked too.

The three-token form of `provision` and `deprovision` (`--agent A [--owner O] [--alias OWNER/NAME]`, or `agent.owner.name`) is described in [operations.md](operations.md#provisioning-and-deprovisioning-by-the-three-tokens); with `agent_token` set, only that form of those two verbs runs. The setting gates creating and naming three-token endpoints (`provision`, the dotted form, and the `--agent`/`--owner`/`--alias` flags); an existing endpoint stays operable by its flat key regardless (`suspend`, `resume`, `suspended`, and `deprovision FLATKEY --drained-to N --suspension ID` work with the setting off).

## Exit codes and arguments

`mashctl policy` with any subcommand prints its usage to stderr and exits 2: the group has no subcommands yet. Every verb exits 0 on success, 2 for a usage error (a wrong or missing argument, an unknown flag, a stray argument, an argument that fails validation) and 1 for any failure; `suspended` also uses 3 for "not suspended"; `audit` uses 1 only for `--strict` findings and 3 when it could not complete (see [audit.md](audit.md#exit-codes)). An argument that fails validation is named in the message but never printed back, so a secret pasted into the wrong place is not echoed. `mashctl deprovision NAME` for a name with no record and no inbox prints `no endpoint named NAME` and exits 1; the admin operation itself stays idempotent, only the verb looks first. `mashctl permissions NAME` prints the legacy wide mail permission set for hand-made users; it says so on stderr (stdout stays the JSON or the `--nsc` flags), because endpoints added by `mashctl add` get a narrower, per-endpoint set in their JWT.

## Zones were removed

Zones are gone ([ADR 0091](decisions/0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md)): mail has one five-token subject. A directory record a zoned release wrote still reads, so the upgrade guards can name it. `mashctl audit` reports it as an error (`audit --strict` fails), and `mash serve` refuses to start while one is in the directory. `mashctl add` and `mashctl reissue` refuse while a zone policy of an older release is applied; with one applied, stay on the last release that has zones.

`mashctl unzone NAME [--sys-creds FILE]` makes a zoned record five-token: it checks that no zone policy is applied (it needs the system account credentials; there is no flag that skips the check), validates the inbox stream, rewrites the record and moves the stream's subjects to the five-token filter without losing stored mail. It is idempotent. It does not reissue the user: afterwards run `mashctl permissions NAME --nsc`. The steps for an upgrade are in [operations](operations.md#upgrading-admin-tooling-across-zone-removal).

## Before you push with nsc

If you also edit the account with `nsc`, these steps keep it from undoing mashctl.

- Run `nsc pull -A` before any `nsc push`, so a push from a stale local copy does not overwrite a revocation mashctl made. `mashctl issued check` shows keys connected to a server that mash did not issue.
- `nsc push` rejects an account server URL that starts with `tls://`; give nsc a `nats://` URL. For a server with a private CA, nsc needs `SSL_CERT_FILE` set to the CA's PEM file (mashctl takes `tls_ca` instead).
- `nsc revocations add-user` followed by an `nsc push` revokes a user; `mashctl retire` and `mashctl deprovision` revoke the user of an endpoint themselves.
- To cut off old creds with mashctl alone, `mashctl reissue NAME` rotates the user key (the old key is revoked) when its creds file differs from what a mint issues now.
- A suspended endpoint's stored mail stays readable. `mash send` to a suspended endpoint fails with "no response from stream" and does not say the endpoint is suspended; this is a known limitation.
