Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint set

Set a value on an endpoint's record.

Release: 0.6.1. Group: Endpoints.

## Usage

```text
mashctl endpoint set <address> <key=value...> [options]
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
| `key=value` | yes | One or more. Keys: role, description, host, note, capability (a list, with commas: capability=review,deploy), label.&lt;name&gt;. |

## Examples

```text
mashctl endpoint set acme.demo.bob role=reviewer label.team=blue
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
  "set": {
    "role": "reviewer"
  },
  "dry_run": false
}
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "address": "acme.demo.bob",
  "set": {
    "role": "reviewer"
  },
  "dry_run": false
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## What it replaces

In 0.5.5, this was set-meta and label.
