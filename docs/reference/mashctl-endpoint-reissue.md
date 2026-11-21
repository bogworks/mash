Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint reissue

Give an endpoint new credentials and cancel the old ones.

Release: 0.5.6. Group: Endpoints.

## Usage

```text
mashctl endpoint reissue <address> [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--out` | `<path>` | Write a join file for another machine. |
| `--public-key` | `<path>` | Sign this public key. Needs --out. |
| `--dry-run` |  | Print what would change, and change nothing. |

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | The endpoint, as owner.group.name. |

## Examples

```text
mashctl endpoint reissue acme.demo.bob --out acme.demo.bob.join
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Reissued

Stream: stdout. Exit code: 0.

```text
Reissued credentials for acme.demo.bob
Join file: acme.demo.bob.join
```

### With --detail

Stream: stdout. Exit code: 0.

```text
Reissued credentials for acme.demo.bob
Join file: acme.demo.bob.join
servers acknowledged: 2
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "address": "acme.demo.bob",
  "join_file": "acme.demo.bob.join"
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

It takes the same options as create.
