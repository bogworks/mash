Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl version

Print the version. It can also be run as --version.

Release: 0.5.9. Group: Mesh.

## Usage

```text
mashctl version [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Examples

```text
mashctl version
mashctl --version
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
mashctl v0.5.6 (3f9a41c)
```

### A build with no version stamped

Stream: stdout. Exit code: 0.

```text
mashctl v0.0.0-dev (unknown)
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "version": "0.5.6",
  "commit": "3f9a41c2e0b7d5a1c4e08b2d77f3a95e1b6c4098"
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## What it replaces

In 0.5.5, mashctl version printed no commit.
