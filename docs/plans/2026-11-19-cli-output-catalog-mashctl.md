# mashctl output catalog

Status: research; no target output; v0.5.5 tag 8e9f2fe, captured 2026-11-19

This file records what `mashctl` v0.5.5 prints, command by command, for the review of the CLI output. It proposes no redesign and no target output. Each captured case is marked against nine points from the maintainer's review of a video of v0.5.5.

| point | what it marks |
|---|---|
| P1 | the line `mashctl: target server ... (from ...), owner ..., admin creds ... (from ...)` printed before the output |
| P2 | one address printed three ways with no labels (`alice/dev/design  alice.dev.design  alice_dev_design`) |
| P3 | tables that are not aligned, with no headers |
| P4 | `next:` lines placed against the output, and `next:` lines that do not name the screen or command that follows |
| P5 | lines over 80 columns |
| P6 | words the reader is not told about: caps, labels, seed, JWT, creds, flat key, issue record, lease, bucket, revoked-at, kind |
| P7 | output meant for an agent (it belongs behind `--json`) shown to a person |
| P8 | a field repeated on one screen (owner, group, instance) |
| P9 | information the user did not ask for, which belongs behind `--detail` |

## How this was captured

**Source.** The tree is `git archive v0.5.5`, extracted to a scratch directory. The tag object is `8e9f2fe` and it peels to commit `e389eaf`. A plain `go build` of this tree reports `mash 0.0.0-dev (commit unknown)`, so both binaries (`./cmd/mashctl`, `./cmd/mash`) were built with `-ldflags "-X github.com/bogworks/mash/internal/endpoint.Version=0.5.5 -X main.commit=e389eaf"`. Then `mash version` prints `mash 0.5.5 (commit e389eaf)` and `mashctl version` prints `mashctl 0.5.5` (see the first section).

**Environment.** Every command ran as `env -i PATH=<scratch>/bin:/usr/bin:/bin:~/.local/bin HOME=<scratch>/home MASH_CONFIG_DIR=<scratch>/cfg MASH_STATE_DIR=<scratch>/state COMMAND`, with no other `MASH_*` variable from the shell. Some cases add one or more variables or use another config directory; the case title says which. Standard input is `/dev/null`; standard output and standard error go to separate files, so the output is what a pipe sees. `COLUMNS` is unset. The code of `mashctl`, `mash` and `internal/ctl` has no test for a terminal (no `isatty`, `IsTerminal`, `ModeCharDevice`, `NO_COLOR` or `COLUMNS`), so the same bytes appear on a terminal. The scratch directory is reached through the symlink `/tmp/mc`, so the paths in the blocks are short; a config directory under a real home directory adds to the line widths that contain a path.

**Fake data.** Owner `acme`, group `team`, endpoints `alice`, `alice2`, `bob` and `carol` (`carol` with `--tool`), plus others made and deleted in the cases. The server is the one `mashctl init --local --owner acme --port 20001 --start` makes (first case of the `init` section), on a port reserved for this capture. Its PID was recorded, and the server was stopped by that PID after the last case that needs it; the `down_` cases ran after that.

**Cases.** A section holds the cases of one command in the order of capture. Each case shows the exact command line, the exit code and standard output and standard error, verbatim. `Case N (name)` gives the number the Breaks lists use and the name used in this file. Standard output and standard error longer than 40 lines are cut at 40 lines (marked in the block); `--help` text is never cut. Cases whose command line has `--json` are counted and not marked. Setup commands that only repeat an earlier case (for example the creation of `dave`, `erin`, `fay`, `gus` and `hal`) are not listed.

**Marks.** A script reads each case and reports: lines over 80 columns (a tab counts to the next multiple of 8; the count and the first such line), the preamble line (P1), a line that shows one instance in two or three of `acme/team/NAME`, `acme.team.NAME` and `acme_team_NAME` (P2), lines that begin `next:` or `Next:` (P4), and these words in the output: cap, label, seed, JWT, creds, flat key, issue record, lease, bucket, revoked-at, kind (P6). P3, P7, P8 and P9, and the reading of what a `next:` line names, are by hand. The preamble line is 197 columns in this capture and contains the words `admin creds`, so every case that prints it also carries P5 and P6.

**Secrets.** No seed, JWT or creds file content was printed. Every output file was searched for a seed prefix, `eyJ` and `BEGIN NATS`; none was found, so nothing was replaced. The values shown that start with `U`, `A` or `O` followed by 55 characters are public keys.

**Not captured.** These need a state the capture did not set up:

- `issued check` with a connected key that mash did not issue, and with a revoked key still connected (needs a client that connects with a key outside the record).
- Refusals `future_issued` (needs a clock change), `superseded`, `servers`, `revocation_unknown`, `staged_key_revoked` and `write`, and a bulk run where `--changed` finds a changed permission set or a half-finished rotation.
- A second `mashctl` process holding the reissue lock: the lock was held with `flock(1)` on the `.lock` file (`reissue_busy` case), so the pid in that message is not the holder's.
- An `adopt` of a server whose account has no JetStream (the message about the disk limit; see Findings).
- A terminal run: the code has no terminal test, so none was made.
- `mash who`, `mash send`, `mash inbox`, `mash read` and `mash status` beyond the cases in Appendix A, and the commands that need a running Claude Code session (`mash serve`, `mash hook`, `mash prompt`).
- Cluster servers (`--servers N` above 1).

## The flow create, then start a session (P4)

The demo flow is: make the local server, create an endpoint, start a session as that endpoint. Reading each `next:` line against that flow:

| step | command | line printed | what follows in the flow |
|---|---|---|---|
| 1 | `mashctl init --local --owner acme --port 20001 --start` | `Ready: ... Create an endpoint with: mashctl endpoint create acme/dev/design --make-key   (it writes the creds file; ...)` after a blank line | `mashctl endpoint create` with the user's group and name; the line fixes the group as `dev` (the group in the flow is `team`) and the name as `design` |
| 2 | `mashctl endpoint create acme/team/alice --make-key` | `next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=team MASH_NAME=alice` directly under the `creds` line | starting Claude Code with those variables; the line names the variables and no command, and does not name `mash status` |
| 3 (`--public-key`) | `mash key create --out bob.seed` | `next: send bob.pub to the host that runs mashctl, then: mashctl endpoint create ADDRESS --public-key bob.pub` | `mashctl endpoint create` with the address; the line has the placeholder `ADDRESS` |
| 4 (`--public-key`) | `mashctl endpoint create acme/team/bob --public-key bob.pub` | `next: on the endpoint's machine, run: mash credential create --seed bob.seed --jwt bob.jwt` | `mash credential create` with the seed and the JWT; the file names are shorter than the paths printed above it and there is no `--out` |
| 5 (`--public-key`) | `mash credential create ...` | `next: mash status` | starting the session; `mash status` connects to nothing and prints no `next:` line |
| 6 | `mashctl endpoint create ... --dry-run` | the same `next:` line as step 2 or 4 | running the command again without `--dry-run`; nothing was created |
| 7 | `mashctl endpoint reissue ADDRESS --make-key` | `next: restart the sessions of this endpoint so they read the new creds` | restarting the session; the line names no command |

No line in the flow names the command that starts the session, and no `next:` line is printed by `mash status`. The `Next:` blocks of `init --local` (without `--start`) and `adopt` are marked in their sections.

## Summary

One row per command. Counts are cases (captured runs) that carry the point. JSON cases are counted in `cases` and are not marked. P1, P2, P4, P5 and P6 come from the script; P3, P7, P8 and P9 are read by hand.

| command | cases | P1 | P2 | P3 | P4 | P5 | P6 | P7 | P8 | P9 |
|---|---|---|---|---|---|---|---|---|---|---|
| mashctl (no verb, version, removed and unknown verbs) | 8 | 0 | 0 | 4 | 0 | 7 | 4 | 4 | 0 | 2 |
| adopt | 9 | 0 | 0 | 1 | 4 | 9 | 9 | 4 | 4 | 4 |
| audit | 9 | 0 | 5 | 5 | 0 | 7 | 7 | 5 | 5 | 5 |
| deprovision | 11 | 10 | 0 | 0 | 0 | 11 | 11 | 2 | 3 | 2 |
| endpoint | 3 | 0 | 0 | 1 | 0 | 2 | 1 | 0 | 0 | 0 |
| endpoint create | 34 | 15 | 0 | 1 | 11 | 32 | 23 | 0 | 9 | 3 |
| endpoint delete | 18 | 10 | 10 | 1 | 0 | 15 | 11 | 1 | 4 | 3 |
| endpoint list | 8 | 3 | 2 | 2 | 1 | 6 | 3 | 0 | 2 | 2 |
| endpoint reissue | 28 | 15 | 3 | 1 | 9 | 23 | 19 | 0 | 6 | 5 |
| endpoint show | 13 | 6 | 4 | 1 | 0 | 10 | 7 | 0 | 4 | 4 |
| inbox inspect | 10 | 0 | 2 | 1 | 1 | 7 | 7 | 2 | 2 | 2 |
| init | 5 | 3 | 0 | 1 | 1 | 5 | 5 | 0 | 1 | 1 |
| init --local | 8 | 0 | 0 | 0 | 2 | 8 | 7 | 0 | 2 | 2 |
| issued | 3 | 0 | 0 | 1 | 0 | 1 | 3 | 0 | 0 | 0 |
| issued check | 5 | 2 | 0 | 2 | 0 | 4 | 4 | 2 | 0 | 2 |
| issued list | 4 | 1 | 0 | 1 | 0 | 2 | 3 | 1 | 0 | 1 |
| label | 8 | 4 | 0 | 0 | 0 | 6 | 8 | 0 | 1 | 0 |
| limits | 7 | 4 | 0 | 2 | 0 | 5 | 4 | 1 | 0 | 0 |
| list | 2 | 1 | 0 | 1 | 0 | 1 | 1 | 1 | 0 | 1 |
| permissions | 5 | 0 | 0 | 1 | 0 | 4 | 2 | 2 | 0 | 2 |
| provision | 6 | 5 | 2 | 0 | 0 | 6 | 6 | 0 | 1 | 1 |
| resume | 3 | 2 | 0 | 0 | 0 | 2 | 2 | 0 | 1 | 0 |
| set-meta | 6 | 5 | 0 | 0 | 0 | 6 | 6 | 0 | 2 | 2 |
| suspend | 5 | 4 | 0 | 0 | 0 | 5 | 4 | 0 | 2 | 0 |
| suspended | 5 | 4 | 0 | 0 | 0 | 5 | 4 | 0 | 1 | 0 |
| unlabel | 5 | 3 | 0 | 0 | 0 | 4 | 5 | 0 | 1 | 0 |
| Target, connection and configuration errors (all server verbs) | 33 | 25 | 4 | 2 | 0 | 27 | 26 | 1 | 0 | 1 |
| total | 261 | 122 | 32 | 29 | 29 | 220 | 192 | 26 | 51 | 45 |

## mashctl (no verb, version, removed and unknown verbs)

**Case 1 (usage_bare): no arguments**

```text
$ mashctl
[exit 2]
--- stdout
--- stderr
usage: mashctl <verb> [args]

  mashctl init                                 create the directory and lease buckets (or raise their caps to the configured limits)
  mashctl init --local [--dir D] [--owner O] [--port P] [--start] write the configuration of a local operator-mode NATS server with JetStream (loopback, TLS only), keys and credentials, for one developer; --start also starts it and creates the buckets
  mashctl limits [address...]                  raise existing inboxes to the configured limits (config.json "limits"; never lowers)
  mashctl limits --show                        print the effective limits
  mashctl permissions <owner.group.instance> [--nsc] the NATS permissions an endpoint user needs
  mashctl provision <owner.group.instance> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]...
  mashctl set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]... (a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)
  mashctl suspend <owner.group.instance>       refuse all inbound mail to an endpoint (its stored mail stays readable); prints the last sequence; the endpoint's credentials stay valid
  mashctl resume <owner.group.instance>        undo suspend
  mashctl suspended <owner.group.instance>     print whether an endpoint is suspended and its last sequence; exit 0 if suspended, 3 if not, 1 on error
  mashctl deprovision <owner.group.instance> [--user U | --drained-to SEQ --suspension ID] [--servers N] [--sys-creds FILE] revoke the endpoint's user in the account JWT (--no-revoke skips it; --servers N, else MASH_SERVERS, is how many servers must acknowledge; --sys-creds FILE, else MASH_SYS_CREDS, the system account creds), then remove the record and inbox; with --user, only if the record's user is still U; with --drained-to and --suspension, only if still that suspension (ID) and at last_seq SEQ
  mashctl list
  mashctl audit [--json] [--strict] [--creds-dir DIR] read only: list every directory record, inbox stream, lease and creds file with their state, the orphans; --strict exits 1 on an orphan, an unreadable item, or unread mail lost to eviction (a lower bound from consumer sequences: mail fetched but never shown, or redelivered, can make it undercount, so a clean audit does not prove nothing was lost)
  mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [--to SEQ] [--limit N] read only: one inbox's stored messages (default the last 20) with sender, trust, store time, size, kind and delivery state derived from the deliver consumer's cursors, and the consumer's totals; never creates, fetches from or acknowledges a consumer
  mashctl adopt --server URL --operator-key F --account-key F --sys-creds F [--account N] [--admin-creds F | --mint-admin] [--owner O] [--dry-run] take over an operator-mode server you already run: check your authority over it, then keep the keys and write the config; changes nothing on the server
  mashctl issued list [--json]                 the issue record: every user JWT mashctl issued (public key, address, kind, tags, time, purpose, revoked time)
  mashctl issued check [--json] [--sys-creds FILE] [--servers N] compare the user keys connected to every server with the record and report any key mash did not issue and any revoked key still connected; exit 1 on one, 2 when the check could not complete
  mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key) [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]... [--servers N] [--sys-creds FILE] [--dry-run] [--json] sign the endpoint's key and write its directory record, for example alice/dev/design
  mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE] [--servers N] [--sys-creds FILE] [--dry-run] [--json] revoke the old key on every server and name a new one
  mashctl endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json] [--servers N] [--sys-creds FILE] revoke the endpoint's user key, remove its record and inbox, burn the address, rename its creds file
  mashctl endpoint list [--json]               the endpoints in the directory
  mashctl endpoint show ADDRESS [--json]       one endpoint's directory record, creds file path and issue record
  mashctl label <owner.group.instance> k=v... [--dry-run] set labels on the endpoint's directory record
  mashctl unlabel <owner.group.instance> k... [--dry-run] remove labels from the endpoint's directory record
```

Marks: P3, P5, P6, P7; 20 line(s) over 80 columns, first: `  mashctl init                                 create the directory an...`; jargon: JWT, bucket, cap, creds, issue record, kind, label, lease

**Case 2 (usage_help): --help**

```text
$ mashctl --help
[exit 0]
--- stdout
usage: mashctl <verb> [args]

  mashctl init                                 create the directory and lease buckets (or raise their caps to the configured limits)
  mashctl init --local [--dir D] [--owner O] [--port P] [--start] write the configuration of a local operator-mode NATS server with JetStream (loopback, TLS only), keys and credentials, for one developer; --start also starts it and creates the buckets
  mashctl limits [address...]                  raise existing inboxes to the configured limits (config.json "limits"; never lowers)
  mashctl limits --show                        print the effective limits
  mashctl permissions <owner.group.instance> [--nsc] the NATS permissions an endpoint user needs
  mashctl provision <owner.group.instance> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]...
  mashctl set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]... (a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)
  mashctl suspend <owner.group.instance>       refuse all inbound mail to an endpoint (its stored mail stays readable); prints the last sequence; the endpoint's credentials stay valid
  mashctl resume <owner.group.instance>        undo suspend
  mashctl suspended <owner.group.instance>     print whether an endpoint is suspended and its last sequence; exit 0 if suspended, 3 if not, 1 on error
  mashctl deprovision <owner.group.instance> [--user U | --drained-to SEQ --suspension ID] [--servers N] [--sys-creds FILE] revoke the endpoint's user in the account JWT (--no-revoke skips it; --servers N, else MASH_SERVERS, is how many servers must acknowledge; --sys-creds FILE, else MASH_SYS_CREDS, the system account creds), then remove the record and inbox; with --user, only if the record's user is still U; with --drained-to and --suspension, only if still that suspension (ID) and at last_seq SEQ
  mashctl list
  mashctl audit [--json] [--strict] [--creds-dir DIR] read only: list every directory record, inbox stream, lease and creds file with their state, the orphans; --strict exits 1 on an orphan, an unreadable item, or unread mail lost to eviction (a lower bound from consumer sequences: mail fetched but never shown, or redelivered, can make it undercount, so a clean audit does not prove nothing was lost)
  mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [--to SEQ] [--limit N] read only: one inbox's stored messages (default the last 20) with sender, trust, store time, size, kind and delivery state derived from the deliver consumer's cursors, and the consumer's totals; never creates, fetches from or acknowledges a consumer
  mashctl adopt --server URL --operator-key F --account-key F --sys-creds F [--account N] [--admin-creds F | --mint-admin] [--owner O] [--dry-run] take over an operator-mode server you already run: check your authority over it, then keep the keys and write the config; changes nothing on the server
  mashctl issued list [--json]                 the issue record: every user JWT mashctl issued (public key, address, kind, tags, time, purpose, revoked time)
  mashctl issued check [--json] [--sys-creds FILE] [--servers N] compare the user keys connected to every server with the record and report any key mash did not issue and any revoked key still connected; exit 1 on one, 2 when the check could not complete
  mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key) [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]... [--servers N] [--sys-creds FILE] [--dry-run] [--json] sign the endpoint's key and write its directory record, for example alice/dev/design
  mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE] [--servers N] [--sys-creds FILE] [--dry-run] [--json] revoke the old key on every server and name a new one
  mashctl endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json] [--servers N] [--sys-creds FILE] revoke the endpoint's user key, remove its record and inbox, burn the address, rename its creds file
  mashctl endpoint list [--json]               the endpoints in the directory
  mashctl endpoint show ADDRESS [--json]       one endpoint's directory record, creds file path and issue record
  mashctl label <owner.group.instance> k=v... [--dry-run] set labels on the endpoint's directory record
  mashctl unlabel <owner.group.instance> k... [--dry-run] remove labels from the endpoint's directory record
--- stderr
```

Marks: P3, P5, P6, P7; 20 line(s) over 80 columns, first: `  mashctl init                                 create the directory an...`; jargon: JWT, bucket, cap, creds, issue record, kind, label, lease

**Case 3 (version): version**

```text
$ mashctl version
[exit 0]
--- stdout
mashctl 0.5.5
--- stderr
```

Marks: none

**Case 4 (removed_add): removed verb add**

```text
$ mashctl add
[exit 2]
--- stdout
--- stderr
mashctl: add was removed. Use: mashctl endpoint create alice/dev/design --public-key design.pub   (see: mashctl endpoint create --help)
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: add was removed. Use: mashctl endpoint create alice/dev/desig...`

**Case 5 (removed_reissue): removed verb reissue**

```text
$ mashctl reissue
[exit 2]
--- stdout
--- stderr
mashctl: reissue was removed. Use: mashctl endpoint reissue alice/dev/design --public-key FILE   (see: mashctl endpoint reissue --help)
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: reissue was removed. Use: mashctl endpoint reissue alice/dev/...`

**Case 6 (removed_retire): removed verb retire**

```text
$ mashctl retire
[exit 2]
--- stdout
--- stderr
mashctl: retire was removed. Use: mashctl endpoint delete alice/dev/design   (see: mashctl endpoint delete --help)
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: retire was removed. Use: mashctl endpoint delete alice/dev/de...`

**Case 7 (removed_unzone): unknown verb unzone**

```text
$ mashctl unzone
[exit 2]
--- stdout
--- stderr
mashctl: unknown command "unzone"

usage: mashctl <verb> [args]

  mashctl init                                 create the directory and lease buckets (or raise their caps to the configured limits)
  mashctl init --local [--dir D] [--owner O] [--port P] [--start] write the configuration of a local operator-mode NATS server with JetStream (loopback, TLS only), keys and credentials, for one developer; --start also starts it and creates the buckets
  mashctl limits [address...]                  raise existing inboxes to the configured limits (config.json "limits"; never lowers)
  mashctl limits --show                        print the effective limits
  mashctl permissions <owner.group.instance> [--nsc] the NATS permissions an endpoint user needs
  mashctl provision <owner.group.instance> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]...
  mashctl set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]... (a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)
  mashctl suspend <owner.group.instance>       refuse all inbound mail to an endpoint (its stored mail stays readable); prints the last sequence; the endpoint's credentials stay valid
  mashctl resume <owner.group.instance>        undo suspend
  mashctl suspended <owner.group.instance>     print whether an endpoint is suspended and its last sequence; exit 0 if suspended, 3 if not, 1 on error
  mashctl deprovision <owner.group.instance> [--user U | --drained-to SEQ --suspension ID] [--servers N] [--sys-creds FILE] revoke the endpoint's user in the account JWT (--no-revoke skips it; --servers N, else MASH_SERVERS, is how many servers must acknowledge; --sys-creds FILE, else MASH_SYS_CREDS, the system account creds), then remove the record and inbox; with --user, only if the record's user is still U; with --drained-to and --suspension, only if still that suspension (ID) and at last_seq SEQ
  mashctl list
  mashctl audit [--json] [--strict] [--creds-dir DIR] read only: list every directory record, inbox stream, lease and creds file with their state, the orphans; --strict exits 1 on an orphan, an unreadable item, or unread mail lost to eviction (a lower bound from consumer sequences: mail fetched but never shown, or redelivered, can make it undercount, so a clean audit does not prove nothing was lost)
  mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [--to SEQ] [--limit N] read only: one inbox's stored messages (default the last 20) with sender, trust, store time, size, kind and delivery state derived from the deliver consumer's cursors, and the consumer's totals; never creates, fetches from or acknowledges a consumer
  mashctl adopt --server URL --operator-key F --account-key F --sys-creds F [--account N] [--admin-creds F | --mint-admin] [--owner O] [--dry-run] take over an operator-mode server you already run: check your authority over it, then keep the keys and write the config; changes nothing on the server
  mashctl issued list [--json]                 the issue record: every user JWT mashctl issued (public key, address, kind, tags, time, purpose, revoked time)
  mashctl issued check [--json] [--sys-creds FILE] [--servers N] compare the user keys connected to every server with the record and report any key mash did not issue and any revoked key still connected; exit 1 on one, 2 when the check could not complete
  mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key) [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]... [--servers N] [--sys-creds FILE] [--dry-run] [--json] sign the endpoint's key and write its directory record, for example alice/dev/design
  mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE] [--servers N] [--sys-creds FILE] [--dry-run] [--json] revoke the old key on every server and name a new one
  mashctl endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json] [--servers N] [--sys-creds FILE] revoke the endpoint's user key, remove its record and inbox, burn the address, rename its creds file
  mashctl endpoint list [--json]               the endpoints in the directory
  mashctl endpoint show ADDRESS [--json]       one endpoint's directory record, creds file path and issue record
  mashctl label <owner.group.instance> k=v... [--dry-run] set labels on the endpoint's directory record
  mashctl unlabel <owner.group.instance> k... [--dry-run] remove labels from the endpoint's directory record
```

Marks: P3, P5, P6, P7, P9; 20 line(s) over 80 columns, first: `  mashctl init                                 create the directory an...`; jargon: JWT, bucket, cap, creds, issue record, kind, label, lease

**Case 8 (unknown_verb): unknown verb**

```text
$ mashctl frobnicate
[exit 2]
--- stdout
--- stderr
mashctl: unknown command "frobnicate"

usage: mashctl <verb> [args]

  mashctl init                                 create the directory and lease buckets (or raise their caps to the configured limits)
  mashctl init --local [--dir D] [--owner O] [--port P] [--start] write the configuration of a local operator-mode NATS server with JetStream (loopback, TLS only), keys and credentials, for one developer; --start also starts it and creates the buckets
  mashctl limits [address...]                  raise existing inboxes to the configured limits (config.json "limits"; never lowers)
  mashctl limits --show                        print the effective limits
  mashctl permissions <owner.group.instance> [--nsc] the NATS permissions an endpoint user needs
  mashctl provision <owner.group.instance> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]...
  mashctl set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]... (a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)
  mashctl suspend <owner.group.instance>       refuse all inbound mail to an endpoint (its stored mail stays readable); prints the last sequence; the endpoint's credentials stay valid
  mashctl resume <owner.group.instance>        undo suspend
  mashctl suspended <owner.group.instance>     print whether an endpoint is suspended and its last sequence; exit 0 if suspended, 3 if not, 1 on error
  mashctl deprovision <owner.group.instance> [--user U | --drained-to SEQ --suspension ID] [--servers N] [--sys-creds FILE] revoke the endpoint's user in the account JWT (--no-revoke skips it; --servers N, else MASH_SERVERS, is how many servers must acknowledge; --sys-creds FILE, else MASH_SYS_CREDS, the system account creds), then remove the record and inbox; with --user, only if the record's user is still U; with --drained-to and --suspension, only if still that suspension (ID) and at last_seq SEQ
  mashctl list
  mashctl audit [--json] [--strict] [--creds-dir DIR] read only: list every directory record, inbox stream, lease and creds file with their state, the orphans; --strict exits 1 on an orphan, an unreadable item, or unread mail lost to eviction (a lower bound from consumer sequences: mail fetched but never shown, or redelivered, can make it undercount, so a clean audit does not prove nothing was lost)
  mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [--to SEQ] [--limit N] read only: one inbox's stored messages (default the last 20) with sender, trust, store time, size, kind and delivery state derived from the deliver consumer's cursors, and the consumer's totals; never creates, fetches from or acknowledges a consumer
  mashctl adopt --server URL --operator-key F --account-key F --sys-creds F [--account N] [--admin-creds F | --mint-admin] [--owner O] [--dry-run] take over an operator-mode server you already run: check your authority over it, then keep the keys and write the config; changes nothing on the server
  mashctl issued list [--json]                 the issue record: every user JWT mashctl issued (public key, address, kind, tags, time, purpose, revoked time)
  mashctl issued check [--json] [--sys-creds FILE] [--servers N] compare the user keys connected to every server with the record and report any key mash did not issue and any revoked key still connected; exit 1 on one, 2 when the check could not complete
  mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key) [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]... [--servers N] [--sys-creds FILE] [--dry-run] [--json] sign the endpoint's key and write its directory record, for example alice/dev/design
  mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE] [--servers N] [--sys-creds FILE] [--dry-run] [--json] revoke the old key on every server and name a new one
  mashctl endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json] [--servers N] [--sys-creds FILE] revoke the endpoint's user key, remove its record and inbox, burn the address, rename its creds file
  mashctl endpoint list [--json]               the endpoints in the directory
  mashctl endpoint show ADDRESS [--json]       one endpoint's directory record, creds file path and issue record
  mashctl label <owner.group.instance> k=v... [--dry-run] set labels on the endpoint's directory record
  mashctl unlabel <owner.group.instance> k... [--dry-run] remove labels from the endpoint's directory record
```

Marks: P3, P5, P6, P7, P9; 20 line(s) over 80 columns, first: `  mashctl init                                 create the directory an...`; jargon: JWT, bucket, cap, creds, issue record, kind, label, lease

Breaks:

- P3 (table or listing not aligned, or no headers): cases 1, 2, 7, 8.
- P5 (lines over 80 columns): cases 1, 2, 4, 5, 6, 7, 8.
- P6 (jargon words): cases 1 (JWT, bucket, cap, creds, issue record, kind, label, lease), 2 (JWT, bucket, cap, creds, issue record, kind, label, lease), 7 (JWT, bucket, cap, creds, issue record, kind, label, lease), 8 (JWT, bucket, cap, creds, issue record, kind, label, lease).
- P7 (output meant for an agent, shown to a human): cases 1, 2, 7, 8.
- P9 (information not asked for, which belongs behind --detail): cases 7, 8.
- P3: the verb list in usage_bare, usage_help, removed_unzone and unknown_verb puts the description in a column for some verbs (`  mashctl init                                 create the directory ...`) and on the same line as the arguments for others (`  mashctl permissions <owner.group.instance> [--nsc] the NATS permissions an endpoint user needs`), so the description column moves line by line. There are no headers.
- P5: the usage block has 20 lines over 80 columns; the longest are `init --local`, `deprovision`, `audit` and `adopt`, each a whole paragraph on one line.
- P6: the usage text uses `lease buckets`, `caps`, `--cap`, `labels`, `issue record`, `JWT` and `kind` without a definition.
- P9: a bare `mashctl` (usage_bare), `mashctl --help` and an unknown verb print the same block; an unknown verb prints `unknown command "frobnicate"` and then the whole block, so the one-line error is above the block and not below it.
- Removed verbs (removed_add, removed_reissue, removed_retire): one line each on stderr, exit 2, naming the replacement; `mashctl add` is named as removed and `unzone` is treated as unknown. The replacement line is over 80 columns.
- `mashctl version` prints `mashctl 0.5.5`; the other binary prints `mash 0.5.5 (commit e389eaf)`. The two lines have different formats.


## adopt

**Case 1 (help_adopt): --help**

```text
$ mashctl adopt --help
[exit 0]
--- stdout
usage:
  mashctl adopt --server URL --operator-key F --account-key F --sys-creds F [--account N] [--admin-creds F | --mint-admin] [--owner O] [--dry-run] take over an operator-mode server you already run: check your authority over it, then keep the keys and write the config; changes nothing on the server
--- stderr
```

Marks: P3, P5, P6; 1 line(s) over 80 columns, first: `  mashctl adopt --server URL --operator-key F --account-key F --sys-cr...`; jargon: creds

**Case 2 (ad_noargs): no arguments (second config dir, empty)**

```text
$ mashctl adopt
[exit 2]
--- stdout
--- stderr
mashctl: adopt: missing: --server (the NATS URL of the server you run); --operator-key (the operator signing seed: mashctl cannot create authority on a server it did not set up, so adopt needs yours); --account-key (the identity seed of the account mash uses: mashctl cannot create an account on a server it did not set up); --sys-creds (system account user creds: they prove access to the resolver and are how `mashctl endpoint delete` revokes a user key)
adopt takes over a server you already run in operator mode; it cannot stand one up or reconfigure one from a URL (use `mashctl init --local` for a new local server)
usage: mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]
```

Marks: P5, P6; 3 line(s) over 80 columns, first: `mashctl: adopt: missing: --server (the NATS URL of the server you run)...`; jargon: creds, seed

**Case 3 (ad_missingfile): operator key file missing (second config dir; MASH_TLS_CA set to the CA of the first)**

```text
$ mashctl adopt --server tls://127.0.0.1:20001 --operator-key /tmp/mc/adopt-src/nope.nk --account-key /tmp/mc/adopt-src/account.nk --sys-creds /tmp/mc/adopt-src/sys.creds --owner acme --account MASH
[exit 1]
--- stdout
mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account "MASH" (read-only: nothing is written until every check passes)
--- stderr
mashctl: adopt: --operator-key: open /tmp/mc/adopt-src/nope.nk: no such file or directory
  it must be a 0600 file holding the operator SIGNING seed (it starts with SO)
```

Marks: P5, P6; 2 line(s) over 80 columns, first: `mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account...`; jargon: seed

**Case 4 (ad_plain): --server nats:// URL, --dry-run (second config dir; MASH_TLS_CA set)**

```text
$ mashctl adopt --server nats://127.0.0.1:20001 --operator-key /tmp/mc/adopt-src/operator.nk --account-key /tmp/mc/adopt-src/account.nk --sys-creds /tmp/mc/adopt-src/sys.creds --owner acme --account MASH --dry-run
[exit 2]
--- stdout
mashctl adopt: checking nats://127.0.0.1:20001 as owner "acme", account "MASH" (read-only: nothing is written until every check passes)
  1. files          ok: operator key OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU, account key AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6, system creds (modes 0600, seeds not printed)
--- stderr
mashctl: adopt: mash: refusing a plain nats:// connection to "127.0.0.1": every mash connection requires TLS, to any host (loopback included); set insecure_plaintext (MASH_INSECURE_PLAINTEXT=1) to allow plain text; the fix is to use a tls:// URL with the server's certificate (tls_ca for a private CA) (nothing was changed)
```

Marks: P5, P6; 3 line(s) over 80 columns, first: `mashctl adopt: checking nats://127.0.0.1:20001 as owner "acme", accoun...`; jargon: creds, seed

**Case 5 (ad_dry): --dry-run with --mint-admin (second config dir; MASH_TLS_CA set)**

