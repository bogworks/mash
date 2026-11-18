# mash on Synadia Cloud

[`scripts/quickstart-synadia.sh`](../scripts/quickstart-synadia.sh) sets mash up on [Synadia Cloud](https://cloud.synadia.com) from one API token for your team. It is a demo script, not part of the mash binary: mash does not create NATS users ([ADR 0038](decisions/0038-synadia-setup-is-a-script-outside-the-binary.md), ADR 0008). The step-by-step guide is [guides/nats-synadia.md](guides/nats-synadia.md). The short walkthrough is in the [README](../README.md#quickstart-on-synadia-cloud-free-tier); the script needs bash, curl, jq and a `mash` binary, and `--help` lists its flags. `--group` is required: an endpoint's group has no default. Every connection requires TLS, so the URL Synadia reports must be `tls://`; a plain URL is refused, naming `insecure_plaintext`.

## What the script does

1. Finds your team, system and account (name one with `--team` / `--account` if the token sees several) and checks the stream limit.
2. Creates one temporary admin NATS user (`mash-quickstart-admin-<hex>`), then one NATS user per endpoint with the permissions `mashctl permissions <address>` prints (the address is `owner.group.instance`). Creds files go to `<owner.group.instance>.creds` in the config directory (`~/.config/mash` by default; `--config-dir`, else `$MASH_CONFIG_DIR`, else `$XDG_CONFIG_HOME/mash`), mode 0600.
3. Writes `~/.config/mash/config.json` (`url`, `owner`); it refuses to change a different one without `--force`.
4. Runs `mash admin init` and `mash admin provision` with the admin creds (`mash admin` is the deprecated alias of `mashctl`: it works in 0.5.5 and prints a deprecation notice on stderr; the verbs for you to run are `mashctl init` and `mashctl provision`), sends a test message between the first two endpoints, and deletes the admin user (also when it fails or is interrupted).

`--dry-run` does step 1 only and prints the plan. The token is read from `SYNADIA_API_TOKEN`, from one line on stdin (`--token-stdin`) or from a prompt; it is never taken from the command line, never stored and never printed. A real run holds a `mkdir` lock, `~/.config/mash/.quickstart.lock`, until it ends, so a second run on the same config directory is refused; if no run is going, remove a stale lock with `rm ~/.config/mash/.quickstart.lock/pid && rmdir ~/.config/mash/.quickstart.lock` (the lock holds a `pid` file, so a bare `rmdir` fails; only do this when no quickstart run is active). If provisioning fails for an endpoint, the script runs the deprecated `mash admin deprovision <address> --user <key>` for it (the verb for you to run is `mashctl deprovision`), with the NATS user key this run created, before it deletes the admin user, so a half-created inbox is removed. The `--user` guard removes the record and inbox only if the record still carries that key, checked and deleted against the record's revision; a metadata-only change by another admin in between is retried on the new revision, while a record that is another admin's, or that changed to another user or kept changing, is left in place (for a changed record the script also keeps this run's NATS user and creds file, since the endpoint may use them), and so is an inbox with no record (nothing proves it is this run's). The test message carries a random token, which must appear in the `[verified]` inbox entry.

## Free-tier limits

The free tier cannot run mash: its 5 streams are fewer than the 6 that one endpoint needs. The script refuses such an account before it creates anything. An account with a higher stream limit is needed (5 shared streams plus 1 per endpoint).

| Limit | Free tier | Effect on mash |
|---|---|---|
| Streams | 5 | 5 shared buckets plus 1 inbox per endpoint: no endpoint fits (at most 0); an account with a limit of 5 + N holds N endpoints |
| Connections | 5 | One per live session |
| Message payload | 512 KiB | mash caps a send to the server's `max_payload` |
| Replication | R1 only | No replicas, so no high availability |

The script reads the account's stream limit and checks the plan (5 shared plus 1 per endpoint; `mash admin init` makes the `mash-directory`, `mash-leases`, `mash-retired`, `mash-issued` and `mash-adoption` buckets, one stream each) against it before it creates anything. If the plan does not fit it stops and says how many streams the account allows and what the plan needs. After `init` it also counts the endpoints the mash directory already holds (each has an inbox stream) and stops, naming them, if the plan no longer fits; a `maximum number of streams` error from mash gets the same hint. Streams made by other tools are not counted, so on an account that runs other streams, pass fewer `--endpoint`s than the limit suggests.

## Troubleshooting

- **401 / unauthorized.** The token is wrong, expired or revoked. Create a new API token for your team and re-run.
- **Account full.** The capacity message above. Remove an endpoint you no longer use (see "Removing an endpoint" below), or ask for fewer `--endpoint`s.
- **A NATS user with that name already exists.** The script refuses to reuse one. Delete the user in the Synadia console, or pick another `--endpoint` name. If the endpoint's directory record is still there, remove it with `mashctl endpoint delete ADDRESS --no-revoke` and an admin creds file (see below) first.
- **A run failed in the middle of provisioning an endpoint and the account is nearly full.** If provisioning fails after it created the inbox stream but before it wrote the directory record, the script finds no record to remove and leaves the inbox stream (`inbox-<owner>_<group>_<instance>`) behind; it holds one of the account's streams. Remove it with an admin credential as in "Removing an endpoint" below (`mashctl deprovision ADDRESS --no-revoke`, without `--user`, with an admin creds file), then re-run.
- **`config.json` already has a different `url` or `owner`.** Re-run with the old `--owner`, or pass `--force` to overwrite it.
- **`MASH_URL`, `MASH_OWNER` or `MASH_CREDS` is set.** The environment beats `config.json` in every later session. The script ignores them for its own run and says so; unset them afterwards.
- **Leftover `mash-quickstart-admin-*` users.** The script deletes its admin user on exit, but a `kill -9` or a lost network can leave one behind, and the script warns if it cannot delete it. It never cleans up other runs' users: delete leftovers in the Synadia console when no other run is in progress.
- **The API changed.** Synadia's control-plane API (`/api/core/beta/`) is undocumented and may change; the script stops on a response it does not understand rather than guessing. `SYNADIA_API_URL` overrides the base URL, for tests.

## Removing an endpoint

The quickstart script deletes its temporary admin user when it ends, and `mashctl endpoint delete` needs an admin credential. On Synadia there is no operator key and no system account credentials, so the revocation step cannot run: pass `--no-revoke`. Without it the command refuses with `no system account credentials`. The full procedure is in [guides/nats-synadia.md](guides/nats-synadia.md) ("Removing").

1. In the Synadia console, create an admin NATS user in the same account with publish allowed on `$JS.API.>`, `$KV.>` and `_INBOX.>` and subscribe allowed on `_INBOX.>` and `$JS.EVENT.>`. Download its creds file and run `chmod 600` on it.
2. Delete the endpoint in mash, with your usual `MASH_URL` and `MASH_OWNER`:

   ```sh
   MASH_ADMIN_CREDS=/path/admin.creds mashctl endpoint delete ADDRESS --no-revoke
   ```

   This removes the directory record and the inbox stream, which frees a stream on the free tier.
3. Delete the endpoint's own NATS user (named by the address) in the console. `--no-revoke` leaves its key valid, so this deletion is the only revocation on Synadia.
4. Delete the admin user from step 1 in the console.

## Not available on Synadia

Synadia holds the account signing key, and the script never has it. `mashctl endpoint create`, `mashctl issued check` and `mashctl init --local` do not work on Synadia; the script makes the NATS users through the API instead.
