Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint unset

Remove a value from an endpoint's record.

Release: 0.5.9. Group: Endpoints.

## Usage

```text
mashctl endpoint unset <address> <key...> [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--dry-run` |  | Print what would change, and change nothing. |

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | The endpoint, as owner.group.name. |
| `key` | yes | One or more keys. capability removes every capability; to remove one, set the list again without it. |

## Examples

```text
mashctl endpoint unset acme.demo.bob label.team
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Updated

Stream: stdout. Exit code: 0.

```text
Updated endpoint acme.demo.bob
```

### --json

Stream: stdout. Exit code: 0.

```text
{
  "address": "acme.demo.bob",
  "unset": [
    "role"
  ],
  "dry_run": false
}
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "address": "acme.demo.bob",
  "unset": [
    "role"
  ],
  "dry_run": false
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## What it replaces

In 0.5.5, this was set-meta with a '-' value, and unlabel.
