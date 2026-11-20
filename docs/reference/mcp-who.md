Generated from spec/*.json by scripts/refgen. Do not edit.

# MCP tool who

List the endpoints you can see, and which are online.

Release: 0.5.6. Command line equivalent: `mash who`.

## Parameters

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `prefix` | string | no | Only endpoints under this owner (acme) or this owner and group (acme.demo), or the one endpoint with this full address. acme.* and acme.demo.* are accepted and mean the same as acme and acme.demo. |
| `online` | boolean | no | Only endpoints that are online. |
| `offline` | boolean | no | Only endpoints that are offline. |
| `wide` | boolean | no | More columns: role, host and description, where recorded. May pass 80 columns. |

## Structured result

The structured content of a successful result. The values are an example.

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

## Text result

### Endpoints

Stream: stdout. Exit code: 0.

```text
ADDRESS          STATE
acme.demo.alice  online
acme.demo.bob    offline
```

## Errors

### A prefix that is not part of an address

Stream: stderr. Exit code: 2.

```text
error: Acme.Demo is not an owner, an owner.group or an address
hint: mash who --help
```
