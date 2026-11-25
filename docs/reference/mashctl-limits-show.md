Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl limits show

Print the limits in force.

Release: 0.5.9. Group: Mesh.

## Usage

```text
mashctl limits show [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Examples

```text
mashctl limits show
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Limits

Stream: stdout. Exit code: 0.

```text
inbox size:  16 MiB
inbox age:   30 days
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "inbox_bytes": 16777216,
  "inbox_age": "720h"
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

These are the server's storage limits. When a session is woken for a backlog
is an endpoint's own setting: see mash serve.

## What it replaces

In 0.5.5, this was mashctl limits --show. The numbers shown are examples; the
defaults are the code's.
