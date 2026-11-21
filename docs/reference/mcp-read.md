Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

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

The call returns this text.

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

The call returns this text with `isError` set.

```text
error: acme.demo.alice has no message 9
hint: mash inbox
```

### A message that was refused

The call returns this text with `isError` set.

```text
error: message 12 was refused: it was not verified
hint: mash doctor
```
