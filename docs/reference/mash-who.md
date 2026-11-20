Generated from spec/*.json by scripts/refgen. Do not edit.

# mash who

List the endpoints you can see, and which are online.

Release: 0.5.6. Group: Everyday.

## Usage

```text
mash who [prefix] [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--online` |  | Only endpoints that are online. |
| `--offline` |  | Only endpoints that are offline. |
| `--wide` |  | More columns: role, host and description, where recorded. May pass 80 columns. |

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `prefix` | no | Only endpoints under this owner (acme) or this owner and group (acme.demo), or the one endpoint with this full address. acme.* and acme.demo.* are accepted and mean the same as acme and acme.demo. |

## Examples

```text
mash who
mash who acme.demo --online
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
ADDRESS          STATE
acme.demo.alice  online
acme.demo.bob    offline
```

### With --wide

Stream: stdout. Exit code: 0.

```text
ADDRESS          STATE    ROLE      HOST      DESCRIPTION
acme.demo.alice  online   reviewer  myserver  reviews pull requests
acme.demo.bob    offline  writer    myserver  -
```

### Nothing under the prefix

Stream: stdout. Exit code: 0.

```text
No endpoints under acme.lab
```

### None at all

Stream: stdout. Exit code: 0.

```text
No endpoints
```

### None online

Stream: stdout. Exit code: 0.

```text
No online endpoints
```

### None offline under the prefix

Stream: stdout. Exit code: 0.

```text
No offline endpoints under acme.lab
```

### A prefix that is not part of an address

Stream: stderr. Exit code: 2.

```text
error: Acme.Demo is not an owner, an owner.group or an address
hint: mash who --help
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "endpoints": [
    {
      "address": "acme.demo.alice",
      "online": true,
      "role": "reviewer",
      "host": "myserver",
      "description": "reviews pull requests",
      "capability": [
        "review"
      ],
      "labels": {
        "team": "blue"
      }
    }
  ]
}
```

## MCP tool

[`who`](mcp-who.md)

## Notes

A prefix follows the address the way a domain name does: acme is everything
under acme, acme.demo is everything under acme.demo, and acme.demo.bob is that
one endpoint, never acme.demo.bobby. There is no other pattern matching:
capitals are refused, acme.demo.bob.* is refused, and an empty prefix means
none. The filters by role, capability, label and free text that today's
command and tool have are gone in 0.5.6; the JSON and the MCP tool's
structured result carry role, host, description, capability and labels for
every endpoint, so a caller can still choose by them.

## What it replaces

Today the MCP tool always returns the long form, the CLI has three output
modes, and matching is by substring of several fields.
