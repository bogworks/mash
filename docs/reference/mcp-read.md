Generated from spec/*.json by scripts/refgen. Do not edit.

# MCP tool read

Show one message in full.

Release: 0.5.6. Command line equivalent: `mash read`.

## Parameters

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `number` | integer | yes | The message's number, from mash inbox. |

## Structured result

The structured content of a successful result. The values are an example.

```json
{
  "number": 2,
  "from": "acme.demo.bob",
  "sent": "2026-11-19T09:44:04Z",
  "trust": "verified",
  "kind": "question",
  "urgent": true,
  "reply_to": null,
  "relay": null,
  "body": "ready?"
}
```

## Text result

### A message

Stream: stdout. Exit code: 0.

```text
number:   2
from:     acme.demo.bob
sent:     8s ago
trust:    verified
kind:     question
urgent:   yes

> ready?
```

## Errors

### No such message

Stream: stderr. Exit code: 1.

```text
error: acme.demo.alice has no message 9
hint: mash inbox
```
