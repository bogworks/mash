Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl doctor

Check the server, mash's storage on it and every endpoint's records, and say
how to fix what is wrong. Changes nothing.

Release: 0.5.8. Group: Mesh.

## Usage

```text
mashctl doctor [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--strict` |  | Exit 1 on a warning too. |
| `--audit-users` |  | Fail if a user connected to the mash account was not issued by mashctl; use it to check who is connected now (--strict does not do this). |

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Examples

```text
mashctl doctor
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | A problem was found; the report on stdout says which. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### All well

Stream: stdout. Exit code: 0.

```text
config     ok
server     ok    tls://localhost:4222
sign-in    ok
storage    ok
endpoints  ok    3 records, 3 inboxes
keys       ok    every connected user was issued by mashctl

No problems.
```

### A problem

Stream: stdout, then the hint on stderr. Exit code: 1.

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

### Another tool's user is connected (not a problem)

Stream: stdout. Exit code: 0.

```text
config     ok
server     ok    tls://localhost:4222
sign-in    ok
storage    ok
endpoints  ok    3 records, 3 inboxes
keys       ok    mashctl's users ok; 1 other user, not issued by mashctl

No problems.
```

### --audit-users finds a user mashctl did not issue

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config     ok
server     ok    tls://localhost:4222
sign-in    ok
storage    ok
endpoints  ok    3 records, 3 inboxes
keys       FAIL  1 connected user was not issued by mashctl:
                 UDXU4RCSJNZOIQHZNWXHXORDPRTGNJAHAHFRGZNEEJCPQTT2M7NLCNF4

1 problem.
hint: revoke it with the tool that issued it (mashctl revokes only its own)
```

### A revoked key is connected and mashctl's users keep connecting and leaving

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config     ok
server     ok    tls://localhost:4222
sign-in    ok
storage    ok
endpoints  ok    3 records, 3 inboxes
keys       FAIL  1 revoked key is still connected
                 mashctl's users kept connecting and leaving; read 3 times

1 problem.
hint: mashctl endpoint list
```

### --audit-users: two servers whose users keep changing

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config     ok
server     ok    tls://localhost:4222
sign-in    ok
storage    ok
endpoints  ok    3 records, 3 inboxes
keys       FAIL  server s1's connected users kept changing
                 server NABCDEFGHI...LMNOPQRSTUV's connected users kept changing
                 read 3 times

1 problem.
hint: wait a minute, then run mashctl doctor --audit-users again
```

### No server is configured

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config     FAIL  no server is configured
server     skip  needs the config
sign-in    skip  needs the server
storage    skip  needs sign-in
endpoints  skip  needs the storage
keys       skip  needs sign-in

1 problem.
hint: mashctl init --config <file>
```

## JSON

With `--json` the result is this shape. The values are an example.

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

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

What is a warning and what is a problem. Warnings: a lease with no endpoint, a
retired name seen in use, and a missing record store (issued, adoption or
retired); the fix for a missing store is to run mashctl init --config
&lt;file&gt; again, which is safe. A problem: an endpoint whose credentials
were being cancelled and never finished; the fix is to run that mashctl
endpoint delete again.

## What it replaces

In 0.5.5, these checks were mashctl audit and mashctl issued check. mashctl
issued list has no command of its own any more: one endpoint's issue record is
in mashctl endpoint show.
