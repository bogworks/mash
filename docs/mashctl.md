# mashctl: adding, re-issuing, labeling and retiring endpoints

`mashctl add` and `mashctl reissue` mint NATS users from the compiled [policy file](policy-file.md). Each zone has one scoped signing key (kept in the key directory, `<config dir>/keys` or `MASH_KEYS_DIR`, holds one key file per zone, made by `mashctl add` or `mashctl policy apply` when a zone is new); a user is signed by its zone's key and carries only its tags, so the permissions come from the key. See [ADR 0052](decisions/0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md) and [ADR 0053](decisions/0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md).

## Prerequisites

`add` needs the account's identity key (`account.nk` in the key directory), and an account that lists the zone keys. `mashctl init` does not create that key: it only creates the directory and lease buckets on the server. The key comes from `mashctl init --local` (which makes a local server and its keys) or from `mashctl adopt` (which imports the account seed you already hold). The account push is `mashctl policy apply`. Until `policy apply` has pushed a new zone's key, creds minted for that zone cannot authenticate. `add` tells you when an account update is needed.

## Connections and trust

Plain `nats://` to a non-loopback host is refused for admin and system connections: every verb that connects with the admin or system creds (and `adopt`) requires TLS, and fails when the server does not offer it. Local servers made by `init --local` are loopback-only and opt in explicitly: `init --local` writes `"insecure_loopback": true` into the `config.json` it creates (the one minimal escape, a setting and not a flag, which may be renamed when the command-line settings are redesigned; the same setting is `MASH_INSECURE_LOOPBACK=1`, honoured only for loopback URLs), and `init --local --start` connects only to the loopback server it just started, which needs no opt-in. `policy apply`, `diff`, `repair`, `drift` and `adopt` verify the account JWT's signature, subject and issuer (the operator key, or a key declared by `<key dir>/operator.jwt`), and `apply` carries only its own zone keys: another signing key is refused when apply would carry it, until the tool that added it removes it; an explicit `--replace-zone-key ZONE` or `--remove-zone ZONE` drops the named zone's own scoped key (exact role match only). Admin and system connections never add servers the server advertises, and with `insecure_loopback` dial only localhost or a loopback address. See [operations](operations.md#applying-the-policy).

## Adopting an existing server

```sh
mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]
```

`adopt` is `init --local` for a NATS server you already run in operator mode with a full resolver and JetStream. It needs authority you already hold (the operator signing seed, the mash account's identity seed, system account creds), because a URL does not prove it and `mashctl` cannot create authority on a server it did not set up. It checks the files, the system creds, the account on the resolver, JetStream and the operator key without changing anything, then keeps the keys in the key store, copies the system creds to `<config dir>/server/sys.creds`, optionally records or mints admin creds, and writes `config.json` (`url`, `owner`, `admin_creds`, and `tls_ca` when the loaded configuration sets one; `tls_cert` and `tls_key` likewise) and an open `policy.yaml` if absent. It changes nothing on the server; the cutover of a live account is a separate step you run (`mashctl init`, `mashctl add`, `mashctl policy apply`). See [Adopting an existing server](operations.md#adopting-an-existing-server) for the checks, the files and the refusals.

## Adding an endpoint

```sh
mashctl add NAME [--label k=v]... [--tool] [--zone Z] [--policy FILE] [--note N]
```

`--tool` adds the label `kind=tool`. `--zone Z` sets the policy's subject label (default `zone`) to Z. The policy file is `--policy`, else `MASH_POLICY`, else `<config dir>/policy.yaml`. It needs the server and admin credentials (as `provision` does) and the account key.

`add` refuses a policy that would exceed the JWT budgets (the 1 MiB account JWT, the per-user connect limit) before changing anything.

A zone that already has endpoints must already have its signing key: if the key file is missing, `add` refuses before changing anything, since a new key would sign creds the account JWT does not trust; restore the key file from backup. The key is created only for a new zone (a leftover key from an earlier failed add is reused).

Order of operations; a failure at any step undoes the earlier ones, so no half-made endpoint is left:

1. Lock and load the policy file; the name must be valid and not declared.
2. Declare the endpoint in memory and compile the result.
3. Mint the user (the zone's signing key is created only if the zone is new; an unused key stays, it is in no account JWT).
4. Provision the directory record and inbox. A server failure here writes nothing.
5. Write `NAME.creds` (mode 0600, never over an existing file; the directory is made 0700 if missing). On failure the provision is rolled back.
6. Save the policy file, comments kept. If the file changed since it was loaded, the creds file and the provision are rolled back and the command says to re-run it.
7. Report `added NAME (zone Z) creds PATH`. Seeds, JWTs and creds contents are never printed.

## Follow-ups it reports

- `reissue needed: a, b (run: mashctl reissue a b)`: adding the endpoint changed the tags of existing endpoints (an `allow:` or `deny:` exception now names it), so their user JWTs are stale.
- `account update needed: run mashctl policy apply`: a new zone appeared or a zone's permission template changed, so the account JWT must carry the new scoped keys.

## Re-issuing

```sh
mashctl reissue NAME... [--policy FILE]
mashctl reissue --changed [--policy FILE]
```

Mints the endpoint's user JWT again from the current policy and replaces its creds file atomically (0600). The user key is kept, so the directory record is unchanged; the command is offline (policy file, key directory and creds file only). `--changed` re-issues every declared endpoint whose creds file holds a JWT with other tags, another issuer than the zone's current key, or another issuer account, and prints `nothing to reissue` when none does; an endpoint with no creds file on this machine is skipped with a note. Naming an endpoint with no creds file is an error: retire it and add it again.

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

- `mashctl suspend NAME` refuses all inbound mail to NAME's inbox and prints `suspended NAME: id ID, last_seq N, msgs M`. The id is a random token naming this one suspension. Stored mail stays readable; the sequence is final while the suspension holds.
- `mashctl resume NAME` restores delivery and prints `resumed NAME`.
- `mashctl suspended NAME` prints `suspended NAME since T: id ID, last_seq N, msgs M` and exits 0, or `NAME is not suspended` and exits 3. Any error exits 1.
- To remove an endpoint after reading its last mail: `mashctl suspend NAME` (note `id ID` and `last_seq N`), drain through N, then `mashctl deprovision NAME --suspension ID --drained-to N` (both flags are required together; either alone is a usage error). The deprovision refuses, changing nothing, unless the inbox is still suspended under that same id and at last_seq N when it runs, and the directory record still belongs to the user key it had at suspend time, so a `resume` by another admin cannot lose mail. The id ties the drain to that one suspension, so a re-provisioned endpoint of the same name cannot be removed by an old command. Example: `mashctl deprovision design --suspension 9f3c... --drained-to 41`. Plain `deprovision` does not check, and `--drained-to` cannot be combined with `--user`.

See [operations.md](operations.md#suspend-an-endpoints-inbound-mail).

## Retiring an endpoint

```sh
mashctl retire NAME [--policy FILE] [--delete-creds] [--force]
```

Needs the server and admin credentials. Order of operations (a failure leaves the earlier, safer state):

1. Lock and load the policy file. NAME must be declared, unless `--force` (then only the server side is cleaned up).
2. Remove the entry in memory. If tests still name NAME the command refuses (`tests still name NAME: tests[N] from->to; remove them first`); with `--force` the file is then left as it is (it would not parse) and the command exits 1 after the server step, telling you to edit the file by hand.
3. Read NAME's user key from the directory record and delete the record and inbox only if the record still holds that key (`DeprovisionIfUser`, under the name's lock). A record someone replaced meanwhile is left in place, and a failure here changes nothing. With no record the command refuses (and says to finish a partly removed endpoint with `mashctl deprovision NAME`); with `--force` it says so and goes on to the file.

   A refusal prints `nothing was changed`. Any other error from this step is reported as `PARTIAL`: the record may already be gone but the lease fence or the inbox delete did not finish, and the policy file is untouched. A retry of `mashctl retire NAME` now refuses (no record), so finish with two commands: `mashctl deprovision NAME` (removes what is left), then `mashctl retire NAME --force` (removes the policy entry; creds and the revocation note as in steps 5 and 6).
4. Save the policy file. If it changed since it was loaded, NAME is already gone from the server: the command says so and to remove the entry by hand, and exits 1.
5. Creds file: kept, unless `--delete-creds`, which removes `NAME.creds` if it is a regular file (a symlink or other file is left and reported; a missing file is fine).
6. Print `retired NAME`, the consequences above (`reissue needed` for other endpoints, `account update needed`), and always the revocation note.

What it does not do: **revoke the user.** mashctl has no revocation yet (`nsc` has: see [Before you push with nsc](#before-you-push-with-nsc)), so NAME's old creds still authenticate; the note says `revocation is not implemented yet: delete the creds file and rotate the zone key if the user must be cut off`. It also does not push the account JWT: run `mashctl policy apply` when told to.

## Checking and repairing drift

```sh
mashctl policy drift  [--policy FILE] [--sys-creds FILE] [--creds-dir DIR] [--json]
mashctl policy repair [--dry-run] [--policy FILE] [--sys-creds FILE] [--servers N] [--keep-zone ZONE]...
```

`drift` compares the policy file with the account JWT's zone keys, the key store, the directory records and the creds files `add` and `reissue` write, reports each state as `ok` or a list, fixes nothing, and exits 0 only when all four match. The creds files are read from `--creds-dir`, else where `add` writes them, and compared as `reissue --changed` does; `drift` then names each stale endpoint and what differs. `repair` is additive EXCEPT that it clears labels the file does not name on endpoints the file declares. It creates missing zone key files, pushes the account through `apply`, sets a record's zone and makes its labels equal to the file's (setting differing ones and clearing the ones the file lacks), and prints the `mashctl reissue` command for stale creds without running it; it never revokes a credential or deletes a key file, a record or a creds file, and never touches an endpoint the file does not declare. Every label it clears or changes is listed, one line each (`clear label NAME key=oldvalue`, `set label NAME key=value (was oldvalue)`). `repair --dry-run` writes nothing (no key file, no account push, no directory change) and prints the same lines. See [Checking and repairing drift](operations.md#checking-and-repairing-drift) for the four states, the steps and the limits.

## Before you push with nsc

If you also edit the account with `nsc`, these steps keep it from undoing mashctl.

- Run `nsc pull -A` before any `nsc push`. A push from a stale local copy writes the account JWT without mashctl's scoped zone keys, so the zone keys are silently reverted and creds minted for those zones stop authenticating. `mashctl policy drift` shows it (an `account` finding), and `mashctl policy repair --servers N` pushes the zone keys again.
- `nsc push` rejects an account server URL that starts with `tls://`; give nsc a `nats://` URL. For a server with a private CA, nsc needs `SSL_CERT_FILE` set to the CA's PEM file (mashctl takes `tls_ca` instead).
- `nsc revocations add-user` followed by an `nsc push` revokes a user, and a later `mashctl policy apply` keeps the revocation, because apply replaces only the scoped signing keys of the file's zones and leaves the rest of the account JWT as it is. mashctl has no revocation verb of its own.
- To cut off old creds with mashctl alone, rotate the zone key: `mashctl policy apply --replace-zone-key ZONE`, then `mashctl reissue --changed` to give the endpoints new creds. Creds signed by the old key stop working.
- A suspended endpoint's stored mail stays readable. `mash send` to a suspended endpoint fails with "no response from stream" and does not say the endpoint is suspended; this is a known limitation.
