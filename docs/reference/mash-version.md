Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash version

Print the version. It can also be run as --version.

Release: 0.6.1. Group: Everyday.

## Usage

```text
mash version [options]
```

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Examples

```text
mash version
mash --version
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Version

Stream: stdout. Exit code: 0.

```text
mash v0.5.6 (3f9a41c)
```

### A build with no version stamped

Stream: stdout. Exit code: 0.

```text
mash v0.0.0-dev (unknown)
```

### --json, no plugin

Stream: stdout. Exit code: 0.

```text
{
  "version": "0.6.1",
  "commit": "3f9a41c2e0b7d5a1c4e08b2d77f3a95e1b6c4098",
  "plugin": null
}
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "version": "0.6.1",
  "commit": "3f9a41c2e0b7d5a1c4e08b2d77f3a95e1b6c4098",
  "plugin": null
}
```

## MCP tool

[`version`](mcp-version.md)

## Notes

The short commit in brackets, seven characters, tells two builds of the same
version apart. --json carries the whole commit, so a tool that pins a version
can compare it exactly.

## What it replaces

In 0.5.5, mashctl printed no commit, and the MCP tool added three more lines.
