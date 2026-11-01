# mash on Synadia Cloud

[`scripts/quickstart-synadia.sh`](../scripts/quickstart-synadia.sh) sets mash up on [Synadia Cloud](https://cloud.synadia.com) from one API token for your team. It is a demo script, not part of the mash binary: mash does not create NATS users ([ADR 0038](decisions/0038-synadia-setup-is-a-script-outside-the-binary.md), ADR 0008). The walkthrough is in the [README](../README.md#quickstart-on-synadia-cloud-free-tier); the script needs bash, curl, jq and a `mash` binary, and `--help` lists its flags.

## What the script does

1. Finds your team, system and account (name one with `--team` / `--account` if the token sees several) and checks the stream limit.
2. Creates one temporary admin NATS user (`mash-quickstart-admin-<hex>`), then one NATS user per endpoint with the permissions `mash admin permissions <name>` prints. Creds files go to `<name>.creds` in the config directory (`~/.config/mash` by default; `--config-dir`, else `$MASH_CONFIG_DIR`, else `$XDG_CONFIG_HOME/mash`), mode 0600.
3. Writes `~/.config/mash/config.json` (`url`, `owner`); it refuses to change a different one without `--force`.
4. Runs `mash admin init` and `mash admin provision` with the admin creds, sends a test message between the first two endpoints, and deletes the admin user (also when it fails or is interrupted).

`--dry-run` does step 1 only and prints the plan. The token is read from `SYNADIA_API_TOKEN`, from one line on stdin (`--token-stdin`) or from a prompt; it is never taken from the command line, never stored and never printed. A real run holds a `mkdir` lock, `~/.config/mash/.quickstart.lock`, until it ends, so a second run on the same config directory is refused; if no run is going, remove a stale lock with `rm ~/.config/mash/.quickstart.lock/pid && rmdir ~/.config/mash/.quickstart.lock` (the lock holds a `pid` file, so a bare `rmdir` fails; only do this when no quickstart run is active). If `mash admin provision` fails for an endpoint, the script runs `mash admin deprovision` for it, but only when `mash admin list` shows a record for that name carrying the NATS user key this run created (a record that is not this run's is left in place), before it deletes the admin user, so a half-created inbox is removed. The test message carries a random token, which must appear in the `[verified]` inbox entry.

## Free-tier limits

| Limit | Free tier | Effect on mash |
|---|---|---|
| Streams | 5 | 2 shared buckets plus 1 inbox per endpoint: at most 3 endpoints |
| Connections | 5 | One per live session |
| Message payload | 512 KiB | mash caps a send to the server's `max_payload` |
| Replication | R1 only | No replicas, so no high availability |

The script reads the account's stream limit and checks the plan (2 shared plus 1 per endpoint) against it before it creates anything. If the plan does not fit it stops and says how many streams the account allows and what the plan needs. After `mash admin init` it also counts the endpoints the mash directory already holds (each has an inbox stream) and stops, naming them, if the plan no longer fits; a `maximum number of streams` error from mash gets the same hint. Streams made by other tools are not counted, so on an account that runs other streams, pass fewer `--endpoint`s than the limit suggests.

## Troubleshooting

- **401 / unauthorized.** The token is wrong, expired or revoked. Create a new API token for your team and re-run.
- **Account full.** The capacity message above. Remove an endpoint you no longer use (see "Removing an endpoint" below), or ask for fewer `--endpoint`s.
- **A NATS user with that name already exists.** The script refuses to reuse one. Delete the user in the Synadia console, or pick another `--endpoint` name. If the endpoint's directory record is still there, run `mash admin deprovision <name>` (see below) first.
- **A run failed in the middle of provisioning an endpoint and the account is nearly full.** If `mash admin provision` fails after it created the inbox stream but before it wrote the directory record, the script finds no record to remove and leaves the inbox stream (`inbox-<name>`) behind; it holds one of the free tier's streams. Remove it with an admin credential as in "Removing an endpoint" below (`mash admin deprovision <name>`), then re-run.
- **`config.json` already has a different `url` or `owner`.** Re-run with the old `--owner`, or pass `--force` to overwrite it.
- **`MASH_URL`, `MASH_OWNER` or `MASH_CREDS` is set.** The environment beats `config.json` in every later session. The script ignores them for its own run and says so; unset them afterwards.
- **Leftover `mash-quickstart-admin-*` users.** The script deletes its admin user on exit, but a `kill -9` or a lost network can leave one behind, and the script warns if it cannot delete it. It never cleans up other runs' users: delete leftovers in the Synadia console when no other run is in progress.
- **The API changed.** Synadia's control-plane API (`/api/core/beta/`) is undocumented and may change; the script stops on a response it does not understand rather than guessing. `SYNADIA_API_URL` overrides the base URL, for tests.

## Removing an endpoint

The quickstart script deletes its temporary admin user when it ends, and `mash admin deprovision` needs an admin
credential. To remove an endpoint, make one yourself:

1. In the Synadia console, create a NATS user in the same account with publish allowed on `$JS.API.>`, `$KV.>` and
   `_INBOX.>` and subscribe allowed on `_INBOX.>` and `$JS.EVENT.>`. Download its creds file.
2. Run it with your usual settings (`MASH_URL`, `MASH_OWNER` as in `config.json`):

   ```sh
   MASH_ADMIN_CREDS=/path/to/admin.creds mash admin deprovision <name>
   ```

   This fences the name and removes its directory record and inbox stream, which frees a stream on the free tier.
3. Delete the endpoint's own NATS user (named `<name>`) and the temporary admin user in the console. Deprovisioning
   does not revoke users.
