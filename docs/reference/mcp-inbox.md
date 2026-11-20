Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# MCP tool inbox

List the messages this endpoint has received.

Release: 0.5.6. Command line equivalent: `mash inbox`.

## Parameters

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `new` | boolean | no | Only new messages: those that have not yet reached a session. |
| `limit` | integer | no | How many to show, newest last. Default 20, at most 100. |
| `after` | integer | no | Only messages after this number. |
| `wide` | boolean | no | More columns: the full sender address, trust and size. May pass 80 columns. |

## Structured result

The structured content of a successful result. The values are an example.

```json
{
  "endpoint": "acme.demo.alice",
  "total": 2,
  "new": 2,
  "urgent": 1,
  "wake_count": 15,
  "over_wake_count": false,
  "wake_age": "1h",
  "oldest_waiting": "10s",
  "over_wake_age": false,
  "messages": [
    {
      "number": 1,
      "from": "acme.demo.bob",
      "sent": "2026-11-19T09:44:02Z",
      "state": "new",
      "urgent": false,
      "kind": "task",
      "trust": "verified",
      "preview": "lunch at noon?"
    }
  ]
}
```

## Text result

### Messages

Stream: stdout. Exit code: 0.

```text
2 messages for acme.demo.alice, 2 new, 1 urgent.

NUMBER  FROM  WHEN     STATE  URGENT  KIND      MESSAGE
1       bob   10s ago  new    no      task      lunch at noon?
2       bob   8s ago   new    yes     question  ready?
```
