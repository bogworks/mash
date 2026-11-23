# mashctl doctor: is the server, its storage and every endpoint in order

`mashctl doctor` checks the server, mash's storage on it and every endpoint's records, and says how to fix what is wrong.
It is read only: it writes nothing to the server and nothing to disk, and it can be run at any time, on any number of
machines. It takes its server, owner and credentials from `config.json` like the other `mashctl` verbs (see
[mashctl.md](mashctl.md)); `mashctl init --config FILE` writes them.

```text
mashctl doctor [--strict] [--json]
```

The generated reference page, [mashctl doctor](reference/mashctl-doctor.md), has the exact output and exit codes; this page explains what each check means.

- `--strict` exits 1 on a warning too.
- `--json` prints the result as JSON.

It takes over `mashctl audit` and `mashctl issued check`, which no longer exist: `mashctl audit` and `mashctl issued`
are unknown commands (exit 2). The records of one endpoint are `mashctl endpoint show ADDRESS`; its inbox is
`mashctl endpoint inbox ADDRESS`.

## The output

Every check always has its line, whatever the result, then a blank line, the count and, when the first problem has a
fix, one `hint:` line. When all is well:

```text
config     ok
server     ok    tls://localhost:4222
sign-in    ok
storage    ok
endpoints  ok    3 records, 3 inboxes
keys       ok    every connected key was issued here

No problems.
```

When a check fails or warns, the `ok` lines are still printed, and so is one `hint:` line with the command that fixes
the first problem (the first warning, when nothing failed). When that first one has no command that fixes it, there is
no hint:

```text
config     ok
server     ok    tls://localhost:4222
sign-in    ok
storage    ok
endpoints  FAIL  1 inbox has no endpoint record: acme.demo.dave
keys       ok    every connected key was issued here

1 problem.
hint: mashctl endpoint show acme.demo.dave
```

A line is the check's name, its state (`ok`, `warn`, `FAIL` or `skip`) and, after that, its detail when it has one. A
`skip` line is a check that could not run because an earlier one failed; its detail says what it needs (`needs the
config`, `needs the server`, `needs sign-in`, `needs the storage`), and it counts as neither a problem nor a warning.
The count is `No problems.`, `1 problem.`, `2 problems.`, and a warning is added after a comma: `1 problem, 2 warnings.`,
`No problems, 1 warning.`. Addresses are shown dotted (`owner.group.name`). `--json` lists all six checks in `checks`
too, with `"state": "skip"` for a skipped one.

## The checks

They run in this order. A check that cannot run because an earlier one failed is shown as `skip` and is not counted, so a
server that does not answer shows one `FAIL` line and four `skip` lines, not five failures.

| Check | `FAIL` when | `warn` when | Fix |
| --- | --- | --- | --- |
| `config` | the configuration does not load, or has no servers, no `admin_creds` or no owner (the detail is the reason) | | `mashctl init --config <file>` |
| `server` | the first server listed does not answer over TLS (the detail is the server's URL when it does) | | `mashctl status` |
| `sign-in` | the server refuses the admin credentials, or they cannot be used | | `mashctl init --config <file>` |
| `storage` | the directory or lease bucket is missing, the lease bucket allows rollup writes (it has not been hardened) or cannot be read | another bucket is missing, a bucket's size cap is below the configured one, or a bucket is 90% full or more | `mashctl init --config <file>` |
| `endpoints` | a directory record cannot be read; an inbox has no endpoint record; a record has no inbox; an inbox lost mail unread; a retirement was started and not finished; the retired-name bucket has no size cap or a bad configuration | an inbox is 90% full or more; the retired-name bucket is at or over 50% of its cap; a lease has no endpoint record | the command for the first finding: `mashctl endpoint show ADDRESS`, `mashctl endpoint inbox ADDRESS`, `mashctl endpoint delete ADDRESS`, `mashctl limits apply ADDRESS` |
| `keys` | a connected key was not issued here and is not in `external_keys`, a key recorded as revoked is still connected, or not every server answered | no `system_creds` in the configuration, so nothing was checked | `mashctl endpoint list`; `mashctl init --config <file>` |

When a check finds several things its detail lists them, separated by `;`, failures first, and names up to three
addresses for each (`and N more` after that).

### storage

The directory and lease buckets must exist. A bucket's size cap is compared with the limit in `config.json` (`limits`, see
`mashctl limits show`): a cap below it is a warning, because `mashctl init --config FILE` raises it and never lowers
it. A bucket at 90% of its cap or more is a warning. The lease bucket must not allow rollup writes: `mashctl init --config
FILE` hardens it.

