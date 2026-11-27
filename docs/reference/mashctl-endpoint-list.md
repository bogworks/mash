Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint list

List endpoints.

Release: 0.6.1. Group: Endpoints.

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
OWNER  GROUP  NAME   ADDRESS          STATE
acme   demo   alice  acme.demo.alice  online
acme   demo   bob    acme.demo.bob    offline
```

### With --wide

Stream: stdout. Exit code: 0.

```text
OWNER  GROUP  ADDRESS          STATE    CREATED           WAITING  LABELS
acme   demo   acme.demo.alice  online   2026-11-19 09:40  0        team=blue
acme   demo   acme.demo.bob    offline  2026-11-19 09:41  1        -
```

### None

Stream: stdout. Exit code: 0.

```text
No endpoints.
```

### --json

Stream: stdout. Exit code: 0.

```text
{
  "endpoints": [
    {
      "address": "acme.demo.alice",
      "owner": "acme",
      "group": "demo",
      "name": "alice",
      "online": true,
      "suspended": false
    },
    {
      "address": "acme.demo.bob",
      "owner": "acme",
      "group": "demo",
      "name": "bob",
      "online": false,
      "suspended": false
    }
  ]
}
```

### --json, suspended while online

Stream: stdout. Exit code: 0.

```text
{
  "endpoints": [
    {
      "address": "acme.demo.alice",
      "owner": "acme",
      "group": "demo",
      "name": "alice",
      "online": true,
      "suspended": true
    },
    {
      "address": "acme.demo.bob",
      "owner": "acme",
      "group": "demo",
      "name": "bob",
      "online": false,
      "suspended": false
    }
  ]
}
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
      "online": true,
      "suspended": false
    }
  ]
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

Every row prints its owner and group, so a line can be grepped or cut on its
own. With --wide, ADDRESS takes the place of NAME (the address carries the
name).

## What it replaces

In 0.5.5, this was also reachable as mashctl list, which is removed.
