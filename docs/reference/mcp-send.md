Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# MCP tool send

Send mail to another endpoint by its address, owner.group.name. Choose the
kind for what you need from the recipient: blocker (you cannot continue
without them), question (you need an answer) and task (you are asking them to
do something; the default) wake an idle recipient; result (the outcome of
something they asked for) and fyi (information, no answer needed) wait for
their next turn. Use urgent only for what cannot wait: it interrupts. When you
answer a message, set reply_to to its number so the recipient sees which one.
When you pass a message on, set relay to its number. Do not send a reply that
only acknowledges. A reply marked question that asks nothing is sent as fyi.
Use id when you retry after a timeout, so the message is not delivered twice.

Release: 0.5.9. Command line equivalent: `mash send`.

## Parameters

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `address` | string | yes | Who to send to, as owner.group.name. |
| `message` | string | yes | The text. |
| `kind` | string | no | What sort of message this is. blocker, question and task wake an idle recipient; result and fyi wait for its next turn. Default: task. One exception: a reply (--reply-to) marked question whose text has no question mark and is at most 280 characters asks nothing, so it is sent as fyi and the output says so. One of: blocker, question, task, result, fyi. |
| `urgent` | boolean | no | Deliver at once, into a running turn where the agent host allows it. |
| `reply_to` | integer | no | The number of the message in your inbox that this answers. Signed, so the recipient can trust the link. |
| `relay` | integer | no | The number of a message in your inbox that this passes on. Signed, so the recipient can trust the link. |
| `id` | string | no | A key of your own. A second send with the same key to the same address within two minutes stores nothing new, so a retry cannot deliver twice. |

## Structured result

The structured content of a successful result. The values are an example.

```json
{
  "to": "acme.demo.alice",
  "number": 42,
  "kind": "task",
  "urgent": false,
  "duplicate": false,
  "retagged": false
}
```

## Text result

### Sent

The call returns this text.

```text
Mail sent (number 42).
```

## Errors

### An address in another form

The call returns this text with `isError` set.

```text
error: acme_demo_alice is not an address; write it as acme.demo.alice
hint: mash send --help
```

### No such endpoint

The call returns this text with `isError` set.

```text
error: no endpoint has the address acme.demo.alise
hint: mash who
```

### The recipient is suspended or was deleted

The call returns this text with `isError` set.

```text
error: acme.demo.alice is suspended or was deleted; nothing was sent
hint: mash who
```

### No credentials here

The call returns this text with `isError` set.

```text
error: acme.demo.bob has no credentials here: ~/.config/mash/acme.demo.bob.creds is missing
hint: mash doctor
```

### A deleted endpoint here

The call returns this text with `isError` set.

```text
error: acme.demo.bob: this endpoint was deleted; its address cannot be used again
hint: mash doctor
```

### Not signed in

The call returns this text with `isError` set.

```text
error: acme.demo.bob cannot sign in: the server refused its credentials
hint: mash doctor
```
