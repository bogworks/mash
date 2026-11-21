Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint resume

Undo suspend.

Release: 0.5.6. Group: Endpoints.

## Usage

```text
mashctl endpoint resume <address> [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | The endpoint, as owner.group.name. |

## Examples

```text
mashctl endpoint resume acme.demo.bob
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Resumed

Stream: stdout. Exit code: 0.

```text
Resumed endpoint acme.demo.bob
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "address": "acme.demo.bob",
  "state": "offline"
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.