```text
$ mashctl adopt --server tls://127.0.0.1:20001 --operator-key /tmp/mc/adopt-src/operator.nk --account-key /tmp/mc/adopt-src/account.nk --sys-creds /tmp/mc/adopt-src/sys.creds --owner acme --account MASH --mint-admin --dry-run
[exit 0]
--- stdout
mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account "MASH" (read-only: nothing is written until every check passes)
  1. files          ok: operator key OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU, account key AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6, system creds (modes 0600, seeds not printed)
  2. server         ok: refused a connection without credentials (operator mode); the system creds connect
  2. account        ok: "MASH" found on the resolver, subject = the account key, not expired. Not visible from here: whether the operator has revoked it
  3. jetstream      ok: the account JWT gives it: disk 2GiB, streams 64, consumers 128
  3. jetstream      (the JWT is what the account asks for; step 6 asks the server whether it enabled JetStream)
  4. system user    ok: the ping was answered (standalone, 1 server(s) replied) and the claims lookup worked. The claims update (push) permission is not exercised here: `mashctl endpoint delete` is the first to use it
  5. operator key   ok: it issued the account JWT (issuer OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU), whose signature is valid
  6. admin creds    ok: connected (minted)
  6. jetstream      ok: the server answered for the account: 9 stream(s), 4 consumer(s), 22530B of disk in use

All checks passed. --dry-run: nothing is written. This is what adopt would do:
  would import the operator key OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU into /tmp/mc/cfg2/keys (operator.nk, 0600)
  would import the account key AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6 into /tmp/mc/cfg2/keys (account.nk, 0600)
  would copy the system creds to /tmp/mc/cfg2/server/sys.creds (0600)
  would write the minted mashadmin user (it connected) to /tmp/mc/cfg2/server/mashadmin.creds (0600)
  would write /tmp/mc/cfg2/config.json with "url": "tls://127.0.0.1:20001", "owner": "acme", "admin_creds": "/tmp/mc/cfg2/server/mashadmin.creds", "tls_ca": "/tmp/mc/cfg/server/ca.pem"

Next:
  1. mashctl init                                  the directory and lease buckets (needs the admin creds)
  2. mashctl provision owner.group.instance ...    declare an endpoint (see mashctl help)

What adopt does not do: it changes nothing on the server, except that --mint-admin records the minted mashadmin user in the issue record (creating its bucket if there is none). It pushes no account update and creates no users on the server (the optional mashadmin exists only as a local creds file). The cutover of a live account, and revoking old credentials, is a separate step you run yourself.
--- stderr
```

Marks: P4, P5, P6, P7, P8, P9; 16 line(s) over 80 columns, first: `mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account...`; jargon: JWT, bucket, creds, issue record, lease, seed

**Case 6 (ad_dry_noadmin): --dry-run without admin creds (second config dir; MASH_TLS_CA set)**

```text
$ mashctl adopt --server tls://127.0.0.1:20001 --operator-key /tmp/mc/adopt-src/operator.nk --account-key /tmp/mc/adopt-src/account.nk --sys-creds /tmp/mc/adopt-src/sys.creds --owner acme --account MASH --dry-run
[exit 0]
--- stdout
mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account "MASH" (read-only: nothing is written until every check passes)
  1. files          ok: operator key OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU, account key AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6, system creds (modes 0600, seeds not printed)
  2. server         ok: refused a connection without credentials (operator mode); the system creds connect
  2. account        ok: "MASH" found on the resolver, subject = the account key, not expired. Not visible from here: whether the operator has revoked it
  3. jetstream      ok: the account JWT gives it: disk 2GiB, streams 64, consumers 128
  3. jetstream      (the JWT is what the account asks for; step 6 asks the server whether it enabled JetStream)
  4. system user    ok: the ping was answered (standalone, 1 server(s) replied) and the claims lookup worked. The claims update (push) permission is not exercised here: `mashctl endpoint delete` is the first to use it
  5. operator key   ok: it issued the account JWT (issuer OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU), whose signature is valid
  6. jetstream      not checked: without --admin-creds or --mint-admin there is no account user to ask the server whether JetStream is enabled for the account

All checks passed. --dry-run: nothing is written. This is what adopt would do:
  would import the operator key OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU into /tmp/mc/cfg2/keys (operator.nk, 0600)
  would import the account key AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6 into /tmp/mc/cfg2/keys (account.nk, 0600)
  would copy the system creds to /tmp/mc/cfg2/server/sys.creds (0600)
  would write /tmp/mc/cfg2/config.json with "url": "tls://127.0.0.1:20001", "owner": "acme", "tls_ca": "/tmp/mc/cfg/server/ca.pem"

Next:
  1. mashctl init                                  the directory and lease buckets (needs the admin creds)
  2. mashctl provision owner.group.instance ...    declare an endpoint (see mashctl help)
  no admin creds were given: `mashctl init` and `provision` need them. Pass --admin-creds FILE or --mint-admin, or set "admin_creds" in config.json

What adopt does not do: it changes nothing on the server, except that --mint-admin records the minted mashadmin user in the issue record (creating its bucket if there is none). It pushes no account update and creates no users on the server (the optional mashadmin exists only as a local creds file). The cutover of a live account, and revoking old credentials, is a separate step you run yourself.
--- stderr
```

Marks: P4, P5, P6, P7, P8, P9; 16 line(s) over 80 columns, first: `mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account...`; jargon: JWT, bucket, creds, issue record, lease, seed

**Case 7 (ad_both): --mint-admin and --admin-creds together (second config dir)**

```text
$ mashctl adopt --server tls://127.0.0.1:20001 --operator-key /tmp/mc/adopt-src/operator.nk --account-key /tmp/mc/adopt-src/account.nk --sys-creds /tmp/mc/adopt-src/sys.creds --owner acme --account MASH --mint-admin --admin-creds /tmp/mc/adopt-src/sys.creds
[exit 2]
--- stdout
--- stderr
mashctl: adopt: --admin-creds and --mint-admin are alternatives: pass one
usage: mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `usage: mashctl adopt --server URL [--account NAME] --operator-key FILE...`; jargon: creds

**Case 8 (ad_real): real run with --mint-admin (second config dir; MASH_TLS_CA set)**

```text
$ mashctl adopt --server tls://127.0.0.1:20001 --operator-key /tmp/mc/adopt-src/operator.nk --account-key /tmp/mc/adopt-src/account.nk --sys-creds /tmp/mc/adopt-src/sys.creds --owner acme --account MASH --mint-admin
[exit 0]
--- stdout
mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account "MASH" (read-only: nothing is written until every check passes)
  1. files          ok: operator key OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU, account key AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6, system creds (modes 0600, seeds not printed)
  2. server         ok: refused a connection without credentials (operator mode); the system creds connect
  2. account        ok: "MASH" found on the resolver, subject = the account key, not expired. Not visible from here: whether the operator has revoked it
  3. jetstream      ok: the account JWT gives it: disk 2GiB, streams 64, consumers 128
  3. jetstream      (the JWT is what the account asks for; step 6 asks the server whether it enabled JetStream)
  4. system user    ok: the ping was answered (standalone, 1 server(s) replied) and the claims lookup worked. The claims update (push) permission is not exercised here: `mashctl endpoint delete` is the first to use it
  5. operator key   ok: it issued the account JWT (issuer OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU), whose signature is valid
  6. admin creds    ok: connected (minted)
  6. jetstream      ok: the server answered for the account: 9 stream(s), 4 consumer(s), 22530B of disk in use

All checks passed.
  operator key OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU imported in /tmp/mc/cfg2/keys
  account key AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6 imported in /tmp/mc/cfg2/keys
  system creds copied at /tmp/mc/cfg2/server/sys.creds
  recorded the admin user in the issue record (bucket mash-issued)
  minted the mashadmin user and wrote /tmp/mc/cfg2/server/mashadmin.creds (0600)
  write /tmp/mc/cfg2/config.json with "url": "tls://127.0.0.1:20001", "owner": "acme", "admin_creds": "/tmp/mc/cfg2/server/mashadmin.creds", "tls_ca": "/tmp/mc/cfg/server/ca.pem"

Next:
  1. mashctl init                                  the directory and lease buckets (needs the admin creds)
  2. mashctl provision owner.group.instance ...    declare an endpoint (see mashctl help)

What adopt does not do: it changes nothing on the server, except that --mint-admin records the minted mashadmin user in the issue record (creating its bucket if there is none). It pushes no account update and creates no users on the server (the optional mashadmin exists only as a local creds file). The cutover of a live account, and revoking old credentials, is a separate step you run yourself.
--- stderr
```

Marks: P4, P5, P6, P7, P8, P9; 15 line(s) over 80 columns, first: `mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account...`; jargon: JWT, bucket, creds, issue record, lease, seed

**Case 9 (ad_again): the same real run a second time (second config dir; MASH_TLS_CA set)**

```text
$ mashctl adopt --server tls://127.0.0.1:20001 --operator-key /tmp/mc/adopt-src/operator.nk --account-key /tmp/mc/adopt-src/account.nk --sys-creds /tmp/mc/adopt-src/sys.creds --owner acme --account MASH --mint-admin
[exit 0]
--- stdout
mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account "MASH" (read-only: nothing is written until every check passes)
  1. files          ok: operator key OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU, account key AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6, system creds (modes 0600, seeds not printed)
  2. server         ok: refused a connection without credentials (operator mode); the system creds connect
  2. account        ok: "MASH" found on the resolver, subject = the account key, not expired. Not visible from here: whether the operator has revoked it
  3. jetstream      ok: the account JWT gives it: disk 2GiB, streams 64, consumers 128
  3. jetstream      (the JWT is what the account asks for; step 6 asks the server whether it enabled JetStream)
  4. system user    ok: the ping was answered (standalone, 1 server(s) replied) and the claims lookup worked. The claims update (push) permission is not exercised here: `mashctl endpoint delete` is the first to use it
  5. operator key   ok: it issued the account JWT (issuer OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU), whose signature is valid
  6. admin creds    ok: connected (kept)
  6. jetstream      ok: the server answered for the account: 9 stream(s), 4 consumer(s), 22891B of disk in use

All checks passed.
  operator key OAQCFPM5BOL22AGI6O76QGKIQHRLYN7DZZEATDRWD6QTEC5462SVCBJU already stored in /tmp/mc/cfg2/keys
  account key AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6 already stored in /tmp/mc/cfg2/keys
  system creds already there at /tmp/mc/cfg2/server/sys.creds
  keep the existing /tmp/mc/cfg2/server/mashadmin.creds (it connected; not minted again)
  recorded the admin user in the issue record (bucket mash-issued)
  /tmp/mc/cfg2/config.json exists and is left as it is; make sure it has: "url": "tls://127.0.0.1:20001", "owner": "acme", "admin_creds": "/tmp/mc/cfg2/server/mashadmin.creds", "tls_ca": "/tmp/mc/cfg/server/ca.pem"

Next:
  1. mashctl init                                  the directory and lease buckets (needs the admin creds)
  2. mashctl provision owner.group.instance ...    declare an endpoint (see mashctl help)

What adopt does not do: it changes nothing on the server, except that --mint-admin records the minted mashadmin user in the issue record (creating its bucket if there is none). It pushes no account update and creates no users on the server (the optional mashadmin exists only as a local creds file). The cutover of a live account, and revoking old credentials, is a separate step you run yourself.
--- stderr
```

Marks: P4, P5, P6, P7, P8, P9; 16 line(s) over 80 columns, first: `mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account...`; jargon: JWT, bucket, creds, issue record, lease, seed

Breaks:

- P3 (table or listing not aligned, or no headers): cases 1.
- P4 (a `next:` or `Next:` line is present): cases 5, 6, 8, 9.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 6, 7, 8, 9.
- P6 (jargon words): cases 1 (creds), 2 (creds, seed), 3 (seed), 4 (creds, seed), 5 (JWT, bucket, creds, issue record, lease, seed), 6 (JWT, bucket, creds, issue record, lease, seed), 7 (creds), 8 (JWT, bucket, creds, issue record, lease, seed), 9 (JWT, bucket, creds, issue record, lease, seed).
- P7 (output meant for an agent, shown to a human): cases 5, 6, 8, 9.
- P8 (field repeated on one screen): cases 5, 6, 8, 9.
- P9 (information not asked for, which belongs behind --detail): cases 5, 6, 8, 9.
- P1: adopt prints no `mashctl: target server` line; it prints its own header `mashctl adopt: checking tls://127.0.0.1:20001 as owner "acme", account "MASH" (read-only: nothing is written until every check passes)` on stdout. In ad_missingfile and ad_plain that header is printed and then the command stops.
- P4: the `Next:` block in ad_dry, ad_dry_noadmin, ad_real and ad_again lists `1. mashctl init` and `2. mashctl provision owner.group.instance ...    declare an endpoint (see mashctl help)`. The second item has the placeholder `owner.group.instance` and names `provision`, which writes a record for a user the operator signed by hand; docs/mashctl.md says the cutover is `mashctl init`, then `mashctl endpoint create` for each endpoint. The block starts after a blank line and is followed by a blank line and a paragraph that begins `What adopt does not do`.
- P5: the check lines are over 80 columns (step 1 prints the three key ids on one line, step 4 and the closing paragraph are one line each); the lines that name the files it would write are over 80 columns because of the path and the `config.json` content.
- P6: `operator key`, `account key`, `system creds`, `admin creds`, `JWT`, `resolver`, `lease` in the closing paragraph, `issue record`, `seed` in the usage error (`the operator SIGNING seed (it starts with SO)`). None is defined in the output.
- P7/P9: the eight check steps and the repeated key ids are a report for an operator or an agent; a human who ran adopt asked to take over a server and needs the result and the next command. The same key ids appear in step 1, step 5 and the `would import` lines (P8).
- ad_missingfile: with `--owner` and `--account` given, the missing operator key file is reported after the header; without `--owner` the command stops first with `no owner: pass --owner O ...` before it looks at the files, so a missing file and a missing owner give different order of checks in one command.
- ad_plain: the plain `nats://` refusal exits 2 after step 1 has printed `ok` for the files. The refusal text names `insecure_plaintext (MASH_INSECURE_PLAINTEXT=1)`.
- The default `--account` is the owner (`acme`), so a run that names no `--account` stops with `the account JWT is named "MASH", not "acme": pass --account MASH` (seen in the first attempts of the capture; the cases here pass `--account MASH`).


## audit

**Case 1 (help_audit): --help**

```text
$ mashctl audit --help
[exit 0]
--- stdout
usage:
  mashctl audit [--json] [--strict] [--creds-dir DIR] read only: list every directory record, inbox stream, lease and creds file with their state, the orphans; --strict exits 1 on an orphan, an unreadable item, or unread mail lost to eviction (a lower bound from consumer sequences: mail fetched but never shown, or redelivered, can make it undercount, so a clean audit does not prove nothing was lost)
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  mashctl audit [--json] [--strict] [--creds-dir DIR] read only: list ...`; jargon: creds, lease

**Case 2 (audit): success, text**

```text
$ mashctl audit
[exit 0]
--- stdout
mashctl audit version 2 generated 2026-11-19T00:22:46Z owner acme
creds directories: /tmp/mc/cfg
records 4, running 0
record acme_team_alice	stopped	address acme.team.alice	user UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ	stream inbox-acme_team_alice	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice
record acme_team_alice2	stopped	address acme.team.alice2	user UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN	stream inbox-acme_team_alice2	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice2
record acme_team_bob	stopped	address acme.team.bob	user UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC	stream inbox-acme_team_bob	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.bob
record acme_team_carol	stopped	address acme.team.carol	user UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	stream inbox-acme_team_carol	msgs 3	first 1	last 3	suspended false	pending 3	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.carol
orphans: 0 stream(s), 0 lease(s), 0 creds file(s)
fenced lease acme_team_ivy (a deprovision tombstone; it expires)
fenced lease acme_team_kim (a deprovision tombstone; it expires)
fenced lease acme_team_lee (a deprovision tombstone; it expires)
retired markers 3 (final 3, pending 0)	bucket mash-retired	bytes 846 of 67108864	used 0.00%
retired marker acme_team_ivy	final	retired_at 2026-11-19T00:22:24Z	user UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS
retired marker acme_team_kim	final	retired_at 2026-11-19T00:22:31Z	user UAM42OQT4W7JIRTKCAWVKJM3TI2EOIXZNSP53RMGK45BTQUMBRI7IGM7
retired marker acme_team_lee	final	retired_at 2026-11-19T00:22:32Z	user UCODJ3XFUJKE6T4JPJXRSY6EYROK3J6RILGMAO5JRKCIBGQFIG2IJNBH
--- stderr
```

Marks: P2, P3, P5, P6, P7, P8, P9; 8 line(s) over 80 columns, first: `record acme_team_alice	stopped	address acme.team.alice	user UAGT4ZQXAL...`; jargon: bucket, creds, lease

**Case 3 (audit_strict): --strict, no findings**

```text
$ mashctl audit --strict
[exit 0]
--- stdout
mashctl audit version 2 generated 2026-11-19T00:22:46Z owner acme
creds directories: /tmp/mc/cfg
records 4, running 0
record acme_team_alice	stopped	address acme.team.alice	user UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ	stream inbox-acme_team_alice	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice
record acme_team_alice2	stopped	address acme.team.alice2	user UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN	stream inbox-acme_team_alice2	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice2
record acme_team_bob	stopped	address acme.team.bob	user UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC	stream inbox-acme_team_bob	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.bob
record acme_team_carol	stopped	address acme.team.carol	user UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	stream inbox-acme_team_carol	msgs 3	first 1	last 3	suspended false	pending 3	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.carol
orphans: 0 stream(s), 0 lease(s), 0 creds file(s)
fenced lease acme_team_ivy (a deprovision tombstone; it expires)
fenced lease acme_team_kim (a deprovision tombstone; it expires)
fenced lease acme_team_lee (a deprovision tombstone; it expires)
retired markers 3 (final 3, pending 0)	bucket mash-retired	bytes 846 of 67108864	used 0.00%
retired marker acme_team_ivy	final	retired_at 2026-11-19T00:22:24Z	user UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS
retired marker acme_team_kim	final	retired_at 2026-11-19T00:22:31Z	user UAM42OQT4W7JIRTKCAWVKJM3TI2EOIXZNSP53RMGK45BTQUMBRI7IGM7
retired marker acme_team_lee	final	retired_at 2026-11-19T00:22:32Z	user UCODJ3XFUJKE6T4JPJXRSY6EYROK3J6RILGMAO5JRKCIBGQFIG2IJNBH
--- stderr
```

Marks: P2, P3, P5, P6, P7, P8, P9; 8 line(s) over 80 columns, first: `record acme_team_alice	stopped	address acme.team.alice	user UAGT4ZQXAL...`; jargon: bucket, creds, lease

**Case 4 (audit_creds): --creds-dir**

```text
$ mashctl audit --creds-dir /tmp/mc/cfg
[exit 0]
--- stdout
mashctl audit version 2 generated 2026-11-19T00:22:46Z owner acme
creds directories: /tmp/mc/cfg
records 4, running 0
record acme_team_alice	stopped	address acme.team.alice	user UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ	stream inbox-acme_team_alice	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice
record acme_team_alice2	stopped	address acme.team.alice2	user UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN	stream inbox-acme_team_alice2	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice2
record acme_team_bob	stopped	address acme.team.bob	user UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC	stream inbox-acme_team_bob	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.bob
record acme_team_carol	stopped	address acme.team.carol	user UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	stream inbox-acme_team_carol	msgs 3	first 1	last 3	suspended false	pending 3	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.carol
orphans: 0 stream(s), 0 lease(s), 0 creds file(s)
fenced lease acme_team_ivy (a deprovision tombstone; it expires)
fenced lease acme_team_kim (a deprovision tombstone; it expires)
fenced lease acme_team_lee (a deprovision tombstone; it expires)
retired markers 3 (final 3, pending 0)	bucket mash-retired	bytes 846 of 67108864	used 0.00%
retired marker acme_team_ivy	final	retired_at 2026-11-19T00:22:24Z	user UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS
retired marker acme_team_kim	final	retired_at 2026-11-19T00:22:31Z	user UAM42OQT4W7JIRTKCAWVKJM3TI2EOIXZNSP53RMGK45BTQUMBRI7IGM7
retired marker acme_team_lee	final	retired_at 2026-11-19T00:22:32Z	user UCODJ3XFUJKE6T4JPJXRSY6EYROK3J6RILGMAO5JRKCIBGQFIG2IJNBH
--- stderr
```

Marks: P2, P3, P5, P6, P7, P8, P9; 8 line(s) over 80 columns, first: `record acme_team_alice	stopped	address acme.team.alice	user UAGT4ZQXAL...`; jargon: bucket, creds, lease

**Case 5 (audit_orphan_strict): --strict with an orphan creds file**

```text
$ mashctl audit --strict
[exit 1]
--- stdout
mashctl audit version 2 generated 2026-11-19T00:24:12Z owner acme
creds directories: /tmp/mc/cfg
records 4, running 0
record acme_team_alice	stopped	address acme.team.alice	user UAACYAM6OK4GD6AAITPMI547LQAQKMCTT5E3FDUZGLPB7TXUT7KIE262	stream inbox-acme_team_alice	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice
record acme_team_alice2	stopped	address acme.team.alice2	user UC7CRFJFYEBATKCIBOY5VFGYV7CYSW7ULGTQ5X3VOPIJIRQJ3GGMDD6A	stream inbox-acme_team_alice2	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice2
record acme_team_bob	stopped	address acme.team.bob	user UASKC43DFRQFDAXI5IBI3IWYJM4NB5QA7VRSP3FNQGTSE5BP3N2SNOP3	stream inbox-acme_team_bob	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.bob
record acme_team_carol	stopped	address acme.team.carol	user UC7OYWAAHDQTK4PTM3O65RSHKUDCEXSDE5QFA2FNULY42FJYE53ZPPCI	stream inbox-acme_team_carol	msgs 3	first 1	last 3	suspended false	pending 3	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.carol
orphans: 0 stream(s), 0 lease(s), 1 creds file(s)
orphan creds /tmp/mc/cfg/acme.team.bob.creds	user UBH6YD4HP7LRXHHW7ADO5AKELNC3Z7JKW24V6DRNDKW7QVZVSAIDSKFT
fenced lease acme_team_dave (a deprovision tombstone; it expires)
fenced lease acme_team_erin (a deprovision tombstone; it expires)
fenced lease acme_team_fay (a deprovision tombstone; it expires)
fenced lease acme_team_gus (a deprovision tombstone; it expires)
fenced lease acme_team_hal (a deprovision tombstone; it expires)
retired markers 8 (final 8, pending 0)	bucket mash-retired	bytes 2263 of 67108864	used 0.00%
retired marker acme_team_dave	final	retired_at 2026-11-19T00:24:03Z	user UCSZM7WQVG46DF4NUFL37WGSTF7TXKOHOFHVPN7PL4CV6FEWDKOKXFEK
retired marker acme_team_erin	final	retired_at 2026-11-19T00:24:04Z	user UCV5C3OQGXLB6SUY5ETGZOCIZDLEESG6QAI5FVT5FNQNKUHSQZTYQBFJ
retired marker acme_team_fay	final	retired_at 2026-11-19T00:24:04Z	user UCOWLNCNNIVLF4Q7LTKETGV4YWUY75BUV2743QQCWXEC7F2POD6WPS23
retired marker acme_team_gus	final	retired_at 2026-11-19T00:24:05Z	user UBAAIFHACTC5HHJWAW22W4IJJS77TVDGRIEGPGDDHW7UPE5W5L6ZQWXG
retired marker acme_team_hal	final	retired_at 2026-11-19T00:24:11Z	user UCASZ5XDRPQMYDACXPPP44ECCQLMGAATJMN3W6AVYTKRESD7JJO6Z3FK
retired marker acme_team_ivy	final	retired_at 2026-11-19T00:22:24Z	user UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS
retired marker acme_team_kim	final	retired_at 2026-11-19T00:22:31Z	user UAM42OQT4W7JIRTKCAWVKJM3TI2EOIXZNSP53RMGK45BTQUMBRI7IGM7
retired marker acme_team_lee	final	retired_at 2026-11-19T00:22:32Z	user UCODJ3XFUJKE6T4JPJXRSY6EYROK3J6RILGMAO5JRKCIBGQFIG2IJNBH
--- stderr
mashctl: audit --strict: 1 finding(s): 0 orphan stream(s), 0 orphan lease(s), 1 orphan creds file(s), 0 item(s) not readable or retired-marker finding(s), 0 inbox(es) vanished and 0 recreated since the earlier audit, 0 final retired marker(s) lost since the earlier audit
```

Marks: P2, P3, P5, P6, P7, P8, P9; 15 line(s) over 80 columns, first: `record acme_team_alice	stopped	address acme.team.alice	user UAACYAM6OK...`; jargon: bucket, creds, lease

**Case 6 (audit_compare): --compare an earlier --json report**

```text
$ mashctl audit --compare /tmp/mc/work/audit-earlier.json
[exit 0]
--- stdout
mashctl audit version 2 generated 2026-11-19T00:25:24Z owner acme
creds directories: /tmp/mc/cfg
records 4, running 0
record acme_team_alice	stopped	address acme.team.alice	user UAACYAM6OK4GD6AAITPMI547LQAQKMCTT5E3FDUZGLPB7TXUT7KIE262	stream inbox-acme_team_alice	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice
record acme_team_alice2	stopped	address acme.team.alice2	user UC7CRFJFYEBATKCIBOY5VFGYV7CYSW7ULGTQ5X3VOPIJIRQJ3GGMDD6A	stream inbox-acme_team_alice2	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.alice2
record acme_team_bob	stopped	address acme.team.bob	user UASKC43DFRQFDAXI5IBI3IWYJM4NB5QA7VRSP3FNQGTSE5BP3N2SNOP3	stream inbox-acme_team_bob	msgs 0	first 0	last 0	suspended false	pending 0	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.bob
record acme_team_carol	stopped	address acme.team.carol	user UC7OYWAAHDQTK4PTM3O65RSHKUDCEXSDE5QFA2FNULY42FJYE53ZPPCI	stream inbox-acme_team_carol	msgs 3	first 1	last 3	suspended false	pending 3	ack_pending 0	deliver true	policy all	start_seq 0	filter none	ack_floor 0	delivered 0	num_redelivered 0	subjects agents.mail.acme.team.carol
orphans: 0 stream(s), 0 lease(s), 1 creds file(s)
orphan creds /tmp/mc/cfg/acme.team.bob.creds	user UBH6YD4HP7LRXHHW7ADO5AKELNC3Z7JKW24V6DRNDKW7QVZVSAIDSKFT
fenced lease acme_team_dave (a deprovision tombstone; it expires)
fenced lease acme_team_erin (a deprovision tombstone; it expires)
fenced lease acme_team_fay (a deprovision tombstone; it expires)
fenced lease acme_team_gus (a deprovision tombstone; it expires)
fenced lease acme_team_hal (a deprovision tombstone; it expires)
retired markers 8 (final 8, pending 0)	bucket mash-retired	bytes 2263 of 67108864	used 0.00%
retired marker acme_team_dave	final	retired_at 2026-11-19T00:24:03Z	user UCSZM7WQVG46DF4NUFL37WGSTF7TXKOHOFHVPN7PL4CV6FEWDKOKXFEK
retired marker acme_team_erin	final	retired_at 2026-11-19T00:24:04Z	user UCV5C3OQGXLB6SUY5ETGZOCIZDLEESG6QAI5FVT5FNQNKUHSQZTYQBFJ
retired marker acme_team_fay	final	retired_at 2026-11-19T00:24:04Z	user UCOWLNCNNIVLF4Q7LTKETGV4YWUY75BUV2743QQCWXEC7F2POD6WPS23
retired marker acme_team_gus	final	retired_at 2026-11-19T00:24:05Z	user UBAAIFHACTC5HHJWAW22W4IJJS77TVDGRIEGPGDDHW7UPE5W5L6ZQWXG
retired marker acme_team_hal	final	retired_at 2026-11-19T00:24:11Z	user UCASZ5XDRPQMYDACXPPP44ECCQLMGAATJMN3W6AVYTKRESD7JJO6Z3FK
retired marker acme_team_ivy	final	retired_at 2026-11-19T00:22:24Z	user UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS
retired marker acme_team_kim	final	retired_at 2026-11-19T00:22:31Z	user UAM42OQT4W7JIRTKCAWVKJM3TI2EOIXZNSP53RMGK45BTQUMBRI7IGM7
retired marker acme_team_lee	final	retired_at 2026-11-19T00:22:32Z	user UCODJ3XFUJKE6T4JPJXRSY6EYROK3J6RILGMAO5JRKCIBGQFIG2IJNBH
compare against the audit generated 2026-11-19T00:25:24Z: 4 inbox stream(s), 0 moved, 0 vanished, 0 retired, 0 recreated, 0 creation unknown
compare stream inbox-acme_team_alice	unchanged	last_seq 0->0	delivered 0->0	consumer_seq 0->0	ack_floor 0->0	present true->true
compare stream inbox-acme_team_alice2	unchanged	last_seq 0->0	delivered 0->0	consumer_seq 0->0	ack_floor 0->0	present true->true
compare stream inbox-acme_team_bob	unchanged	last_seq 0->0	delivered 0->0	consumer_seq 0->0	ack_floor 0->0	present true->true
compare stream inbox-acme_team_carol	unchanged	last_seq 3->3	delivered 0->0	consumer_seq 0->0	ack_floor 0->0	present true->true
--- stderr
```

Marks: P2, P3, P5, P6, P7, P8, P9; 19 line(s) over 80 columns, first: `record acme_team_alice	stopped	address acme.team.alice	user UAACYAM6OK...`; jargon: bucket, creds, lease

**Case 7 (audit_compare_bad): --compare file missing**

```text
$ mashctl audit --compare /tmp/mc/work/nope.json
[exit 3]
--- stdout
--- stderr
mashctl: audit: --compare: no such file or directory
```

Marks: none

**Case 8 (audit_flag): unknown option**

