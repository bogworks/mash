# `mash init synadia` live proof (PR #130): 2026-10-31 21:16-21:17Z
Binary <scratch>/mash-main, `mash 0.0.0-dev (commit 7ebf0ab4a05dbe47116d3fd2bd9452ff470598d3)` (main). Real Synadia
free-tier test account (team "<team>", system NGS, account Default, url tls://connect.ngs.global). The token
was piped from `secrets get` into `--token-stdin`, never in env, argv, disk or this directory. HOME was a temp dir
(deleted afterwards). Ambient MASH_URL, MASH_CREDS, MASH_NAME, MASH_OWNER, MASH_LAUNCH_TOKEN were unset for the mash
command only (see Surprises). Full output: timeline.log. Nothing was deleted in the account.

| step | command (flags after `init synadia --token-stdin`) | result |
|---|---|---|
| 1 dry-run | `--dry-run --endpoint m2proof-a --endpoint m2proof-b --owner bogworks-mash2proof` | PASS, exit 0. Plan printed, 4 of 5 streams, nothing created |
| 2 real run | same without `--dry-run` | PASS, exit 0. Admin user created and deleted; both endpoints created; test mail `m2proof-a sent #1 to m2proof-b, which read it as [verified]` |
| 3 rerun | same as 2 | PASS, exit 0. "reused user ... already provisioned" for both; fresh admin created and deleted; test mail #2 [verified] |
| 4 capacity | read from the plan line of step 2 | Room: 4 of 5 streams planned, "Room for 1 more endpoint(s)". The earlier lane's 3 endpoints were not in the way (see Surprises) |
| 5a duplicate, same HOME | `--owner someone-else --no-test` | refused locally, exit 1: `config.json already has owner "bogworks-mash2proof", not "someone-else"; use --force to overwrite it`. No Synadia call after discovery |
| 5b duplicate, fresh HOME | `--endpoint m2proof-a --owner someone-else --no-test` | refused, exit 1: `endpoint m2proof-a is already provisioned for owner "bogworks-mash2proof" ... re-run with --owner bogworks-mash2proof, or deprovision it first`. No side effects |

Post-run checks:
- Temp admin gone: yes. Every successful run printed "Deleted the temporary admin user". A later run (extra, `--no-test`)
  printed no leftover-admin warning, so the admin created in the failed 5b run was cleaned up too.
- Modes in temp HOME (`stat -c %a`): both endpoint creds files 600 (m2proof-a, m2proof-b), config.json 600, config.json.lock 600.
- Token scan of timeline.log (eyJ, Bearer, 40+ char base64): clean. No creds content recorded.

## Surprises
- The duplicate-NATS-user response was not reachable. Re-running reuses the user, and a different owner is stopped by the
  config guard (5a) or the directory check (5b) before any user create. The PR's guess about a raw duplicate-user error
  from the API was therefore not observed.
- Ambient `MASH_URL` / `MASH_CREDS` in a running mash session make init refuse (exit 1, before any create) with a
  message naming the variable. The message is clear, but anyone running init from inside a mash endpoint's shell hits it.
- The dry-run cannot count streams already in use ("reading them needs the temporary admin user"), so it shows only the
  plan, not real usage. The real run's capacity check passed with 4 of 5, so the account held none of the earlier lane's
  endpoints at this time (the 4 streams would be 2 shared plus these 2 endpoints only if the others are gone or on another account).
- When init fails after creating the admin user (5b), no "Deleted" line is printed, yet no leftover admin was found next run.
  The cleanup appears to happen silently on the error path.
- Left behind in the account: users and streams m2proof-a and m2proof-b, shared buckets, 2 test mails. Not deleted, per instruction.
