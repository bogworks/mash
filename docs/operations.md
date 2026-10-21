# Operating mash

mash needs a NATS server in operator mode with JetStream, and one account for mash. mash itself never mints NATS users and never holds an operator or account seed: you issue endpoint users with your own tooling (`nsc`, or any JWT issuer), using the permissions `mash admin permissions` prints, and mash creates the streams and directory entries behind them.

## The account

Create an account (these examples call it `MASH`) with JetStream limits sized for your endpoints: one stream per endpoint plus two KV buckets, and one consumer per endpoint plus temporary watch consumers.

```sh
nsc add account MASH
nsc edit account MASH --sk generate --js-disk-storage 2G --js-streams 64 --js-consumer 128
nsc push --account MASH
```

mash stores everything on file storage; it needs no memory store. A server with no memory store configured refuses an account that asks for one, so leave `--js-mem-storage` unset.

Add an admin user for mash's admin commands (full access to the account), and keep its credentials only where you run `mash admin`:

```sh
nsc add user --account MASH mashadmin
nsc generate creds --account MASH --name mashadmin > ~/.config/mash/admin.creds
chmod 600 ~/.config/mash/admin.creds
```

Then create the directory and lease buckets once:

```sh
export MASH_URL=tls://nats.example.com:4222 MASH_OWNER=alice MASH_ADMIN_CREDS=~/.config/mash/admin.creds
mash admin init
```

## Adding an endpoint

An endpoint is one named Claude Code session. Names are lower-case letters, digits and single hyphens, up to 48 characters, unique per owner.

```sh
name=design
mapfile -t flags < <(mash admin permissions "$name" --nsc)
nsc add user --account MASH "$name" "${flags[@]}"
user=$(nsc describe user --account MASH --name "$name" --json | jq -r .sub)
account=$(nsc describe account MASH --json | jq -r .sub)
mash admin provision "$name" --user "$user" --account "$account" --note "design session on laptop"
nsc generate creds --account MASH --name "$name" > ~/.config/mash/$name.creds
chmod 600 ~/.config/mash/$name.creds
```

Deliver the credentials file to the machine that runs the session, and nowhere else.

`mash admin permissions <name>` prints the permission set as JSON; `--nsc` prints the same set as `nsc add user` flags, one argument per line. The set lets an endpoint:

- send mail to any endpoint of the owner, and send live SAP prompts and status probes to them;
- publish its own heartbeat and answer SAP discovery;
- read and acknowledge its own inbox, and read its own history;
- read (never write) the directory;
- hold its own lease and nothing else.

It cannot read another endpoint's inbox or history, take another endpoint's name, write the directory, or delete or purge any stream. The integration tests assert each of these denials against a real server.

## Removing an endpoint

```sh
mash admin deprovision design
nsc revocations add-user --account MASH --name design
nsc push --account MASH
```

Deprovisioning removes the directory record, the lease and the inbox stream. Revoking the user stops its credentials from connecting at all. Until you revoke, mail it sends still arrives, but labelled `[unknown-signer]`, because its key is no longer in the directory.

## Inbox limits

Each inbox keeps mail for 30 days or 16 MiB, whichever comes first, dropping the oldest. Single messages are limited to 1 MiB including headers. Message deletion and purging are denied on the stream itself, so even the account admin cannot quietly rewrite history; deleting the whole stream (deprovisioning) is the only way to remove it.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| The session says it has no mash identity | `MASH_NAME` was not set when Claude Code started. |
| The session says the name is already in use | Another live session holds the lease. It expires 90 s after that session dies. |
| `--channels` says "not on the approved channels allowlist" | The managed settings allowlist is missing (see install.md, step 3). |
| Mail arrives labelled `[unknown-signer]` | The sender's key is not in the directory: it was deprovisioned, or it is not a mash endpoint. |
| `mash: endpoint X is not provisioned` | Run `mash admin provision`, or check `MASH_NAME` for a typo. |
| `do not match endpoint` | The credentials file belongs to a different endpoint than `MASH_NAME`. |
| Clients take 30-60 s to reconnect after a network blip | The server rate-limits new connections per source address. Clients retry with jitter; raise the limit for trusted sources if this matters. |
