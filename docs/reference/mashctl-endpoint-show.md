Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint show

Show one endpoint.

Release: 0.5.6. Group: Endpoints.

## Usage

```text
mashctl endpoint show <address> [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | The endpoint, as owner.group.name. |

## Examples

```text
mashctl endpoint show acme.demo.bob
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### An endpoint

Stream: stdout. Exit code: 0.

```text
address:    acme.demo.bob
state:      offline
created:    2026-11-19 09:40 UTC
waiting:    1 message
labels:     team=blue
public key: UDXU4RCSV3GJRXWQ4PNDKYEL7ZBSMSGGTJYNDQGHFM4QWHBRD3ZTQ4LX
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "address": "acme.demo.bob",
  "state": "offline",
  "created": "2026-11-19T09:40:00Z",
  "waiting": 1,
  "labels": {
    "team": "blue"
  },
  "public_key": "UDXU4RCSV3GJRXWQ4PNDKYEL7ZBSMSGGTJYNDQGHFM4QWHBRD3ZTQ4LX"
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

role, host, description, capability and note each appear only when set.
'online' here means something holds the endpoint's lease, which lapses 90
seconds after its holder stops; mash who reads the endpoint's own heartbeat
over the same 90 seconds. The two answer the same question and can differ for
up to that long.
