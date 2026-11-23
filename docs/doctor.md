# mashctl doctor: is the server, its storage and every endpoint in order

`mashctl doctor` checks the server, mash's storage on it and every endpoint's records, and says how to fix what is wrong.
It is read only: it writes nothing to the server and nothing to disk, and it can be run at any time, on any number of
machines. It takes its server, owner and credentials from `config.json` like the other `mashctl` verbs (see
[mashctl.md](mashctl.md)); `mashctl init --config FILE` writes them.

```text
mashctl doctor [--strict] [--audit-users] [--json]
```

The generated reference page, [mashctl doctor](reference/mashctl-doctor.md), has the exact output and exit codes; this page explains what each check means.

- `--strict` exits 1 on a warning too.
- `--audit-users` fails if a user connected to the mash account was not issued by mashctl; use it to check who is connected now (`--strict` does not do this).
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
keys       ok    every connected user was issued by mashctl

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
keys       ok    every connected user was issued by mashctl

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
| `keys` | a key recorded as revoked is still connected, not every server answered, or, with `--audit-users`, a connected user was not issued by mashctl (the finding names it) | no `system_creds` in the configuration, so nothing was checked (a failure under `--audit-users`, which cannot audit without it) | `mashctl endpoint list`; `mashctl init --config <file>`; with `--audit-users`, `revoke it with the tool that issued it (mashctl revokes only its own)` |

When a check finds several things its detail lists them, separated by `;`, failures first, and names up to three
addresses for each (`and N more` after that).

With `--audit-users`, the connected users mashctl did not issue are the exception: each key is shown whole on a line of
its own under the detail (up to three, then `and N more`), and that finding comes last in the `keys` line:

```text
keys       FAIL  2 connected users were not issued by mashctl:
                 UAB2DMH4SPCMUGKLPFSR3O54L2YQ53CAGXIUSSU2GE55OHENR3PXIN4A
                 UDXU4RCSJNZOIQHXEVIZRFQ3C4SQ3N7KRBYQ3JNLKSDGFSAPQQBWHWKR
```

With `--json` the same lines are in the `detail` string, separated by `\n`, the keys whole.

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
for every user JWT it issues). A key the record holds as revoked that is still connected means the revocation did not
take effect (check that every server holds the account JWT with the revocation, and revoke the key again). Only public
keys are used.

#### Other users on the account

Health covers mash's own endpoints, the users mashctl issued, storage and sign-in. It never fails because other users
share the account: mash is a tenant of the NATS account, not its owner. A connected user that mashctl did not issue is
counted on the `keys` line and is not a problem, with or without `--strict`:

```text
keys       ok    mashctl's users ok; 2 other users, not issued by mashctl
```

With one it reads `mashctl's users ok; 1 other user, not issued by mashctl`; with none, `every connected user was issued by mashctl`.

`mashctl doctor --audit-users` is the explicit check that every connected user was issued by mashctl (it sees only the
users connected now), and the only thing that fails on such a user. It is not implied by `--strict`, and works with or without it. It names the keys, up to three, then
`and N more`:

```text
keys       FAIL  1 connected user was not issued by mashctl:
                 UDXU4RCSJNZOIQHZNWXHXORDPRTGNJAHAHFRGZNEEJCPQTT2M7NLCNF4

1 problem.
hint: revoke it with the tool that issued it (mashctl revokes only its own)
```

Each user is shown by its public key (U...). Ask whoever owns the NATS account to revoke it. A connection that reports no key is counted too and shown as `(no key reported)`. With no such user, `--audit-users` leaves the row as
`every connected user was issued by mashctl`.

When the `keys` row fails without `--audit-users` (a revoked key is still connected, or not every server answered), the count of other users stays in the detail, on a line after the failure: `2 other users, not issued by mashctl`.

The check is complete only when every server answered: the servers it expects are the count under `servers` in
`config.json`, and a cluster that reports more servers than are listed is not complete either. A server that did not
answer, or whose connection list changed while it was read and could not be read whole, fails the check with the reason,
even when no other user was found. Without the issue record (the `mash-issued` bucket) the keys row fails with `no issue record; mashctl cannot tell its users from others` and the fix `mashctl init --config <file>`, with or without `--audit-users`. Without `system_creds` the check warns and reads nothing; with `--audit-users` it fails instead, since no user was audited.

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

`checks` always holds all six checks, in order. A check that could not run because an earlier one failed has state
`skip`, so a failed run lists the checks after the failed one as `skip`.
`state` is `ok`, `warn`, `fail` or `skip`; `detail` is `""` when there is none; `fix` is the command that fixes it, or `null`.
`problems` and `warnings` count the checks in that state.

## What it cannot see

It shows state, not use: it cannot see publishes, connections of endpoints, or an inbox's contents (see
`mashctl endpoint inbox`).
