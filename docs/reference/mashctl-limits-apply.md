Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl limits apply

Raise existing inboxes to the configured limits. Never lowers one.

Release: 0.5.6. Group: Mesh.

## Usage

```text
mashctl limits apply [prefix] [options]
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
| `prefix` | no | Only inboxes under this owner (acme), this owner and group (acme.demo), or the one endpoint with this address. Default: all. |

## Examples

```text
mashctl limits apply
mashctl limits apply acme.demo
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Applied

Stream: stdout. Exit code: 0.

```text
Raised 2 inboxes
```

### Nothing to do

Stream: stdout. Exit code: 0.

```text
Nothing to raise
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "raised": 2
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

When it is needed. An inbox gets its limits when its endpoint is created. If
you later raise the limits in config.json, inboxes that already exist keep the
old ones until you run this. To be looked at again by 2026-11-27: whether a
change to the limits should simply apply everywhere by itself.

## What it replaces

Today mashctl limits.
