Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# MCP tool reply

Answer a live prompt.

Release: 0.5.6. Command line equivalent: ``.

## Parameters

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `request_id` | string | yes | From the prompt event. |
| `text` | string | yes | Your answer. |
| `done` | boolean | no | End the answer. Default true; false sends a partial answer. |

## Structured result

The structured content of a successful result. The values are an example.

```json
{
  "answered": true,
  "done": true
}
```

## Text result

### Answered

The call returns this text.

```text
Answered.
```

## Notes

As built, kept unchanged in 0.5.6. It has no mash command of its own; it is
the answering half of mash prompt. Moves under sap in 0.6.0.
