# scripts/quickstart-synadia.sh against the real Synadia Cloud API

- Date: 2026-10-31 (UTC ~22:43). Commit under test: 6648f4f plus the one-line MASH_LAUNCH_TOKEN unset (see below).
- Account: a free-tier Synadia Cloud account (team/system/account 'Default', tls://connect.ngs.global), throwaway test team.
- Token: read from the secret store and piped to `--token-stdin`; never in argv or env. The evidence directory was scanned for the token: not present.
- Run: `quickstart-synadia.sh --token-stdin --owner mashlive --endpoint qs-a --endpoint qs-b` with an empty temp HOME. Exit 0. Output: [run.log](run.log).

## Result
- Created a temporary admin user, two endpoint users, creds files at mode 600, config.json at mode 600 (url and owner only), the two buckets, two inbox streams; provisioned both; test mail qs-a to qs-b read as [verified]; the admin user was deleted by the script.
- A `--dry-run` before it discovered team, system and account and created nothing.
- The account's stream limit is 5; the plan used 4.

## The first real run failed, and what it taught
The first real run stopped at the second inbox: `maximum number of streams reached`. Endpoints from an earlier live test still held streams, and the script's capacity check counted only its own plan. The script now runs `mash admin list` after `mash admin init`, counts the directory's other endpoints, and stops with a hint (and gives the same hint on that server error). Streams created by other tools are still not counted.
The run also printed a `MASH_LAUNCH_TOKEN` note because the variable was set in the calling shell; the script now ignores it like the other MASH_* variables.

## Cleanup
Both endpoints deprovisioned with a throwaway admin user, all test NATS users and that admin user deleted; the account holds only its pre-existing user afterwards.

## Not covered
Behaviour under kill -9 between creating and deleting the admin user (the console shows a mash-quickstart-admin-* user to delete).
