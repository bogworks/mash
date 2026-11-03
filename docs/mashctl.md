# mashctl: adding, re-issuing, labeling and retiring endpoints

`mashctl add` and `mashctl reissue` mint NATS users from the compiled [policy file](policy-file.md). Each zone has one scoped signing key (kept in the key directory, `<config dir>/keys` or `MASH_KEYS_DIR`, holds one key file per zone, made by `mashctl add` or `mashctl policy apply` when a zone is new); a user is signed by its zone's key and carries only its tags, so the permissions come from the key. See [ADR 0052](decisions/0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md) and [ADR 0053](decisions/0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md).

## Prerequisites

`add` needs the account's identity key (`account.nk` in the key directory), and an account that lists the zone keys. `mashctl init` does not create that key: it only creates the directory and lease buckets on the server. The key comes from `mashctl init --local` (which makes a local server and its keys) or from `mashctl adopt` (which imports the account seed you already hold). The account push is `mashctl policy apply`. Until `policy apply` has pushed a new zone's key, creds minted for that zone cannot authenticate. `add` tells you when an account update is needed.

## Connections and trust

Plain `nats://` to a non-loopback host is refused for admin and system connections: every verb that connects with the admin or system creds (and `adopt`) requires TLS, and fails when the server does not offer it. Local servers made by `init --local` are loopback-only and opt in explicitly: `init --local` writes `"insecure_loopback": true` into the `config.json` it creates (the one minimal escape, a setting and not a flag, which may be renamed when the command-line settings are redesigned; the same setting is `MASH_INSECURE_LOOPBACK=1`, honoured only for loopback URLs), and `init --local --start` connects only to the loopback server it just started, which needs no opt-in. `policy apply`, `diff`, `repair`, `drift` and `adopt` verify the account JWT's signature, subject and issuer (the operator key, or a key declared by `<key dir>/operator.jwt`), and `apply` carries only its own zone keys: another signing key is refused when apply would carry it, until the tool that added it removes it; an explicit `--replace-zone-key ZONE` or `--remove-zone ZONE` drops the named zone's own scoped key (exact role match only). Admin and system connections never add servers the server advertises, and with `insecure_loopback` dial only localhost or a loopback address. See [operations](operations.md#applying-the-policy).

## Which server does mashctl change?

`mashctl` takes its server, owner and admin credentials from `config.json`, and the endpoint's own `MASH_*` variables win over the file; there is no flag above them. Inside a mash agent session those variables are the session's, so `mashctl` would act on the session's own server. Before any verb that connects to a server, mashctl prints one line to stderr, `mashctl: target server <url> (from <source>), owner <owner> (from <source>), admin creds <path> (from <source>)`, and warns when an environment variable overrides a different `config.json` value or when `MASH_NAME` or `MASH_LAUNCH_TOKEN` shows the shell is inside a session. It informs and never refuses; offline verbs print nothing of it. A flag or context to choose the target is deferred to the CLI redesign (the CLI is frozen until then): use `env -u MASH_URL -u MASH_OWNER -u MASH_ADMIN_CREDS -u MASH_TLS_CA mashctl ...`, or run outside the session. See [operations](operations.md#which-server-does-mashctl-change).

## Adopting an existing server

```sh
mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]
```

`adopt` is `init --local` for a NATS server you already run in operator mode with a full resolver and JetStream. It needs authority you already hold (the operator signing seed, the mash account's identity seed, system account creds), because a URL does not prove it and `mashctl` cannot create authority on a server it did not set up. It checks the files, the system creds, the account on the resolver, JetStream and the operator key without changing anything, then keeps the keys in the key store, copies the system creds to `<config dir>/server/sys.creds`, optionally records or mints admin creds, and writes `config.json` (`url`, `owner`, `admin_creds`, and `tls_ca` when the loaded configuration sets one; `tls_cert` and `tls_key` likewise) and an open `policy.yaml` if absent. It changes nothing on the server; the cutover of a live account is a separate step you run (`mashctl init`, `mashctl add`, `mashctl policy apply`). `adopt` connect-tests the admin creds it ends with (given, minted or kept) before it reports success. On an operator with strict signing key usage the account identity key cannot sign users, so `--mint-admin` is refused (nothing written); pass `--admin-creds FILE` with a user signed by one of the account's signing keys. See [Adopting an existing server](operations.md#adopting-an-existing-server) for the checks, the files and the refusals.

## Adding an endpoint

```sh
mashctl add NAME [--label k=v]... [--tool] [--zone Z] [--policy FILE] [--note N]
```

`--tool` adds the label `kind=tool`. `--zone Z` sets the policy's subject label (default `zone`) to Z. The policy file is `--policy`, else `MASH_POLICY`, else `<config dir>/policy.yaml`. It needs the server and admin credentials (as `provision` does) and the account key.

`add` refuses a policy that would exceed the JWT budgets (the 1 MiB account JWT, the per-user connect limit) before changing anything.

A zone that already has endpoints must already have its signing key: if the key file is missing, `add` refuses before changing anything, since a new key would sign creds the account JWT does not trust; restore the key file from backup. The key is created only for a new zone (a leftover key from an earlier failed add is reused).

For an existing zone, or a new zone whose key file already exists (left by an earlier failed add), `add` also needs the system account credentials (`MASH_SYS_CREDS`, or `<server dir>/sys.creds` as `init --local` leaves them) to verify that the zone key is published in the account JWT as a scoped signer. It refuses, before changing anything, when the credentials are missing or the key is absent or a plain signing key (users it signed would have no restrictions); run `mashctl policy apply` (and `mashctl drift` to check). Every server's answer is checked; set `MASH_SERVERS` to the server count so none is missed. A new zone whose key this add creates is not checked: the key is new and `policy apply` publishes it.

Order of operations; a failure at any step undoes the earlier ones, so no half-made endpoint is left:

1. Lock and load the policy file; the name must be valid and not declared.
2. Declare the endpoint in memory and compile the result.
3. Mint the user (the zone's signing key is created only if the zone is new; an unused key stays, it is in no account JWT).
4. Provision the directory record and inbox. A server failure here writes nothing.
5. Write `NAME.creds` (mode 0600, never over an existing file; the directory is made 0700 if missing). On failure the provision is rolled back.
6. Save the policy file, comments kept. If the file changed since it was loaded, the creds file and the provision are rolled back and the command says to re-run it.
7. Report `added NAME (zone Z) creds PATH`. Seeds, JWTs and creds contents are never printed.

### A killed add

The rollback above covers errors, not kills. If `mashctl add` is killed mid-run (SIGKILL, power loss), it can leave an orphan that nothing can undo: a directory record and inbox and/or a `NAME.creds` file, with no endpoint `NAME` in the policy file. `add NAME` then refuses, and so does a plain `retire NAME`; `mashctl policy drift` reports `record-extra NAME`. All three say:

```
NAME looks like an interrupted add (a directory record or creds file exists but the policy file has no endpoint NAME): run mashctl retire NAME --force --delete-creds, then mashctl add NAME again
```

That is the whole recovery; add never resumes by itself. A zone signing key that a failed or killed add created is kept: an unused key is in no account JWT, and the retry reuses it.

### The policy file lock

Every command that edits the policy file holds an advisory `flock` on `<policy file>.lock` and a second one fails at once with `policy file is being edited by another mashctl`. The kernel releases the lock when the holder exits, even when it is killed, so a leftover `policy.yaml.lock` file is harmless and is not a reason to refuse: the next run reuses it and removes it when it finishes. Never remove it by hand while a mashctl is running. `Close` removes the file only while it holds the lock and only if the path is still the file it locked.

## Follow-ups it reports

- `reissue needed: a, b (run: mashctl reissue a b)`: adding the endpoint changed the tags of existing endpoints (an `allow:` or `deny:` exception now names it), so their user JWTs are stale.
- `account update needed: run mashctl policy apply`: a new zone appeared or a zone's permission template changed, so the account JWT must carry the new scoped keys.

## Re-issuing

```sh
mashctl reissue NAME... [--policy FILE]
mashctl reissue --changed [--policy FILE]
```

Mints the endpoint's user JWT again from the current policy and replaces its creds file atomically (0600). Because the new JWT is signed with the seed stored in the creds file, every reissue first connects with the admin credentials and reads the endpoint's directory record, and refuses, changing nothing, when there is no record (`NAME has no directory record (retired or never added): add it again; a leftover creds file is not reissued`), when the record belongs to another account, or when the record names a different user key than the creds file's seed. The messages name only public-key prefixes. Every reissue also refuses, changing nothing, unless the zone key that signs the new JWT is a scoped signing key of its zone in the account JWT on every server (`the zone key X... for zone Z is not in the account JWT on every server: run `mashctl policy apply` first; nothing was changed`): a JWT from an unpublished key cannot connect, and a rotation must not revoke the working key for one. A reissue that only adds allow tags (or the `deny:_none` placeholder) then keeps the user key and leaves the record unchanged, but only after it reads the account JWT (system account credentials, as `retire`; `MASH_SYS_CREDS`, and `MASH_SERVERS` for a cluster) and finds the key is not in the revocation list of any server's copy (every server must answer and the server set must be complete, as for `retire`; a cluster needs `MASH_SERVERS`): a key found revoked is rotated as below, never re-signed, and if the account JWT cannot be read or verified the reissue refuses (`cannot tell whether the user key U... is revoked: ...; nothing was changed`). A rotation also refuses, changing nothing, when the creds file's current JWT claims it was issued in the future (`NAME's current creds JWT claims it was issued in the future (T): the revocation would not cover it; fix this machine's clock (or the machine that minted it) and run again`), because a revocation does not cover a JWT issued after it; the same applies to a revoked key. The name lock is re-checked just before the new creds are staged, and the reissue refuses if it is no longer held. A creds file issued for another account is refused, changing nothing (`NAME's creds file was issued for another account (X...); mashctl can only revoke in this account, so it cannot cut that JWT off: revoke it where it was issued, then retire and add NAME again`). The record check, the revocation check and the rewrite of the creds file run under the endpoint's name lock, so two reissues of one name never interleave (the second waits, then finds the record changed and refuses, or finds nothing to do). A reissue that narrows the creds file's JWT (a tag it carries is gone, a `deny:<peer>` tag is added, or its signer or name is not the current one) rotates the user key instead, because a user JWT does not expire and the old, wider one would otherwise stay valid. It also needs the system account credentials, as `retire` does (`MASH_SYS_CREDS`, and `MASH_SERVERS` for a cluster). The order is: make a fresh user key and stage its creds in `<creds file>.next` (0600, synced), revoke the old key (confirmed), update the directory record from the old key to the new one (a compare-and-set), and only then replace the creds file with the staged creds; it prints `reissued NAME (tags: ...; new user key, old key revoked)`. If the server is unreachable, the record check refuses or the revocation is not confirmed, the record and the creds file are unchanged and the old creds still work; running the same reissue again retries and reuses the staged key. If the record update or the file write fails after the revocation, the endpoint is cut off but the new seed is safe in the staged file: run `mashctl reissue NAME` again and it finishes the rotation (it re-signs the staged key with the current policy, accepts a record that already names it, and replaces the creds file). Never retire the endpoint for this: retiring deletes its inbox. A staged file that is unreadable, loose, for another account or name, or whose JWT does not match its seed is refused (`remove the stale staged file X and run again`). `--changed` does this per endpoint. `--changed` re-issues every declared endpoint whose creds file holds a JWT with other tags, another issuer than the zone's current key, or another issuer account, and also every endpoint with a staged file `<creds file>.next` (an interrupted rotation, even if the policy was reverted since: the reissue resumes it, or refuses a stale staged file) and every endpoint whose creds file's user key the account JWT revokes (the JWT may still match the policy). Finding revoked keys reads the account JWT, so `--changed` needs the server and the system account credentials (`MASH_SYS_CREDS`, and `MASH_SERVERS` for a cluster) for each endpoint it has no other reason to select; when that cannot be read it prints `mashctl: reissue NAME: cannot tell whether its user key is revoked: ...`, goes on with the other endpoints and exits 1. It prints `nothing to reissue` only when no endpoint is selected and nothing failed; an endpoint with no creds file on this machine is skipped with a note. Naming an endpoint with no creds file is an error: retire it and add it again.

## Labels

```sh
mashctl label NAME k=v [k=v]... [--policy FILE] [--dry-run] [--sync]
mashctl unlabel NAME k [k]... [--policy FILE] [--dry-run] [--sync]
```

These edit the policy file (comments are kept). By default they are offline: nothing touches the server or any creds, so nothing changes for the running fleet until you act on what they print. The endpoint must be declared; keys and values are checked as the directory checks labels. `unlabel` of a label that is not set warns and exits 0. `--dry-run` prints what would be left to do and writes nothing. If the file changed since it was loaded, nothing is written and the command says to re-run it.

The directory record's labels (what `who --long` shows) are not changed by the policy edit, and `mashctl policy apply` pushes zone keys into the account JWT but does not reconcile directory labels. So every edit that changes the file ends with `directory labels not updated: run mashctl set-meta NAME --label k=v ... (or re-run with --sync) to refresh who --long` (`unlabel` prints `--label k=-`). `--sync` does it for you: after the policy file is saved it connects, sets (or, for `unlabel`, clears) the same labels on the record and prints `directory labels updated for NAME`; an endpoint with no record yet prints `NAME has no directory record yet; labels will be set when it is added`. The policy file is the source of truth and the record a compiled output, so if the server step fails the file stays saved, the command exits 1 and prints the exact `mashctl set-meta` to retry. `--sync` with `--dry-run` connects to nothing and writes nothing. A re-run of an edit the file already holds prints `nothing to change`, but with `--sync` still reconciles the record.

After the save the policy is compiled before and after, and each consequence is one line (none when nothing needs doing):

- `reissue needed: x, y (run: mashctl reissue x y)`: other endpoints' tags changed.
- `account update needed: run mashctl policy apply`: a zone appeared or went away, or a zone's permissions changed.
- `NAME moves from zone A to zone B: ...`: the subject label (default `zone`) was set or unset, so the endpoint's zone and mail address change. The directory record keeps zone A: `mashctl set-zone NAME B` takes a record that has no zone, but refuses one already in a zone (no endpoint changes zone in this version, ADR 0054), so for a zoned endpoint `mashctl retire NAME` and `mashctl add NAME --zone B` is the way. Until it is reissued its creds are signed by zone A's key and keep zone A's permissions.

## Suspending an endpoint's inbound mail

- `mashctl suspend NAME` refuses all inbound mail to NAME's inbox and prints `suspended NAME: id ID, last_seq N, msgs M (new mail parked; the endpoint's credentials are still valid)`. Suspend does not revoke: it is reversible, and the endpoint can still connect with its creds. The id is a random token naming this one suspension. Stored mail stays readable; the sequence is final while the suspension holds.
- `mashctl resume NAME` restores delivery and prints `resumed NAME`.
- `mashctl suspended NAME` prints `suspended NAME since T: id ID, last_seq N, msgs M` and exits 0, or `NAME is not suspended` and exits 3. Any error exits 1.
- To remove an endpoint after reading its last mail: `mashctl suspend NAME` (note `id ID` and `last_seq N`), drain through N, then `mashctl deprovision NAME --suspension ID --drained-to N` (both flags are required together; either alone is a usage error). The deprovision refuses, changing nothing, unless the inbox is still suspended under that same id and at last_seq N when it runs, and the directory record still belongs to the user key it had at suspend time, so a `resume` by another admin cannot lose mail. The id ties the drain to that one suspension, so a re-provisioned endpoint of the same name cannot be removed by an old command. Example: `mashctl deprovision design --suspension 9f3c... --drained-to 41`. The revocation of the endpoint's user key happens in this command, only once those checks hold (see below). Plain `deprovision` does not check, and `--drained-to` cannot be combined with `--user`.

See [operations.md](operations.md#suspend-an-endpoints-inbound-mail).

## Retiring an endpoint

```sh
mashctl retire NAME [--policy FILE] [--delete-creds] [--force] [--no-revoke]
```

Without `--force`, a NAME that has a record or creds file but no policy entry refuses with the interrupted-add recovery shown under [A killed add](#a-killed-add).

Needs the server and admin credentials, and, to revoke the user, the operator and account keys in the key store and the system account credentials (`MASH_SYS_CREDS`, or the `sys.creds` that `mashctl init --local` wrote; `MASH_SERVERS` as for `policy apply`; revocation has no flags for these). Order of operations (a failure leaves the earlier, safer state):

1. Lock and load the policy file. NAME must be declared, unless `--force` (then only the server side is cleaned up).
2. Remove the entry in memory. If tests still name NAME the command refuses (`tests still name NAME: tests[N] from->to; remove them first`); with `--force` the file is then left as it is (it would not parse) and the command exits 1 after the server step, telling you to edit the file by hand.
3. Read NAME's user key from the directory record and **revoke it** (see Revocation below); if the revocation fails the command stops here, with nothing removed, and says why and that `--no-revoke` skips it (`--no-revoke` is a temporary flag name, to be renamed with the CLI redesign). Then delete the record and inbox only if the record still holds that key (`DeprovisionIfUser`, under the name's lock). A record someone replaced meanwhile is left in place, and a failure here changes nothing. With no record the command refuses (and says to finish a partly removed endpoint with `mashctl deprovision NAME`); with `--force` it says so and goes on to the file.

   A refusal prints `nothing was changed`. Any other error from this step is reported as `PARTIAL`: the record may already be gone but the lease fence or the inbox delete did not finish, and the policy file is untouched. A retry of `mashctl retire NAME` now refuses (no record), so finish with two commands: `mashctl deprovision NAME` (removes what is left), then `mashctl retire NAME --force` (removes the policy entry; creds and the revocation note as in steps 5 and 6).
4. Save the policy file. If it changed since it was loaded, NAME is already gone from the server: the command says so and to remove the entry by hand, and exits 1.
5. Creds file: renamed to `NAME.creds.retired-<UTC yyyymmddThhmmssZ>` in the same directory (mode kept), so `mashctl add NAME` works again at once, and the rename is reported. `--delete-creds` removes `NAME.creds` instead. Only a regular file is touched (a symlink or other file is left and reported); a missing file is fine and nothing is printed. The retired file still holds the user's seed: delete it when you no longer need it. `retire` does not revoke the user: anyone holding the old creds keeps access until the zone key is rotated or the user is revoked with nsc (see Before you push with nsc), same as after `--delete-creds`.
6. Print `retired NAME`, the consequences above (`reissue needed` for other endpoints, `account update needed`), and the revocation line (`user key U... revoked in the account JWT`, or the `--no-revoke` warning).

Retire does not push policy changes: run `mashctl policy apply` when told to.

### Revocation

`retire` and `deprovision` revoke the endpoint's NATS user in the same command (`mashctl suspend` does not). The revocation is an entry in the account JWT, by user public key and time: NATS refuses a user JWT for that key issued at or before the revocation time, and a connected user is closed when the server takes the account update, so credentials stop working everywhere the update reached. mashctl signs the update with the operator key, pushes it through the system account to every known server, requires every one to acknowledge it, and reads it back; if that cannot be shown it exits non-zero with `not confirmed` and running the command again is safe. An existing revocation is never lowered or removed, and `policy apply` keeps revocations when it re-signs the account JWT.

`deprovision NAME` revokes the user of NAME's directory record, `deprovision NAME --user U` revokes U (unless the record names another user: then it refuses and revokes nothing), and `--suspension ID --drained-to N` revokes only when its checks pass. With no record there is nothing to revoke. `--no-revoke` (same warning, same temporary name) is the one mashctl option for it, and the system credentials and server count come from `MASH_SYS_CREDS` and `MASH_SERVERS`; `mash admin deprovision` has no key store and still only advises revoking with your operator tooling.

Because revocation is by key and time, a reissue (`mashctl reissue`, `mashctl add`) for a retired name needs a new user key (a new nkey), and a user JWT issued in the same second as the revocation is revoked too. Rotating a zone key (`--replace-zone-key`) is the other way to cut off every credential a zone signed.

## Exit codes and arguments

`policy diff` and `policy apply --dry-run` exit 1 when they refuse (a conflict, an unknown signing key, a zone outside the policy nobody acknowledged, a replaced zone key, an issuer that does not verify, or servers that hold different account JWTs that cannot be told apart), so a CI gate built on `diff` fails on a refusal; they exit 0 when they only report differences or nothing. Every verb exits 0 on success, 2 for a usage error (a wrong or missing argument, an unknown flag, a stray argument, an argument that fails validation) and 1 for any failure; `suspended` also uses 3 for "not suspended". An argument that fails validation is named in the message but never printed back, so a secret pasted into the wrong place is not echoed. `mashctl deprovision NAME` for a name with no record and no inbox prints `no endpoint named NAME` and exits 1; the admin operation itself stays idempotent, only the verb looks first. `mashctl permissions NAME` prints the legacy wide mail permission set for hand-made users; it says so on stderr (stdout stays the JSON or the `--nsc` flags), because endpoints added by `mashctl add` get their permissions from the policy file's zone key (see `mashctl policy explain`).

## Checking and repairing drift

```sh
mashctl policy drift  [--policy FILE] [--sys-creds FILE] [--creds-dir DIR] [--json]
mashctl policy repair [--dry-run] [--policy FILE] [--sys-creds FILE] [--servers N] [--keep-zone ZONE]...
```

`drift` compares the policy file with the account JWT's zone keys, the key store, the directory records and the creds files `add` and `reissue` write, reports each state as `ok` or a list, fixes nothing, and exits 0 only when all four match. The creds files are read from `--creds-dir`, else where `add` writes them, and compared as `reissue --changed` does; `drift` then names each stale endpoint and what differs. `repair` is additive EXCEPT that it clears labels the file does not name on endpoints the file declares. It creates missing zone key files, pushes the account through `apply`, sets a record's zone and makes its labels equal to the file's (setting differing ones and clearing the ones the file lacks), and prints the `mashctl reissue` command for stale creds without running it; it never revokes a credential or deletes a key file, a record or a creds file, and never touches an endpoint the file does not declare. Every label it clears or changes is listed, one line each (`clear label NAME key=oldvalue`, `set label NAME key=value (was oldvalue)`). `repair --dry-run` writes nothing (no key file, no account push, no directory change) and prints the same lines. See [Checking and repairing drift](operations.md#checking-and-repairing-drift) for the four states, the steps and the limits.

`drift` asks every server for the account JWT (the expected count is the ping's, or `MASH_SERVERS` when larger) and reports servers that differ (`servers differ: N of M servers answered; K distinct account JWTs`, with the issued-at of each and how many servers hold it) or that did not answer (`only N of M servers answered`); the account block is `ok` only when all of them hold the same JWT and it matches the file. Fix split servers with `mashctl policy apply` again; `policy apply` and `repair` print `already up to date` only when every server confirmed.

## Before you push with nsc

If you also edit the account with `nsc`, these steps keep it from undoing mashctl.

- Run `nsc pull -A` before any `nsc push`. A push from a stale local copy writes the account JWT without mashctl's scoped zone keys, so the zone keys are silently reverted and creds minted for those zones stop authenticating. `mashctl policy drift` shows it (an `account` finding), and `mashctl policy repair --servers N` pushes the zone keys again.
- `nsc push` rejects an account server URL that starts with `tls://`; give nsc a `nats://` URL. For a server with a private CA, nsc needs `SSL_CERT_FILE` set to the CA's PEM file (mashctl takes `tls_ca` instead).
- `nsc revocations add-user` followed by an `nsc push` revokes a user, and a later `mashctl policy apply` keeps the revocation, because apply replaces only the scoped signing keys of the file's zones and leaves the rest of the account JWT as it is. mashctl has no revocation verb of its own.
- To cut off old creds with mashctl alone, rotate the zone key. `--replace-zone-key` only replaces a server key that differs from the zone's key file, so first move the zone's key file aside (`ZONE.nk` in the `zones` directory of the key directory; keep a backup), then run `mashctl policy apply --replace-zone-key ZONE`: apply makes a new key and swaps it in for the old one on the server. Then run `mashctl reissue --changed` to give the endpoints new creds. Creds signed by the old key stop working. If you skip the move, the command succeeds without changing the signer and the old creds keep working.
- A suspended endpoint's stored mail stays readable. `mash send` to a suspended endpoint fails with "no response from stream" and does not say the endpoint is suspended; this is a known limitation.
