Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash read

Show one message in full.

Release: 0.5.8. Group: Everyday.

## Usage

```text
mash read <number> [options]
```

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `number` | yes | The message's number, from mash inbox. |

## Examples

```text
mash read 2
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

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

### An answer to something you sent

Stream: stdout. Exit code: 0.

```text
number:   5
from:     acme.demo.alice
sent:     1m ago
trust:    verified
kind:     result
urgent:   no
reply to: 42

> yes
```

### No such message

Stream: stderr. Exit code: 1.

```text
error: acme.demo.alice has no message 9
hint: mash inbox
```

### A message that was refused

Stream: stderr. Exit code: 1.

```text
error: message 12 was refused: it was not verified
hint: mash doctor
```

## JSON

With `--json` the result is this shape. The values are an example.

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

## MCP tool

[`read`](mcp-read.md)

## Notes

What 'read' means. A message is 'new' until the server has been told it
reached the endpoint's session: when the hook showed it to the agent, or the
agent opened it with the read tool. Until then it is waiting. mash read at a
shell shows a message but, with no session online, cannot mark it; it stays
'new' until a session next comes online. That limit is tracked in mash's
backlog. The message's text is printed with '&gt; ' before every line, as
quoted mail is, and with control characters escaped: everything after '&gt; '
is the sender's words, and nothing the sender writes can pass for mash's own
output or for an instruction to an agent reading it. The command and the tool
quote alike. --json carries the text unquoted in 'body'.

## What it replaces

In 0.5.5, read worked the same way, and 0.5.6 adds no new mechanism. The
labels are the JSON field names.
