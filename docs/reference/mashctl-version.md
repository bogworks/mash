Generated from spec/*.json by scripts/refgen. Do not edit.

# mashctl version

Print the version.

Release: 0.5.6. Group: Mesh.

## Usage

```text
mashctl version [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Examples

```text
mashctl version
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
  "commit": "3f9a41c"
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## What it replaces

Today prints no commit.