### endpoints

Every directory record has an inbox stream and every inbox stream has a record. A full inbox drops its oldest mail, read
or not; the loss is derived from the stream: a message the endpoint never received that is no longer there was lost
unread, and the count is a lower bound (mail that was fetched but never shown, or that was redelivered, can make it read
low, so a clean run does not prove that nothing was lost). Mail that was read and then aged out is ordinary turnover and
is not reported. An inbox at 90% of its cap or more that has lost nothing unread is a warning. Read mail sooner, or find
the sender that floods the inbox; `mashctl limits apply` raises an existing inbox to the configured limit.

A retirement that was started and not finished (a `pending` marker in the retired-name bucket) fails the check: run
`mashctl endpoint delete ADDRESS` again to finish it. The bucket that holds the markers has a 64 MiB cap and a full one
makes provisioning refuse new identities for good, so the check warns when it is at or over 50% of its cap; mash never
changes an existing bucket, so recreate it with a larger cap and its markers copied before it fills.

### keys

It asks every server, through the system account (`system_creds` in `config.json`), which user public keys are
connected in the mash account, and compares them with the issue record (the `mash-issued` bucket, which `mashctl` writes
for every user JWT it issues). A key that is connected and not in the record was not issued here; a key the record holds
as revoked that is still connected means the revocation did not take effect (check that every server holds the account
JWT with the revocation, and revoke the key again). No key is exempt, not even the admin user the command connects
with: `mashctl init` records it. Only public keys are used.

On a shared account, another tool may have issued users that mash did not. List their exact user public keys (`U...`)
in `config.json` as `external_keys`, an array of strings. A connected key that is listed, and not in the record, is
not a finding, and the line says so:

```text
keys       ok    every connected key was issued here or is listed in external_keys (1 external)
```

A key that is not listed and not in the record still fails, with or without `--strict`. A key the record holds keeps its
own rules: a revoked key that is still connected fails even when it is also listed. An entry that is not a user public
key is a configuration error that names the entry. With no `external_keys`, nothing changes. The list is per machine
that runs `mashctl`, and an entry for a key that is no longer connected does no harm.

The check is complete only when every server answered: the servers it expects are the count under `servers` in
`config.json`, and a cluster that reports more servers than are listed is not complete either. A server that did not
answer, or whose connection list changed while it was read and could not be read whole, fails the check with the reason,
even when no foreign key was found. Without `system_creds` the check warns and reads nothing.

## Exit codes

- `0` it worked: no problem, and no warning when `--strict` is given.
- `1` a problem was found (or, with `--strict`, a warning). The lines are on standard output.
- `2` the command line was wrong, or a plain `nats://` URL was refused for TLS; nothing was attempted.

## JSON

```json
{
  "problems": 0,
  "warnings": 0,
  "checks": [
    {
      "name": "server",
      "state": "ok",
      "detail": "tls://localhost:4222",
      "fix": null
    }
  ]
}
```

`checks` holds every check that ran, in order, so a failed run lists the checks up to and including the one that failed.
`state` is `ok`, `warn` or `fail`; `detail` is `""` when there is none; `fix` is the command that fixes it, or `null`.
`problems` and `warnings` count the checks in that state.

## What it cannot see

It shows state, not use: it cannot see publishes, connections of endpoints, or an inbox's contents (see
`mashctl endpoint inbox`).
