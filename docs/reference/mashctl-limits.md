Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl limits

Show or apply the storage limits.

Release: 0.5.6. Group: Mesh.

## Usage

```text
mashctl limits <command> [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Commands

| Command | Description |
| --- | --- |
| [`mashctl limits show`](mashctl-limits-show.md) | Print the limits in force. |
| [`mashctl limits apply`](mashctl-limits-apply.md) | Raise existing inboxes to the configured limits. Never lowers one. |
