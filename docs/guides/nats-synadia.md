---
name: mash-nats-synadia
description: Use when you want mash to run on Synadia Cloud (the free tier) instead of a NATS server you run yourself. Covers the API token, the dry run, the real run of scripts/quickstart-synadia.sh, the checks afterwards, adding and removing an endpoint, and the failures the script reports. Do not use it for a server you run in operator mode (see docs/operations.md) or for a one-machine setup (`mashctl init --local`).
---

# mash on Synadia Cloud (free tier)

This guide is for a person to follow, or to point an agent at. It is built on [`scripts/quickstart-synadia.sh`](../../scripts/quickstart-synadia.sh) and was written against mash 0.5.5. An agent following it must ask the questions in "Questions to ask first" before it runs anything, must never see the API token, and must stop at each step marked "person" until the person has done it.

The sections: [What this sets up](#what-this-sets-up), [What mash needs from the NATS server](#what-mash-needs-from-the-nats-server-and-how-synadia-cloud-provides-it), [Questions to ask first](#questions-to-ask-first), [Steps](#steps), [Checks](#checks), [Adding and removing an endpoint](#adding-and-removing-an-endpoint-later), [Troubleshooting](#troubleshooting), [Limits](#limits), [Not run here](#not-run-here).

## What this sets up

- One NATS account on Synadia Cloud holds mash's two shared buckets (the directory and the leases) and one inbox stream for each endpoint.
- Each endpoint has its own NATS user and creds file. An endpoint is addressed `owner.group.instance`: the owner is whose endpoints these are and is the permission fence, the group is the set of endpoints of one owner that work together, the instance is the name of one session.
- `~/.config/mash/config.json` (or the config directory you choose) holds the server `url` and the `owner`.
- The script is a quickstart outside the mash binary ([ADR 0038](../decisions/0038-synadia-setup-is-a-script-outside-the-binary.md)). It talks to Synadia's management API, creates the NATS users, and runs mash's own commands for the directory, the inboxes and the test message. It needs bash, curl, jq and a `mash` binary. Install mash and `mashctl` first ([install.md](../install.md), sections 1 and 2; `mashctl` comes from `go install`).

## What mash needs from the NATS server and how Synadia Cloud provides it

| mash needs | Synadia Cloud |
|---|---|
| JetStream, with file storage and R1 streams | The account has JetStream limits. The free tier allows 5 streams (figure from [synadia.md](../synadia.md), not re-checked). mash uses 2 for its shared buckets and 1 per endpoint, so the free tier fits 3 endpoints. The script reads the account's R1 stream limit and stops, creating nothing, if the plan does not fit. |
| An account and users in it | Synadia runs the operator and the account. You do not run `nsc`, an operator key or a resolver. The script creates users in the account through the management API. |
| TLS on every connection | mash requires TLS on every connection, loopback included. Synadia's servers use a certificate from a public CA, so the URL is `tls://...` and `config.json` needs no `tls_ca`. The script writes the server URL the API reports. |
| A user per endpoint, with mash's permissions | The script runs `mashctl permissions <address>` for each endpoint and creates the NATS user with exactly that permission set (publish and subscribe subjects, which fence the user to its owner). Creds go to `<config dir>/<address>.creds`, mode 0600. |
| An admin credential for the directory | The script creates one temporary admin user (`mash-quickstart-admin-<hex>`, publish on `$JS.API.>`, `$KV.>`, `_INBOX.>`; subscribe on `_INBOX.>`, `$JS.EVENT.>`), uses it for `init` and `provision`, and deletes it when it ends, also when it fails. No admin credential is left on disk. |
| Connections, one per live session | The free tier allows 5 (not re-checked). |
| A message size | mash caps a send at 1 MiB with headers, or the server's `max_payload` if that is smaller. [synadia.md](../synadia.md) gives 512 KiB for the free tier (not re-checked). |

What does not apply on Synadia Cloud:

- `mashctl init --local` makes a local operator-mode server with keys of its own. You have no use for it here.
- `mashctl endpoint create` signs a user with the account signing key (`account.nk`). Synadia holds that key and the script never has it, so `endpoint create` refuses with `no_account_key`. The script makes the NATS users through the API and then writes only the directory record (`mashctl provision`).
- `mashctl issued check`, and the revocation inside `mashctl endpoint delete`, `endpoint reissue` and `deprovision`, read and change the account JWT with the operator key and the system account creds. You hold neither. `issued check` stops with `no account signing key`; `endpoint delete` without `--no-revoke` stops with `no system account credentials`. On Synadia you remove access by deleting the NATS user in the console (see below).

## Questions to ask first

Ask all of these and wait for the answers.

1. **Where does the API token come from?** The script reads it from the environment variable `SYNADIA_API_TOKEN`, from one line on standard input (`--token-stdin`), or from a prompt when it runs in a terminal. It refuses to take it from the command line (`--token` is an unknown argument), never stores it and never prints it.
   - If the person will run the script in a terminal: the prompt keeps the token out of the shell history.
   - If a secret store holds the token: `... | bash quickstart-synadia.sh --token-stdin ...`.
   - If an agent runs the script: the person puts the token in the environment of the shell the agent uses, for example by typing `read -rs SYNADIA_API_TOKEN; export SYNADIA_API_TOKEN` in that shell. The token is never pasted into the conversation or written into a file the agent reads.
2. **Which team and account?** If the token sees one team and one account with JetStream, the script finds them. If it sees several, it stops and lists them; pass `--team NAME` and `--account NAME` (name or id).
3. **Which owner?** It defaults to your user name, lower-cased. Use `--owner` to choose. The owner, the group and each instance are 1 to 32 lower-case letters, digits and single hyphens, and start with a letter.
4. **Which group?** Required. There is no default and `config.json` has no key for it. One group for a first try (`eng`, `dev`) is enough. A group cannot be changed later: you delete the endpoint and create a new one.
5. **How many endpoints, and what are their instance names?** Each `--endpoint NAME` is one instance; the address is `owner.group.NAME`. With none, the script makes one named `<user>-<host>`. The plan must fit the stream limit: 2 shared streams plus 1 per endpoint. With the free tier's 5 streams that is at most 3 endpoints, and fewer if anything else on the account uses streams (the script does not count streams other tools made). Two endpoints are the least that gives you the test message.
6. **Which config directory?** The default is `$MASH_CONFIG_DIR`, else `$XDG_CONFIG_HOME/mash`, else `~/.config/mash`. `--config-dir DIR` takes an absolute path. If the directory already holds a `config.json` with another `url` or `owner`, the script stops unless you pass `--force`; if it already holds `<address>.creds` for a requested endpoint, the script stops and touches nothing.
7. **Dry run first?** Yes. `--dry-run` does the discovery and the capacity check, calls only read endpoints of the API, prints the plan and creates nothing.

## Steps

### 1. Create the API token (person; not run here)

In general terms: sign in at <https://cloud.synadia.com>, choose the team that owns the account, find the team's API tokens in the console, create a token, and copy it. The account must have JetStream enabled (the free tier does). The token must be allowed to list the team's systems and accounts and to create and delete NATS users in the account; which scope the console offers for that was not checked. The exact console menu names were not checked either, because no Synadia account was used to write this guide.

Give the token to the script by one of the routes in question 1.

### 2. Dry run

```sh
bash scripts/quickstart-synadia.sh --dry-run --group eng --owner alice --endpoint design --endpoint review
```

Observed against the fake API (see [Not run here](#not-run-here)), exit 0:

```text
Synadia: team 'Test Team', system 'Test System', account 'Default'
NATS URL: nats://127.0.0.1:<port>
Plan: owner 'alice', group 'eng', 2 endpoint(s) (design review)
Streams: 2 shared + 2 x 1 per endpoint = 4 (the account allows 5)

Dry run: nothing is created. A real run would:
  - create a temporary admin user, and delete it at the end
  - create NATS user 'alice.eng.design' (creds in ~/.config/mash/alice.eng.design.creds), then provision it
  - create NATS user 'alice.eng.review' (creds in ~/.config/mash/alice.eng.review.creds), then provision it
  - write ~/.config/mash/config.json (url nats://127.0.0.1:<port>, owner alice) and run 'mash admin init'
  - send a test message from alice.eng.design to alice.eng.review
```

Read it for: the team, system and account are the ones you meant; the `NATS URL` starts with `tls://` (the fake's URL is `nats://` because the fake server is plain text; on Synadia expect `tls://`, not observed here); the stream count fits; the creds paths are where you want them. If any of it is wrong, stop and change the flags.

The script also refuses, before it creates anything:

| Case | Message (observed) | Exit |
|---|---|---|
| no `--group` | `quickstart-synadia: --group is required (an endpoint's group has no default)` | 1 |
| a bad name | `quickstart-synadia: group 'Bad_Group' is not valid (1-32 lower-case letters, digits and single hyphens, starting with a letter)` | 1 |
| the token on the command line | `quickstart-synadia: unknown argument: --token (try --help)` | 1 |
| no token anywhere | `quickstart-synadia: no Synadia API token: set SYNADIA_API_TOKEN, use --token-stdin, or run on a terminal` | 1 |
| `SYNADIA_API_URL=http://example.com/api` | `quickstart-synadia: SYNADIA_API_URL 'http://example.com/api' refused: plain http is only allowed to 127.0.0.1 or localhost` | 1 |
| plan too large | `quickstart-synadia: this account allows 3 streams and the plan needs 4; room for 1 endpoint(s). Nothing was created` | 1 |

`SYNADIA_API_URL` is for tests; leave it unset against Synadia.

### 3. The real run

```sh
bash scripts/quickstart-synadia.sh --group eng --owner alice --endpoint design --endpoint review
```

What it does, in order: reads the account and its signing-key group; takes a lock on the config directory (`.quickstart.lock`); checks `config.json` and existing creds files; creates the temporary admin user and downloads its creds to a temporary directory; writes `config.json`; runs `mash admin init` (the directory and lease buckets) and lists the directory; creates each endpoint's NATS user with the permissions `mashctl permissions` prints, downloads its creds (and refuses creds that belong to another key), runs `mash admin provision` (the directory record and the inbox); deletes the admin user; sends the test message.

Observed against the fake API, exit 0 (paths shortened to `~`; the `NATS URL` line and a plain-text warning that the fake's plain connection prints on stderr before each mash command are left out, and the deprecation line is shown once, see the note after the block):

```text
Synadia: team 'Test Team', system 'Test System', account 'Default'
Plan: owner 'alice', group 'eng', 2 endpoint(s) (design review)
Streams: 2 shared + 2 x 1 per endpoint = 4 (the account allows 5)
Created the temporary admin user mash-quickstart-admin-35d34074 (deleted before this script ends).
Wrote ~/.config/mash/config.json.
mash admin is deprecated and will be removed; use mashctl (same verbs, e.g. mashctl list)
mash: directory and lease buckets ready
note: these are the permissions of the endpoint's NATS user: the owner-line set for its address.
Endpoint alice.eng.design: user created, creds in ~/.config/mash/alice.eng.design.creds
provisioned alice.eng.design: inbox inbox-alice_eng_design, mail agents.mail.alice.eng.design
note: these are the permissions of the endpoint's NATS user: the owner-line set for its address.
Endpoint alice.eng.review: user created, creds in ~/.config/mash/alice.eng.review.creds
provisioned alice.eng.review: inbox inbox-alice_eng_review, mail agents.mail.alice.eng.review
Deleted the temporary admin user. No standing admin credential exists.
Test mail: alice.eng.design sent a message to alice.eng.review, which read it as [verified].

Done. Start a session as an endpoint, for example:
  MASH_GROUP=eng MASH_NAME=design claude --channels plugin:mash@mash
```

The script calls `mash admin init` and `mash admin provision`. In 0.5.5 `mash admin` is a deprecated alias of `mashctl` with the same verbs, and each call prints the deprecation line to stderr (the real output repeats it for every call). It is a notice, not a failure.

What it writes (observed):

```text
-rw------- alice.eng.design.creds     (2209 bytes)
-rw------- alice.eng.review.creds     (2209 bytes)
-rw------- config.json                (58 bytes)
```

```json
{
  "url": "nats://127.0.0.1:<port>",
  "owner": "alice"
}
```

The config directory is made mode 0700 if it did not exist. On Synadia `url` is the `tls://` URL the API reports. `config.json` holds no admin creds and no group.

What the script deletes at exit, always (an `EXIT` trap): the temporary admin NATS user (`Deleted the temporary admin user.`), the lock, and its temporary directory. If the run fails before any endpoint is provisioned it also removes the `config.json` it created, or restores the one it replaced, and deletes the endpoint NATS users it made but did not provision (a failed run observed: `Setup failed: removed the ~/.config/mash/config.json this run created.`). If a delete of a NATS user fails, the script says `COULD NOT delete ...` with the user's id: delete it in the console.

### 4. The test message

With two or more endpoints the script sends `quickstart-synadia.sh test <random token>` from the first endpoint to the second, reads the second's inbox, and requires that exact token to appear as `[verified]`. `--no-test` skips it; with one endpoint it says `Test mail: skipped, it needs two endpoints; add a second --endpoint.`

### 5. Start sessions

```sh
MASH_GROUP=eng MASH_NAME=design claude --channels plugin:mash@mash
```

The owner comes from `config.json`; a session needs `MASH_GROUP` and `MASH_NAME` (the instance). Install the plugin and the channel allowlist as in [install.md](../install.md), sections 2 and 3.

## Checks

Each command below was run against the fake API, with `MASH_GROUP`/`MASH_NAME` set for the `mash` ones.

```sh
MASH_GROUP=eng MASH_NAME=review mash status      # config OK, creds OK, version
MASH_GROUP=eng MASH_NAME=review mash inbox       # the test message
MASH_GROUP=eng MASH_NAME=design mash who         # the endpoints and who is online
mashctl permissions alice.eng.design             # the permission set; needs the owner (config.json)
```

- `mash status` printed `name: OK - alice.eng.review`, `config: OK - url ..., owner alice`, `creds: OK - ~/.config/mash/alice.eng.review.creds exists and is readable`, and `version: mash 0.5.5 (commit ...)`. Its `tls:` line was `WARN` only because the fake is plain text; on Synadia it should not warn. The connection line says `not checked from the CLI (a running session reports it)`.
- `mash inbox` printed `#1  from alice.eng.design [verified]  ... quickstart-synadia.sh test qs-<token>`.
- `mash who` printed both addresses as `offline` with the note `created by quickstart-synadia.sh`.
- `mashctl permissions alice.eng.design` printed the publish and subscribe subjects of that endpoint (JSON; `>` is printed as `>`): publish on `agents.mail.alice.*.*`, `agents.prompt.alice.*.*`, `agents.status.alice.*.*`, `agents.hb.alice.eng.design`, `_INBOX.alice.>`, `$SRV.>` and the JetStream API subjects for its own inbox, the directory and its own lease; subscribe on `_INBOX.alice.alice_eng_design.>`, `agents.prompt.alice.eng.design`, `agents.status.alice.eng.design`, `$SRV.>` and `agents.hb.alice.*.*`.
- The admin checks need an admin creds file (the script left none). Make one as in "Removing an endpoint", then `MASH_ADMIN_CREDS=/path/admin.creds mashctl endpoint list` prints one line per endpoint (`alice/eng/design  alice.eng.design  alice_eng_design  created ..., note "created by quickstart-synadia.sh"`) and `2 endpoints`. `mashctl endpoint show alice.eng.design` prints the record and `issue record: none for this key`: the script's users were not signed by mashctl, so the issue record has no entry for them. `mashctl audit` lists the two records and `orphans: 0 stream(s), 0 lease(s), 0 creds file(s)`.

Before any `mashctl` command that connects, mashctl prints one stderr line, `mashctl: target server <url> (from ...), owner ..., admin creds ...`. Read it: it shows which server the command will change ([mashctl.md](../mashctl.md#which-server-does-mashctl-change)).

## Adding and removing an endpoint later

### Adding

Run the script again with the new instance only, with the same owner and group:

```sh
bash scripts/quickstart-synadia.sh --dry-run --group eng --owner alice --endpoint third
bash scripts/quickstart-synadia.sh --group eng --owner alice --endpoint third
```

Observed with two endpoints already provisioned: the plan reads `2 shared + 1 x 1 per endpoint = 3 (the account allows 5)`, it makes a temporary admin user, re-runs `init` (safe to repeat), provisions `alice.eng.third`, deletes the admin user, and prints `Test mail: skipped, it needs two endpoints; add a second --endpoint.` (a test needs two `--endpoint`s in the same run). `config.json` is rewritten with the same `url` and `owner`; no `--force` is needed unless they differ.

The script counts the endpoints already in the directory. If they and the new ones do not fit, it stops after `init`, names them, deletes the admin user, and restores `config.json` (observed with a limit of 4, three endpoints held and two requested):

```text
quickstart-synadia: this account allows 4 streams; it already holds the 2 shared buckets plus 3 endpoint inbox(es) (alice.eng.design alice.eng.review alice.eng.third), and the plan needs 2 more.
Remove an endpoint you no longer use (docs/synadia.md, "Removing an endpoint"), or pass fewer --endpoint.
Deleted the temporary admin user.
Setup failed: restored the previous ~/.config/mash/config.json.
```

(That message points at docs/synadia.md's removal section; the steps below are the 0.5.5 commands.)

### Removing

`mash admin deprovision`, which docs/synadia.md describes, still exists as a deprecated alias and removes the record and inbox without revoking. In 0.5.5 use `mashctl endpoint delete`. It needs an admin creds file, because the script deleted its own.

1. **Make an admin user (person; not run here).** In the Synadia console create a NATS user in the same account with publish allowed on `$JS.API.>`, `$KV.>` and `_INBOX.>` and subscribe allowed on `_INBOX.>` and `$JS.EVENT.>`. Download its creds to a file and run `chmod 600` on it; mashctl refuses admin creds that other users can read.
2. **Delete the endpoint in mash.** On Synadia there is no operator key, so the revocation step cannot run; pass `--no-revoke`:

   ```sh
   MASH_ADMIN_CREDS=/path/admin.creds mashctl endpoint delete alice.eng.third --no-revoke
   ```

   Observed: it prints the `WARNING: --no-revoke: ... NATS user key U... is NOT revoked and can still connect ...` block (before and after), then `deleted alice/eng/third (alice.eng.third)` and `creds file renamed to ~/.config/mash/alice.eng.third.creds.retired-<time> (--delete-creds removes it instead)`, exit 0. The directory record and inbox are gone (the stream is freed), the address is burned and never assigned again, and `mashctl endpoint list` no longer shows it. Running the same command again prints `no directory record on the server ...` and `the address was already retired and nothing was left on the server`, exit 0. A group cannot be changed: to move an endpoint to another group, delete it and run the script for the new address.
3. **Delete the endpoint's NATS user in the console (person; not run here).** The user is named by the address (`alice.eng.third`). `--no-revoke` leaves its key valid, so until the user is deleted in the console its creds can still connect; that deletion is the only revocation on Synadia Cloud. Whether Synadia closes a live connection when its user is deleted was not checked.
4. **Delete the admin user you made in step 1 in the console.** The retired creds file still holds the old seed; remove it when you no longer need it.

Without `--no-revoke`, `mashctl endpoint delete` stops before it changes anything: `no system account credentials: pass --sys-creds FILE or set MASH_SYS_CREDS ...` then `nothing was revoked; the key is still valid` (exit 1; observed).

To remove a failed script run's leftover inbox (the script found no record to remove), operations.md says to run `mashctl deprovision <address>` without `--user` (with `--user` and no record it refuses), under an admin creds file; add `--no-revoke` on Synadia for the reason above. This path is from [operations.md](../operations.md#removing-an-endpoint) and was not run here.

## Troubleshooting

The first four were run against the fake API; the others come from [synadia.md](../synadia.md) and were checked against the script's text, not run.

- **`mash: refusing a plain nats:// connection to "<host>": every mash connection requires TLS ...`** (observed; the script then prints `mash admin init failed`, deletes the admin user and removes the `config.json` it wrote). The URL the API reported has no `tls://`. The script does not check the scheme before it creates the admin user. Stop; do not set `MASH_INSECURE_PLAINTEXT=1` on a network you do not control.
- **`... config.json already has url 'X', not 'Y'; use --force to overwrite it`** (observed, exit 1, nothing created). Re-run with the old `--owner`, point `--config-dir` at another directory, or pass `--force` if the old value is wrong.
- **`this account allows N streams and the plan needs M`** (observed). The account cannot take the plan. Pass fewer `--endpoint`s, or remove an endpoint (above).
- **`<dir>/<address>.creds already exists and would be overwritten`** (from the script and its tests). The file may be the only local copy of another account's creds. Move it away or pick another instance name.
- **401 or "the API token is wrong, expired or revoked".** The script's text for HTTP 401 and 403. Create a new API token and re-run.
- **`several teams are visible; name one with --team: ...`** (or accounts, `--account`). The script's message when the token sees more than one.
- **`no account with JetStream is visible to this token`**, **`has no usable signing-key group`**. The account has no JetStream limits, or no signing-key group that is not scoped, not disabled and has its seed. Fix the account in the console.
- **A NATS user named `<address>` already exists in the account.** The script refuses to reuse one. Delete it in the console, or pick another instance name. If the endpoint's directory record is still there, the script stops first with `the mash directory already holds a record for '<address>' (stale or taken)`: remove it with `mashctl endpoint delete <address> --no-revoke` and an admin creds file, or pick another name. That message still names `mash admin deprovision <address>`, the deprecated alias, which also works.
- **`another quickstart run is using <dir>`.** A run holds `<dir>/.quickstart.lock`. If none is running (a killed run leaves it), remove it with `rm <dir>/.quickstart.lock/pid && rmdir <dir>/.quickstart.lock`. Only then.
- **A run failed while it provisioned an endpoint and the account is nearly full.** If the failure came after the inbox stream was created and before the record was written, the script can find no record to remove and says so; the inbox (`inbox-<owner>_<group>_<instance>`) keeps one of your streams. Remove it as in the last paragraph of "Removing".
- **`MASH_URL`, `MASH_OWNER`, `MASH_GROUP`, `MASH_CREDS`, `MASH_NAME`, `MASH_ADMIN_CREDS` or `MASH_LAUNCH_TOKEN` is set.** The script ignores them for its own run and prints `note: ignoring ...`; in later sessions they beat `config.json`, so unset them.
- **Leftover `mash-quickstart-admin-*` users.** A killed run (`kill -9`, a lost network) can leave one. The script warns when it cannot delete its own and never deletes another run's. Delete leftovers in the console when no run is going.
- **The API changed.** Synadia's management API (`/api/core/beta/`) is undocumented and unstable. The script stops on a response it does not understand.

## Limits

| Limit | Free tier (as [synadia.md](../synadia.md) states it, not re-checked) | Effect on mash |
|---|---|---|
| Streams | 5 | 2 shared + 1 per endpoint: at most 3 endpoints |
| Connections | 5 | one per live session |
| Message payload | 512 KiB | mash caps a send to the server's `max_payload` |
| Replication | R1 only | no replicas, no high availability |

mash's own caps (16 MiB and 30 days per inbox; see [operations.md](../operations.md) "Inbox limits" and "Limits") apply on Synadia too. Whether the free tier has a storage cap that these can reach is not stated in synadia.md and was not checked. The script never takes a user limit of its own: it creates the users with unlimited subscriptions, data and payload inside the account's limits.

## Not run here

Everything was run on this build (mash 0.5.5, commit e389eaf) against a fake: the script's own test harness serves the management API from a loopback HTTP server, and a real local `nats-server` in operator mode (the test environment) stands in for Synadia's servers. The script's own tests (`go test ./scripts -run Quickstart`) pass. The expected output above is from those runs, not from Synadia.

Not run, and so not confirmed:

- the live Synadia API (`https://cloud.synadia.com/api/core/beta`): its response shapes, the user and creds endpoints, what a deleted user's live connection does;
- the console steps (creating the API token, the admin user, deleting users): described in general terms, no Synadia account was used;
- the free-tier limits as Synadia applies them (5 streams, 5 connections, 512 KiB payload, R1): taken from [synadia.md](../synadia.md), not re-checked against Synadia;
- TLS to a public CA: the fake is plain text and ran with `MASH_INSECURE_PLAINTEXT=1`; a `tls://` URL from Synadia is expected, not observed;
- the script itself is not part of 0.5.5's acceptance tests (the [README](../../README.md#quickstart-on-synadia-cloud-free-tier) says so).

See also: [synadia.md](../synadia.md) (limits and failures; parts of it name the older `mash admin` commands), [operations.md](../operations.md), [install.md](../install.md), [mashctl.md](../mashctl.md).
