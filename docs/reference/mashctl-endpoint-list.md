Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint list

List endpoints.

Release: 0.5.6. Group: Endpoints.

## Usage

```text
mashctl endpoint list [prefix] [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--wide` |  | More columns: created, messages waiting and labels. May pass 80 columns. |

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `prefix` | no | Only endpoints under this owner (acme) or this owner and group (acme.demo). The same rule as mash who. |

## Examples

```text
mashctl endpoint list
mashctl endpoint list acme.demo
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Endpoints

Stream: stdout. Exit code: 0.

```text
OWNER  GROUP  NAME   STATE
acme   demo   alice  online
              bob    offline
       ops    carol  suspended
```

### With --wide

Stream: stdout. Exit code: 0.

```text
OWNER  GROUP  NAME   STATE      CREATED           WAITING  LABELS
acme   demo   alice  online     2026-11-19 09:40  0        team=blue
              bob    offline    2026-11-19 09:41  1        -
       ops    carol  suspended  2026-11-18 16:02  0        -
```

### None

Stream: stdout. Exit code: 0.

```text
No endpoints.
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "endpoints": [
    {
      "address": "acme.demo.alice",
      "owner": "acme",
      "group": "demo",
      "name": "alice",
      "state": "online"
    }
  ]
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

A repeated owner or group is left blank, so the eye finds where one ends.
