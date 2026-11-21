Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint suspend

Refuse new mail to an endpoint. Its stored mail and its credentials are
untouched.

Release: 0.5.6. Group: Endpoints.

## Usage

```text
mashctl endpoint suspend <address> [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | The endpoint, as owner.group.name. |

## Examples

```text
mashctl endpoint suspend acme.demo.bob
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Suspended

Stream: stdout. Exit code: 0.

```text
Suspended endpoint acme.demo.bob
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "address": "acme.demo.bob",
  "state": "suspended"
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.
