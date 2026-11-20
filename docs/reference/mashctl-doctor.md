Generated from spec/*.json by scripts/refgen. Do not edit.

# mashctl doctor

Check the server, mash's storage on it and every endpoint's records, and say
how to fix what is wrong. Changes nothing.

Release: 0.5.6. Group: Mesh.

## Usage

```text
mashctl doctor [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--strict` |  | Exit 1 on a warning too. |

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
| 1 | It failed; one line on stderr says why. |
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
keys       ok    every connected key was issued here

No problems.
```

### A problem

Stream: stdout. Exit code: 1.

```text
endpoints  FAIL  1 inbox has no endpoint record: acme.demo.dave

1 problem.
hint: mashctl endpoint show acme.demo.dave
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
retired); the fix for a missing store is to run mashctl init --config <file>
again, which is safe. A problem: an endpoint whose credentials were being
cancelled and never finished; the fix is to run that mashctl endpoint delete
again.

## What it replaces

Takes over mashctl audit and mashctl issued check. mashctl issued list has no
command of its own any more: one endpoint's issue record is in mashctl
endpoint show.