```text
$ mashctl audit --frob
[exit 2]
--- stdout
--- stderr
mashctl: audit: unexpected option "--frob"
usage: mashctl audit [--json] [--strict] [--creds-dir DIR] [--compare EARLIER.json]
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `usage: mashctl audit [--json] [--strict] [--creds-dir DIR] [--compare ...`; jargon: creds

**Case 9 (audit_json): --json**

```text
$ mashctl audit --json
[exit 0]
--- stdout
{
  "version": 2,
  "generated": "2026-11-19T00:22:46Z",
  "owner": "acme",
  "account": "AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6",
  "creds_dirs": [
    "/tmp/mc/cfg"
  ],
  "records": [
    {
      "key": "acme_team_alice",
      "address": "acme.team.alice",
      "owner": "acme",
      "group": "team",
      "instance": "alice",
      "user": "UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ",
      "running": false,
      "inbox": {
        "stream": "inbox-acme_team_alice",
        "present": true,
        "msgs": 0,
        "first_seq": 0,
        "last_seq": 0,
        "created": "2026-11-19T00:21:38.690058937Z",
        "subjects": [
          "agents.mail.acme.team.alice"
        ],
        "suspended": false,
        "deliver": {
          "present": true,
          "pending": 0,
          "ack_pending": 0,
          "deliver_policy": "all",
          "start_seq": 0,
          "filter_subjects": [],
          "ack_floor": 0,
          "delivered": 0,
          "delivered_consumer_seq": 0,
          "ack_floor_consumer_seq": 0,
          "num_redelivered": 0,
[... 195 more stdout lines omitted from this document]
--- stderr
```

Marks: none (JSON, for agents; not marked); stdout trimmed to 40 of 235 lines

Breaks:

- P2 (one address printed in two or three forms on one line): cases 2, 3, 4, 5, 6.
- P3 (table or listing not aligned, or no headers): cases 2, 3, 4, 5, 6.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 6, 8.
- P6 (jargon words): cases 1 (creds, lease), 2 (bucket, creds, lease), 3 (bucket, creds, lease), 4 (bucket, creds, lease), 5 (bucket, creds, lease), 6 (bucket, creds, lease), 8 (creds).
- P7 (output meant for an agent, shown to a human): cases 2, 3, 4, 5, 6.
- P8 (field repeated on one screen): cases 2, 3, 4, 5, 6.
- P9 (information not asked for, which belongs behind --detail): cases 2, 3, 4, 5, 6.
- P1: audit prints no preamble line.
- P2/P8: `record acme_team_alice	stopped	address acme.team.alice	user UAGT...	stream inbox-acme_team_alice	msgs 0 ...` prints the address as a flat key, a dotted address and a stream name that contains the flat key; the owner `acme` is also on the header line.
- P3: the record lines, `fenced lease`, `retired marker` and `compare stream` lines are tab separated and have no header; the fields are `name value` pairs in a row (`msgs 0	first 0	last 0`), so the column a value sits in changes with the content.
- P5: the record lines are about 330 columns; a user public key (56 characters) is on every record and every retired marker.
- P6/P9: output lists `fenced lease ... (a deprovision tombstone; it expires)` and `retired marker ... final` for each deleted endpoint; these are not asked for by a plain `mashctl audit`. `bucket mash-retired bytes 2263 of 67108864 used 0.00%` is in the same listing.
- The text output does not change with `--strict` (audit and audit_strict are identical); exit 1 only when a finding exists (audit_orphan_strict). The finding is a line on stderr, `mashctl: audit --strict: 1 finding(s): 0 orphan stream(s), 0 orphan lease(s), 1 orphan creds file(s), ...`, and the orphan itself is one line in the middle of the listing (`orphan creds /tmp/mc/cfg/acme.team.bob.creds ...`).
- audit_compare_bad exits 3 with `mashctl: audit: --compare: no such file or directory`; the usage line lists `--compare` (audit_flag) while `mashctl audit --help` does not.


## deprovision

**Case 1 (help_deprovision): --help**

```text
$ mashctl deprovision --help
[exit 0]
--- stdout
usage:
  mashctl deprovision <owner.group.instance> [--user U | --drained-to SEQ --suspension ID] [--servers N] [--sys-creds FILE] revoke the endpoint's user in the account JWT (--no-revoke skips it; --servers N, else MASH_SERVERS, is how many servers must acknowledge; --sys-creds FILE, else MASH_SYS_CREDS, the system account creds), then remove the record and inbox; with --user, only if the record's user is still U; with --drained-to and --suspension, only if still that suspension (ID) and at last_seq SEQ
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  mashctl deprovision <owner.group.instance> [--user U | --drained-to ...`; jargon: JWT, creds

**Case 2 (depro_noarg): no arguments**

```text
$ mashctl deprovision
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl deprovision <owner.group.instance> [--user <public-key> | --drained-to SEQ --suspension ID] [--no-revoke] [--servers N] [--sys-creds FILE]
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 3 (depro_missing): address with no record**

```text
$ mashctl deprovision acme.team.zed
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
no endpoint named acme.team.zed
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 4 (depro_half): --drained-to without --suspension**

```text
$ mashctl deprovision acme.team.ivy --drained-to 3
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl deprovision <owner.group.instance> [--user <public-key> | --drained-to SEQ --suspension ID] [--no-revoke] [--servers N] [--sys-creds FILE]
mashctl: --drained-to and --suspension go together (both from suspend's output)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 5 (depro_both): --user with --drained-to**

```text
$ mashctl deprovision acme.team.ivy --user UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS --drained-to 3 --suspension abc
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl deprovision <owner.group.instance> [--user <public-key> | --drained-to SEQ --suspension ID] [--no-revoke] [--servers N] [--sys-creds FILE]
mashctl: --user and --drained-to cannot be combined
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 6 (depro_plain): plain deprovision of a provisioned endpoint**

```text
$ mashctl deprovision acme.team.ivy
[exit 0]
--- stdout
revoked user key UD7CJ5RS...: 1 server(s) acknowledged
deprovisioned acme.team.ivy (user key UD7CJ5RS... revoked in the account JWT)
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
warning: user key UD7CJ5RS... has no issue record, so a future-dated credential cannot be ruled out; revoking anyway
```

Marks: P1, P5, P6, P7, P8, P9; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: JWT, creds, issue record

**Case 7 (depro_plain_norevoke): deprovision of an endpoint that is already gone**

```text
$ mashctl deprovision acme.team.ivy --no-revoke
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
no endpoint named acme.team.ivy
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 8 (depro_wrongid): --suspension that is not the current one**

```text
$ mashctl deprovision acme.team.kim --suspension deadbeef --drained-to 0
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: deprovision acme.team.kim: nothing was revoked; the key is still valid
mash: inbox of acme_team_kim: this is not the suspension you drained (id 89e193d8ce44524da2f49444a6bd38b5, you hold deadbeef); suspend state changed, read it again
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 9 (depro_susp): --suspension and --drained-to that match**

```text
$ mashctl deprovision acme.team.kim --suspension 89e193d8ce44524da2f49444a6bd38b5 --drained-to 0
[exit 0]
--- stdout
revoked user key UAM42OQT...: 1 server(s) acknowledged
deprovisioned acme.team.kim (suspension 89e193d8ce44524da2f49444a6bd38b5 drained to 0; user key UAM42OQT... revoked in the account JWT)
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
warning: user key UAM42OQT... has no issue record, so a future-dated credential cannot be ruled out; revoking anyway
```

Marks: P1, P5, P6, P7, P8, P9; 3 line(s) over 80 columns, first: `deprovisioned acme.team.kim (suspension 89e193d8ce44524da2f49444a6bd38...`; jargon: JWT, creds, issue record

**Case 10 (depro_norevoke): --no-revoke**

```text
$ mashctl deprovision acme.team.lee --no-revoke
[exit 0]
--- stdout
deprovisioned acme.team.lee (--no-revoke: its NATS user is not revoked; revoke it with your operator tooling)
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
WARNING: --no-revoke: acme.team.lee's NATS user key UCODJ3XFUJKE6T4JPJXRSY6EYROK3J6RILGMAO5JRKCIBGQFIG2IJNBH is NOT revoked and can still connect, publish and receive until it is revoked in the account JWT; credentials issued to it stay valid. (--no-revoke is a temporary flag name: it will be renamed with the CLI redesign.)
WARNING: --no-revoke: acme.team.lee's NATS user key UCODJ3XFUJKE6T4JPJXRSY6EYROK3J6RILGMAO5JRKCIBGQFIG2IJNBH is NOT revoked and can still connect, publish and receive until it is revoked in the account JWT; credentials issued to it stay valid. (--no-revoke is a temporary flag name: it will be renamed with the CLI redesign.)
```

Marks: P1, P5, P6, P8; 4 line(s) over 80 columns, first: `deprovisioned acme.team.lee (--no-revoke: its NATS user is not revoked...`; jargon: JWT, creds

**Case 11 (depro_hal): deprovision of an endpoint made by endpoint create (leaves its creds file)**

```text
$ mashctl deprovision acme.team.hal
[exit 0]
--- stdout
revoked user key UCASZ5XD...: 1 server(s) acknowledged
deprovisioned acme.team.hal (user key UCASZ5XD... revoked in the account JWT)
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: JWT, creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2, 3, 4, 5, 6, 7, 8, 9, 10, 11.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11.
- P6 (jargon words): cases 1 (JWT, creds), 2 (creds), 3 (creds), 4 (creds), 5 (creds), 6 (JWT, creds, issue record), 7 (creds), 8 (creds), 9 (JWT, creds, issue record), 10 (JWT, creds), 11 (JWT, creds).
- P7 (output meant for an agent, shown to a human): cases 6, 9.
- P8 (field repeated on one screen): cases 6, 9, 10.
- P9 (information not asked for, which belongs behind --detail): cases 6, 9.
- P1: deprovision prints the preamble before its usage errors too (depro_noarg, depro_half, depro_both), although those stop before any connection is made.
- P6/P8: `revoked user key UD7CJ5RS...: 1 server(s) acknowledged` is followed by `deprovisioned acme.team.ivy (user key UD7CJ5RS... revoked in the account JWT)`; the key prefix is printed twice, and `JWT` and `account` are not defined.
- depro_norevoke: the `WARNING: --no-revoke: acme.team.lee's NATS user key U... is NOT revoked and can still connect, publish and receive unt...` line is printed twice on stderr and again in a changed form on stdout (`deprovisioned acme.team.lee (--no-revoke: its NATS user is not revoked; revoke it with your operator tooling)`).
- depro_plain: a user key that has no issue record prints `warning: user key UD7CJ5RS... has no issue record, so a future-dated credential cannot be ruled out; revoking anyway` on stderr, and the command continues.
- depro_wrongid: two stderr lines, the first from mashctl (`mashctl: deprovision acme.team.kim: nothing was revoked; the key is still valid`) and the second with the prefix `mash:`; the prefix differs between lines of one error.
- depro_plain_norevoke and depro_missing: the text is `no endpoint named acme.team.ivy`, without the `mashctl:` prefix, exit 1.
- depro_hal: deprovision of an endpoint made by `endpoint create` removes the record and inbox and leaves `acme.team.hal.creds` in the config directory; the next case in this capture, endpoint delete without `--force` (ed_nrec_nocreds_refuse), renames that file.


## endpoint

**Case 1 (help_endpoint): --help**

```text
$ mashctl endpoint --help
[exit 0]
--- stdout
usage:
  mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key) [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]... [--servers N] [--sys-creds FILE] [--dry-run] [--json] sign the endpoint's key and write its directory record, for example alice/dev/design
  mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE] [--servers N] [--sys-creds FILE] [--dry-run] [--json] revoke the old key on every server and name a new one
  mashctl endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json] [--servers N] [--sys-creds FILE] revoke the endpoint's user key, remove its record and inbox, burn the address, rename its creds file
  mashctl endpoint list [--json]               the endpoints in the directory
  mashctl endpoint show ADDRESS [--json]       one endpoint's directory record, creds file path and issue record
--- stderr
```

Marks: P3, P5, P6; 4 line(s) over 80 columns, first: `  mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N]...`; jargon: JWT, cap, creds, issue record, label

**Case 2 (bare_endpoint): no sub-verb**

```text
$ mashctl endpoint
[exit 2]
--- stdout
usage: mashctl endpoint <verb> [args]
  verbs: create, reissue, delete, list, show
  try: mashctl endpoint create --help
--- stderr
```

Marks: none

**Case 3 (bare_endpoint_nosub): unknown sub-verb**

```text
$ mashctl endpoint frob
[exit 2]
--- stdout
--- stderr
mashctl: endpoint: unknown verb "frob" (the verbs are create, reissue, delete, list, show; try: mashctl endpoint --help)
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint: unknown verb "frob" (the verbs are create, reissue,...`

Breaks:

- P3 (table or listing not aligned, or no headers): cases 1.
- P5 (lines over 80 columns): cases 1, 3.
- P6 (jargon words): cases 1 (JWT, cap, creds, issue record, label).
- P3: help_endpoint prints five verb lines of different lengths, each wrapped to a different point; the usage line for create is 324 columns.
- P4: bare_endpoint prints `try: mashctl endpoint create --help` as the next step; no endpoint exists yet in that state, and create needs a key flag the line does not name.
- `mashctl endpoint` with no sub-verb exits 2 and prints on stdout; `mashctl issued` and `mashctl inbox` with no sub-verb exit 2 and print on stderr (see issued and inbox inspect).


## endpoint create

**Case 1 (help_endpoint_create): --help**

```text
$ mashctl endpoint create --help
[exit 0]
--- stdout
usage: mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key)
                               [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]...
                               [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design, or one token (the instance) with --group. There is no default group.
  --public-key FILE  sign the public key the endpoint made (mash key create); the JWT is written to --jwt-out.
                     A key the account JWT revokes is refused (key_revoked); a check that cannot be made is an error
                     (revocation_unknown), and nothing is changed
  --make-key         make the key on this host and write <config dir>/<address>.creds (mode 0600); the key is kept in
                     <creds file>.pending until the creds file is written, so the same command run again after a kill
                     finishes with the same key. A staged key the account revokes is refused (staged_key_revoked); when
                     the address is taken or retired the file is removed, since that key can never be used for it
  A group cannot be changed later: make a new endpoint and delete the old one. The environment does not give a group.
  exit codes: 0 done or planned, 1 refused, 2 usage, 6 error
Example:
  mashctl endpoint create alice/dev/design --public-key design.pub
--- stderr
```

Marks: P3, P5, P6; 11 line(s) over 80 columns, first: `usage: mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--na...`; jargon: JWT, cap, creds, label

**Case 2 (bare_endpoint_create): no arguments**

```text
$ mashctl endpoint create
[exit 2]
--- stdout
--- stderr
mashctl: endpoint create: error (group_missing): an endpoint needs a group; write --group dev or alice/dev/design
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint create: error (group_missing): an endpoint needs a g...`

**Case 3 (ec_dry): --make-key --dry-run**

```text
$ mashctl endpoint create acme/team/alice --make-key --dry-run
[exit 0]
--- stdout
would create endpoint acme/team/alice
  address  acme.team.alice
  creds    /tmp/mc/cfg/acme.team.alice.creds
next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=team MASH_NAME=alice
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8; 2 line(s) over 80 columns, first: `next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=tea...`; jargon: creds

**Case 4 (ec_make): --make-key**

```text
$ mashctl endpoint create acme/team/alice --make-key
[exit 0]
--- stdout
created endpoint acme/team/alice
  address  acme.team.alice
  creds    /tmp/mc/cfg/acme.team.alice.creds
next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=team MASH_NAME=alice
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8, P9; 2 line(s) over 80 columns, first: `next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=tea...`; jargon: creds

**Case 5 (ec_make_json): --make-key --json**

```text
$ mashctl endpoint create acme/team/alice2 --make-key --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "created",
  "owner": "acme",
  "group": "team",
  "instance": "alice2",
  "address": "acme.team.alice2",
  "key": "acme_team_alice2",
  "creds_path": "/tmp/mc/cfg/acme.team.alice2.creds",
  "creds_written": true,
  "user_public_key": "UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN",
  "labels": {},
  "caps": [],
  "description": "",
  "note": "",
  "next_steps": [
    {
      "sender": "endpoint",
      "step": "session",
      "action": "start a session as this endpoint",
      "address": "acme.team.alice2",
      "purpose": "use",
      "detail": "MASH_OWNER=acme MASH_GROUP=team MASH_NAME=alice2"
    }
  ],
  "refusal": null
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked)

**Case 6 (ec_pub): --public-key and --jwt-out**

```text
$ mashctl endpoint create acme/team/bob --public-key /tmp/mc/work/bob.pub --jwt-out /tmp/mc/work/bob.jwt
[exit 0]
--- stdout
created endpoint acme/team/bob
  address  acme.team.bob
  jwt      /tmp/mc/work/bob.jwt
next: on the endpoint's machine, run: mash credential create --seed bob.seed --jwt bob.jwt
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8; 2 line(s) over 80 columns, first: `next: on the endpoint's machine, run: mash credential create --seed bo...`; jargon: JWT, creds, seed

**Case 7 (ec_pub_dry): --public-key already issued, --dry-run**

```text
$ mashctl endpoint create acme/team/erin --public-key /tmp/mc/work/bob.pub --dry-run
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint create: refused (public_key_issued): the public key UAO35ZE3... was already issued to another address; each key is used at one address: make a new key for this endpoint with: mash key create --out erin.seed
```

Marks: P1, P4, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds, seed

**Case 8 (ec_tool): --tool, --label, --note**

```text
$ mashctl endpoint create acme/team/carol --make-key --tool --label env=demo --note "demo tool"
[exit 0]
--- stdout
created endpoint acme/team/carol
  address  acme.team.carol
  creds    /tmp/mc/cfg/acme.team.carol.creds
next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=team MASH_NAME=carol
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8, P9; 2 line(s) over 80 columns, first: `next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=tea...`; jargon: creds

**Case 9 (ec_tool_dry): --tool --dry-run**

```text
$ mashctl endpoint create acme/team/dave --make-key --tool --label env=demo --dry-run
[exit 0]
--- stdout
would create endpoint acme/team/dave
  address  acme.team.dave
  creds    /tmp/mc/cfg/acme.team.dave.creds
next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=team MASH_NAME=dave
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8; 2 line(s) over 80 columns, first: `next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=tea...`; jargon: creds

**Case 10 (ecx_default_jwt): --public-key --dry-run with the default --jwt-out (run in the work directory)**

```text
$ env -C /tmp/mc/work mashctl endpoint create acme/team/hank --public-key /tmp/mc/work/hank.pub --dry-run
[exit 0]
--- stdout
would create endpoint acme/team/hank
  address  acme.team.hank
  jwt      ./acme.team.hank.jwt
next: on the endpoint's machine, run: mash credential create --seed hank.seed --jwt acme.team.hank.jwt
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8; 2 line(s) over 80 columns, first: `next: on the endpoint's machine, run: mash credential create --seed ha...`; jargon: JWT, creds, seed

**Case 11 (kc_ned): mash key create (first step of --public-key)**

```text
$ mash key create --out /tmp/mc/work/ned.seed
[exit 0]
--- stdout
created key
  public key  UBTXXMRDI7OHQXDXWUA2IDHKI2ZS5PDUE3NDTF3B2FNX4TJ2E4ELPIPA
  seed        /tmp/mc/work/ned.seed
  public file /tmp/mc/work/ned.pub
next: send /tmp/mc/work/ned.pub to the host that runs mashctl, then: mashctl endpoint create ADDRESS --public-key /tmp/mc/work/ned.pub
--- stderr
```

Marks: P4, P5, P6; 1 line(s) over 80 columns, first: `next: send /tmp/mc/work/ned.pub to the host that runs mashctl, then: m...`; jargon: seed

**Case 12 (ec_ned): --public-key with --describe and --cap**

```text
$ mashctl endpoint create acme/team/ned --public-key /tmp/mc/work/ned.pub --jwt-out /tmp/mc/work/ned.jwt --describe "no local creds" --cap read
[exit 0]
--- stdout
created endpoint acme/team/ned
  address  acme.team.ned
  jwt      /tmp/mc/work/ned.jwt
next: on the endpoint's machine, run: mash credential create --seed ned.seed --jwt ned.jwt
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8, P9; 2 line(s) over 80 columns, first: `next: on the endpoint's machine, run: mash credential create --seed ne...`; jargon: JWT, creds, seed

**Case 13 (ecx_flags): owner, group and name by flag, --dry-run**

```text
$ mashctl endpoint create --owner acme --group team --name gina --make-key --dry-run
[exit 0]
--- stdout
would create endpoint acme/team/gina
  address  acme.team.gina
  creds    /tmp/mc/cfg/acme.team.gina.creds
next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=team MASH_NAME=gina
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8; 2 line(s) over 80 columns, first: `next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=tea...`; jargon: creds

**Case 14 (ecx_oneword): one token with --group, --dry-run**

```text
$ mashctl endpoint create gina --group team --make-key --dry-run
[exit 0]
--- stdout
would create endpoint acme/team/gina
  address  acme.team.gina
  creds    /tmp/mc/cfg/acme.team.gina.creds
next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=team MASH_NAME=gina
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8; 2 line(s) over 80 columns, first: `next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=tea...`; jargon: creds

**Case 15 (ecx_exists): endpoint exists, --make-key**

```text
$ mashctl endpoint create acme/team/alice --make-key
[exit 1]
--- stdout
--- stderr
mashctl: endpoint create: refused (file_exists): /tmp/mc/cfg/acme.team.alice.creds already exists and is never overwritten; name another file with --jwt-out or remove it
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `mashctl: endpoint create: refused (file_exists): /tmp/mc/cfg/acme.team...`; jargon: JWT, creds

**Case 16 (ecx_taken): endpoint exists, --public-key**

```text
$ mashctl endpoint create acme/team/alice --public-key /tmp/mc/work/hank.pub --jwt-out /tmp/mc/work/taken.jwt
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint create: refused (address_taken): endpoint acme.team.alice already exists; use reissue to give it a new key
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 17 (ecx_two): two-token address**

```text
$ mashctl endpoint create team/alice --make-key
[exit 2]
--- stdout
--- stderr
mashctl: endpoint create: error (address_ambiguous): two tokens are ambiguous; write alice/dev/design or --group dev design
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint create: error (address_ambiguous): two tokens are am...`

**Case 18 (ecx_nogroup): one token, no group**

```text
$ mashctl endpoint create alice --make-key
[exit 2]
--- stdout
--- stderr
mashctl: endpoint create: error (group_missing): an endpoint needs a group; write --group dev or alice/dev/design
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint create: error (group_missing): an endpoint needs a g...`

**Case 19 (ecx_bad): instance with a space**

```text
$ mashctl endpoint create 'acme/team/Bad Name' --make-key
[exit 1]
--- stdout
--- stderr
mashctl: endpoint create: refused (name_invalid): sap: instance token "Bad Name" must be lower-case
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint create: refused (name_invalid): sap: instance token ...`

**Case 20 (ecx_nokey): no key flag**

```text
$ mashctl endpoint create acme/team/frank
[exit 2]
--- stdout
--- stderr
mashctl: endpoint create: error (key_missing): an endpoint needs a key: write --public-key FILE (the endpoint's public key, from mash key create) or --make-key (make the key on this host)
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint create: error (key_missing): an endpoint needs a key...`

**Case 21 (ecx_bothkeys): --make-key and --public-key**

```text
$ mashctl endpoint create acme/team/frank --make-key --public-key /tmp/mc/work/bob.pub
[exit 2]
--- stdout
--- stderr
mashctl: endpoint create: error (key_both): give one of --public-key FILE and --make-key, not both
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint create: error (key_both): give one of --public-key F...`

**Case 22 (ecx_pubmissing): --public-key file missing**

```text
$ mashctl endpoint create acme/team/frank --public-key /tmp/mc/work/nonexistent.pub
[exit 1]
--- stdout
--- stderr
mashctl: endpoint create: refused (public_key_unreadable): cannot read the public key file /tmp/mc/work/nonexistent.pub: no such file or directory
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint create: refused (public_key_unreadable): cannot read...`

**Case 23 (ecx_pubinvalid): --public-key file is not a key**

```text
$ mashctl endpoint create acme/team/frank --public-key /tmp/mc/work/bad.pub
[exit 1]
--- stdout
--- stderr
mashctl: endpoint create: refused (public_key_invalid): /tmp/mc/work/bad.pub does not hold a NATS user public key (U, then 55 base32 characters); make one with: mash key create --out NAME.seed
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `mashctl: endpoint create: refused (public_key_invalid): /tmp/mc/work/b...`; jargon: seed

**Case 24 (ecx_flag): unknown option**

```text
$ mashctl endpoint create acme/team/frank --make-key --frob
[exit 2]
--- stdout
--- stderr
mashctl: endpoint create: unknown option "--frob"
usage: mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key)
                               [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]...
                               [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design, or one token (the instance) with --group. There is no default group.
  --public-key FILE  sign the public key the endpoint made (mash key create); the JWT is written to --jwt-out.
                     A key the account JWT revokes is refused (key_revoked); a check that cannot be made is an error
                     (revocation_unknown), and nothing is changed
  --make-key         make the key on this host and write <config dir>/<address>.creds (mode 0600); the key is kept in
                     <creds file>.pending until the creds file is written, so the same command run again after a kill
                     finishes with the same key. A staged key the account revokes is refused (staged_key_revoked); when
                     the address is taken or retired the file is removed, since that key can never be used for it
  A group cannot be changed later: make a new endpoint and delete the old one. The environment does not give a group.
  exit codes: 0 done or planned, 1 refused, 2 usage, 6 error
Example:
  mashctl endpoint create alice/dev/design --public-key design.pub
```

Marks: P5, P6; 11 line(s) over 80 columns, first: `usage: mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--na...`; jargon: JWT, cap, creds, label

**Case 25 (ecx_extra): extra argument**

```text
$ mashctl endpoint create acme/team/frank extra --make-key
[exit 2]
--- stdout
--- stderr
mashctl: endpoint create: one endpoint address only
usage: mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key)
                               [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]...
                               [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design, or one token (the instance) with --group. There is no default group.
  --public-key FILE  sign the public key the endpoint made (mash key create); the JWT is written to --jwt-out.
                     A key the account JWT revokes is refused (key_revoked); a check that cannot be made is an error
                     (revocation_unknown), and nothing is changed
  --make-key         make the key on this host and write <config dir>/<address>.creds (mode 0600); the key is kept in
                     <creds file>.pending until the creds file is written, so the same command run again after a kill
                     finishes with the same key. A staged key the account revokes is refused (staged_key_revoked); when
                     the address is taken or retired the file is removed, since that key can never be used for it
  A group cannot be changed later: make a new endpoint and delete the old one. The environment does not give a group.
  exit codes: 0 done or planned, 1 refused, 2 usage, 6 error
Example:
  mashctl endpoint create alice/dev/design --public-key design.pub
```

Marks: P5, P6; 11 line(s) over 80 columns, first: `usage: mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--na...`; jargon: JWT, cap, creds, label

**Case 26 (ecx_disagree): address and --owner differ**

```text
$ mashctl endpoint create acme/team/frank --owner zed --make-key
[exit 2]
--- stdout
--- stderr
mashctl: endpoint create: error (naming_disagrees): the address and --owner name different owner values; give one
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint create: error (naming_disagrees): the address and --...`

**Case 27 (ecx_label_bad): label without =**

```text
$ mashctl endpoint create acme/team/frank --make-key --label 'bad label' --dry-run
[exit 2]
--- stdout
--- stderr
mashctl: endpoint create: --label wants k=v (the value is not shown)
usage: mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key)
                               [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]...
                               [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design, or one token (the instance) with --group. There is no default group.
  --public-key FILE  sign the public key the endpoint made (mash key create); the JWT is written to --jwt-out.
                     A key the account JWT revokes is refused (key_revoked); a check that cannot be made is an error
                     (revocation_unknown), and nothing is changed
  --make-key         make the key on this host and write <config dir>/<address>.creds (mode 0600); the key is kept in
                     <creds file>.pending until the creds file is written, so the same command run again after a kill
                     finishes with the same key. A staged key the account revokes is refused (staged_key_revoked); when
                     the address is taken or retired the file is removed, since that key can never be used for it
  A group cannot be changed later: make a new endpoint and delete the old one. The environment does not give a group.
  exit codes: 0 done or planned, 1 refused, 2 usage, 6 error
Example:
  mashctl endpoint create alice/dev/design --public-key design.pub
```

Marks: P5, P6; 11 line(s) over 80 columns, first: `usage: mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--na...`; jargon: JWT, cap, creds, label

**Case 28 (ecx_servers): --servers 0**

```text
$ mashctl endpoint create acme/team/frank --make-key --servers 0 --dry-run
[exit 2]
--- stdout
--- stderr
mashctl: endpoint: --servers must be a whole number of servers, 1 or more, got "0"
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint: --servers must be a whole number of servers, 1 or m...`

**Case 29 (ecx_json_refusal): refusal with --json**

```text
$ mashctl endpoint create acme/team/alice --make-key --json
[exit 1]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "refused",
  "owner": "acme",
  "group": "team",
  "instance": "alice",
  "address": "acme.team.alice",
  "key": "acme_team_alice",
  "creds_path": "/tmp/mc/cfg/acme.team.alice.creds",
  "creds_written": false,
  "labels": {},
  "caps": [],
  "description": "",
  "note": "",
  "next_steps": [],
  "refusal": {
    "code": "file_exists",
    "detail": "/tmp/mc/cfg/acme.team.alice.creds already exists and is never overwritten; name another file with --jwt-out or remove it"
  }
}
--- stderr
mashctl: endpoint create: refused (file_exists): /tmp/mc/cfg/acme.team.alice.creds already exists and is never overwritten; name another file with --jwt-out or remove it
```

Marks: none (JSON, for agents; not marked)

**Case 30 (ecx_fileexists): --jwt-out file exists**

```text
$ mashctl endpoint create acme/team/hank --public-key /tmp/mc/work/hank.pub --jwt-out /tmp/mc/work/exists.jwt
[exit 1]
--- stdout
--- stderr
mashctl: endpoint create: refused (file_exists): /tmp/mc/work/exists.jwt already exists and is never overwritten; name another file with --jwt-out or remove it
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `mashctl: endpoint create: refused (file_exists): /tmp/mc/work/exists.j...`; jargon: JWT

**Case 31 (ecx_revoked): --public-key of a key revoked by reissue**

```text
$ mashctl endpoint create acme/team/zoe --public-key /tmp/mc/work/bob.pub --jwt-out /tmp/mc/work/zoe.jwt
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint create: refused (key_revoked): the public key UAO35ZE3... was revoked; make a new key with: mash key create --out NAME.seed
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds, seed

**Case 32 (ecx_revoked2): --public-key of a key revoked by deprovision**

```text
$ mashctl endpoint create acme/team/zoe --public-key /tmp/mc/work/ivy.pub --jwt-out /tmp/mc/work/zoe.jwt
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint create: refused (key_revoked): the public key UD7CJ5RS... was revoked; make a new key with: mash key create --out NAME.seed
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds, seed

**Case 33 (ec_retired): address was deleted, --make-key**

```text
$ mashctl endpoint create acme/team/dave --make-key
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint create: refused (key_retired): endpoint acme.team.dave was retired and its address is never reassigned
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 34 (ec_retired_pub): address was deleted, --public-key**

```text
$ mashctl endpoint create acme/team/dave --public-key /tmp/mc/work/hank.pub --jwt-out /tmp/mc/work/dave.jwt
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint create: refused (key_retired): endpoint acme.team.dave was retired and its address is never reassigned
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 3, 4, 6, 7, 8, 9, 10, 12, 13, 14, 16, 31, 32, 33, 34.
- P3 (table or listing not aligned, or no headers): cases 1.
- P4 (a `next:` or `Next:` line is present): cases 3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 14.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 30, 31, 32, 33, 34.
- P6 (jargon words): cases 1 (JWT, cap, creds, label), 3 (creds), 4 (creds), 6 (JWT, creds, seed), 7 (creds, seed), 8 (creds), 9 (creds), 10 (JWT, creds, seed), 11 (seed), 12 (JWT, creds, seed), 13 (creds), 14 (creds), 15 (JWT, creds), 16 (creds), 23 (seed), 24 (JWT, cap, creds, label), 25 (JWT, cap, creds, label), 27 (JWT, cap, creds, label), 30 (JWT), 31 (creds, seed), 32 (creds, seed), 33 (creds), 34 (creds).
- P8 (field repeated on one screen): cases 3, 4, 6, 8, 9, 10, 12, 13, 14.
- P9 (information not asked for, which belongs behind --detail): cases 4, 8, 12.
- P1: the preamble is on stderr before every result that needed the server (all cases with `P1` above) and is not printed by the usage errors (ecx_two, ecx_nogroup, ecx_nokey and others).
- P2/P8: the first two lines of a result are `created endpoint acme/team/alice` and `  address  acme.team.alice`; the creds path on the third line has the address a third time (`/tmp/mc/cfg/acme.team.alice.creds`). The address is labelled only on the second line.
- P4 (ec_make, ec_tool): the last line `next: start a session as this endpoint: MASH_OWNER=acme MASH_GROUP=team MASH_NAME=alice` follows the creds line with no blank line. It names three environment variables and no command. In the flow create then start a session, the next screen is the Claude Code session started with those variables; mash status (`mash status`) is the command that checks the setup before it, and it is not named.
- P4 (ec_dry, ec_tool_dry): a `--dry-run` prints `would create endpoint ...` and then the same `next: start a session ...` line as a real run; nothing was created, so the next step is to run the command without `--dry-run`.
- P4 (ec_pub, ecx_default_jwt, ec_ned): `next: on the endpoint's machine, run: mash credential create --seed bob.seed --jwt bob.jwt` names the right command; it gives the file names without the paths printed in the line above (`jwt      /tmp/mc/work/bob.jwt`) and without `--out`. After it, `mash credential create` prints `next: mash status`, and `mash status` prints no next line; the session start is not named in the chain.
- P4 (kc_ned): `next: send /tmp/mc/work/ned.pub to the host that runs mashctl, then: mashctl endpoint create ADDRESS --public-key /tmp/mc/work/ned.pub` has the placeholder ADDRESS and the long path twice.
- P5: the lines over 80 columns in a success result are the preamble (197 columns) and the `next:` line (87 columns for `--make-key`, 90 for `--public-key`); the help text has a 324-column line for the create usage.
- P6: `creds`, `JWT`, `seed`, `caps` and `labels` appear in the result lines, in `next:` and in the --help text (`--cap C`, `--label k=v`, `--tool`) without a definition; the help text defines `--tool` as a label (`kind=tool`) in docs only.
- P7: the refusal lines name a code in parentheses, `refused (public_key_issued)`, `error (group_missing)`, `refused (file_exists)`; the codes are for scripts and agents. The text after the code carries the instruction.
- P9: ec_tool and ec_ned echo nothing of the note, description, label or cap values they set; show (endpoint show) is the only place to see them. ec_make_json prints the whole record, including `labels`, `caps`, `description`, `note` and `next_steps` as objects.
- ecx_exists: an existing endpoint with `--make-key` is refused as `file_exists` (the creds file is there), not as `address_taken`; `address_taken` appears only with `--public-key` (ecx_taken). The `address_taken` text says `use reissue to give it a new key`, and the verb is `endpoint reissue`.
- ecx_flag, ecx_extra, ecx_label_bad: a usage error prints the whole `--help` text after the one-line error (about 20 lines on stderr), so the error line is the first line and the fix is below it. ecx_label_bad prints `--label wants k=v (the value is not shown)`.
- ecx_bad: `refused (name_invalid): sap: instance token "Bad Name" must be lower-case` carries the prefix `sap:` and echoes the value (the docs say a value that fails validation is never printed back).
- ecx_revoked, ecx_revoked2: a key revoked by `reissue` or `deprovision` is refused as `key_revoked`; a key that was only issued to another address is refused as `public_key_issued` (ec_pub_dry). The two texts print the key as `UAO35ZE3...`, the first 8 characters.
- ec_retired, ec_retired_pub: `refused (key_retired): endpoint acme.team.dave was retired and its address is never reassigned`; the word is `retired` and the verb that retired it is `endpoint delete`.


## endpoint delete

**Case 1 (help_endpoint_delete): --help**

```text
$ mashctl endpoint delete --help
[exit 0]
--- stdout
usage: mashctl endpoint delete ADDRESS [--delete-creds] [--force] [--no-revoke] [--dry-run] [--json]
  ADDRESS is owner/group/instance (for example alice/dev/design) or owner.group.instance.
  Revokes the endpoint's user key, removes its directory record and inbox, burns the address, and renames its creds
  file (if one is in the config directories) to NAME.creds.retired-<time>.
  --delete-creds   remove the creds file instead of renaming it
  --force          also handle the creds file when the server has no record
  --no-revoke      do not revoke the user key
  --dry-run        say what would happen and change nothing
  --json           print the result as JSON (schema mashctl-endpoint@1)
  --servers N and --sys-creds FILE override MASH_SERVERS and MASH_SYS_CREDS
  exit: 0 deleted or unchanged, 1 refused, 2 usage, 5 partial, 6 error
Example:
  mashctl endpoint delete alice/dev/design --dry-run
--- stderr
```

Marks: P3, P5, P6; 3 line(s) over 80 columns, first: `usage: mashctl endpoint delete ADDRESS [--delete-creds] [--force] [--n...`; jargon: creds

**Case 2 (bare_endpoint_delete): no arguments**

```text
$ mashctl endpoint delete
[exit 2]
--- stdout
--- stderr
mashctl: endpoint delete: no endpoint address: give owner/group/instance, for example alice/dev/design; try: mashctl endpoint delete --help
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint delete: no endpoint address: give owner/group/instan...`

**Case 3 (ed_dry): --dry-run**

```text
$ mashctl endpoint delete acme/team/dave --dry-run
[exit 0]
--- stdout
would delete acme/team/dave (acme.team.dave) (nothing was changed)
would revoke user key UCSZM7WQ...
would remove the directory record and the inbox, and burn the address
would rename creds file /tmp/mc/cfg/acme.team.dave.creds to /tmp/mc/cfg/acme.team.dave.creds.retired-<time>
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P2, P5, P6, P8; 2 line(s) over 80 columns, first: `would rename creds file /tmp/mc/cfg/acme.team.dave.creds to /tmp/mc/cf...`; jargon: creds

**Case 4 (ed_dry_json): --dry-run --json**

```text
$ mashctl endpoint delete acme/team/dave --dry-run --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "planned",
  "owner": "acme",
  "group": "team",
  "instance": "dave",
  "address": "acme.team.dave",
  "key": "acme_team_dave",
  "creds": {
    "path": "/tmp/mc/cfg/acme.team.dave.creds",
    "action": "renamed"
  },
  "next_steps": [],
  "refusal": null
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked)

**Case 5 (ed): delete**

```text
$ mashctl endpoint delete acme/team/dave
[exit 0]
--- stdout
deleted acme/team/dave (acme.team.dave)
creds file renamed to /tmp/mc/cfg/acme.team.dave.creds.retired-20261119T002404Z (--delete-creds removes it instead)
revoked user key UCSZM7WQ...: 1 server(s) acknowledged
user key UCSZM7WQ... revoked in the account JWT: its creds stop working when the server takes the update; the creds file is separate
servers acknowledged the revocation: 1
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P2, P5, P6, P8, P9; 3 line(s) over 80 columns, first: `creds file renamed to /tmp/mc/cfg/acme.team.dave.creds.retired-2026111...`; jargon: JWT, creds

**Case 6 (ed_json): delete --json**

```text
$ mashctl endpoint delete acme/team/erin --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "deleted",
  "owner": "acme",
  "group": "team",
  "instance": "erin",
  "address": "acme.team.erin",
  "key": "acme_team_erin",
  "creds": {
    "path": "/tmp/mc/cfg/acme.team.erin.creds",
    "action": "renamed"
  },
  "servers_acknowledged": 1,
  "next_steps": [],
  "refusal": null
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked)

**Case 7 (ed_again): delete of an address already deleted**

```text
$ mashctl endpoint delete acme/team/dave
[exit 0]
--- stdout
deleted acme/team/dave (acme.team.dave)
the address was already retired and nothing was left on the server
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint delete acme/team/dave: no directory record on the server, so no user key is known and nothing was revoked; only the creds file is handled
```

Marks: P1, P2, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 8 (ed_force): --force on an address already deleted**

```text
$ mashctl endpoint delete acme/team/dave --force
[exit 0]
--- stdout
deleted acme/team/dave (acme.team.dave)
the address was already retired and nothing was left on the server
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint delete acme/team/dave: no directory record on the server, so no user key is known and nothing was revoked; only the creds file is handled
```

Marks: P1, P2, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 9 (ed_norevoke): --no-revoke**

```text
$ mashctl endpoint delete acme/team/fay --no-revoke
[exit 0]
--- stdout
deleted acme/team/fay (acme.team.fay)
creds file renamed to /tmp/mc/cfg/acme.team.fay.creds.retired-20261119T002404Z (--delete-creds removes it instead)
acme/team/fay: its user key is not revoked (--no-revoke): its creds still authenticate until the key is revoked in the account JWT (nsc, or your operator tooling) or the signing key is rotated
servers acknowledged the revocation: 0
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
WARNING: --no-revoke: acme.team.fay's NATS user key UCOWLNCNNIVLF4Q7LTKETGV4YWUY75BUV2743QQCWXEC7F2POD6WPS23 is NOT revoked and can still connect, publish and receive until it is revoked in the account JWT; credentials issued to it stay valid. (--no-revoke is a temporary flag name: it will be renamed with the CLI redesign.)
WARNING: --no-revoke: acme.team.fay's NATS user key UCOWLNCNNIVLF4Q7LTKETGV4YWUY75BUV2743QQCWXEC7F2POD6WPS23 is NOT revoked and can still connect, publish and receive until it is revoked in the account JWT; credentials issued to it stay valid. (--no-revoke is a temporary flag name: it will be renamed with the CLI redesign.)
```

Marks: P1, P2, P5, P6, P8, P9; 5 line(s) over 80 columns, first: `creds file renamed to /tmp/mc/cfg/acme.team.fay.creds.retired-20261119...`; jargon: JWT, creds

**Case 10 (ed_delcreds): --delete-creds**

```text
$ mashctl endpoint delete acme/team/gus --delete-creds
[exit 0]
--- stdout
deleted acme/team/gus (acme.team.gus)
revoked user key UBAAIFHA...: 1 server(s) acknowledged
user key UBAAIFHA... revoked in the account JWT: its creds stop working when the server takes the update; the creds file is separate
servers acknowledged the revocation: 1
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P2, P5, P6, P8, P9; 2 line(s) over 80 columns, first: `user key UBAAIFHA... revoked in the account JWT: its creds stop workin...`; jargon: JWT, creds

**Case 11 (ed_nrec_nocreds_refuse): no directory record, creds file present, no --force**

```text
$ mashctl endpoint delete acme/team/hal
[exit 0]
--- stdout
deleted acme/team/hal (acme.team.hal)
creds file renamed to /tmp/mc/cfg/acme.team.hal.creds.retired-20261119T002412Z (--delete-creds removes it instead)
the address was already retired and nothing was left on the server
servers acknowledged the revocation: 0
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint delete acme/team/hal: no directory record on the server, so no user key is known and nothing was revoked; only the creds file is handled
```

Marks: P1, P2, P5, P6, P7; 3 line(s) over 80 columns, first: `creds file renamed to /tmp/mc/cfg/acme.team.hal.creds.retired-20261119...`; jargon: creds

**Case 12 (ed_force_dry): --force --dry-run, creds file already renamed**

```text
$ mashctl endpoint delete acme/team/hal --force --dry-run
[exit 0]
--- stdout
would delete acme/team/hal (acme.team.hal) (nothing was changed)
no directory record: no user key is known and none would be revoked
no creds file would be changed
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P2, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 13 (ed_force_creds): --force, no record**

```text
$ mashctl endpoint delete acme/team/hal --force
[exit 0]
--- stdout
deleted acme/team/hal (acme.team.hal)
the address was already retired and nothing was left on the server
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint delete acme/team/hal: no directory record on the server, so no user key is known and nothing was revoked; only the creds file is handled
```

Marks: P1, P2, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 14 (ed_bad): not an address**

```text
$ mashctl endpoint delete gus
[exit 2]
--- stdout
--- stderr
mashctl: endpoint delete: not an endpoint address (the value is not shown): write owner/group/instance, for example alice/dev/design
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint delete: not an endpoint address (the value is not sh...`

**Case 15 (ed_noarg): no argument**

```text
$ mashctl endpoint delete
[exit 2]
--- stdout
--- stderr
mashctl: endpoint delete: no endpoint address: give owner/group/instance, for example alice/dev/design; try: mashctl endpoint delete --help
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint delete: no endpoint address: give owner/group/instan...`

**Case 16 (ed_missing): address never created**

```text
$ mashctl endpoint delete acme/team/zed
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint delete acme/team/zed: no directory record on the server (mashctl endpoint list shows the endpoints that exist; --force handles the creds file anyway); if the endpoint was partly removed, finish with mashctl deprovision acme.team.zed
```

Marks: P1, P2, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 17 (ed_json_missing): address never created, --json**

```text
$ mashctl endpoint delete acme/team/zed --json
[exit 1]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "refused",
  "owner": "acme",
  "group": "team",
  "instance": "zed",
  "address": "acme.team.zed",
  "key": "acme_team_zed",
  "creds": {
    "path": "",
    "action": "none"
  },
  "next_steps": [
    "mashctl endpoint list shows the endpoints that exist"
  ],
  "refusal": {
    "code": "not_found",
    "detail": "no directory record on the server (mashctl endpoint list shows the endpoints that exist; --force handles the creds file anyway); if the endpoint was partly removed, finish with mashctl deprovision acme.team.zed"
  }
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint delete acme/team/zed: no directory record on the server (mashctl endpoint list shows the endpoints that exist; --force handles the creds file anyway); if the endpoint was partly removed, finish with mashctl deprovision acme.team.zed
```

Marks: none (JSON, for agents; not marked)

**Case 18 (ed_servers): --servers 0**

```text
$ mashctl endpoint delete acme/team/hal --servers 0 --dry-run
[exit 2]
--- stdout
--- stderr
mashctl: endpoint: --servers must be a whole number of servers, 1 or more, got "0"
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint: --servers must be a whole number of servers, 1 or m...`

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 3, 5, 7, 8, 9, 10, 11, 12, 13, 16.
- P2 (one address printed in two or three forms on one line): cases 3, 5, 7, 8, 9, 10, 11, 12, 13, 16.
- P3 (table or listing not aligned, or no headers): cases 1.
- P5 (lines over 80 columns): cases 1, 2, 3, 5, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 18.
- P6 (jargon words): cases 1 (creds), 3 (creds), 5 (JWT, creds), 7 (creds), 8 (creds), 9 (JWT, creds), 10 (JWT, creds), 11 (creds), 12 (creds), 13 (creds), 16 (creds).
- P7 (output meant for an agent, shown to a human): cases 11.
- P8 (field repeated on one screen): cases 3, 5, 9, 10.
- P9 (information not asked for, which belongs behind --detail): cases 5, 9, 10.
- P1/P2: every result that reached the server prints the preamble; every success line shows the address twice in two forms on one line: `deleted acme/team/dave (acme.team.dave)`.
- P8/P9 (ed): after `deleted acme/team/dave (acme.team.dave)` the output says the key was revoked three times: `revoked user key UCSZM7WQ...: 1 server(s) acknowledged`, `user key UCSZM7WQ... revoked in the account JWT: its creds stop working when the server takes the update; the creds file is separate`, `servers acknowledged the revocation: 1`. The server count is printed twice.
- P6: `creds file`, `account JWT`, `user key`, `directory record`, `burn the address` (help text) are not defined.
- ed_dry: the dry run lists four `would` lines, one of which has the unexpanded name `acme.team.dave.creds.retired-<time>`; the real run prints the expanded name with a timestamp (`...retired-20261119T002404Z`).
- ed_again, ed_force: a second delete of a deleted address prints `deleted acme/team/dave (acme.team.dave)` on stdout and `no directory record on the server, so no user key is known and nothing was revoked; only the creds file is handled` on stderr; the first line says it deleted, the second that nothing was done. With `--force` the output is the same as without it (ed_force).
- ed_norevoke: `WARNING: --no-revoke: ...` is on stderr twice; stdout has a line over 80 columns `acme/team/fay: its user key is not revoked (--no-revoke): its creds still authenticate ...` and `servers acknowledged the revocation: 0`.
- ed_nrec_nocreds_refuse: this case is delete without `--force` of an address with no record and a creds file present; the command exits 0 and renames the creds file. docs/mashctl.md says it refuses with exit 1 when there is no directory record and that `--force` handles the creds file; see Findings.
- ed_missing: when there is no record and no creds file the command exits 1 with `no directory record on the server (mashctl endpoint list shows the endpoints that exist; --force handles the creds file anyway); i...`; the line is 250 columns.
- P4: the refusal lines carry hints (`mashctl endpoint list shows the endpoints that exist`, `try: mashctl endpoint delete --help`) as parentheses or after a semicolon; none is a `next:` line.


## endpoint list

**Case 1 (help_endpoint_list): --help**

```text
$ mashctl endpoint list --help
[exit 0]
--- stdout
usage: mashctl endpoint list [--json]
  Prints the endpoints in the directory, one per line: owner/group/instance, owner.group.instance and what is on record.
  --json   print the endpoints as JSON (schema mashctl-endpoint-list@1)
  exit: 0 listed (also when there are none), 2 usage, 6 error
Example:
  mashctl endpoint list
--- stderr
```

Marks: P5; 1 line(s) over 80 columns, first: `  Prints the endpoints in the directory, one per line: owner/group/ins...`

**Case 2 (el_empty): empty directory**

```text
$ mashctl endpoint list
[exit 0]
--- stdout
no endpoints
make one with: mashctl endpoint create --help
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 3 (el_empty_json): empty directory, --json**

```text
$ mashctl endpoint list --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint-list@1",
  "endpoints": []
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked)

**Case 4 (el_three): four endpoints**

```text
$ mashctl endpoint list
[exit 0]
--- stdout
acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T00:21:38Z
acme/team/alice2  acme.team.alice2  acme_team_alice2  created 2026-11-19T00:21:38Z
acme/team/bob  acme.team.bob  acme_team_bob  created 2026-11-19T00:21:39Z
acme/team/carol  acme.team.carol  acme_team_carol  created 2026-11-19T00:21:50Z, note "demo tool", labels env=demo,kind=tool
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
4 endpoints
```

Marks: P1, P2, P3, P5, P6, P8, P9; 3 line(s) over 80 columns, first: `acme/team/alice2  acme.team.alice2  acme_team_alice2  created 2026-11-...`; jargon: creds, kind, label

**Case 5 (el_three_json): four endpoints, --json**

```text
$ mashctl endpoint list --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint-list@1",
  "endpoints": [
    {
      "owner": "acme",
      "group": "team",
      "instance": "alice",
      "address": "acme.team.alice",
      "key": "acme_team_alice",
      "user_public_key": "UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ",
      "created": "2026-11-19T00:21:38Z",
      "note": "",
      "labels": {},
      "caps": [],
      "description": "",
      "role": "",
      "host": ""
    },
    {
      "owner": "acme",
      "group": "team",
      "instance": "alice2",
      "address": "acme.team.alice2",
      "key": "acme_team_alice2",
      "user_public_key": "UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN",
      "created": "2026-11-19T00:21:38Z",
      "note": "",
      "labels": {},
      "caps": [],
      "description": "",
      "role": "",
      "host": ""
    },
    {
      "owner": "acme",
      "group": "team",
      "instance": "bob",
      "address": "acme.team.bob",
      "key": "acme_team_bob",
      "user_public_key": "UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC",
[... 28 more stdout lines omitted from this document]
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked); stdout trimmed to 40 of 68 lines

**Case 6 (el_after): after three deletes (hal is still listed)**

```text
$ mashctl endpoint list
[exit 0]
--- stdout
acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T00:21:38Z, host "laptop", labels env=demo, caps read, description "makes designs"
acme/team/alice2  acme.team.alice2  acme_team_alice2  created 2026-11-19T00:21:38Z
acme/team/bob  acme.team.bob  acme_team_bob  created 2026-11-19T00:21:39Z
acme/team/carol  acme.team.carol  acme_team_carol  created 2026-11-19T00:21:50Z, note "demo tool", labels env=demo,kind=tool
acme/team/hal  acme.team.hal  acme_team_hal  created 2026-11-19T00:24:03Z
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
5 endpoints
```

Marks: P1, P2, P3, P5, P6, P8, P9; 4 line(s) over 80 columns, first: `acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T...`; jargon: cap, creds, kind, label

**Case 7 (el_flag): unknown option**

```text
$ mashctl endpoint list --frob
[exit 2]
--- stdout
--- stderr
mashctl: endpoint list: unknown option "--frob"; try: mashctl endpoint list --help
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint list: unknown option "--frob"; try: mashctl endpoint...`

**Case 8 (el_extra): extra argument**

```text
$ mashctl endpoint list extra
[exit 2]
--- stdout
--- stderr
mashctl: endpoint list: takes no argument; try: mashctl endpoint show ADDRESS for one endpoint, or mashctl endpoint list --help
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint list: takes no argument; try: mashctl endpoint show ...`

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2, 4, 6.
- P2 (one address printed in two or three forms on one line): cases 4, 6.
- P3 (table or listing not aligned, or no headers): cases 4, 6.
- P4 (a `next:` or `Next:` line is present): cases 2.
- P5 (lines over 80 columns): cases 1, 2, 4, 6, 7, 8.
- P6 (jargon words): cases 2 (creds), 4 (creds, kind, label), 6 (cap, creds, kind, label).
- P8 (field repeated on one screen): cases 4, 6.
- P9 (information not asked for, which belongs behind --detail): cases 4, 6.
- P1: the preamble is printed on stderr before the output in every case that connects, then the count `4 endpoints` is printed after the output on stderr; the preamble and the count are both on stderr and the table is on stdout.
- P2/P3/P8: each row is `acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T00:21:38Z` (the address in three forms, no labels, no headers); columns are separated by two spaces and not padded, so the second column starts at a different place per row (`acme/team/alice2  acme.team.alice2`). The owner and the group are the same on every row.
- P6/P9: the last column is `created TIME` and, when set, `note "demo tool", labels env=demo,kind=tool`, `host "laptop"`, `caps read`, `description "makes designs"`; fields that were not asked for. The row for carol shows `kind=tool` as a label that `--tool` set.
- P4 (el_empty): `no endpoints` is on stdout and `make one with: mashctl endpoint create --help` is on stdout; it names `--help`, not the command that makes an endpoint (`endpoint create ADDRESS --make-key`).
- The count is on stderr (`4 endpoints`) and the table is on stdout, so `mashctl endpoint list 2>/dev/null` shows no count and `mashctl endpoint list >f` puts the count on the screen.


## endpoint reissue

**Case 1 (help_endpoint_reissue): --help**

```text
$ mashctl endpoint reissue --help
[exit 0]
--- stdout
usage: mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE]
                                [--servers N] [--sys-creds FILE] [--dry-run] [--json]
       mashctl endpoint reissue (--all | --changed) --make-key [--yes] [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design. The old key is revoked on every server, then the directory record names the new key.
  --public-key FILE  sign the new public key the endpoint made; the JWT is written to --jwt-out
  --make-key         make the key on this host and replace <config dir>/<address>.creds
  --all              every endpoint that has a creds file on this host (same-host only: --make-key, never --public-key);
                     an endpoint with no creds file here is skipped (no_local_creds), not rotated
  --changed          every endpoint whose creds file on this host has a permission set that differs from the current one;
                     an endpoint with no creds file here is skipped (no_local_creds)
  --yes              do the bulk reissue; without it (or with --dry-run) the command lists what it would reissue
  A bulk run reissues one endpoint at a time in address order; a refusal or error on one does not stop the others.
  The bulk JSON has schema mashctl-endpoint-bulk@1.
  A key made by --make-key is kept in <creds file>.pending (mode 0600) until the creds file is written, so the same
  command run again after a kill finishes with the same key.
  A rerun of --public-key FILE --jwt-out FILE after a run that switched the directory to the key but did not write the
  JWT is a resume: <jwt-out>.pending (mode 0600) records the rotation, and the rerun writes the JWT and removes it.
  --public-key FILE is checked first: a key the account JWT revokes is refused (key_revoked), also when it is the
  endpoint's current key (a resume or a restore never signs a revoked key; make a new key with mash key create);
  a check that cannot be made is an error (revocation_unknown), and nothing is changed.
  A staged key that the account revokes is refused (staged_key_revoked): remove the .pending file and run again;
  a check that cannot be made is an error (revocation_unknown).
  --changed also reissues an endpoint with a staged <creds file>.pending (rotation_pending) or whose current key is
  revoked (key_revoked); one whose revocation cannot be checked is an error, revocation_unknown, never "unchanged".
  Refusals include key_unchanged: --public-key names the key the endpoint already has, so nothing would be rotated.
  Other refusals: future_issued (the old key's issue record is dated after the revocation time; fix the clock),
  creds_mismatch (--make-key found a creds file that is not this endpoint's current creds: its name, key or account
  differs; move it aside with mv FILE FILE.old and run again, or use --public-key), creds_unreadable (the file exists
  and cannot be read or parsed).
  Errors include superseded: another reissue changed the endpoint's key while this one ran, so the file this run wrote
  was removed; run the command again.
  Refusals include reissue_busy: another reissue of ADDRESS holds the lock (its pid and host are named when readable).
  A reissue of an endpoint holds a lock on its creds file's .lock file; the operating system releases it when the run
  ends or is killed, so a killed run needs nothing removed. The lock works on one host (a local filesystem), not across
  hosts or on a network filesystem that does not honour flock. A .lock directory made by an earlier mash is reported
  and removed once by hand.
  exit codes: 0 done, planned or unchanged, 1 refused, 2 usage, 5 partial (bulk), 6 error
Example:
  mashctl endpoint reissue alice/dev/design --public-key design.pub
  mashctl endpoint reissue --changed --make-key --yes
--- stderr
```

Marks: P3, P5, P6; 31 line(s) over 80 columns, first: `usage: mashctl endpoint reissue ADDRESS (--public-key FILE | --make-ke...`; jargon: JWT, creds, issue record

**Case 2 (bare_endpoint_reissue): no arguments**

```text
$ mashctl endpoint reissue
[exit 2]
--- stdout
--- stderr
mashctl: endpoint reissue: error (address_missing): no endpoint address: write alice/dev/design
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint reissue: error (address_missing): no endpoint addres...`

**Case 3 (er_nokey): no key flag**

```text
$ mashctl endpoint reissue acme.team.bob
[exit 2]
--- stdout
--- stderr
mashctl: endpoint reissue: error (key_missing): a reissue needs a new key: write --public-key FILE (the endpoint's new public key, from mash key create) or --make-key (make the key on this host)
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint reissue: error (key_missing): a reissue needs a new ...`

**Case 4 (er_bothkeys): both key flags**

```text
$ mashctl endpoint reissue acme.team.bob --make-key --public-key /tmp/mc/work/bob2.pub
[exit 2]
--- stdout
--- stderr
mashctl: endpoint reissue: error (key_both): give one of --public-key FILE and --make-key, not both
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint reissue: error (key_both): give one of --public-key ...`

**Case 5 (er_unchanged): --public-key is the current key**

```text
$ mashctl endpoint reissue acme.team.bob --public-key /tmp/mc/work/bob.pub --jwt-out /tmp/mc/work/bob-re.jwt
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint reissue: refused (key_unchanged): the public key is already this endpoint's key, so nothing would be rotated; make a new key with: mash key create --out NAME.seed
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds, seed

**Case 6 (er_missing): endpoint missing**

```text
$ mashctl endpoint reissue acme.team.zed --make-key
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint reissue: refused (endpoint_missing): endpoint acme.team.zed does not exist; create it with: mashctl endpoint create acme/team/zed
```

Marks: P1, P2, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 7 (er_bad): not an address**

```text
$ mashctl endpoint reissue bob --make-key
[exit 2]
--- stdout
--- stderr
mashctl: endpoint reissue: error (address_invalid): not an endpoint address (the value is not shown): write owner/group/instance, for example alice/dev/design
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint reissue: error (address_invalid): not an endpoint ad...`

**Case 8 (er_dry): --make-key --dry-run**

```text
$ mashctl endpoint reissue acme.team.alice --make-key --dry-run
[exit 0]
--- stdout
would reissue endpoint acme/team/alice
  address  acme.team.alice
  creds    /tmp/mc/cfg/acme.team.alice.creds
next: restart the sessions of this endpoint so they read the new creds
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 9 (er_dry_json): --make-key --dry-run --json**

```text
$ mashctl endpoint reissue acme.team.carol --make-key --dry-run --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "planned",
  "owner": "acme",
  "group": "team",
  "instance": "carol",
  "address": "acme.team.carol",
  "key": "acme_team_carol",
  "creds_path": "/tmp/mc/cfg/acme.team.carol.creds",
  "creds_written": false,
  "labels": {},
  "caps": [],
  "description": "",
  "note": "",
  "next_steps": [
    {
      "sender": "endpoint",
      "step": "session",
      "action": "restart the sessions of this endpoint so they read the new creds",
      "address": "acme.team.carol",
      "purpose": "use",
      "detail": "creds file /tmp/mc/cfg/acme.team.carol.creds"
    }
  ],
  "refusal": null
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked)

**Case 10 (er_make): --make-key**

```text
$ mashctl endpoint reissue acme.team.alice --make-key
[exit 0]
--- stdout
reissued endpoint acme/team/alice
  address  acme.team.alice
  creds    /tmp/mc/cfg/acme.team.alice.creds
next: restart the sessions of this endpoint so they read the new creds
servers acknowledged the account update: 1
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
revoked user key UAGT4ZQX...: 1 server(s) acknowledged
```

Marks: P1, P4, P5, P6, P8, P9; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 11 (er_make_json): --make-key --json**

```text
$ mashctl endpoint reissue acme.team.alice --make-key --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "reissued",
  "owner": "acme",
  "group": "team",
  "instance": "alice",
  "address": "acme.team.alice",
  "key": "acme_team_alice",
  "creds_path": "/tmp/mc/cfg/acme.team.alice.creds",
  "creds_written": true,
  "user_public_key": "UAPSDW3E5VMYV4GASH4VHHCO3WEW6EQJFL6WI2YYSLZP5NUUISBSGUB2",
  "servers_acknowledged": 1,
  "labels": {},
  "caps": [],
  "description": "",
  "note": "",
  "next_steps": [
    {
      "sender": "endpoint",
      "step": "session",
      "action": "restart the sessions of this endpoint so they read the new creds",
      "address": "acme.team.alice",
      "purpose": "use",
      "detail": "creds file /tmp/mc/cfg/acme.team.alice.creds"
    }
  ],
  "refusal": null
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
revoked user key UA2VGOPQ...: 1 server(s) acknowledged
```

Marks: none (JSON, for agents; not marked)

**Case 12 (er_pub_dry): --public-key --dry-run**

```text
$ mashctl endpoint reissue acme.team.bob --public-key /tmp/mc/work/bob2.pub --jwt-out /tmp/mc/work/bob2.jwt --dry-run
[exit 0]
--- stdout
would reissue endpoint acme/team/bob
  address  acme.team.bob
  jwt      /tmp/mc/work/bob2.jwt
next: on the endpoint's machine, run: mash credential create --seed bob.seed --jwt bob2.jwt
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P8; 2 line(s) over 80 columns, first: `next: on the endpoint's machine, run: mash credential create --seed bo...`; jargon: JWT, creds, seed

**Case 13 (er_pub): --public-key**

```text
$ mashctl endpoint reissue acme.team.bob --public-key /tmp/mc/work/bob2.pub --jwt-out /tmp/mc/work/bob2.jwt
[exit 0]
--- stdout
reissued endpoint acme/team/bob
  address  acme.team.bob
  jwt      /tmp/mc/work/bob2.jwt
next: on the endpoint's machine, run: mash credential create --seed bob.seed --jwt bob2.jwt
servers acknowledged the account update: 1
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
revoked user key UBH6YD4H...: 1 server(s) acknowledged
```

Marks: P1, P4, P5, P6, P8, P9; 2 line(s) over 80 columns, first: `next: on the endpoint's machine, run: mash credential create --seed bo...`; jargon: JWT, creds, seed

**Case 14 (er_pub_exists): --public-key, --jwt-out exists**

```text
$ mashctl endpoint reissue acme.team.bob --public-key /tmp/mc/work/bob2.pub --jwt-out /tmp/mc/work/bob2.jwt
[exit 1]
--- stdout
--- stderr
mashctl: endpoint reissue: refused (file_exists): /tmp/mc/work/bob2.jwt already exists and is never overwritten; name another file with --jwt-out or remove it
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `mashctl: endpoint reissue: refused (file_exists): /tmp/mc/work/bob2.jw...`; jargon: JWT

**Case 15 (er_json_unchanged): --public-key issued to another address, --json**

```text
$ mashctl endpoint reissue acme/team/alice2 --public-key /tmp/mc/work/bob2.pub --jwt-out /tmp/mc/work/x.jwt --json
[exit 1]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "refused",
  "owner": "acme",
  "group": "team",
  "instance": "alice2",
  "address": "acme.team.alice2",
  "key": "acme_team_alice2",
  "jwt_path": "/tmp/mc/work/x.jwt",
  "jwt_written": false,
  "labels": {},
  "caps": [],
  "description": "",
  "note": "",
  "next_steps": [
    {
      "sender": "endpoint",
      "step": "key",
      "action": "make a new key for this endpoint on its machine",
      "address": "acme.team.alice2",
      "purpose": "use",
      "detail": "mash key create --out alice2.seed"
    }
  ],
  "refusal": {
    "code": "public_key_issued",
    "detail": "the public key UASKC43D... was already issued to another address; each key is used at one address: make a new key for this endpoint with: mash key create --out alice2.seed"
  }
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint reissue: refused (public_key_issued): the public key UASKC43D... was already issued to another address; each key is used at one address: make a new key for this endpoint with: mash key create --out alice2.seed
```

Marks: none (JSON, for agents; not marked)

**Case 16 (er_credsmismatch): creds file is not the endpoint's current key**

```text
$ mashctl endpoint reissue acme.team.bob --make-key
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint reissue: refused (creds_mismatch): the creds file /tmp/mc/cfg/acme.team.bob.creds is not this endpoint's current creds: its key is not the key the directory names for this endpoint; nothing was changed: move the file aside (mv /tmp/mc/cfg/acme.team.bob.creds /tmp/mc/cfg/acme.team.bob.creds.old) and run again, or use --public-key
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 17 (er_busy): another reissue holds the lock (held with flock(1) on the .lock file)**

```text
$ mashctl endpoint reissue acme.team.alice2 --make-key
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint reissue: refused (reissue_busy): another reissue of acme.team.alice2 holds the lock (pid 85044 on bf2); the lock is released when that run ends or is killed; run this again later
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 18 (er_all_dry): --all --dry-run**

```text
$ mashctl endpoint reissue --all --make-key --dry-run
[exit 0]
--- stdout
would reissue acme/team/alice
would reissue acme/team/alice2
would reissue acme/team/bob
would reissue acme/team/carol
reissued 0, skipped 0, refused 0, error 0
would reissue 4; nothing was changed; to do it run: mashctl endpoint reissue --all --make-key --yes
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P9; 2 line(s) over 80 columns, first: `would reissue 4; nothing was changed; to do it run: mashctl endpoint r...`; jargon: creds

**Case 19 (er_all_dry2): --all --dry-run with an endpoint that has no local creds**

```text
$ mashctl endpoint reissue --all --make-key --dry-run
[exit 0]
--- stdout
would reissue acme/team/alice
would reissue acme/team/alice2
would reissue acme/team/bob
would reissue acme/team/carol
skipped acme/team/ned (no_local_creds): its creds are not on this host; reissue it on its own machine with: mash key create, then mashctl endpoint reissue acme/team/ned --public-key FILE
reissued 0, skipped 1, refused 0, error 0
would reissue 4; nothing was changed; to do it run: mashctl endpoint reissue --all --make-key --yes
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6, P9; 3 line(s) over 80 columns, first: `skipped acme/team/ned (no_local_creds): its creds are not on this host...`; jargon: creds

**Case 20 (er_all_json): --all --dry-run --json**

```text
$ mashctl endpoint reissue --all --make-key --dry-run --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint-bulk@1",
  "outcome": "planned",
  "counts": {
    "reissued": 0,
    "planned": 4,
    "skipped": 1,
    "refused": 0,
    "error": 0
  },
  "endpoints": [
    {
      "owner": "acme",
      "group": "team",
      "instance": "alice",
      "address": "acme.team.alice",
      "key": "acme_team_alice",
      "outcome": "planned",
      "reason": null,
      "creds_path": "/tmp/mc/cfg/acme.team.alice.creds"
    },
    {
      "owner": "acme",
      "group": "team",
      "instance": "alice2",
      "address": "acme.team.alice2",
      "key": "acme_team_alice2",
      "outcome": "planned",
      "reason": null,
      "creds_path": "/tmp/mc/cfg/acme.team.alice2.creds"
    },
    {
      "owner": "acme",
      "group": "team",
      "instance": "bob",
      "address": "acme.team.bob",
      "key": "acme_team_bob",
      "outcome": "planned",
      "reason": null,
      "creds_path": "/tmp/mc/cfg/acme.team.bob.creds"
[... 34 more stdout lines omitted from this document]
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked); stdout trimmed to 40 of 74 lines

**Case 21 (er_all_noyes): --all without --yes**

```text
$ mashctl endpoint reissue --all --make-key
[exit 0]
--- stdout
would reissue acme/team/alice
would reissue acme/team/alice2
would reissue acme/team/bob
would reissue acme/team/carol
reissued 0, skipped 0, refused 0, error 0
would reissue 4; nothing was changed; to do it run: mashctl endpoint reissue --all --make-key --yes
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P4, P5, P6; 2 line(s) over 80 columns, first: `would reissue 4; nothing was changed; to do it run: mashctl endpoint r...`; jargon: creds

**Case 22 (er_all_yes): --all --yes (one creds mismatch)**

```text
$ mashctl endpoint reissue --all --make-key --yes
[exit 5]
--- stdout
reissued acme/team/alice
  servers acknowledged the account update: 1
reissued acme/team/alice2
  servers acknowledged the account update: 1
refused acme/team/bob (creds_mismatch)
reissued acme/team/carol
  servers acknowledged the account update: 1
reissued 3, skipped 0, refused 1, error 0
partial: some endpoints did not finish; rerun with --changed, not --all (--all rotates the ones that succeeded again): mashctl endpoint reissue --changed --make-key --yes
next: restart the sessions of the reissued endpoints so they read the new creds
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
revoked user key UAPSDW3E...: 1 server(s) acknowledged
revoked user key UCX3KAJI...: 1 server(s) acknowledged
mashctl: endpoint reissue acme/team/bob: creds_mismatch: the creds file /tmp/mc/cfg/acme.team.bob.creds is not this endpoint's current creds: its key is not the key the directory names for this endpoint; nothing was changed: move the file aside (mv /tmp/mc/cfg/acme.team.bob.creds /tmp/mc/cfg/acme.team.bob.creds.old) and run again, or use --public-key
revoked user key UAEXLQVH...: 1 server(s) acknowledged
```

Marks: P1, P2, P4, P5, P6, P8, P9; 3 line(s) over 80 columns, first: `partial: some endpoints did not finish; rerun with --changed, not --al...`; jargon: creds

**Case 23 (er_busy_all): --all --yes while one endpoint is locked**

```text
$ mashctl endpoint reissue --all --make-key --yes
[exit 5]
--- stdout
reissued acme/team/alice
  servers acknowledged the account update: 1
refused acme/team/alice2 (reissue_busy)
refused acme/team/bob (creds_mismatch)
reissued acme/team/carol
  servers acknowledged the account update: 1
reissued 2, skipped 0, refused 2, error 0
partial: some endpoints did not finish; rerun with --changed, not --all (--all rotates the ones that succeeded again): mashctl endpoint reissue --changed --make-key --yes
next: restart the sessions of the reissued endpoints so they read the new creds
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
revoked user key UCEP7T3M...: 1 server(s) acknowledged
mashctl: endpoint reissue acme/team/alice2: reissue_busy: another reissue of acme.team.alice2 holds the lock (pid 85044 on bf2); the lock is released when that run ends or is killed; run this again later
mashctl: endpoint reissue acme/team/bob: creds_mismatch: the creds file /tmp/mc/cfg/acme.team.bob.creds is not this endpoint's current creds: its key is not the key the directory names for this endpoint; nothing was changed: move the file aside (mv /tmp/mc/cfg/acme.team.bob.creds /tmp/mc/cfg/acme.team.bob.creds.old) and run again, or use --public-key
revoked user key UBSH3UEY...: 1 server(s) acknowledged
```

Marks: P1, P2, P4, P5, P6, P8; 4 line(s) over 80 columns, first: `partial: some endpoints did not finish; rerun with --changed, not --al...`; jargon: creds

**Case 24 (er_changed): --changed**

```text
$ mashctl endpoint reissue --changed --make-key
[exit 0]
--- stdout
reissued 0, skipped 0, refused 0, error 0
nothing to reissue
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 25 (er_changed_dry_json): --changed --dry-run --json**

```text
$ mashctl endpoint reissue --changed --make-key --dry-run --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint-bulk@1",
  "outcome": "unchanged",
  "counts": {
    "reissued": 0,
    "planned": 0,
    "skipped": 0,
    "refused": 0,
    "error": 0
  },
  "endpoints": [],
  "next_steps": [],
  "refusal": null
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked)

**Case 26 (er_changed_yes): --changed --yes**

```text
$ mashctl endpoint reissue --changed --make-key --yes
[exit 0]
--- stdout
reissued 0, skipped 0, refused 0, error 0
nothing to reissue
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 27 (er_all_nomake): --all without --make-key**

```text
$ mashctl endpoint reissue --all
[exit 2]
--- stdout
--- stderr
mashctl: endpoint reissue: the bulk forms take --make-key only: many public keys cannot arrive in one command; run endpoint reissue ADDRESS --public-key FILE once per endpoint
usage: mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE]
                                [--servers N] [--sys-creds FILE] [--dry-run] [--json]
       mashctl endpoint reissue (--all | --changed) --make-key [--yes] [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design. The old key is revoked on every server, then the directory record names the new key.
  --public-key FILE  sign the new public key the endpoint made; the JWT is written to --jwt-out
  --make-key         make the key on this host and replace <config dir>/<address>.creds
  --all              every endpoint that has a creds file on this host (same-host only: --make-key, never --public-key);
                     an endpoint with no creds file here is skipped (no_local_creds), not rotated
  --changed          every endpoint whose creds file on this host has a permission set that differs from the current one;
                     an endpoint with no creds file here is skipped (no_local_creds)
  --yes              do the bulk reissue; without it (or with --dry-run) the command lists what it would reissue
  A bulk run reissues one endpoint at a time in address order; a refusal or error on one does not stop the others.
  The bulk JSON has schema mashctl-endpoint-bulk@1.
  A key made by --make-key is kept in <creds file>.pending (mode 0600) until the creds file is written, so the same
  command run again after a kill finishes with the same key.
  A rerun of --public-key FILE --jwt-out FILE after a run that switched the directory to the key but did not write the
  JWT is a resume: <jwt-out>.pending (mode 0600) records the rotation, and the rerun writes the JWT and removes it.
  --public-key FILE is checked first: a key the account JWT revokes is refused (key_revoked), also when it is the
  endpoint's current key (a resume or a restore never signs a revoked key; make a new key with mash key create);
  a check that cannot be made is an error (revocation_unknown), and nothing is changed.
  A staged key that the account revokes is refused (staged_key_revoked): remove the .pending file and run again;
  a check that cannot be made is an error (revocation_unknown).
  --changed also reissues an endpoint with a staged <creds file>.pending (rotation_pending) or whose current key is
  revoked (key_revoked); one whose revocation cannot be checked is an error, revocation_unknown, never "unchanged".
  Refusals include key_unchanged: --public-key names the key the endpoint already has, so nothing would be rotated.
  Other refusals: future_issued (the old key's issue record is dated after the revocation time; fix the clock),
  creds_mismatch (--make-key found a creds file that is not this endpoint's current creds: its name, key or account
  differs; move it aside with mv FILE FILE.old and run again, or use --public-key), creds_unreadable (the file exists
  and cannot be read or parsed).
  Errors include superseded: another reissue changed the endpoint's key while this one ran, so the file this run wrote
  was removed; run the command again.
  Refusals include reissue_busy: another reissue of ADDRESS holds the lock (its pid and host are named when readable).
  A reissue of an endpoint holds a lock on its creds file's .lock file; the operating system releases it when the run
  ends or is killed, so a killed run needs nothing removed. The lock works on one host (a local filesystem), not across
  hosts or on a network filesystem that does not honour flock. A .lock directory made by an earlier mash is reported
  and removed once by hand.
  exit codes: 0 done, planned or unchanged, 1 refused, 2 usage, 5 partial (bulk), 6 error
Example:
  mashctl endpoint reissue alice/dev/design --public-key design.pub
[... 1 more stderr lines omitted from this document]
```

Marks: P5, P6; 32 line(s) over 80 columns, first: `mashctl: endpoint reissue: the bulk forms take --make-key only: many p...`; jargon: JWT, creds, issue record; stderr trimmed to 40 of 41 lines

**Case 28 (er_all_pub): --all with --public-key**

```text
$ mashctl endpoint reissue --all --public-key /tmp/mc/work/bob2.pub
[exit 2]
--- stdout
--- stderr
mashctl: endpoint reissue: the bulk forms take --make-key only: many public keys cannot arrive in one command; run endpoint reissue ADDRESS --public-key FILE once per endpoint
usage: mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE]
                                [--servers N] [--sys-creds FILE] [--dry-run] [--json]
       mashctl endpoint reissue (--all | --changed) --make-key [--yes] [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design. The old key is revoked on every server, then the directory record names the new key.
  --public-key FILE  sign the new public key the endpoint made; the JWT is written to --jwt-out
  --make-key         make the key on this host and replace <config dir>/<address>.creds
  --all              every endpoint that has a creds file on this host (same-host only: --make-key, never --public-key);
                     an endpoint with no creds file here is skipped (no_local_creds), not rotated
  --changed          every endpoint whose creds file on this host has a permission set that differs from the current one;
                     an endpoint with no creds file here is skipped (no_local_creds)
  --yes              do the bulk reissue; without it (or with --dry-run) the command lists what it would reissue
  A bulk run reissues one endpoint at a time in address order; a refusal or error on one does not stop the others.
  The bulk JSON has schema mashctl-endpoint-bulk@1.
  A key made by --make-key is kept in <creds file>.pending (mode 0600) until the creds file is written, so the same
  command run again after a kill finishes with the same key.
  A rerun of --public-key FILE --jwt-out FILE after a run that switched the directory to the key but did not write the
  JWT is a resume: <jwt-out>.pending (mode 0600) records the rotation, and the rerun writes the JWT and removes it.
  --public-key FILE is checked first: a key the account JWT revokes is refused (key_revoked), also when it is the
  endpoint's current key (a resume or a restore never signs a revoked key; make a new key with mash key create);
  a check that cannot be made is an error (revocation_unknown), and nothing is changed.
  A staged key that the account revokes is refused (staged_key_revoked): remove the .pending file and run again;
  a check that cannot be made is an error (revocation_unknown).
  --changed also reissues an endpoint with a staged <creds file>.pending (rotation_pending) or whose current key is
  revoked (key_revoked); one whose revocation cannot be checked is an error, revocation_unknown, never "unchanged".
  Refusals include key_unchanged: --public-key names the key the endpoint already has, so nothing would be rotated.
  Other refusals: future_issued (the old key's issue record is dated after the revocation time; fix the clock),
  creds_mismatch (--make-key found a creds file that is not this endpoint's current creds: its name, key or account
  differs; move it aside with mv FILE FILE.old and run again, or use --public-key), creds_unreadable (the file exists
  and cannot be read or parsed).
  Errors include superseded: another reissue changed the endpoint's key while this one ran, so the file this run wrote
  was removed; run the command again.
  Refusals include reissue_busy: another reissue of ADDRESS holds the lock (its pid and host are named when readable).
  A reissue of an endpoint holds a lock on its creds file's .lock file; the operating system releases it when the run
  ends or is killed, so a killed run needs nothing removed. The lock works on one host (a local filesystem), not across
  hosts or on a network filesystem that does not honour flock. A .lock directory made by an earlier mash is reported
  and removed once by hand.
  exit codes: 0 done, planned or unchanged, 1 refused, 2 usage, 5 partial (bulk), 6 error
Example:
  mashctl endpoint reissue alice/dev/design --public-key design.pub
[... 1 more stderr lines omitted from this document]
```

Marks: P5, P6; 32 line(s) over 80 columns, first: `mashctl: endpoint reissue: the bulk forms take --make-key only: many p...`; jargon: JWT, creds, issue record; stderr trimmed to 40 of 41 lines

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 5, 6, 8, 10, 12, 13, 16, 17, 18, 19, 21, 22, 23, 24, 26.
- P2 (one address printed in two or three forms on one line): cases 6, 22, 23.
- P3 (table or listing not aligned, or no headers): cases 1.
- P4 (a `next:` or `Next:` line is present): cases 8, 10, 12, 13, 18, 19, 21, 22, 23.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 13, 14, 16, 17, 18, 19, 21, 22, 23, 24, 26, 27, 28.
- P6 (jargon words): cases 1 (JWT, creds, issue record), 5 (creds, seed), 6 (creds), 8 (creds), 10 (creds), 12 (JWT, creds, seed), 13 (JWT, creds, seed), 14 (JWT), 16 (creds), 17 (creds), 18 (creds), 19 (creds), 21 (creds), 22 (creds), 23 (creds), 24 (creds), 26 (creds), 27 (JWT, creds, issue record), 28 (JWT, creds, issue record).
- P8 (field repeated on one screen): cases 8, 10, 12, 13, 22, 23.
- P9 (information not asked for, which belongs behind --detail): cases 10, 13, 18, 19, 22.
- P1/P2: `would reissue acme/team/alice`, the success block (`reissued endpoint acme/team/bob`, `  address  acme.team.bob`, `  creds    /tmp/mc/cfg/acme.team.bob.creds`) show the address in two or three forms over three lines; the bulk lines (`reissued acme/team/alice`) use the slash form only.
- P8 (er_make, er_pub): the revocation is printed on stderr (`revoked user key UAO35ZE3...: 1 server(s) acknowledged`) and again on stdout (`servers acknowledged the account update: 1`); the stdout line follows the `next:` line, so it is below the `next:` line and not above it.
- P4 (er_make): `next: restart the sessions of this endpoint so they read the new creds` is an action without a command; in the flow reissue then start a session the next screen is the session restart. `er_dry` prints the same `next:` line for a run that changed nothing.
- P4 (er_pub, er_pub_dry): `next: on the endpoint's machine, run: mash credential create --seed bob.seed --jwt bob2.jwt` is the right command; the seed name is the old seed (`bob.seed`) and the new key is `bob2.seed`, so the line names a file that holds the revoked key.
- P4 (er_all_dry, er_all_noyes): the last line is `would reissue 4; nothing was changed; to do it run: mashctl endpoint reissue --all --make-key --yes`; it is the next command and it is correct. It is not a `next:` line and it is not separated from the counts line above it (`reissued 0, skipped 0, refused 0, error 0`), which says `reissued 0` in a dry run.
- P4 (er_all_yes, er_busy_all): two lines follow the results: `partial: some endpoints did not finish; rerun with --changed, not --all (--all rotates the ones that succeeded again): mashctl endpoint reissue --changed --make-key --yes` (170 columns) and `next: restart the sessions of the reissued endpoints so they read the new creds`; the first is the next command, the second is the next step after it.
- er_all_yes: the dry run (er_all_dry) listed acme/team/bob as `would reissue`; the real run refused it as `creds_mismatch`, because the creds file was written for the earlier key. The dry run does not run the creds check.
- er_all_dry2: an endpoint with no creds file on the host is listed as `skipped acme/team/ned (no_local_creds): its creds are not on this host; reissue it on its own machine with: mash key create, then mashctl endpoint reissue acme/team/ned -...`; the counts line says `skipped 1` and the `would reissue 4` line does not count it.
- er_busy: the lock is held by `flock(1)` on `acme.team.alice2.creds.lock` for 25 seconds; the command waited 10 seconds and refused as `reissue_busy`. The text names `pid 85044 on bf2`, which is not the pid of the process that held the lock (87460); flock(1) does not write the lock file, so the pid in it is from the last mashctl run that did. A second mashctl run as the holder would name its own pid.
- er_credsmismatch: `creds_mismatch: the creds file /tmp/mc/cfg/acme.team.bob.creds is not this endpoint's current creds: its key is not the key the direc...`; the file was left by the earlier `--public-key` reissue of the same endpoint.
- er_changed, er_changed_yes: `reissued 0, skipped 0, refused 0, error 0` then `nothing to reissue`; the `reissued 0` line is printed when nothing was reissued.
- er_all_nomake, er_all_pub: a usage error prints about 30 lines: the one-line error, then the whole `endpoint reissue --help` text (docs of every refusal code); the help text has 31 lines over 80 columns.
- P7: JSON of the bulk run (er_all_json) and the single run (er_make_json) is the agent form; the text form prints `next:` and counts for people.


## endpoint show

**Case 1 (help_endpoint_show): --help**

```text
$ mashctl endpoint show --help
[exit 0]
--- stdout
usage: mashctl endpoint show ADDRESS [--json]
  ADDRESS is owner/group/instance (for example alice/dev/design) or owner.group.instance.
  Prints the endpoint's whole directory record, the creds file for it in the config directories (its path only), and
  its issue record, if there is one. Changes nothing.
  --json   print the result as JSON (schema mashctl-endpoint@1)
  exit: 0 shown, 1 refused (no such endpoint), 2 usage, 6 error
Example:
  mashctl endpoint show alice/dev/design
--- stderr
```

Marks: P3, P5, P6; 2 line(s) over 80 columns, first: `  ADDRESS is owner/group/instance (for example alice/dev/design) or ow...`; jargon: creds, issue record

**Case 2 (bare_endpoint_show): no arguments**

```text
$ mashctl endpoint show
[exit 2]
--- stdout
--- stderr
mashctl: endpoint show: no endpoint address: give owner/group/instance, for example alice/dev/design; try: mashctl endpoint show --help
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint show: no endpoint address: give owner/group/instance...`

**Case 3 (es_found): found, flags and labels empty**

```text
$ mashctl endpoint show acme/team/alice
[exit 0]
--- stdout
acme/team/alice  acme.team.alice
  owner:        acme
  group:        team
  instance:     alice
  agent:        claude-code
  account:      AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6
  user key:     UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ
  mail subject: agents.mail.acme.team.alice
  created:      2026-11-19T00:21:38Z
  note:         
  role:         
  host:         
  description:  
  caps:         
  labels:       
  creds file:   /tmp/mc/cfg/acme.team.alice.creds
  issue record: purpose issue, issued 2026-11-19T00:21:38Z, not revoked
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P2, P5, P6, P8, P9; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: cap, creds, issue record, label

**Case 4 (es_after_meta): after set-meta**

```text
$ mashctl endpoint show acme/team/alice
[exit 0]
--- stdout
acme/team/alice  acme.team.alice
  owner:        acme
  group:        team
  instance:     alice
  agent:        claude-code
  account:      AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6
  user key:     UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ
  mail subject: agents.mail.acme.team.alice
  created:      2026-11-19T00:21:38Z
  note:         
  role:         
  host:         laptop
  description:  makes designs
  caps:         read
  labels:       
  creds file:   /tmp/mc/cfg/acme.team.alice.creds
  issue record: purpose issue, issued 2026-11-19T00:21:38Z, not revoked
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P2, P5, P6, P8, P9; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: cap, creds, issue record, label

**Case 5 (es_tool): found, tool**

```text
$ mashctl endpoint show acme.team.carol
[exit 0]
--- stdout
acme/team/carol  acme.team.carol
  owner:        acme
  group:        team
  instance:     carol
  agent:        claude-code
  account:      AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6
  user key:     UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN
  mail subject: agents.mail.acme.team.carol
  created:      2026-11-19T00:21:50Z
  note:         demo tool
  role:         
  host:         
  description:  
  caps:         
  labels:       env=demo,kind=tool
  creds file:   /tmp/mc/cfg/acme.team.carol.creds
  issue record: purpose issue, issued 2026-11-19T00:21:50Z, not revoked
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P2, P5, P6, P8, P9; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: cap, creds, issue record, kind, label

**Case 6 (es_json): found, --json**

```text
$ mashctl endpoint show acme/team/carol --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "shown",
  "owner": "acme",
  "group": "team",
  "instance": "carol",
  "address": "acme.team.carol",
  "key": "acme_team_carol",
  "user_public_key": "UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN",
  "created": "2026-11-19T00:21:50Z",
  "note": "demo tool",
  "labels": {
    "env": "demo",
    "kind": "tool"
  },
  "caps": [],
  "description": "",
  "role": "",
  "host": "",
  "agent": "claude-code",
  "account": "AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6",
  "mail_subject": "agents.mail.acme.team.carol",
  "creds": {
    "path": "/tmp/mc/cfg/acme.team.carol.creds",
    "exists": true
  },
  "issued": {
    "purpose": "issue",
    "issued_at": "2026-11-19T00:21:50Z",
    "revoked": false
  },
  "refusal": null
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked)

**Case 7 (es_nocreds): endpoint whose creds file is not on this host**

```text
$ mashctl endpoint show acme/team/ned
[exit 0]
--- stdout
acme/team/ned  acme.team.ned
  owner:        acme
  group:        team
  instance:     ned
  agent:        claude-code
  account:      AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6
  user key:     UBTXXMRDI7OHQXDXWUA2IDHKI2ZS5PDUE3NDTF3B2FNX4TJ2E4ELPIPA
  mail subject: agents.mail.acme.team.ned
  created:      2026-11-19T00:25:45Z
  note:         
  role:         
  host:         
  description:  no local creds
  caps:         read
  labels:       
  creds file:   /tmp/mc/cfg/acme.team.ned.creds
  issue record: purpose issue, issued 2026-11-19T00:25:45Z, not revoked
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P2, P5, P6, P8, P9; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: cap, creds, issue record, label

**Case 8 (es_nocreds_json): same, --json**

```text
$ mashctl endpoint show acme/team/ned --json
[exit 0]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "shown",
  "owner": "acme",
  "group": "team",
  "instance": "ned",
  "address": "acme.team.ned",
  "key": "acme_team_ned",
  "user_public_key": "UBTXXMRDI7OHQXDXWUA2IDHKI2ZS5PDUE3NDTF3B2FNX4TJ2E4ELPIPA",
  "created": "2026-11-19T00:25:45Z",
  "note": "",
  "labels": {},
  "caps": [
    "read"
  ],
  "description": "no local creds",
  "role": "",
  "host": "",
  "agent": "claude-code",
  "account": "AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6",
  "mail_subject": "agents.mail.acme.team.ned",
  "creds": {
    "path": "/tmp/mc/cfg/acme.team.ned.creds",
    "exists": true
  },
  "issued": {
    "purpose": "issue",
    "issued_at": "2026-11-19T00:25:45Z",
    "revoked": false
  },
  "refusal": null
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked)

**Case 9 (es_notfound): not found**

```text
$ mashctl endpoint show acme/team/zed
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint show acme/team/zed: no directory record for acme/team/zed; mashctl endpoint list shows the endpoints that exist
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 10 (es_json_missing): not found, --json**

```text
$ mashctl endpoint show acme/team/zed --json
[exit 1]
--- stdout
{
  "schema": "mashctl-endpoint@1",
  "outcome": "refused",
  "owner": "acme",
  "group": "team",
  "instance": "zed",
  "address": "acme.team.zed",
  "key": "acme_team_zed",
  "user_public_key": "",
  "created": "",
  "note": "",
  "labels": {},
  "caps": [],
  "description": "",
  "role": "",
  "host": "",
  "agent": "",
  "account": "",
  "mail_subject": "",
  "creds": {
    "path": "/tmp/mc/cfg/acme.team.zed.creds",
    "exists": true
  },
  "issued": null,
  "refusal": {
    "code": "not_found",
    "detail": "no directory record for acme/team/zed; mashctl endpoint list shows the endpoints that exist"
  }
}
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked)

**Case 11 (es_deleted): deleted address**

```text
$ mashctl endpoint show acme/team/dave
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint show acme/team/dave: no directory record for acme/team/dave; mashctl endpoint list shows the endpoints that exist
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 12 (es_bad): not an address**

```text
$ mashctl endpoint show alice
[exit 2]
--- stdout
--- stderr
mashctl: endpoint show: not an endpoint address (the value is not shown): write owner/group/instance, for example alice/dev/design
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint show: not an endpoint address (the value is not show...`

**Case 13 (es_extra): extra argument**

```text
$ mashctl endpoint show acme/team/alice extra
[exit 2]
--- stdout
--- stderr
mashctl: endpoint show: one endpoint address only; try: mashctl endpoint show --help
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint show: one endpoint address only; try: mashctl endpoi...`

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 3, 4, 5, 7, 9, 11.
- P2 (one address printed in two or three forms on one line): cases 3, 4, 5, 7.
- P3 (table or listing not aligned, or no headers): cases 1.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 7, 9, 11, 12, 13.
- P6 (jargon words): cases 1 (creds, issue record), 3 (cap, creds, issue record, label), 4 (cap, creds, issue record, label), 5 (cap, creds, issue record, kind, label), 7 (cap, creds, issue record, label), 9 (creds), 11 (creds).
- P8 (field repeated on one screen): cases 3, 4, 5, 7.
- P9 (information not asked for, which belongs behind --detail): cases 3, 4, 5, 7.
- P1: the preamble is on stderr in every case that connects.
- P2/P8: the first line `acme/team/alice  acme.team.alice` prints two forms of the address; the next three lines print `owner:`, `group:` and `instance:` again; `mail subject: agents.mail.acme.team.alice` prints it a fourth time and `creds file:   /tmp/mc/cfg/acme.team.alice.creds` a fifth time.
- P9: `agent: claude-code`, `account: AD7BK6VI...`, `user key: UAGT...`, `mail subject:` and the empty lines `note:`, `role:`, `host:`, `description:`, `caps:`, `labels:` (printed with nothing after the colon) are shown without being asked; the empty fields are printed for every endpoint (es_found).
- P6: `user key`, `account`, `mail subject`, `caps`, `labels`, `creds file` and `issue record: purpose issue, issued 2026-11-19T00:21:38Z, not revoked` use words that are not explained (`purpose issue`).
- es_nocreds: the endpoint made with `--public-key` has no creds file in the config directory; the text still prints `creds file:   /tmp/mc/cfg/acme.team.ned.creds`, the path where the file would be. The JSON for the missing endpoint (es_json_missing) prints `"exists": true` for a file that does not exist; see Findings.
- es_notfound, es_deleted: `no directory record for acme/team/zed; mashctl endpoint list shows the endpoints that exist`, exit 1, one line over 80 columns on stderr.
- es_bad: `not an endpoint address (the value is not shown): write owner/group/instance, for example alice/dev/design`; the example uses `alice/dev/design`, not the owner of the target.


## inbox inspect

**Case 1 (help_inbox): inbox --help**

```text
$ mashctl inbox --help
[exit 0]
--- stdout
usage:
  mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [--to SEQ] [--limit N] read only: one inbox's stored messages (default the last 20) with sender, trust, store time, size, kind and delivery state derived from the deliver consumer's cursors, and the consumer's totals; never creates, fetches from or acknowledges a consumer
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [...`; jargon: kind

**Case 2 (help_inbox_inspect): inbox inspect --help**

```text
$ mashctl inbox inspect --help
[exit 0]
--- stdout
usage:
  mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [--to SEQ] [--limit N] read only: one inbox's stored messages (default the last 20) with sender, trust, store time, size, kind and delivery state derived from the deliver consumer's cursors, and the consumer's totals; never creates, fetches from or acknowledges a consumer
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [...`; jargon: kind

**Case 3 (bare_inbox): inbox, no sub-verb**

```text
$ mashctl inbox
[exit 2]
--- stdout
--- stderr
mashctl: inbox: the sub-verb is inspect
usage: mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [--to SEQ] [--limit N]
  address: owner.group.instance, or its flat key owner_group_instance
```

Marks: P3, P4, P5, P6; 1 line(s) over 80 columns, first: `usage: mashctl inbox inspect <owner.group.instance> [--json] [--from S...`; jargon: flat key

**Case 4 (bare_inbox_nosub): inbox with unknown sub-verb**

```text
$ mashctl inbox frob
[exit 2]
--- stdout
--- stderr
mashctl: inbox: the sub-verb is inspect
usage: mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [--to SEQ] [--limit N]
  address: owner.group.instance, or its flat key owner_group_instance
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `usage: mashctl inbox inspect <owner.group.instance> [--json] [--from S...`; jargon: flat key

**Case 5 (ii): three messages, none delivered**

```text
$ mashctl inbox inspect acme.team.carol
[exit 0]
--- stdout
inbox acme.team.carol: stream inbox-acme_team_carol, first_seq 1, last_seq 3, messages 3, bytes 1992
consumer deliver: ack_floor stream 0 consumer 0, delivered stream 0 consumer 0, num_pending 3, num_redelivered 0, num_ack_pending 0
SEQ  MSG-ID                            SENDER           TRUST     STORED                SIZE  KIND      STATE
1    4f83ac040b5043183dd2285b26e0ba98  acme_team_alice  verified  2026-11-19T00:22:41Z  11    task      undelivered
2    2250e9385e15acec2c342f4b904e2799  acme_team_alice  verified  2026-11-19T00:22:41Z  10    question  undelivered
3    a078cb9ea3375b81b880e2197da4851e  acme_team_alice  verified  2026-11-19T00:22:41Z  2     task      undelivered
note: per-message delivery count and ack time are not kept by JetStream or mash; the consumer's num_redelivered is the aggregate
note: a message above the consumer's ack floor and at or below its last delivered sequence is delivered and either not acknowledged yet or acknowledged out of order: JetStream does not report acknowledgements above the ack floor
--- stderr
```

Marks: P2, P5, P6, P7, P8, P9; 8 line(s) over 80 columns, first: `inbox acme.team.carol: stream inbox-acme_team_carol, first_seq 1, last...`; jargon: kind

**Case 6 (ii_limit): --from 2 --limit 1**

```text
$ mashctl inbox inspect acme.team.carol --from 2 --limit 1
[exit 0]
--- stdout
inbox acme.team.carol: stream inbox-acme_team_carol, first_seq 1, last_seq 3, messages 3, bytes 1992
consumer deliver: ack_floor stream 0 consumer 0, delivered stream 0 consumer 0, num_pending 3, num_redelivered 0, num_ack_pending 0
SEQ  MSG-ID                            SENDER           TRUST     STORED                SIZE  KIND      STATE
2    2250e9385e15acec2c342f4b904e2799  acme_team_alice  verified  2026-11-19T00:22:41Z  10    question  undelivered
note: per-message delivery count and ack time are not kept by JetStream or mash; the consumer's num_redelivered is the aggregate
note: a message above the consumer's ack floor and at or below its last delivered sequence is delivered and either not acknowledged yet or acknowledged out of order: JetStream does not report acknowledgements above the ack floor
--- stderr
```

Marks: P2, P5, P6, P7, P8, P9; 6 line(s) over 80 columns, first: `inbox acme.team.carol: stream inbox-acme_team_carol, first_seq 1, last...`; jargon: kind

**Case 7 (ii_json): --json**

```text
$ mashctl inbox inspect acme.team.carol --json
[exit 0]
--- stdout
{
  "schema": "mash-inbox-inspect@1",
  "address": "acme.team.carol",
  "owner": "acme",
  "group": "team",
  "instance": "carol",
  "stream": {
    "name": "inbox-acme_team_carol",
    "first_seq": 1,
    "last_seq": 3,
    "messages": 3,
    "bytes": 1992
  },
  "consumer": {
    "name": "deliver",
    "ack_floor": {
      "stream_seq": 0,
      "consumer_seq": 0
    },
    "delivered": {
      "stream_seq": 0,
      "consumer_seq": 0
    },
    "num_pending": 3,
    "num_redelivered": 0,
    "num_ack_pending": 0
  },
  "messages": [
    {
      "seq": 1,
      "msg_id": "4f83ac040b5043183dd2285b26e0ba98",
      "sender": "acme_team_alice",
      "trust": "verified",
      "stored_at": "2026-11-19T00:22:41Z",
      "size": 11,
      "kind": "task",
      "state": "undelivered",
      "delivery_count": null,
      "ack_time": null
    },
[... 30 more stdout lines omitted from this document]
--- stderr
```

Marks: none (JSON, for agents; not marked); stdout trimmed to 40 of 70 lines

**Case 8 (ii_missing): address with no record**

```text
$ mashctl inbox inspect acme.team.zed
[exit 1]
--- stdout
--- stderr
mash: no endpoint acme.team.zed in the directory
```

Marks: none

**Case 9 (ii_bad): not an address**

```text
$ mashctl inbox inspect carol
[exit 2]
--- stdout
--- stderr
mashctl: inbox inspect: not an endpoint address (the value is not shown): give owner.group.instance
usage: mashctl inbox inspect <owner.group.instance> [--json] [--from SEQ] [--to SEQ] [--limit N]
  address: owner.group.instance, or its flat key owner_group_instance
```

Marks: P5, P6; 2 line(s) over 80 columns, first: `mashctl: inbox inspect: not an endpoint address (the value is not show...`; jargon: flat key

**Case 10 (ii_noinbox): endpoint dry-run only (no record)**

```text
$ mashctl inbox inspect acme.team.dave
[exit 1]
--- stdout
--- stderr
mash: no endpoint acme.team.dave in the directory
```

Marks: none

Breaks:

- P2 (one address printed in two or three forms on one line): cases 5, 6.
- P3 (table or listing not aligned, or no headers): cases 3.
- P4 (a `next:` or `Next:` line is present): cases 3.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 6, 9.
- P6 (jargon words): cases 1 (kind), 2 (kind), 3 (flat key), 4 (flat key), 5 (kind), 6 (kind), 9 (flat key).
- P7 (output meant for an agent, shown to a human): cases 5, 6.
- P8 (field repeated on one screen): cases 5, 6.
- P9 (information not asked for, which belongs behind --detail): cases 5, 6.
- P1: inbox inspect prints no preamble line.
- P2/P8: `inbox acme.team.carol: stream inbox-acme_team_carol, ...` prints the address and the stream name (which holds the flat key); every row of the table prints the sender as the flat key `acme_team_alice` (the same owner and group on each row, in the third form).
- P7/P9: the two lines above the table (`inbox ...: stream inbox-acme_team_carol, first_seq 1, last_seq 3, messages 3, bytes 1992` and `consumer deliver: ack_floor stream 0 consumer 0, delivered stream 0 consumer 0, num_pending 3, num_redelivered 0, num_ack_pending 0`) are JetStream counters for an agent or a test; the docs say so (`so a test can prove what the transport kept`). The MSG-ID column has a 32-character hash; the STATE column has `undelivered`.
- P3: the table has a header and aligned columns (`SEQ  MSG-ID ... STATE`); the two lines above it have no header and are comma separated.
- P5: the consumer line is 131 columns and the two `note:` lines are 128 and 228 columns.
- P6: `consumer`, `deliver`, `ack_floor`, `num_redelivered`, `stream`, `kind` (header `KIND` holds the message kind task or question) are not defined in the output; the `note:` text uses `JetStream`, `acknowledged`, `ack floor`.
- bare_inbox, bare_inbox_nosub: `mashctl: inbox: the sub-verb is inspect` then the usage and `address: owner.group.instance, or its flat key owner_group_instance`; on stderr, exit 2.
- ii_missing, ii_noinbox: `mash: no endpoint acme.team.zed in the directory`; the prefix is `mash:` and not `mashctl:`, exit 1.


## init

**Case 1 (help_init): --help**

```text
$ mashctl init --help
[exit 0]
--- stdout
usage:
  mashctl init                                 create the directory and lease buckets (or raise their caps to the configured limits)
  mashctl init --local [--dir D] [--owner O] [--port P] [--start] write the configuration of a local operator-mode NATS server with JetStream (loopback, TLS only), keys and credentials, for one developer; --start also starts it and creates the buckets
--- stderr
```

Marks: P3, P5, P6; 2 line(s) over 80 columns, first: `  mashctl init                                 create the directory an...`; jargon: bucket, cap, lease

**Case 2 (init_local_start): init --local --start (first command of the catalog; creates the server)**

```text
$ mashctl init --local --owner acme --port 20001 --start
[exit 0]
--- stdout
config dir: /tmp/mc/cfg (set MASH_CONFIG_DIR to change)
mashctl: recorded the operator JWT in /tmp/mc/cfg/keys/operator.jwt (mashctl accepts account JWTs issued by the keys it declares)
mashctl: local NATS server configured in /tmp/mc/cfg/server
  server config   /tmp/mc/cfg/server/server.conf (listens on 127.0.0.1:20001, loopback only, TLS)
  server cert     /tmp/mc/cfg/server/server.pem, signed by /tmp/mc/cfg/server/ca.pem (valid 10 years)
  admin creds     /tmp/mc/cfg/server/mashadmin.creds
  system creds    /tmp/mc/cfg/server/sys.creds
  signing keys    /tmp/mc/cfg/keys (operator.nk, system.nk, account.nk; never copy them off this machine)
  config          /tmp/mc/cfg/config.json
  server          started, pid 63205 (log /tmp/mc/cfg/server/server.log)
mashctl: directory and lease buckets ready

Ready: the local server is up at tls://127.0.0.1:20001. Create an endpoint with: mashctl endpoint create acme/dev/design --make-key   (it writes the creds file; to make the key on the endpoint's machine instead, see: mashctl endpoint create --help)
--- stderr
```

Marks: P4, P5, P6, P8, P9; 5 line(s) over 80 columns, first: `mashctl: recorded the operator JWT in /tmp/mc/cfg/keys/operator.jwt (m...`; jargon: JWT, bucket, creds, lease

**Case 3 (init_after): init, buckets only**

```text
$ mashctl init
[exit 0]
--- stdout
mashctl: directory and lease buckets ready
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: bucket, creds, lease

**Case 4 (init_extra): extra argument**

```text
$ mashctl init extra
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl init   |   mashctl init --local [--dir D] [--owner O] [--port P] [--start]
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 5 (init_flag): unknown option**

```text
$ mashctl init --frob
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl init   |   mashctl init --local [--dir D] [--owner O] [--port P] [--start]
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 3, 4, 5.
- P3 (table or listing not aligned, or no headers): cases 1.
- P4 (a `next:` or `Next:` line is present): cases 2.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5.
- P6 (jargon words): cases 1 (bucket, cap, lease), 2 (JWT, bucket, creds, lease), 3 (bucket, creds, lease), 4 (creds), 5 (creds).
- P8 (field repeated on one screen): cases 2.
- P9 (information not asked for, which belongs behind --detail): cases 2.
- P1: `mashctl init` (init_after) prints the preamble on stderr before `mashctl: directory and lease buckets ready`; init_local_start prints neither the preamble nor an error, and ends with a `Ready:` line.
- P4 (init_local_start): the last line `Ready: the local server is up at tls://127.0.0.1:20001. Create an endpoint with: mashctl endpoint create acme/dev/design --make-key   (it writes the creds file; to make the key on the endpoint's machine instead, see: mashctl endpoint create --help)` is 248 columns, comes after a blank line, and names the group `dev` in the example; the demo flow uses another group name (`team`), so the command in the line is not the one that follows. The next command is `mashctl endpoint create acme/GROUP/NAME --make-key`; the line gives a fixed example group.
- P8/P9 (init_local_start): the output prints the config dir on its first line and prints it again in 9 more lines (the paths of seven files, the key dir and the log); it lists every file it wrote (server.conf, server.pem, ca.pem, mashadmin.creds, sys.creds, key dir, config.json) and the PID and log path. The user asked for a server.
- P6: `operator JWT`, `signing keys`, `admin creds`, `system creds`, `directory and lease buckets`, `operator-mode`, `JetStream` are not defined.
- init_local_start: the line `mashctl: directory and lease buckets ready` is printed after init made five buckets (see Findings).
- init_extra, init_flag: usage on stderr, exit 2, with the preamble printed before the usage error (the preamble line is printed although the command takes no connection in that state).


## init --local

**Case 1 (il_nostart): without --start (third config dir, port 20003)**

```text
$ mashctl init --local --owner acme --port 20003
[exit 0]
--- stdout
config dir: /tmp/mc/cfg3 (set MASH_CONFIG_DIR to change)
mashctl: recorded the operator JWT in /tmp/mc/cfg3/keys/operator.jwt (mashctl accepts account JWTs issued by the keys it declares)
mashctl: local NATS server configured in /tmp/mc/cfg3/server
  server config   /tmp/mc/cfg3/server/server.conf (listens on 127.0.0.1:20003, loopback only, TLS)
  server cert     /tmp/mc/cfg3/server/server.pem, signed by /tmp/mc/cfg3/server/ca.pem (valid 10 years)
  admin creds     /tmp/mc/cfg3/server/mashadmin.creds
  system creds    /tmp/mc/cfg3/server/sys.creds
  signing keys    /tmp/mc/cfg3/keys (operator.nk, system.nk, account.nk; never copy them off this machine)
  config          /tmp/mc/cfg3/config.json

Next:
  1. start the server:      nats-server -c /tmp/mc/cfg3/server/server.conf
  2. create the buckets:    mashctl init
  3. create an endpoint:    mash key create --out design.seed; mashctl endpoint create acme/dev/design --public-key design.pub; mash credential create --seed design.seed --jwt acme.dev.design.jwt   (or, on this host: mashctl endpoint create acme/dev/design --make-key)
--- stderr
```

Marks: P4, P5, P6, P8, P9; 5 line(s) over 80 columns, first: `mashctl: recorded the operator JWT in /tmp/mc/cfg3/keys/operator.jwt (...`; jargon: JWT, bucket, creds, seed

**Case 2 (il_dir): --dir (fifth config dir)**

```text
$ mashctl init --local --owner acme --port 20005 --dir /tmp/mc/cfg5/srv
[exit 0]
--- stdout
config dir: /tmp/mc/cfg5 (set MASH_CONFIG_DIR to change)
mashctl: recorded the operator JWT in /tmp/mc/cfg5/keys/operator.jwt (mashctl accepts account JWTs issued by the keys it declares)
mashctl: local NATS server configured in /tmp/mc/cfg5/srv
  server config   /tmp/mc/cfg5/srv/server.conf (listens on 127.0.0.1:20005, loopback only, TLS)
  server cert     /tmp/mc/cfg5/srv/server.pem, signed by /tmp/mc/cfg5/srv/ca.pem (valid 10 years)
  admin creds     /tmp/mc/cfg5/srv/mashadmin.creds
  system creds    /tmp/mc/cfg5/srv/sys.creds
  signing keys    /tmp/mc/cfg5/keys (operator.nk, system.nk, account.nk; never copy them off this machine)
  config          /tmp/mc/cfg5/config.json

Next:
  1. start the server:      nats-server -c /tmp/mc/cfg5/srv/server.conf
  2. create the buckets:    mashctl init
  3. create an endpoint:    mash key create --out design.seed; mashctl endpoint create acme/dev/design --public-key design.pub; mash credential create --seed design.seed --jwt acme.dev.design.jwt   (or, on this host: mashctl endpoint create acme/dev/design --make-key)
--- stderr
```

Marks: P4, P5, P6, P8, P9; 5 line(s) over 80 columns, first: `mashctl: recorded the operator JWT in /tmp/mc/cfg5/keys/operator.jwt (...`; jargon: JWT, bucket, creds, seed

**Case 3 (il_portinuse): --start with the port in use (fourth config dir)**

```text
$ mashctl init --local --owner acme --port 20001 --start
[exit 1]
--- stdout
config dir: /tmp/mc/cfg4 (set MASH_CONFIG_DIR to change)
mashctl: recorded the operator JWT in /tmp/mc/cfg4/keys/operator.jwt (mashctl accepts account JWTs issued by the keys it declares)
mashctl: local NATS server configured in /tmp/mc/cfg4/server
  server config   /tmp/mc/cfg4/server/server.conf (listens on 127.0.0.1:20001, loopback only, TLS)
  server cert     /tmp/mc/cfg4/server/server.pem, signed by /tmp/mc/cfg4/server/ca.pem (valid 10 years)
  admin creds     /tmp/mc/cfg4/server/mashadmin.creds
  system creds    /tmp/mc/cfg4/server/sys.creds
  signing keys    /tmp/mc/cfg4/keys (operator.nk, system.nk, account.nk; never copy them off this machine)
  config          /tmp/mc/cfg4/config.json
--- stderr
mashctl: port 20001 is already in use: something already listens on 127.0.0.1:20001, so nothing was started (pick another with --port, or stop that server)
```

Marks: P5, P6; 5 line(s) over 80 columns, first: `mashctl: recorded the operator JWT in /tmp/mc/cfg4/keys/operator.jwt (...`; jargon: JWT, creds

**Case 4 (il_portinuse_again): second run into the fourth config dir**

```text
$ mashctl init --local --owner acme --port 20004
[exit 1]
--- stdout
config dir: /tmp/mc/cfg4 (set MASH_CONFIG_DIR to change)
--- stderr
mashctl: /tmp/mc/cfg4/server already holds server.conf, operator.jwt, mashadmin.creds, sys.creds, ca.pem, ca.key, server.pem, server.key, resolver, jetstream: refusing to overwrite. Remove them (or choose another --dir) to start over; nothing was changed
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `mashctl: /tmp/mc/cfg4/server already holds server.conf, operator.jwt, ...`; jargon: JWT, creds

**Case 5 (init_again_local): files already exist**

```text
$ mashctl init --local --owner acme --port 20001
[exit 1]
--- stdout
config dir: /tmp/mc/cfg (set MASH_CONFIG_DIR to change)
--- stderr
mashctl: /tmp/mc/cfg/server already holds server.conf, operator.jwt, mashadmin.creds, sys.creds, ca.pem, ca.key, server.pem, server.key, server.log, server.pid, resolver, jetstream: refusing to overwrite. Remove them (or choose another --dir) to start over; nothing was changed
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `mashctl: /tmp/mc/cfg/server already holds server.conf, operator.jwt, m...`; jargon: JWT, creds

**Case 6 (il_noowner): no owner**

```text
$ mashctl init --local --port 20005
[exit 1]
--- stdout
--- stderr
mashctl: init --local needs an owner: pass --owner O (or set "owner" in config.json)
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: init --local needs an owner: pass --owner O (or set "owner" i...`

**Case 7 (il_portword): port is not a number (fifth config dir)**

```text
$ mashctl init --local --owner acme --port abc
[exit 2]
--- stdout
--- stderr
usage: mashctl init --local [--dir D] [--owner O] [--port P] [--start]
  --start starts the new loopback-only, TLS-only server (127.0.0.1) and creates the buckets over TLS, trusting the CA init --local made; init --local without --start connects to nothing
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  --start starts the new loopback-only, TLS-only server (127.0.0.1) an...`; jargon: bucket

**Case 8 (ilx_flag): unknown option**

```text
$ mashctl init --local --frob
[exit 2]
--- stdout
--- stderr
usage: mashctl init --local [--dir D] [--owner O] [--port P] [--start]
  --start starts the new loopback-only, TLS-only server (127.0.0.1) and creates the buckets over TLS, trusting the CA init --local made; init --local without --start connects to nothing
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  --start starts the new loopback-only, TLS-only server (127.0.0.1) an...`; jargon: bucket

Breaks:

- P4 (a `next:` or `Next:` line is present): cases 1, 2.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 6, 7, 8.
- P6 (jargon words): cases 1 (JWT, bucket, creds, seed), 2 (JWT, bucket, creds, seed), 3 (JWT, creds), 4 (JWT, creds), 5 (JWT, creds), 7 (bucket), 8 (bucket).
- P8 (field repeated on one screen): cases 1, 2.
- P9 (information not asked for, which belongs behind --detail): cases 1, 2.
- P1: init --local prints no preamble.
- P4 (il_nostart, il_dir): the `Next:` block has three numbered lines (`1. start the server:      nats-server -c /tmp/mc/cfg3/server/server.conf`, `2. create the buckets:    mashctl init`, `3. create an endpoint:    mash key create --out design.seed; mashctl endpoint create acme/dev/design --public-key design.pub; mash credential create --seed design.seed ...`); the third line is 268 columns and uses the group `dev`.
- init_again_local: `mashctl: /tmp/mc/cfg/server already holds server.conf, operator.jwt, mashadmin.creds, sys.creds, ca.pem, ca.key, server.pem, server.key, server.log, server.pid, resolver, jetstream: refusing to overwrite. Remove them (or choose another --dir) to start over; nothing was changed` is one line of 277 columns that lists twelve names.
- il_portinuse: the config files are written before the port is checked; the output prints the nine lines of the written files and then `mashctl: port 20001 is already in use: something already listens on 127.0.0.1:20001, so nothing was started (pick another with --port, or stop that server)` on stderr, exit 1; the files stay and a second run into the same directory is refused (il_portinuse_again).
- il_portinuse_again: the second run is refused with `already holds ...: refusing to overwrite`; the list has ten names (the first run had not yet created `server.log` and `server.pid`).
- il_portword, ilx_flag: a usage error exits 2 and prints the usage block on stderr (see the cases for its length).


## issued

**Case 1 (help_issued): --help**

```text
$ mashctl issued --help
[exit 0]
--- stdout
usage:
  mashctl issued list [--json]                 the issue record: every user JWT mashctl issued (public key, address, kind, tags, time, purpose, revoked time)
  mashctl issued check [--json] [--sys-creds FILE] [--servers N] compare the user keys connected to every server with the record and report any key mash did not issue and any revoked key still connected; exit 1 on one, 2 when the check could not complete
--- stderr
```

Marks: P3, P5, P6; 2 line(s) over 80 columns, first: `  mashctl issued list [--json]                 the issue record: every...`; jargon: JWT, creds, issue record, kind

**Case 2 (bare_issued): no sub-verb**

```text
$ mashctl issued
[exit 2]
--- stdout
--- stderr
mashctl: issued: list or check
usage: mashctl issued list [--json]
       mashctl issued check [--json] [--sys-creds FILE] [--servers N]
```

Marks: P6; jargon: creds

**Case 3 (bare_issued_nosub): unknown sub-verb**

```text
$ mashctl issued frob
[exit 2]
--- stdout
--- stderr
mashctl: issued: list or check
usage: mashctl issued list [--json]
       mashctl issued check [--json] [--sys-creds FILE] [--servers N]
```

Marks: P6; jargon: creds

Breaks:

- P3 (table or listing not aligned, or no headers): cases 1.
- P5 (lines over 80 columns): cases 1.
- P6 (jargon words): cases 1 (JWT, creds, issue record, kind), 2 (creds), 3 (creds).
- P1: the `issued` verb prints no preamble for the usage errors.
- bare_issued, bare_issued_nosub: `mashctl: issued: list or check` then two usage lines on stderr, exit 2.


## issued check

**Case 1 (help_issued_check): --help**

```text
$ mashctl issued check --help
[exit 0]
--- stdout
usage:
  mashctl issued check [--json] [--sys-creds FILE] [--servers N] compare the user keys connected to every server with the record and report any key mash did not issue and any revoked key still connected; exit 1 on one, 2 when the check could not complete
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  mashctl issued check [--json] [--sys-creds FILE] [--servers N] compa...`; jargon: creds

**Case 2 (ic): success**

```text
$ mashctl issued check
[exit 0]
--- stdout
recorded but not connected (informational, 4):
  UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	endpoint	acme.team.carol
  UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ	endpoint	acme.team.alice
  UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN	endpoint	acme.team.alice2
  UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC	endpoint	acme.team.bob
summary: 1 connected, 0 unrecorded, 0 revoked but connected, 5 recorded (4 not connected), 1 of 1 servers answered
ok: every connected key was issued by mash and none is revoked
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json), system creds /tmp/mc/cfg/server/sys.creds (from the default location)
```

Marks: P1, P3, P5, P6, P7, P9; 6 line(s) over 80 columns, first: `  UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	endpoint	ac...`; jargon: creds

**Case 3 (ic_json): --json**

```text
$ mashctl issued check --json
[exit 0]
--- stdout
{
  "clean": true,
  "complete": true,
  "account": "AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6",
  "record_present": true,
  "servers": {
    "answered": [
      "mash-local"
    ],
    "missing": [],
    "expected": 1
  },
  "unrecorded": [],
  "revoked_connected": [],
  "recorded_not_connected": [
    {
      "public_key": "UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN",
      "address": "acme.team.carol",
      "owner": "acme",
      "group": "team",
      "instance": "carol",
      "kind": "endpoint"
    },
    {
      "public_key": "UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ",
      "address": "acme.team.alice",
      "owner": "acme",
      "group": "team",
      "instance": "alice",
      "kind": "endpoint"
    },
    {
      "public_key": "UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN",
      "address": "acme.team.alice2",
      "owner": "acme",
      "group": "team",
      "instance": "alice2",
      "kind": "endpoint"
    },
    {
[... 16 more stdout lines omitted from this document]
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json), system creds /tmp/mc/cfg/server/sys.creds (from the default location)
```

Marks: none (JSON, for agents; not marked); stdout trimmed to 40 of 56 lines

**Case 4 (ic_sys): --sys-creds**

```text
$ mashctl issued check --sys-creds /tmp/mc/cfg/server/sys.creds
[exit 0]
--- stdout
recorded but not connected (informational, 4):
  UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	endpoint	acme.team.carol
  UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ	endpoint	acme.team.alice
  UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN	endpoint	acme.team.alice2
  UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC	endpoint	acme.team.bob
summary: 1 connected, 0 unrecorded, 0 revoked but connected, 5 recorded (4 not connected), 1 of 1 servers answered
ok: every connected key was issued by mash and none is revoked
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json), system creds /tmp/mc/cfg/server/sys.creds (from --sys-creds)
```

Marks: P1, P3, P5, P6, P7, P9; 6 line(s) over 80 columns, first: `  UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	endpoint	ac...`; jargon: creds

**Case 5 (ic_servers): --servers 0**

```text
$ mashctl issued check --servers 0
[exit 2]
--- stdout
--- stderr
mashctl: issued check: --servers must be a whole number of servers, 1 or more, got "0"
usage: mashctl issued list [--json]
       mashctl issued check [--json] [--sys-creds FILE] [--servers N]
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `mashctl: issued check: --servers must be a whole number of servers, 1 ...`; jargon: creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2, 4.
- P3 (table or listing not aligned, or no headers): cases 2, 4.
- P5 (lines over 80 columns): cases 1, 2, 4, 5.
- P6 (jargon words): cases 1 (creds), 2 (creds), 4 (creds), 5 (creds).
- P7 (output meant for an agent, shown to a human): cases 2, 4.
- P9 (information not asked for, which belongs behind --detail): cases 2, 4.
- P1: the preamble is on stderr before the result.
- P3/P9: `recorded but not connected (informational, 4):` is followed by one row per endpoint with a 56-character public key, a kind word and the dotted address, separated by tabs, no header; four recorded endpoints are listed, none of which the user asked about.
- P5/P6: `summary: 1 connected, 0 unrecorded, 0 revoked but connected, 5 recorded (4 not connected), 1 of 1 servers answered` is 114 columns; `unrecorded`, `revoked but connected`, `recorded` are not defined; the last line is `ok: every connected key was issued by mash and none is revoked`.
- P4: no next step is printed; when the check finds a key it does not know, the next step is not in the output (not captured; see What was not captured).
- ic_servers: `--servers must be a whole number of servers, 1 or more, got "0"` exit 2 then the usage.


## issued list

**Case 1 (help_issued_list): --help**

```text
$ mashctl issued list --help
[exit 0]
--- stdout
usage:
  mashctl issued list [--json]                 the issue record: every user JWT mashctl issued (public key, address, kind, tags, time, purpose, revoked time)
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  mashctl issued list [--json]                 the issue record: every...`; jargon: JWT, issue record, kind

**Case 2 (il): success**

```text
$ mashctl issued list
[exit 0]
--- stdout
UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	endpoint	acme.team.carol	2026-11-19T00:21:50Z	generation 0	tags 	purpose issue
UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ	endpoint	acme.team.alice	2026-11-19T00:21:38Z	generation 0	tags 	purpose issue
UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN	endpoint	acme.team.alice2	2026-11-19T00:21:38Z	generation 0	tags 	purpose issue
UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC	endpoint	acme.team.bob	2026-11-19T00:21:39Z	generation 0	tags 	purpose issue
UBFOC5KRY5JRFS5QWS3K5GHKTEOVAXFWOQL35A2PYIIO3THDCMG7IQFX	admin	mashadmin	2026-11-19T00:21:30Z	generation 0	tags 	purpose issue
5 issued
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P3, P5, P6, P7, P9; 6 line(s) over 80 columns, first: `UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	endpoint	acme...`; jargon: creds

**Case 3 (il_json): --json**

```text
$ mashctl issued list --json
[exit 0]
--- stdout
{
  "present": true,
  "count": 5,
  "issued": [
    {
      "public_key": "UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN",
      "address": "acme.team.carol",
      "owner": "acme",
      "group": "team",
      "instance": "carol",
      "kind": "endpoint",
      "tags": [],
      "generation": 0,
      "issued_at": "2026-11-19T00:21:50Z",
      "purpose": "issue"
    },
    {
      "public_key": "UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ",
      "address": "acme.team.alice",
      "owner": "acme",
      "group": "team",
      "instance": "alice",
      "kind": "endpoint",
      "tags": [],
      "generation": 0,
      "issued_at": "2026-11-19T00:21:38Z",
      "purpose": "issue"
    },
    {
      "public_key": "UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN",
      "address": "acme.team.alice2",
      "owner": "acme",
      "group": "team",
      "instance": "alice2",
      "kind": "endpoint",
      "tags": [],
      "generation": 0,
      "issued_at": "2026-11-19T00:21:38Z",
      "purpose": "issue"
    },
[... 24 more stdout lines omitted from this document]
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: none (JSON, for agents; not marked); stdout trimmed to 40 of 64 lines

**Case 4 (il_flag): unknown option**

```text
$ mashctl issued list --frob
[exit 2]
--- stdout
--- stderr
mashctl: issued list: unexpected option "--frob"
usage: mashctl issued list [--json]
       mashctl issued check [--json] [--sys-creds FILE] [--servers N]
```

Marks: P6; jargon: creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2.
- P3 (table or listing not aligned, or no headers): cases 2.
- P5 (lines over 80 columns): cases 1, 2.
- P6 (jargon words): cases 1 (JWT, issue record, kind), 2 (creds), 4 (creds).
- P7 (output meant for an agent, shown to a human): cases 2.
- P9 (information not asked for, which belongs behind --detail): cases 2.
- P1: the preamble is on stderr before the result.
- P3/P7/P9: each row is the 56-character public key, the kind (`endpoint`, `admin`), the dotted address, the issue time, `generation 0`, `tags ` (empty) and `purpose issue`, tab separated, no header; the last line is `5 issued`. The admin user `mashadmin` is in the list.
- P6: `generation`, `tags`, `purpose` and `issued` use words that are not defined; `issued list` is described in --help as `the issue record: every user JWT mashctl issued`.
- il_flag: the usage error is exit 2 on stderr; the case name `il_flag` is the capture name only.


## label

**Case 1 (help_label): --help**

```text
$ mashctl label --help
[exit 0]
--- stdout
usage:
  mashctl label <owner.group.instance> k=v... [--dry-run] set labels on the endpoint's directory record
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  mashctl label <owner.group.instance> k=v... [--dry-run] set labels o...`; jargon: label

**Case 2 (label_set): set two labels**

```text
$ mashctl label acme.team.alice env=demo tier=2
[exit 0]
--- stdout
labeled acme.team.alice: env=demo tier=2
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6, P8; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 3 (label_dry): --dry-run**

```text
$ mashctl label acme.team.alice env=prod --dry-run
[exit 0]
--- stdout
dry run: would have labeled acme.team.alice (nothing written)
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 4 (label_same): label already set**

```text
$ mashctl label acme.team.alice env=demo
[exit 0]
--- stdout
acme.team.alice: nothing to change
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 5 (label_missing): endpoint has no record**

```text
$ mashctl label acme.team.zed env=demo
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: label acme.team.zed: no directory record for acme.team.zed
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds, label

**Case 6 (label_noargs): no labels**

```text
$ mashctl label acme.team.alice
[exit 2]
--- stdout
--- stderr
mashctl: label: want an address (owner.group.instance) and at least one label
usage: mashctl label <owner.group.instance> k=v [k=v]... [--dry-run]
       mashctl unlabel <owner.group.instance> k [k]... [--dry-run]
```

Marks: P6; jargon: label

**Case 7 (label_bad): bad label key**

```text
$ mashctl label acme.team.alice 'bad key=x'
[exit 2]
--- stdout
--- stderr
mashctl: label: label key in argument 1: a letter, then up to 31 of a-z 0-9 _ . - (the value is not shown)
usage: mashctl label <owner.group.instance> k=v [k=v]... [--dry-run]
       mashctl unlabel <owner.group.instance> k [k]... [--dry-run]
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `mashctl: label: label key in argument 1: a letter, then up to 31 of a-...`; jargon: label

**Case 8 (label_policy): removed option --policy**

```text
$ mashctl label acme.team.alice env=demo --policy x
[exit 2]
--- stdout
--- stderr
mashctl: label: unknown option "--policy"
usage: mashctl label <owner.group.instance> k=v [k=v]... [--dry-run]
       mashctl unlabel <owner.group.instance> k [k]... [--dry-run]
```

Marks: P6; jargon: label

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2, 3, 4, 5.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 7.
- P6 (jargon words): cases 1 (label), 2 (creds), 3 (creds), 4 (creds), 5 (creds, label), 6 (label), 7 (label), 8 (label).
- P8 (field repeated on one screen): cases 2.
- P1: the preamble is printed on stderr before each result.
- label_set: `labeled acme.team.alice: env=demo tier=2` prints the labels that were given, and does not say which were new; label_same prints `acme.team.alice: nothing to change`.
- label_dry: `dry run: would have labeled acme.team.alice (nothing written)` does not say what would change (the labels are not repeated).
- label_missing: `mashctl: label acme.team.zed: no directory record for acme.team.zed`; the address is printed twice in one line.
- label_noargs, label_bad, label_policy: usage on stderr, exit 2; label_bad prints `label key in argument 1: a letter, then up to 31 of a-z 0-9 _ . - (the value is not shown)`.
- P6: `label` is the word the command uses; `mashctl --help` calls them `labels on the endpoint's directory record`, and `--tool` sets the label `kind=tool`.


## limits

**Case 1 (help_limits): --help**

```text
$ mashctl limits --help
[exit 0]
--- stdout
usage:
  mashctl limits [address...]                  raise existing inboxes to the configured limits (config.json "limits"; never lowers)
  mashctl limits --show                        print the effective limits
--- stderr
```

Marks: P5; 1 line(s) over 80 columns, first: `  mashctl limits [address...]                  raise existing inboxes ...`

**Case 2 (limits_show): --show**

```text
$ mashctl limits --show
[exit 0]
--- stdout
inbox_max_bytes 16MiB
inbox_max_age 30d
directory_max_bytes 16MiB
lease_max_bytes 4MiB
inbox_max_consumers -1
--- stderr
```

Marks: P3

**Case 3 (limits_all): all inboxes, nothing to raise**

```text
$ mashctl limits
[exit 0]
--- stdout
acme_team_alice: unchanged
acme_team_alice2: unchanged
acme_team_bob: unchanged
acme_team_carol: unchanged
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 4 (limits_one): one address**

```text
$ mashctl limits acme.team.alice
[exit 0]
--- stdout
acme_team_alice: unchanged
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 5 (limits_raise): config.json limit raised to 32MiB (sixth config dir)**

```text
$ mashctl limits
[exit 0]
--- stdout
acme_team_alice: max_bytes 16777216 -> 33554432
acme_team_alice2: max_bytes 16777216 -> 33554432
acme_team_bob: max_bytes 16777216 -> 33554432
acme_team_carol: max_bytes 16777216 -> 33554432
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg6/config.json), owner acme (from /tmp/mc/cfg6/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg6/config.json)
```

Marks: P1, P5, P6, P7; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg6/config...`; jargon: creds

**Case 6 (limits_show_cfg): --show with the raised limit (sixth config dir)**

```text
$ mashctl limits --show
[exit 0]
--- stdout
inbox_max_bytes 32MiB
inbox_max_age 30d
directory_max_bytes 16MiB
lease_max_bytes 4MiB
inbox_max_consumers -1
--- stderr
```

Marks: P3

**Case 7 (limits_bad): not an address**

```text
$ mashctl limits nobody
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl limits [address...] | mashctl limits --show
mash: not an endpoint address (the value is not shown): give owner.group.instance
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 3, 4, 5, 7.
- P3 (table or listing not aligned, or no headers): cases 2, 6.
- P5 (lines over 80 columns): cases 1, 3, 4, 5, 7.
- P6 (jargon words): cases 3 (creds), 4 (creds), 5 (creds), 7 (creds).
- P7 (output meant for an agent, shown to a human): cases 5.
- P1: limits prints the preamble on stderr when it connects (limits_all, limits_one, limits_raise, limits_bad) and none for `--show`, which does not connect.
- P3: `--show` prints `inbox_max_bytes 16MiB` pairs with a single space; the names are config keys, the values are in different units (`16MiB`, `30d`, `-1`); no header, not aligned.
- P6/P7: `inbox_max_consumers -1` and `directory_max_bytes` are config.json names; `acme_team_alice: max_bytes 16777216 -> 33554432` (limits_raise) prints bytes and the flat key, with no unit and without the dotted address.
- limits_all, limits_one: `acme_team_alice: unchanged` per inbox; one line per endpoint for every endpoint, whether it changed or not.
- limits_bad: stderr `usage: mashctl limits [address...] | mashctl limits --show` then `mash: not an endpoint address (the value is not shown): give owner.group.instance`; the usage line is first and the cause is second; the prefix is `mash:`.


## list

**Case 1 (help_list): --help**

```text
$ mashctl list --help
[exit 0]
--- stdout
usage:
  mashctl list
--- stderr
```

Marks: none

**Case 2 (list_old): list**

```text
$ mashctl list
[exit 0]
--- stdout
acme.team.alice	UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7KOQ	2026-11-19T00:21:38Z	
acme.team.alice2	UAN2NA5ADWTGG3IRSRGP6QOOCYL5LDT3C6Z5OFAQTURU2TOB3AT7ZIZN	2026-11-19T00:21:38Z	
acme.team.bob	UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC	2026-11-19T00:21:39Z	
acme.team.carol	UAEXLQVHY4P37VJ5YUSI5PF3K3ZVMAXVW5BXYKAQA5NPCDRPHDTPN6ZN	2026-11-19T00:21:50Z	demo tool
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P3, P5, P6, P7, P9; 5 line(s) over 80 columns, first: `acme.team.alice	UAGT4ZQXALANXPHIAET7WXOZQRCNHQWDUU2UMRFXF23KU6FJQVUH7K...`; jargon: creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2.
- P3 (table or listing not aligned, or no headers): cases 2.
- P5 (lines over 80 columns): cases 2.
- P6 (jargon words): cases 2 (creds).
- P7 (output meant for an agent, shown to a human): cases 2.
- P9 (information not asked for, which belongs behind --detail): cases 2.
- P1: the preamble is printed on stderr.
- P3/P7: each row is the dotted address, the 56-character public key, the creation time and the note, tab separated, with no header and a trailing tab when the note is empty. `mashctl list` and `mashctl endpoint list` print different columns for the same directory; `list` is in the usage as `mashctl list` with no description.
- help_list: `mashctl list` has no description in `--help`.


## permissions

**Case 1 (help_permissions): --help**

```text
$ mashctl permissions --help
[exit 0]
--- stdout
usage:
  mashctl permissions <owner.group.instance> [--nsc] the NATS permissions an endpoint user needs
--- stderr
```

Marks: P5; 1 line(s) over 80 columns, first: `  mashctl permissions <owner.group.instance> [--nsc] the NATS permissi...`

**Case 2 (perm): text**

```text
$ mashctl permissions acme.team.alice
[exit 0]
--- stdout
{
  "pub": {
    "allow": [
      "agents.mail.acme.*.*",
      "agents.prompt.acme.*.*",
      "agents.status.acme.*.*",
      "agents.hb.acme.team.alice",
      "_INBOX.acme.\u003e",
      "$SRV.\u003e",
      "$JS.API.INFO",
      "$JS.API.STREAM.INFO.inbox-acme_team_alice",
      "$JS.API.CONSUMER.INFO.inbox-acme_team_alice.deliver",
      "$JS.API.CONSUMER.MSG.NEXT.inbox-acme_team_alice.deliver",
      "$JS.ACK.inbox-acme_team_alice.deliver.\u003e",
      "$JS.API.DIRECT.GET.inbox-acme_team_alice",
      "$JS.API.DIRECT.GET.inbox-acme_team_alice.\u003e",
      "$JS.API.STREAM.INFO.KV_mash-directory",
      "$JS.API.DIRECT.GET.KV_mash-directory.\u003e",
      "$JS.API.STREAM.INFO.KV_mash-leases",
      "$KV.mash-leases.acme_team_alice",
      "$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases.acme_team_alice"
    ]
  },
  "sub": {
    "allow": [
      "_INBOX.acme.acme_team_alice.\u003e",
      "agents.prompt.acme.team.alice",
      "agents.status.acme.team.alice",
      "$SRV.\u003e",
      "agents.hb.acme.*.*"
    ]
  }
}
--- stderr
note: these are the permissions of the endpoint's NATS user: the owner-line set for its address.
```

Marks: P5, P6, P7, P9; 1 line(s) over 80 columns, first: `note: these are the permissions of the endpoint's NATS user: the owner...`; jargon: lease

**Case 3 (perm_nsc): --nsc**

```text
$ mashctl permissions acme.team.alice --nsc
[exit 0]
--- stdout
--allow-pub
agents.mail.acme.*.*
--allow-pub
agents.prompt.acme.*.*
--allow-pub
agents.status.acme.*.*
--allow-pub
agents.hb.acme.team.alice
--allow-pub
_INBOX.acme.>
--allow-pub
$SRV.>
--allow-pub
$JS.API.INFO
--allow-pub
$JS.API.STREAM.INFO.inbox-acme_team_alice
--allow-pub
$JS.API.CONSUMER.INFO.inbox-acme_team_alice.deliver
--allow-pub
$JS.API.CONSUMER.MSG.NEXT.inbox-acme_team_alice.deliver
--allow-pub
$JS.ACK.inbox-acme_team_alice.deliver.>
--allow-pub
$JS.API.DIRECT.GET.inbox-acme_team_alice
--allow-pub
$JS.API.DIRECT.GET.inbox-acme_team_alice.>
--allow-pub
$JS.API.STREAM.INFO.KV_mash-directory
--allow-pub
$JS.API.DIRECT.GET.KV_mash-directory.>
--allow-pub
$JS.API.STREAM.INFO.KV_mash-leases
--allow-pub
$KV.mash-leases.acme_team_alice
--allow-pub
$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases.acme_team_alice
--allow-sub
_INBOX.acme.acme_team_alice.>
--allow-sub
agents.prompt.acme.team.alice
[... 6 more stdout lines omitted from this document]
--- stderr
note: these are the permissions of the endpoint's NATS user: the owner-line set for its address.
```

Marks: P3, P5, P6, P7, P9; 1 line(s) over 80 columns, first: `note: these are the permissions of the endpoint's NATS user: the owner...`; jargon: lease; stdout trimmed to 40 of 46 lines

**Case 4 (perm_noarg): no arguments**

```text
$ mashctl permissions
[exit 2]
--- stdout
--- stderr
usage: mashctl permissions <owner.group.instance> [--nsc]
```

Marks: none

**Case 5 (perm_bare): not an address**

```text
$ mashctl permissions alice
[exit 2]
--- stdout
--- stderr
mash: not an endpoint address (the value is not shown): give owner.group.instance
```

Marks: P5; 1 line(s) over 80 columns, first: `mash: not an endpoint address (the value is not shown): give owner.gro...`

Breaks:

- P3 (table or listing not aligned, or no headers): cases 3.
- P5 (lines over 80 columns): cases 1, 2, 3, 5.
- P6 (jargon words): cases 2 (lease), 3 (lease).
- P7 (output meant for an agent, shown to a human): cases 2, 3.
- P9 (information not asked for, which belongs behind --detail): cases 2, 3.
- P1: permissions prints no preamble; it needs `config.json` only for the owner and does not connect (down_perm shows it works with the server stopped).
- P7: perm prints the permission set as JSON on stdout with no `--json` flag (`>` for `>` in the strings); perm_nsc prints the same set as alternating lines `--allow-pub` and the subject (46 lines). Both are input for `nsc` or for an agent.
- P6: `pub`, `allow`, `$JS.API.DIRECT.GET.KV_mash-directory.>`, `$SRV.>`, `_INBOX.acme.>`, `agents.hb`, `lease` (`KV_mash-leases`) are subject names; the stderr line `note: these are the permissions of the endpoint's NATS user: the owner-line set for its address.` uses `owner-line set`, which is not defined.
- perm_noarg, perm_bare: usage exit 2; perm_bare prints `mash: not an endpoint address (the value is not shown): give owner.group.instance`.


## provision

**Case 1 (help_provision): --help**

```text
$ mashctl provision --help
[exit 0]
--- stdout
usage:
  mashctl provision <owner.group.instance> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]...
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  mashctl provision <owner.group.instance> --user U --account A [--not...`; jargon: cap, label

**Case 2 (prov): success**

```text
$ mashctl provision acme.team.ivy --user UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS --account AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6 --note by hand --role r --label a=b
[exit 0]
--- stdout
provisioned acme.team.ivy: inbox inbox-acme_team_ivy, mail agents.mail.acme.team.ivy
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P2, P5, P6, P8, P9; 2 line(s) over 80 columns, first: `provisioned acme.team.ivy: inbox inbox-acme_team_ivy, mail agents.mail...`; jargon: creds

**Case 3 (prov_again): already provisioned**

```text
$ mashctl provision acme.team.ivy --user UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS --account AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mash: endpoint acme.team.ivy is already provisioned
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 4 (prov_noargs): missing flags**

```text
$ mashctl provision acme.team.ivy
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl provision <owner.group.instance> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]...
--user is required
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: cap, creds, label

**Case 5 (prov_baduser): bad --user**

```text
$ mashctl provision acme.team.kim --user notakey --account AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl provision <owner.group.instance> --user U --account A [--note N] [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]...
--user is not a NATS user public key (U, then 55 upper-case base32 characters; the value is not shown)
```

Marks: P1, P5, P6; 3 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: cap, creds, label

**Case 6 (prov_kim): success, second endpoint**

```text
$ mashctl provision acme.team.kim --user UAM42OQT4W7JIRTKCAWVKJM3TI2EOIXZNSP53RMGK45BTQUMBRI7IGM7 --account AD7BK6VI6EGIDZ2XXCLTKUBGR5NOIJYVZ77DTRETXNW2XXPNQTK2UGO6
[exit 0]
--- stdout
provisioned acme.team.kim: inbox inbox-acme_team_kim, mail agents.mail.acme.team.kim
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P2, P5, P6; 2 line(s) over 80 columns, first: `provisioned acme.team.kim: inbox inbox-acme_team_kim, mail agents.mail...`; jargon: creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2, 3, 4, 5, 6.
- P2 (one address printed in two or three forms on one line): cases 2, 6.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 6.
- P6 (jargon words): cases 1 (cap, label), 2 (creds), 3 (creds), 4 (cap, creds, label), 5 (cap, creds, label), 6 (creds).
- P8 (field repeated on one screen): cases 2.
- P9 (information not asked for, which belongs behind --detail): cases 2.
- P1: the preamble is on stderr in every case that connects, including the usage errors (prov_noargs, prov_baduser).
- P2: `provisioned acme.team.ivy: inbox inbox-acme_team_ivy, mail agents.mail.acme.team.ivy` prints the address, the flat key (inside the inbox name) and the mail subject in one line.
- P6: `--user`, `--account` (a public key of the account), `inbox`, `mail subject` are not defined; `provision` writes a directory record only.
- prov_again: `mash: endpoint acme.team.ivy is already provisioned` exit 1; prov_baduser prints the usage, then `--user is not a NATS user public key (U, then 55 upper-case base32 characters; the value is not shown)`.
- The command `mashctl provision` is in the usage of 0.5.5 although `endpoint create` replaced it for people (docs/mashctl.md says `mashctl provision` writes only a directory record, for a user you signed yourself).


## resume

**Case 1 (help_resume): --help**

```text
$ mashctl resume --help
[exit 0]
--- stdout
usage:
  mashctl resume <owner.group.instance>        undo suspend
--- stderr
```

Marks: none

**Case 2 (resume): suspended endpoint**

```text
$ mashctl resume acme.team.bob
[exit 0]
--- stdout
resumed acme.team.bob
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6, P8; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 3 (resume_again): endpoint that is not suspended**

```text
$ mashctl resume acme.team.bob
[exit 0]
--- stdout
resumed acme.team.bob
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2, 3.
- P5 (lines over 80 columns): cases 2, 3.
- P6 (jargon words): cases 2 (creds), 3 (creds).
- P8 (field repeated on one screen): cases 2.
- P1: the preamble is on stderr.
- resume_again: `resumed acme.team.bob` is printed for an endpoint that was not suspended (exit 0); the output is the same as for the first resume.


## set-meta

**Case 1 (help_set-meta): --help**

```text
$ mashctl set-meta --help
[exit 0]
--- stdout
usage:
  mashctl set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]... (a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  mashctl set-meta <owner.group.instance> [--role R] [--host H] [--lab...`; jargon: cap, label

**Case 2 (sm_show): set role, host, description, label and cap**

```text
$ mashctl set-meta acme.team.alice --role designer --host laptop --describe "makes designs" --label tier=1 --cap read
[exit 0]
--- stdout
updated acme.team.alice: role "designer", host "laptop", labels tier=1, description "makes designs", caps read
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6, P8, P9; 2 line(s) over 80 columns, first: `updated acme.team.alice: role "designer", host "laptop", labels tier=1...`; jargon: cap, creds, label

**Case 3 (sm_clear): clear role and a label with -**

```text
$ mashctl set-meta acme.team.alice --role - --label tier=-
[exit 0]
--- stdout
updated acme.team.alice: role "", host "laptop", labels none, description "makes designs", caps read
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6, P8, P9; 2 line(s) over 80 columns, first: `updated acme.team.alice: role "", host "laptop", labels none, descript...`; jargon: cap, creds, label

**Case 4 (sm_noflags): no changes named**

```text
$ mashctl set-meta acme.team.alice
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]...   (at least one; a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: cap, creds, label

**Case 5 (sm_missing): endpoint not provisioned**

```text
$ mashctl set-meta acme.team.zed --role x
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mash: endpoint acme_team_zed is not provisioned
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 6 (sm_bad): not an address**

```text
$ mashctl set-meta alice --role x
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl set-meta <owner.group.instance> [--role R] [--host H] [--label k=v]... [--describe TEXT] [--cap C]... [--uncap C]...   (at least one; a value of - clears the field; --label k=- clears that label; --cap C adds a capability, --uncap C removes one, --cap - clears them all)
mash: not an endpoint address (the value is not shown): give owner.group.instance
```

Marks: P1, P5, P6; 3 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: cap, creds, label

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2, 3, 4, 5, 6.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 6.
- P6 (jargon words): cases 1 (cap, label), 2 (cap, creds, label), 3 (cap, creds, label), 4 (cap, creds, label), 5 (creds), 6 (cap, creds, label).
- P8 (field repeated on one screen): cases 2, 3.
- P9 (information not asked for, which belongs behind --detail): cases 2, 3.
- P1: the preamble is on stderr in every case that connects, including the usage errors that run after the argument parse.
- P9: `updated acme.team.alice: role "designer", host "laptop", labels tier=1, description "makes designs", caps read` prints every field of the record, including those not changed; sm_clear changed two fields and prints all five.
- P6: `role`, `host`, `caps`, `labels`, `description` have no definition; `--cap read` is accepted without a check that `read` is a capability (sm_show).
- sm_missing: `mash: endpoint acme_team_zed is not provisioned` prints the flat key in an address context; sm_bad prints the usage and then `mash: not an endpoint address ...`.
- `set-meta --label tier=1` sets a label on the record; `label` and `unlabel` do the same job (see label).


## suspend

**Case 1 (help_suspend): --help**

```text
$ mashctl suspend --help
[exit 0]
--- stdout
usage:
  mashctl suspend <owner.group.instance>       refuse all inbound mail to an endpoint (its stored mail stays readable); prints the last sequence; the endpoint's credentials stay valid
--- stderr
```

Marks: P5; 1 line(s) over 80 columns, first: `  mashctl suspend <owner.group.instance>       refuse all inbound mail...`

**Case 2 (suspend): success**

```text
$ mashctl suspend acme.team.bob
[exit 0]
--- stdout
suspended acme.team.bob: id 63afd15649fb5dee9904fbba687fbe7c, last_seq 0, msgs 0 (new mail parked; the endpoint's credentials are still valid)
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6, P8; 2 line(s) over 80 columns, first: `suspended acme.team.bob: id 63afd15649fb5dee9904fbba687fbe7c, last_seq...`; jargon: creds

**Case 3 (suspend_again): already suspended**

```text
$ mashctl suspend acme.team.bob
[exit 0]
--- stdout
suspended acme.team.bob: id 63afd15649fb5dee9904fbba687fbe7c, last_seq 0, msgs 0 (new mail parked; the endpoint's credentials are still valid)
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6, P8; 2 line(s) over 80 columns, first: `suspended acme.team.bob: id 63afd15649fb5dee9904fbba687fbe7c, last_seq...`; jargon: creds

**Case 4 (suspend_missing): no inbox**

```text
$ mashctl suspend acme.team.zed
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mash: endpoint acme_team_zed has no inbox stream
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 5 (susp_extra): extra argument**

```text
$ mashctl suspend acme.team.alice extra
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl suspend <owner.group.instance>
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2, 3, 4, 5.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5.
- P6 (jargon words): cases 2 (creds), 3 (creds), 4 (creds), 5 (creds).
- P8 (field repeated on one screen): cases 2, 3.
- P1: the preamble is on stderr in every case that connects.
- P6/P9: `suspended acme.team.bob: id 63afd15649fb5dee9904fbba687fbe7c, last_seq 0, msgs 0 (new mail parked; the endpoint's credentials are still valid)` is 142 columns; `id`, `last_seq` and `parked` are not defined; the id is the token `deprovision --suspension` needs, and nothing in the line says so.
- suspend_again: a second suspend prints the same id and exit 0.
- suspend_missing: `mash: endpoint acme_team_zed has no inbox stream` exit 1; the address is given as the flat key.


## suspended

**Case 1 (help_suspended): --help**

```text
$ mashctl suspended --help
[exit 0]
--- stdout
usage:
  mashctl suspended <owner.group.instance>     print whether an endpoint is suspended and its last sequence; exit 0 if suspended, 3 if not, 1 on error
--- stderr
```

Marks: P5; 1 line(s) over 80 columns, first: `  mashctl suspended <owner.group.instance>     print whether an endpoi...`

**Case 2 (suspended_not): not suspended (exit 3)**

```text
$ mashctl suspended acme.team.bob
[exit 3]
--- stdout
acme.team.bob is not suspended
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 3 (suspended_yes): suspended (exit 0)**

```text
$ mashctl suspended acme.team.bob
[exit 0]
--- stdout
suspended acme.team.bob since 2026-11-19T00:22:17Z: id 63afd15649fb5dee9904fbba687fbe7c, last_seq 0, msgs 0
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6, P8; 2 line(s) over 80 columns, first: `suspended acme.team.bob since 2026-11-19T00:22:17Z: id 63afd15649fb5de...`; jargon: creds

**Case 4 (suspended_missing): no inbox (exit 1)**

```text
$ mashctl suspended acme.team.zed
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mash: endpoint acme_team_zed has no inbox stream
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 5 (suspended_noarg): no arguments**

```text
$ mashctl suspended
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
usage: mashctl suspended <owner.group.instance>
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2, 3, 4, 5.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5.
- P6 (jargon words): cases 2 (creds), 3 (creds), 4 (creds), 5 (creds).
- P8 (field repeated on one screen): cases 3.
- P1: the preamble is on stderr in every case that connects.
- Exit codes: suspended_yes exits 0, suspended_not exits 3 with `acme.team.bob is not suspended` on stdout, suspended_missing exits 1; the last has the flat key in `mash: endpoint acme_team_zed has no inbox stream`.
- suspended_yes: `suspended acme.team.bob since 2026-11-19T00:22:17Z: id 63afd..., last_seq 0, msgs 0` is 107 columns.


## unlabel

**Case 1 (help_unlabel): --help**

```text
$ mashctl unlabel --help
[exit 0]
--- stdout
usage:
  mashctl unlabel <owner.group.instance> k... [--dry-run] remove labels from the endpoint's directory record
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `  mashctl unlabel <owner.group.instance> k... [--dry-run] remove label...`; jargon: label

**Case 2 (unlabel_set): remove a label**

```text
$ mashctl unlabel acme.team.alice tier
[exit 0]
--- stdout
unlabeled acme.team.alice: -tier
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6, P8; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 3 (unlabel_dry): --dry-run**

```text
$ mashctl unlabel acme.team.alice env --dry-run
[exit 0]
--- stdout
dry run: would have unlabeled acme.team.alice (nothing written)
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 4 (unlabel_notset): label not set**

```text
$ mashctl unlabel acme.team.alice nothere
[exit 0]
--- stdout
acme.team.alice: nothing to change
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: unlabel acme.team.alice: label nothere is not set
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds, label

**Case 5 (unlabel_noargs): no labels**

```text
$ mashctl unlabel acme.team.alice
[exit 2]
--- stdout
--- stderr
mashctl: unlabel: want an address (owner.group.instance) and at least one label
usage: mashctl label <owner.group.instance> k=v [k=v]... [--dry-run]
       mashctl unlabel <owner.group.instance> k [k]... [--dry-run]
```

Marks: P6; jargon: label

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 2, 3, 4.
- P5 (lines over 80 columns): cases 1, 2, 3, 4.
- P6 (jargon words): cases 1 (label), 2 (creds), 3 (creds), 4 (creds, label), 5 (label).
- P8 (field repeated on one screen): cases 2.
- P1: the preamble is on stderr in every case that connects.
- unlabel_set: `unlabeled acme.team.alice: -tier` prints the removed label with a minus sign; unlabel_notset prints `acme.team.alice: nothing to change` on stdout and `mashctl: unlabel acme.team.alice: label nothere is not set` on stderr, exit 0, so the same event is reported on two streams in two ways.
- unlabel_dry: `dry run: would have unlabeled acme.team.alice (nothing written)` does not say what would change.


## Target, connection and configuration errors (all server verbs)

**Case 1 (env_plain_list): endpoint list with MASH_URL=nats://127.0.0.1:20001**

```text
$ mashctl endpoint list
[exit 2]
--- stdout
--- stderr
mashctl: target server nats://127.0.0.1:20001 (from MASH_URL), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint list: mashctl: mash: refusing a plain nats:// connection to "127.0.0.1": every mash connection requires TLS, to any host (loopback included); set insecure_plaintext (MASH_INSECURE_PLAINTEXT=1) to allow plain text; the fix is to use a tls:// URL with the server's certificate (tls_ca for a private CA)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server nats://127.0.0.1:20001 (from MASH_URL), owner a...`; jargon: creds

**Case 2 (env_plain_init): init with MASH_URL=nats://127.0.0.1:20001**

```text
$ mashctl init
[exit 2]
--- stdout
--- stderr
mashctl: target server nats://127.0.0.1:20001 (from MASH_URL), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: mash: refusing a plain nats:// connection to "127.0.0.1": every mash connection requires TLS, to any host (loopback included); set insecure_plaintext (MASH_INSECURE_PLAINTEXT=1) to allow plain text; the fix is to use a tls:// URL with the server's certificate (tls_ca for a private CA)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server nats://127.0.0.1:20001 (from MASH_URL), owner a...`; jargon: creds

**Case 3 (env_plain_create): endpoint create with MASH_URL=nats://127.0.0.1:20001**

```text
$ mashctl endpoint create acme/team/zoe --make-key
[exit 2]
--- stdout
--- stderr
mashctl: target server nats://127.0.0.1:20001 (from MASH_URL), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint create: error (server): mashctl: mash: refusing a plain nats:// connection to "127.0.0.1": every mash connection requires TLS, to any host (loopback included); set insecure_plaintext (MASH_INSECURE_PLAINTEXT=1) to allow plain text; the fix is to use a tls:// URL with the server's certificate (tls_ca for a private CA); nothing was changed, run the same command again once the server is reachable
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server nats://127.0.0.1:20001 (from MASH_URL), owner a...`; jargon: creds

**Case 4 (env_plain_check): issued check with MASH_URL=nats://127.0.0.1:20001**

```text
$ mashctl issued check
[exit 2]
--- stdout
--- stderr
mashctl: target server nats://127.0.0.1:20001 (from MASH_URL), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json), system creds /tmp/mc/cfg/server/sys.creds (from the default location)
mashctl: issued check: mashctl: mash: refusing a plain nats:// connection to "127.0.0.1": every mash connection requires TLS, to any host (loopback included); set insecure_plaintext (MASH_INSECURE_PLAINTEXT=1) to allow plain text; the fix is to use a tls:// URL with the server's certificate (tls_ca for a private CA)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server nats://127.0.0.1:20001 (from MASH_URL), owner a...`; jargon: creds

**Case 5 (env_plain_ndel): endpoint delete --dry-run with MASH_URL=nats://127.0.0.1:20001**

```text
$ mashctl endpoint delete acme/team/alice2 --dry-run
[exit 2]
--- stdout
--- stderr
mashctl: target server nats://127.0.0.1:20001 (from MASH_URL), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint delete acme/team/alice2: mashctl: mash: refusing a plain nats:// connection to "127.0.0.1": every mash connection requires TLS, to any host (loopback included); set insecure_plaintext (MASH_INSECURE_PLAINTEXT=1) to allow plain text; the fix is to use a tls:// URL with the server's certificate (tls_ca for a private CA)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server nats://127.0.0.1:20001 (from MASH_URL), owner a...`; jargon: creds

**Case 6 (env_owner): MASH_OWNER=other, outside a session**

```text
$ mashctl endpoint list
[exit 0]
--- stdout
acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T00:21:38Z, host "laptop", labels env=demo, caps read, description "makes designs"
acme/team/alice2  acme.team.alice2  acme_team_alice2  created 2026-11-19T00:21:38Z
acme/team/bob  acme.team.bob  acme_team_bob  created 2026-11-19T00:21:39Z
acme/team/carol  acme.team.carol  acme_team_carol  created 2026-11-19T00:21:50Z, note "demo tool", labels env=demo,kind=tool
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner other (from MASH_OWNER), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
4 endpoints
```

Marks: P1, P2, P5, P6; 4 line(s) over 80 columns, first: `acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T...`; jargon: cap, creds, kind, label

**Case 7 (env_owner_session): MASH_OWNER=other with MASH_NAME and MASH_GROUP set**

```text
$ mashctl endpoint list
[exit 0]
--- stdout
acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T00:21:38Z, host "laptop", labels env=demo, caps read, description "makes designs"
acme/team/alice2  acme.team.alice2  acme_team_alice2  created 2026-11-19T00:21:38Z
acme/team/bob  acme.team.bob  acme_team_bob  created 2026-11-19T00:21:39Z
acme/team/carol  acme.team.carol  acme_team_carol  created 2026-11-19T00:21:50Z, note "demo tool", labels env=demo,kind=tool
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner other (from MASH_OWNER), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
warning: MASH_OWNER (other) overrides config.json's owner (acme); mashctl is acting on other. These MASH_* variables belong to the endpoint session you may be inside; unset them to use config.json.
4 endpoints
```

Marks: P1, P2, P5, P6; 5 line(s) over 80 columns, first: `acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T...`; jargon: cap, creds, kind, label

**Case 8 (env_session): MASH_NAME and MASH_LAUNCH_TOKEN set**

```text
$ mashctl endpoint list
[exit 0]
--- stdout
acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T00:21:38Z, host "laptop", labels env=demo, caps read, description "makes designs"
acme/team/alice2  acme.team.alice2  acme_team_alice2  created 2026-11-19T00:21:38Z
acme/team/bob  acme.team.bob  acme_team_bob  created 2026-11-19T00:21:39Z
acme/team/carol  acme.team.carol  acme_team_carol  created 2026-11-19T00:21:50Z, note "demo tool", labels env=demo,kind=tool
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
4 endpoints
```

Marks: P1, P2, P5, P6; 4 line(s) over 80 columns, first: `acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T...`; jargon: cap, creds, kind, label

**Case 9 (env_url_session): MASH_URL=tls://localhost:20001 with MASH_NAME set**

```text
$ mashctl endpoint list
[exit 0]
--- stdout
acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T00:21:38Z, host "laptop", labels env=demo, caps read, description "makes designs"
acme/team/alice2  acme.team.alice2  acme_team_alice2  created 2026-11-19T00:21:38Z
acme/team/bob  acme.team.bob  acme_team_bob  created 2026-11-19T00:21:39Z
acme/team/carol  acme.team.carol  acme_team_carol  created 2026-11-19T00:21:50Z, note "demo tool", labels env=demo,kind=tool
--- stderr
mashctl: target server tls://localhost:20001 (from MASH_URL), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
warning: MASH_URL (tls://localhost:20001) overrides config.json's url (tls://127.0.0.1:20001); mashctl is acting on tls://localhost:20001. These MASH_* variables belong to the endpoint session you may be inside; unset them to use config.json.
warning: MASH_NAME or MASH_LAUNCH_TOKEN is set, so this shell looks like it is inside a mash session, and tls://localhost:20001 (MASH_URL) is that session's own server. To act on another server, run outside the session or with `env -u MASH_URL ...` (and the other MASH_* variables) and config.json.
4 endpoints
```

Marks: P1, P2, P5, P6; 6 line(s) over 80 columns, first: `acme/team/alice  acme.team.alice  acme_team_alice  created 2026-11-19T...`; jargon: cap, creds, kind, label

**Case 10 (noinit_list): endpoint list, empty config dir**

```text
$ mashctl endpoint list
[exit 6]
--- stdout
--- stderr
mashctl: target server unset, owner unset, admin creds unset
mashctl: endpoint list: mashctl: no owner (config.json "owner" or MASH_OWNER)
```

Marks: P1, P6; jargon: creds

**Case 11 (noinit_init): init, empty config dir**

```text
$ mashctl init
[exit 1]
--- stdout
--- stderr
mashctl: target server unset, owner unset, admin creds unset
mashctl: no owner (config.json "owner" or MASH_OWNER)
```

Marks: P1, P6; jargon: creds

**Case 12 (noinit_create): endpoint create, empty config dir**

```text
$ mashctl endpoint create acme/team/zoe --make-key
[exit 1]
--- stdout
--- stderr
mashctl: endpoint create: refused (no_account_key): mashctl: no account signing key: run `mashctl init` (run mashctl init first)
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: endpoint create: refused (no_account_key): mashctl: no accoun...`

**Case 13 (noinit_audit): audit, empty config dir**

```text
$ mashctl audit
[exit 3]
--- stdout
--- stderr
mashctl: no owner (config.json "owner" or MASH_OWNER)
```

Marks: none

**Case 14 (noinit_perm): permissions, empty config dir**

```text
$ mashctl permissions acme.team.alice
[exit 1]
--- stdout
--- stderr
mashctl: no owner (config.json "owner" or MASH_OWNER)
```

Marks: none

**Case 15 (noinit_limits): limits --show, empty config dir**

```text
$ mashctl limits --show
[exit 1]
--- stdout
--- stderr
mashctl: no owner (config.json "owner" or MASH_OWNER)
```

Marks: none

**Case 16 (creds_loose): admin creds file mode 0644**

```text
$ mashctl endpoint list
[exit 6]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg7/config.json), owner acme (from /tmp/mc/cfg7/config.json), admin creds /tmp/mc/cfg7/loose.creds (from /tmp/mc/cfg7/config.json)
mashctl: endpoint list: mash: credentials file /tmp/mc/cfg7/loose.creds has mode 0644: it is readable by other users. Run: chmod 600 /tmp/mc/cfg7/loose.creds
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg7/config...`; jargon: creds

**Case 17 (creds_none): config.json has no admin_creds**

```text
$ mashctl endpoint list
[exit 6]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg8/config.json), owner acme (from /tmp/mc/cfg8/config.json), admin creds unset
mashctl: endpoint list: mashctl: admin commands need MASH_URL and MASH_ADMIN_CREDS
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg8/config...`; jargon: creds

**Case 18 (creds_none_init): init, config.json has no admin_creds**

```text
$ mashctl init
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg8/config.json), owner acme (from /tmp/mc/cfg8/config.json), admin creds unset
mashctl: admin commands need MASH_URL and MASH_ADMIN_CREDS
```

Marks: P1, P5, P6; 1 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg8/config...`; jargon: creds

**Case 19 (creds_missing): admin_creds file does not exist**

```text
$ mashctl endpoint list
[exit 6]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg9/config.json), owner acme (from /tmp/mc/cfg9/config.json), admin creds /tmp/mc/cfg9/missing.creds (from /tmp/mc/cfg9/config.json)
mashctl: endpoint list: mashctl: admin credentials: open /tmp/mc/cfg9/missing.creds: no such file or directory
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg9/config...`; jargon: creds

**Case 20 (down_list): endpoint list, server stopped**

```text
$ mashctl endpoint list
[exit 6]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint list: mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 21 (down_init): init, server stopped**

```text
$ mashctl init
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 22 (down_create): endpoint create --make-key, server stopped**

```text
$ mashctl endpoint create acme/team/zoe --make-key
[exit 6]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint create: error (server): mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up); nothing was changed, run the same command again once the server is reachable
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 23 (down_create_dry): endpoint create --dry-run, server stopped**

```text
$ mashctl endpoint create acme/team/zoe --make-key --dry-run
[exit 6]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint create: error (server): mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up); nothing was changed, run the same command again once the server is reachable
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 24 (down_audit): audit, server stopped**

```text
$ mashctl audit
[exit 3]
--- stdout
--- stderr
mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: connect tls://127.0.0.1:20001: nats: no servers available for...`

**Case 25 (down_check): issued check, server stopped**

```text
$ mashctl issued check
[exit 2]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json), system creds /tmp/mc/cfg/server/sys.creds (from the default location)
mashctl: issued check: mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 26 (down_suspended): suspended, server stopped**

```text
$ mashctl suspended acme.team.alice
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 27 (down_delete): endpoint delete, server stopped**

```text
$ mashctl endpoint delete acme/team/alice
[exit 6]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint delete acme/team/alice: mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 28 (down_reissue): endpoint reissue, server stopped**

```text
$ mashctl endpoint reissue acme/team/alice --make-key
[exit 6]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: endpoint reissue: error (server): mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up); nothing was changed
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 29 (down_label): label, server stopped**

```text
$ mashctl label acme.team.alice a=b
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: label acme.team.alice: mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds, label

**Case 30 (down_inspect): inbox inspect, server stopped**

```text
$ mashctl inbox inspect acme.team.carol
[exit 3]
--- stdout
--- stderr
mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)
```

Marks: P5; 1 line(s) over 80 columns, first: `mashctl: connect tls://127.0.0.1:20001: nats: no servers available for...`

**Case 31 (down_list_old): list, server stopped**

```text
$ mashctl list
[exit 1]
--- stdout
--- stderr
mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config.json), owner acme (from /tmp/mc/cfg/config.json), admin creds /tmp/mc/cfg/server/mashadmin.creds (from /tmp/mc/cfg/config.json)
mashctl: connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)
```

Marks: P1, P5, P6; 2 line(s) over 80 columns, first: `mashctl: target server tls://127.0.0.1:20001 (from /tmp/mc/cfg/config....`; jargon: creds

**Case 32 (down_perm): permissions --nsc, server stopped**

```text
$ mashctl permissions acme.team.alice --nsc
[exit 0]
--- stdout
--allow-pub
agents.mail.acme.*.*
--allow-pub
agents.prompt.acme.*.*
--allow-pub
agents.status.acme.*.*
--allow-pub
agents.hb.acme.team.alice
--allow-pub
_INBOX.acme.>
--allow-pub
$SRV.>
--allow-pub
$JS.API.INFO
--allow-pub
$JS.API.STREAM.INFO.inbox-acme_team_alice
--allow-pub
$JS.API.CONSUMER.INFO.inbox-acme_team_alice.deliver
--allow-pub
$JS.API.CONSUMER.MSG.NEXT.inbox-acme_team_alice.deliver
--allow-pub
$JS.ACK.inbox-acme_team_alice.deliver.>
--allow-pub
$JS.API.DIRECT.GET.inbox-acme_team_alice
--allow-pub
$JS.API.DIRECT.GET.inbox-acme_team_alice.>
--allow-pub
$JS.API.STREAM.INFO.KV_mash-directory
--allow-pub
$JS.API.DIRECT.GET.KV_mash-directory.>
--allow-pub
$JS.API.STREAM.INFO.KV_mash-leases
--allow-pub
$KV.mash-leases.acme_team_alice
--allow-pub
$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases.acme_team_alice
--allow-sub
_INBOX.acme.acme_team_alice.>
--allow-sub
agents.prompt.acme.team.alice
[... 6 more stdout lines omitted from this document]
--- stderr
note: these are the permissions of the endpoint's NATS user: the owner-line set for its address.
```

Marks: P3, P5, P6, P7, P9; 1 line(s) over 80 columns, first: `note: these are the permissions of the endpoint's NATS user: the owner...`; jargon: lease; stdout trimmed to 40 of 46 lines

**Case 33 (down_limits_show): limits --show, server stopped**

```text
$ mashctl limits --show
[exit 0]
--- stdout
inbox_max_bytes 16MiB
inbox_max_age 30d
directory_max_bytes 16MiB
lease_max_bytes 4MiB
inbox_max_consumers -1
--- stderr
```

Marks: P3

Breaks:

- P1 (preamble line `mashctl: target server ...` on stderr): cases 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 16, 17, 18, 19, 20, 21, 22, 23, 25, 26, 27, 28, 29, 31.
- P2 (one address printed in two or three forms on one line): cases 6, 7, 8, 9.
- P3 (table or listing not aligned, or no headers): cases 32, 33.
- P5 (lines over 80 columns): cases 1, 2, 3, 4, 5, 6, 7, 8, 9, 12, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32.
- P6 (jargon words): cases 1 (creds), 2 (creds), 3 (creds), 4 (creds), 5 (creds), 6 (cap, creds, kind, label), 7 (cap, creds, kind, label), 8 (cap, creds, kind, label), 9 (cap, creds, kind, label), 10 (creds), 11 (creds), 16 (creds), 17 (creds), 18 (creds), 19 (creds), 20 (creds), 21 (creds), 22 (creds), 23 (creds), 25 (creds), 26 (creds), 27 (creds), 28 (creds), 29 (creds, label), 31 (creds), 32 (lease).
- P7 (output meant for an agent, shown to a human): cases 32.
- P9 (information not asked for, which belongs behind --detail): cases 32.
- P1: the preamble is the first line of stderr in every server case; when no config exists it prints `mashctl: target server unset, owner unset, admin creds unset` (noinit_list, noinit_init), and then the error. For env_owner, env_owner_session, env_session and env_url_session it prints the preamble, then up to two `warning:` lines about MASH_* variables, and the result; the two warnings are 242 and 298 columns.
- P5: the `warning: MASH_URL (tls://localhost:20001) overrides config.json's url ...` lines are 242 and 298 columns; the owner warning is 197; the plain-URL refusal (env_plain_*) is one line of 318 columns.
- env_plain_list, env_plain_init, env_plain_create, env_plain_check, env_plain_ndel: the refusal names `insecure_plaintext (MASH_INSECURE_PLAINTEXT=1)` and exits 2; the message text repeats `mashctl:` and `mash:` (`mashctl: endpoint list: mashctl: mash: refusing a plain nats:// connection to "127.0.0.1": ...`), three prefixes in one line. `endpoint create` reports it as `error (server)`. Exit codes differ: init exits 2 here and 1 for other connection errors (down_init).
- env_owner: `MASH_OWNER=other` outside a session prints `warning: MASH_OWNER (other) overrides config.json's owner (acme); mashctl is acting on other. ...` only when MASH_NAME is set (env_owner_session); without MASH_NAME the override is printed in the preamble line only (`owner other (from MASH_OWNER)`).
- env_session: with MASH_NAME and MASH_LAUNCH_TOKEN set and no override, no warning is printed, although docs/mashctl.md says the command warns when `MASH_NAME` or `MASH_LAUNCH_TOKEN` shows the shell is inside a session; the code warns only when MASH_URL overrides the config (env_url_session).
- noinit_*: with an empty config dir, `endpoint list` exits 6 with `mashctl: endpoint list: mashctl: no owner (config.json "owner" or MASH_OWNER)`, `init` exits 1, `audit` exits 3, `permissions` and `limits --show` exit 1 with the same sentence; the sentence does not say that the config directory is empty or name the directory, and `mashctl init --local` is not mentioned. `endpoint create` exits 1 with `refused (no_account_key): mashctl: no account signing key: run `mashctl init` (run mashctl init first)`, which says `init` twice and points at `init`, where `init --local` makes the key.
- creds_loose, creds_none, creds_missing, creds_none_init: `mash: credentials file /tmp/mc/cfg7/loose.creds has mode 0644: it is readable by other users. Run: chmod 600 ...`; `admin commands need MASH_URL and MASH_ADMIN_CREDS` (the config key is `admin_creds`); `admin credentials: open ...: no such file or directory`. The exit code for the same class of error is 6 for `endpoint list` and 1 for `init`.
- down_*: the server is stopped. The text is `connect tls://127.0.0.1:20001: nats: no servers available for connection (check MASH_URL and that at least one listed server is up)` with a different number of `mashctl:` prefixes and exit codes by verb: endpoint list/create/delete/reissue 6, init/suspended/label/list 1, audit/inbox inspect 3, issued check 2. down_create_dry exits 6: `--dry-run` connects.
- down_perm, down_limits_show: exit 0 with the server stopped; both read only the config.


## Appendix A: mash commands the feedback also covers

### mash key create and mash credential create

**Case 1 (kc_help): mash key create --help**

```text
$ mash key create --help
[exit 0]
--- stdout
usage: mash key create --out FILE [--pub-out FILE] [--json] [--dry-run]

Make a user key pair. The seed goes to FILE (mode 0600); the public key goes to FILE with a trailing .seed replaced
by .pub (FILE.pub if FILE does not end in .seed), or to --pub-out. It makes a missing parent directory (mode 0700) and refuses to overwrite either file. It prints the
public key and both paths, never the seed.

  --out FILE       the seed file (required)
  --pub-out FILE   the public key file
  --json           one JSON document (schema mash-key@1)
  --dry-run        write nothing; say what would be written

Example:
  mash key create --out design.seed
--- stderr
```

Marks: P5, P6; 2 line(s) over 80 columns, first: `Make a user key pair. The seed goes to FILE (mode 0600); the public ke...`; jargon: seed

**Case 2 (kc): mash key create**

```text
$ mash key create --out /tmp/mc/work/bob.seed
[exit 0]
--- stdout
created key
  public key  UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
  seed        /tmp/mc/work/bob.seed
  public file /tmp/mc/work/bob.pub
next: send /tmp/mc/work/bob.pub to the host that runs mashctl, then: mashctl endpoint create ADDRESS --public-key /tmp/mc/work/bob.pub
--- stderr
```

Marks: P4, P5, P6; 1 line(s) over 80 columns, first: `next: send /tmp/mc/work/bob.pub to the host that runs mashctl, then: m...`; jargon: seed

**Case 3 (kc_dry): --dry-run**

```text
$ mash key create --out /tmp/mc/work/jay.seed --dry-run
[exit 0]
--- stdout
would create key (dry run, nothing written)
  public key  UBP4HTUU5FLRL2S2Q7VJUCLPZEYRB47WTSWOTG6QF6GXA2OUK6HM5KUB
  seed        /tmp/mc/work/jay.seed
  public file /tmp/mc/work/jay.pub
next: send /tmp/mc/work/jay.pub to the host that runs mashctl, then: mashctl endpoint create ADDRESS --public-key /tmp/mc/work/jay.pub
--- stderr
```

Marks: P4, P5, P6; 1 line(s) over 80 columns, first: `next: send /tmp/mc/work/jay.pub to the host that runs mashctl, then: m...`; jargon: seed

**Case 4 (kc_json): --json**

```text
$ mash key create --out /tmp/mc/work/ivy.seed --json
[exit 0]
--- stdout
{"schema":"mash-key@1","outcome":"created","public_key":"UD7CJ5RSKIOORP6QZS6RB5I3JD2LX4IOHRXLC6FBZW6MVYGYU62MBPNS","seed_path":"/tmp/mc/work/ivy.seed","public_path":"/tmp/mc/work/ivy.pub","refusal":null}
--- stderr
```

Marks: none (JSON, for agents; not marked)

**Case 5 (kc_noargs): no arguments**

```text
$ mash key create
[exit 2]
--- stdout
--- stderr
mash key create: --out is required (try: mash key create --help)
```

Marks: none

**Case 6 (kc_exists): seed file exists**

```text
$ mash key create --out /tmp/mc/work/hank.seed
[exit 1]
--- stdout
--- stderr
mash key create: refused (file_exists): /tmp/mc/work/hank.seed exists; mash does not overwrite it
next: pick another --out, or move the existing file
```

Marks: P4, P5, P6; 1 line(s) over 80 columns, first: `mash key create: refused (file_exists): /tmp/mc/work/hank.seed exists;...`; jargon: seed

**Case 7 (cc_help): mash credential create --help**

```text
$ mash credential create --help
[exit 0]
--- stdout
usage: mash credential create --seed FILE --jwt FILE [--out CREDS] [--force] [--json] [--dry-run]

Join a user seed and the user JWT issued for its public key into a credentials file (mode 0600). It refuses if the JWT
was issued for a different key (key_mismatch) and refuses to replace an existing file unless --force is given. It
prints the credentials path and the public key, never the seed or the JWT.

It checks the structure of the JWT, its self-signature, and that it is a user JWT naming the seed's key; it does not
check the issuing account or the expiry.

  --seed FILE   the seed file (required)
  --jwt FILE    the user JWT file (required)
  --out CREDS   the credentials file; default is the config directory plus the JWT's name and .creds
  --force       replace an existing credentials file
  --json        one JSON document (schema mash-credential@1)
  --dry-run     write nothing; say what would be written

Example:
  mash credential create --seed design.seed --jwt design.jwt --out design.creds
--- stderr
```

Marks: P3, P5, P6; 5 line(s) over 80 columns, first: `usage: mash credential create --seed FILE --jwt FILE [--out CREDS] [--...`; jargon: JWT, creds, seed

**Case 8 (cc): mash credential create**

```text
$ mash credential create --seed /tmp/mc/work/bob.seed --jwt /tmp/mc/work/bob.jwt --out /tmp/mc/work/bob.creds
[exit 0]
--- stdout
created credentials
  public key  UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC
  creds       /tmp/mc/work/bob.creds
next: mash status
--- stderr
```

Marks: P4, P6; jargon: creds

**Case 9 (cc_dry): --dry-run, key mismatch**

```text
$ mash credential create --seed /tmp/mc/work/hank.seed --jwt /tmp/mc/work/bob.jwt --out /tmp/mc/work/mm.creds --dry-run
[exit 1]
--- stdout
--- stderr
mash credential create: refused (key_mismatch): the JWT is for UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC, the seed is for UC534JF7VXLC4S7GPSCME5PLGPLDZWFV5FDDASL7CLVANXHOHIGW5XM5
next: check that the seed and the JWT belong together
```

Marks: P4, P5, P6; 1 line(s) over 80 columns, first: `mash credential create: refused (key_mismatch): the JWT is for UAO35ZE...`; jargon: JWT, seed

**Case 10 (cc_exists): creds file exists**

```text
$ mash credential create --seed /tmp/mc/work/bob.seed --jwt /tmp/mc/work/bob.jwt --out /tmp/mc/work/bob.creds
[exit 1]
--- stdout
--- stderr
mash credential create: refused (file_exists): /tmp/mc/work/bob.creds exists; pass --force to replace it
next: pass --force to replace it, or choose another --out
```

Marks: P4, P5, P6; 1 line(s) over 80 columns, first: `mash credential create: refused (file_exists): /tmp/mc/work/bob.creds ...`; jargon: creds

**Case 11 (cc_mismatch): seed and JWT do not match**

```text
$ mash credential create --seed /tmp/mc/work/hank.seed --jwt /tmp/mc/work/bob.jwt --out /tmp/mc/work/mm.creds
[exit 1]
--- stdout
--- stderr
mash credential create: refused (key_mismatch): the JWT is for UAO35ZE32HBKAMOGS4CYOJCJWGWGGEGGDEWOLR5Q2L47QFBJMQJRU3CC, the seed is for UC534JF7VXLC4S7GPSCME5PLGPLDZWFV5FDDASL7CLVANXHOHIGW5XM5
next: check that the seed and the JWT belong together
```

Marks: P4, P5, P6; 1 line(s) over 80 columns, first: `mash credential create: refused (key_mismatch): the JWT is for UAO35ZE...`; jargon: JWT, seed

Breaks:

- P3 (table or listing not aligned, or no headers): cases 7.
- P4 (a `next:` or `Next:` line is present): cases 2, 3, 6, 8, 9, 10, 11.
- P5 (lines over 80 columns): cases 1, 2, 3, 6, 7, 9, 10, 11.
- P6 (jargon words): cases 1 (seed), 2 (seed), 3 (seed), 6 (seed), 7 (JWT, creds, seed), 8 (creds), 9 (JWT, seed), 10 (creds), 11 (JWT, seed).
- P4: kc prints `next: send /tmp/mc/work/bob.pub to the host that runs mashctl, then: mashctl endpoint create ADDRESS --public-key /tmp/mc/work/bob.pub`; kc_dry prints the same `next:` for a run that wrote nothing; kc_exists prints `next: pick another --out, or move the existing file` after the refusal.
- P6: `seed`, `public key`, `creds`, `JWT` are used in the lines; `created key` prints the public key (56 characters) and two paths.
- cc: `created credentials` then `public key` and `creds` paths; `next: mash status`; cc_dry (--dry-run) is refused with `key_mismatch: the JWT is for U..., the seed is for U...`, with the full 56-character keys, which makes the line 193 columns.

### mash status

**Case 1 (m_status): endpoint acme.team.alice (MASH_OWNER, MASH_GROUP, MASH_NAME set)**

```text
$ mash status
[exit 0]
--- stdout
name: OK - acme.team.alice
config: OK - url tls://127.0.0.1:20001, owner acme
name rule: none set
creds: OK - /tmp/mc/cfg/acme.team.alice.creds exists and is readable
tls: OK - tls:// connection
connection: not checked from the CLI (a running session reports it)
clock: n/a - not connected
channel allowlist: OK - local managed settings file enables channels and lists mash@mash
delivery: not checked from the CLI (a running session reports it): hook state and channel notification counts exist only inside a running session
relay trace: none recorded
version: mash 0.5.5 (commit e389eaf)
--- stderr
```

Marks: P3, P5, P6, P9; 2 line(s) over 80 columns, first: `channel allowlist: OK - local managed settings file enables channels a...`; jargon: creds

**Case 2 (noinit_status): empty config dir, no MASH_NAME**

```text
$ mash status
[exit 0]
--- stdout
name: MISSING - not set. Next: start Claude Code with MASH_NAME=<endpoint>
config: MISSING - url is not set. Next: set "url" in ~/.config/mash/config.json or the MASH_URL environment variable; owner is not set. Next: set "owner" in ~/.config/mash/config.json or the MASH_OWNER environment variable
name rule: none set
creds: n/a - no endpoint name, so no creds file is chosen (the default is ~/.config/mash/<endpoint>.creds)
tls: n/a - no url
connection: not checked from the CLI (a running session reports it)
clock: n/a - not connected
channel allowlist: OK - local managed settings file enables channels and lists mash@mash
delivery: not checked from the CLI (a running session reports it): hook state and channel notification counts exist only inside a running session
relay trace: none recorded
version: mash 0.5.5 (commit e389eaf)
--- stderr
```

Marks: P3, P5, P6, P9; 4 line(s) over 80 columns, first: `config: MISSING - url is not set. Next: set "url" in ~/.config/mash/co...`; jargon: creds

**Case 3 (noinit_status2): empty config dir, MASH_NAME set**

```text
$ mash status
[exit 0]
--- stdout
name: OK - acme.team.alice
config: MISSING - url is not set. Next: set "url" in ~/.config/mash/config.json or the MASH_URL environment variable
name rule: none set
creds: MISSING - /tmp/mc/empty/acme.team.alice.creds does not exist. Next: put the endpoint's creds file at /tmp/mc/empty/acme.team.alice.creds, or set MASH_CREDS (or "creds" in config.json) to its path
tls: n/a - no url
connection: not checked from the CLI (a running session reports it)
clock: n/a - not connected
channel allowlist: OK - local managed settings file enables channels and lists mash@mash
delivery: not checked from the CLI (a running session reports it): hook state and channel notification counts exist only inside a running session
relay trace: none recorded
version: mash 0.5.5 (commit e389eaf)
--- stderr
```

Marks: P3, P5, P6, P9; 4 line(s) over 80 columns, first: `config: MISSING - url is not set. Next: set "url" in ~/.config/mash/co...`; jargon: creds

Breaks:

- P3 (table or listing not aligned, or no headers): cases 1, 2, 3.
- P5 (lines over 80 columns): cases 1, 2, 3.
- P6 (jargon words): cases 1 (creds), 2 (creds), 3 (creds).
- P9 (information not asked for, which belongs behind --detail): cases 1, 2, 3.
- P9: the output is eleven lines `name: OK - acme.team.alice`, `config: ...`, `name rule: none set`, `creds: ...`, `tls: ...`, `connection: not checked from the CLI (a running session reports it)`, `clock: n/a - not connected`, `channel allowlist: ...`, `delivery: not checked from the CLI (...)`, `relay trace: none recorded`, `version: ...`; three lines say a check was not made (`connection`, `clock`, `delivery`).
- P6: `channel allowlist`, `relay trace`, `name rule`, `creds`, `tls` are not defined.
- noinit_status: with no config the output has `Next:` text inside the line (`MISSING - not set. Next: start Claude Code with MASH_NAME=<endpoint>`); exit 0 although three lines say MISSING.

### mash who

**Case 1 (m_who): success**

```text
$ mash who
[exit 0]
--- stdout
acme.team.alice	offline
acme.team.alice2	offline
acme.team.bob	offline
acme.team.carol	offline	demo tool
--- stderr
```

Marks: P3, P8

**Case 2 (m_who_long): --long**

```text
$ mash who --long
[exit 0]
--- stdout
acme.team.alice	offline	agent claude-code	host laptop	desc makes designs	caps read	label.env=demo
acme.team.alice2	offline	agent claude-code
acme.team.bob	offline	agent claude-code
acme.team.carol	offline	demo tool	agent claude-code	label.env=demo	label.kind=tool
--- stderr
```

Marks: P3, P5, P6, P8; 2 line(s) over 80 columns, first: `acme.team.alice	offline	agent claude-code	host laptop	desc makes desig...`; jargon: cap, kind, label

**Case 3 (m_who_names): --names**

```text
$ mash who --names
[exit 0]
--- stdout
acme_team_alice
acme_team_alice2
acme_team_bob
acme_team_carol
--- stderr
```

Marks: P8

**Case 4 (m_who_json): --json**

```text
$ mash who --json
[exit 0]
--- stdout
[
  {
    "name": "acme_team_alice",
    "identity": "acme.team.alice",
    "owner": "acme",
    "group": "team",
    "instance": "alice",
    "agent": "claude-code",
    "online": false,
    "host": "laptop",
    "description": "makes designs",
    "caps": [
      "read"
    ],
    "labels": {
      "env": "demo"
    }
  },
  {
    "name": "acme_team_alice2",
    "identity": "acme.team.alice2",
    "owner": "acme",
    "group": "team",
    "instance": "alice2",
    "agent": "claude-code",
    "online": false
  },
  {
    "name": "acme_team_bob",
    "identity": "acme.team.bob",
    "owner": "acme",
    "group": "team",
    "instance": "bob",
    "agent": "claude-code",
    "online": false
  },
  {
    "name": "acme_team_carol",
    "identity": "acme.team.carol",
    "owner": "acme",
[... 11 more stdout lines omitted from this document]
--- stderr
```

Marks: none (JSON, for agents; not marked); stdout trimmed to 40 of 51 lines

**Case 5 (noinit_who): no MASH_NAME**

```text
$ mash who
[exit 1]
--- stdout
--- stderr
mash: set MASH_NAME to the instance you are acting as (with MASH_OWNER and MASH_GROUP: the endpoint is owner.group.instance)
```

Marks: P5; 1 line(s) over 80 columns, first: `mash: set MASH_NAME to the instance you are acting as (with MASH_OWNER...`

**Case 6 (down_m_who): server stopped**

```text
$ mash who
[exit 1]
--- stdout
--- stderr
mash: connect tls://127.0.0.1:20001: nats: no servers available for connection
```

Marks: none

Breaks:

- P3 (table or listing not aligned, or no headers): cases 1, 2.
- P5 (lines over 80 columns): cases 2, 5.
- P6 (jargon words): cases 2 (cap, kind, label).
- P8 (field repeated on one screen): cases 1, 2, 3.
- P3/P8: `acme.team.alice	offline` rows are tab separated, no header; every row carries `acme.team` (the same owner and group).
- m_who_long: `agent claude-code	host laptop	desc makes designs	caps read	label.env=demo` is a row with a variable number of fields, so the columns do not line up.
- noinit_who: `mash: set MASH_NAME to the instance you are acting as (with MASH_OWNER and MASH_GROUP: the endpoint is owner.group.instance)` exit 1; `who` needs an endpoint identity to list endpoints.

### mash send, inbox and read

**Case 1 (m_send): send**

```text
$ mash send acme.team.carol "hello carol"
[exit 0]
--- stdout
sent #1 from acme.team.alice to acme.team.carol
--- stderr
```

Marks: none

**Case 2 (m_send2): send --kind question**

```text
$ mash send acme.team.carol --kind question "second one"
[exit 0]
--- stdout
sent #2 from acme.team.alice to acme.team.carol
--- stderr
```

Marks: none

**Case 3 (m_send_missing): endpoint missing**

```text
$ mash send acme.team.zed hi
[exit 1]
--- stdout
--- stderr
mash: no endpoint named "acme.team.zed" (see who)
```

Marks: none

**Case 4 (m_send_suspended): recipient suspended**

```text
$ mash send acme.team.carol "to suspended"
[exit 1]
--- stdout
--- stderr
mash: send to acme.team.carol: nats: no response from stream
```

Marks: none

**Case 5 (down_m_send): server stopped**

```text
$ mash send acme.team.carol hi
[exit 1]
--- stdout
--- stderr
mash: connect tls://127.0.0.1:20001: nats: no servers available for connection
```

Marks: none

**Case 6 (m_inbox): inbox**

```text
$ mash inbox
[exit 0]
--- stdout
#1	from acme.team.alice [verified]	2026-11-19T00:22:41Z	11 B	hello carol	kind task	instance xgyxcmtq3WuRwxkCurSpdJ
#2	from acme.team.alice [verified]	2026-11-19T00:22:41Z	10 B	second one	kind question	instance 7OzszzWNrQuMHyQBEWB029
--- stderr
```

Marks: P3, P5, P6, P8, P9; 2 line(s) over 80 columns, first: `#1	from acme.team.alice [verified]	2026-11-19T00:22:41Z	11 B	hello car...`; jargon: kind

**Case 7 (m_inbox_empty_after): inbox --limit 1 for a message signed with a revoked key**

```text
$ mash inbox --limit 1
[exit 0]
--- stdout
#3	from UAGT…7KOQ [unknown-signer]	2026-11-19T00:22:41Z	2 B	hi	kind task
--- stderr
```

Marks: P5, P6; 1 line(s) over 80 columns, first: `#3	from UAGT…7KOQ [unknown-signer]	2026-11-19T00:22:41Z	2 B	hi	kind ta...`; jargon: kind

**Case 8 (m_read): read 1**

```text
$ mash read 1
[exit 0]
--- stdout
--- #1 from acme.team.alice [verified] at 2026-11-19T00:22:41Z
instance: xgyxcmtq3WuRwxkCurSpdJ
kind: task
> hello carol
--- stderr
```

Marks: P6, P9; jargon: kind

**Case 9 (m_read_missing): read 99**

```text
$ mash read 99
[exit 1]
--- stdout
--- stderr
mash: no message #99 in acme.team.carol's inbox
```

Marks: none

Breaks:

- P3 (table or listing not aligned, or no headers): cases 6.
- P5 (lines over 80 columns): cases 6, 7.
- P6 (jargon words): cases 6 (kind), 7 (kind), 8 (kind).
- P8 (field repeated on one screen): cases 6.
- P9 (information not asked for, which belongs behind --detail): cases 6, 8.
- P3/P8: `#1	from acme.team.alice [verified]	2026-11-19T00:22:41Z	11 B	hello carol	kind task	instance xgyxcmtq3WuRwxkCurSpdJ` is one tab separated row, no header; every row repeats the sender and the owner.
- P9: the `instance` id on each row and in `mash read` (`instance: xgyxcmtq3WuRwxkCurSpdJ`) is a session id for agents.
- m_send: `sent #1 from acme.team.alice to acme.team.carol` names the sender, which the sender knows; m_send_suspended is `mash: send to acme.team.carol: nats: no response from stream`, exit 1, without the word suspended (docs/mashctl.md names this as a known limitation).
- m_inbox_empty_after: after alice's key was reissued, her earlier message shows as `from UAGT…7KOQ [unknown-signer]`, the public key in place of the address.


## Findings that are not about format

Each item is a behavior that contradicts the text of the program or of `docs/mashctl.md`, or is wrong on its own terms. The command that shows it is given; the case in this file that holds the output is named.

1. **`init` names two buckets and makes five.** `mashctl init` prints `mashctl: directory and lease buckets ready` (`init_after`, `init_local_start`). On a new server made with `mashctl init --local --owner acme --port N --start`, with no endpoint, the account's stream directory holds `KV_mash-directory`, `KV_mash-adoption`, `KV_mash-issued`, `KV_mash-leases` and `KV_mash-retired` (listed from the `jetstream/<account>/streams` directory of the server's store). The `adopt` check after four endpoints exist reports `the server answered for the account: 9 stream(s), 4 consumer(s)`: four inbox streams and five buckets (`ad_dry`). `docs/operations.md` says to size the account at one stream per endpoint plus two for the buckets (the line `one stream per endpoint plus two for the buckets`).
2. **`endpoint delete` without `--force` handles the creds file when there is no record.** `docs/mashctl.md` says `endpoint delete` refuses with exit 1 when there is no directory record on the server, and that `--force` still handles the creds file. With the record removed by `mashctl deprovision acme.team.hal` and the creds file present, `mashctl endpoint delete acme/team/hal` exits 0, renames the creds file and prints `deleted acme/team/hal (acme.team.hal)` (`depro_hal`, `ed_nrec_nocreds_refuse`). With no record and no creds file it exits 1 (`ed_missing`).
3. **`endpoint delete` of an address that is already deleted prints `deleted` and exits 0.** `mashctl endpoint delete acme/team/dave` a second time prints `deleted acme/team/dave (acme.team.dave)` on stdout and `no directory record on the server, so no user key is known and nothing was revoked; only the creds file is handled` on stderr (`ed_again`). `--force` gives the same output (`ed_force`).
4. **`endpoint show` reports a creds file that does not exist.** `mashctl endpoint show acme/team/zed --json` (no such endpoint, no creds file) prints `"creds": {"path": "/tmp/mc/cfg/acme.team.zed.creds", "exists": true}` (`es_json_missing`). The code sets `Exists` to `credsPath != ""` (`cmd/mashctl/endpoint_show.go`), so the field is true whenever a path is computed. The text form of `endpoint show acme/team/ned`, an endpoint made with `--public-key` whose creds file is not on this host, prints `creds file:   /tmp/mc/cfg/acme.team.ned.creds` for a file that is not there (`es_nocreds`).
5. **`--no-revoke` prints its warning twice.** `mashctl endpoint delete acme/team/fay --no-revoke` and `mashctl deprovision acme.team.lee --no-revoke` print the `WARNING: --no-revoke: ... is NOT revoked ...` line twice on stderr (`ed_norevoke`, `depro_norevoke`).
6. **The partial-run advice for `endpoint reissue --all` does nothing for a refused endpoint.** After `--all --make-key --yes` refuses `acme/team/bob` (`creds_mismatch`), the output says `rerun with --changed, not --all`; `mashctl endpoint reissue --changed --make-key --yes` then prints `nothing to reissue` and `bob` stays refused (`er_all_yes`, `er_changed_yes`).
7. **`endpoint reissue --all --dry-run` lists an endpoint that the real run refuses.** The dry run lists `would reissue acme/team/bob`; the real run refuses it as `creds_mismatch`, so the dry run does not run the creds check (`er_all_dry`, `er_all_yes`). The dry run also prints `reissued 0, skipped 0, refused 0, error 0` as its counts line.
8. **`adopt` points to `mashctl provision` for the endpoints.** Its `Next:` block ends with `2. mashctl provision owner.group.instance ...    declare an endpoint`; `docs/mashctl.md` names `mashctl init`, then `mashctl endpoint create` for each endpoint (`ad_real`).
9. **The session warning in the preamble does not match the docs.** `docs/mashctl.md` says mashctl warns when `MASH_NAME` or `MASH_LAUNCH_TOKEN` shows that the shell is inside a session. With `MASH_NAME` and `MASH_LAUNCH_TOKEN` set and no override, it prints no warning (`env_session`); the code (`cmd/mashctl/target.go`) warns about the session only when `MASH_URL` overrides the config (`env_url_session`), and the override warning for `MASH_OWNER` appears only when a session variable is also set (`env_owner`, `env_owner_session`).
10. **`init --local --start` writes the files before it checks the port.** With the port in use, the command prints the nine lines about the files it wrote, then `port 20001 is already in use ... so nothing was started`, exits 1, and leaves the keys, config and server directory (`il_portinuse`). The next run in that directory is refused as `already holds ...: refusing to overwrite` (`il_portinuse_again`).
11. **`endpoint create --make-key` on an existing endpoint is refused as `file_exists`, not `address_taken`.** The creds file is checked before the directory (`ecx_exists`); `address_taken` appears only with `--public-key` (`ecx_taken`). Its text says `use reissue to give it a new key`; the verb is `endpoint reissue`.
12. **`mashctl audit --help` omits `--compare`.** The usage printed for `audit` has `[--json] [--strict] [--creds-dir DIR]`; the usage error of `audit` and `docs/mashctl.md` list `[--compare EARLIER.json]` (`help_audit`, `audit_flag`, `audit_compare`).
13. **Error prefixes differ inside one binary.** Some errors begin `mashctl:` and others `mash:` (`limits_bad`, `sm_missing`, `ii_missing`, `depro_wrongid`, `prov_again`), and one line can carry two or three prefixes (`mashctl: endpoint list: mashctl: mash: refusing a plain nats:// connection ...` in `env_plain_list`).
14. **Exit codes for the same failure differ by verb.** With the server stopped, `endpoint list`, `create`, `delete` and `reissue` exit 6; `init`, `suspended`, `label` and `list` exit 1; `audit` and `inbox inspect` exit 3; `issued check` exits 2 (the `down_` cases). The plain-URL refusal exits 2 for `init` and the endpoint verbs (`env_plain_*`).
15. **`resume` and `suspend` repeat their success text when nothing changed.** `mashctl resume acme.team.bob` on an endpoint that is not suspended prints `resumed acme.team.bob` and exits 0 (`resume_again`); a second `suspend` prints the same id and exits 0 (`suspend_again`).
16. **`permissions` writes `>` as `>` in its JSON.** `_INBOX.acme.>` is printed as `_INBOX.acme.>` (`perm`); the `--nsc` form prints `>` (`perm_nsc`).
17. **A message signed by a key that was later replaced is shown as `unknown-signer`.** After `mashctl endpoint reissue acme/team/alice --make-key`, `mash inbox` for `carol` shows an earlier message from alice as `from UAGT…7KOQ [unknown-signer]`, with the public key in place of the address (`m_inbox_empty_after`). The trust rule in `docs/mashctl.md` explains it; the output does not.
18. **`mashctl endpoint create` takes `--note`, `--describe` and `--cap` values that the result does not show.** `ec_tool` and `ec_ned` print the same lines as a create without them; only `endpoint show` or `endpoint list` shows the note, description and caps. `--cap read` is accepted although `read` is not defined as a capability anywhere in the output (`sm_show`).
