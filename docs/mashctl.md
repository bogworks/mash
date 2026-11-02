# mashctl: adding and re-issuing endpoints

`mashctl add` and `mashctl reissue` mint NATS users from the compiled [policy file](policy-file.md). Each zone has one scoped signing key (kept in the key directory, `<config dir>/keys` or `MASH_KEYS_DIR`, made by `mashctl init`); a user is signed by its zone's key and carries only its tags, so the permissions come from the key. See [ADR 0052](decisions/0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md) and [ADR 0053](decisions/0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md).

## Prerequisites

`add` needs the account signing key, which `mashctl init` creates (the bootstrap verb is a separate change; until it is released the key can be created with the `ctlkeys` package), and an account that lists the zone keys: the account push is `mashctl apply` (also a separate change). Until `apply` has pushed a new zone's key, creds minted for that zone cannot authenticate. `add` tells you when an account update is needed.

## Adding an endpoint

```sh
mashctl add NAME [--label k=v]... [--tool] [--zone Z] [--policy FILE] [--note N]
```

`--tool` adds the label `kind=tool`. `--zone Z` sets the policy's subject label (default `zone`) to Z. The policy file is `--policy`, else `MASH_POLICY`, else `<config dir>/policy.yaml`. It needs the server and admin credentials (as `provision` does) and the account key.

`add` refuses a policy that would exceed the JWT budgets (the 1 MiB account JWT, the per-user connect limit) before changing anything.

Order of operations; a failure at any step undoes the earlier ones, so no half-made endpoint is left:

1. Lock and load the policy file; the name must be valid and not declared.
2. Declare the endpoint in memory and compile the result.
3. Mint the user (the zone's signing key is created if the zone is new; an unused key stays, it is in no account JWT).
4. Provision the directory record and inbox. A server failure here writes nothing.
5. Write `NAME.creds` (mode 0600, never over an existing file; the directory is made 0700 if missing). On failure the provision is rolled back.
6. Save the policy file, comments kept. If the file changed since it was loaded, the creds file and the provision are rolled back and the command says to re-run it.
7. Report `added NAME (zone Z) creds PATH`. Seeds, JWTs and creds contents are never printed.

## Follow-ups it reports

- `reissue needed: a, b (run: mashctl reissue a b)`: adding the endpoint changed the tags of existing endpoints (an `allow:` or `deny:` exception now names it), so their user JWTs are stale.
- `account update needed: run mashctl apply`: a new zone appeared or a zone's permission template changed, so the account JWT must carry the new scoped keys.

## Re-issuing

```sh
mashctl reissue NAME... [--policy FILE]
mashctl reissue --changed [--policy FILE]
```

Mints the endpoint's user JWT again from the current policy and replaces its creds file atomically (0600). The user key is kept, so the directory record is unchanged; the command is offline (policy file, key directory and creds file only). `--changed` re-issues every declared endpoint whose creds file holds a JWT with other tags, another issuer than the zone's current key, or another issuer account, and prints `nothing to reissue` when none does; an endpoint with no creds file on this machine is skipped with a note. Naming an endpoint with no creds file is an error: retire it and add it again.
