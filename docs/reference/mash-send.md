Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash send

Send a message to an endpoint.

Release: 0.5.8. Group: Everyday.

## Usage

```text
mash send <address> [message] [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--kind` | `<kind>`: blocker, question, task, result, fyi | What sort of message this is. blocker, question and task wake an idle recipient; result and fyi wait for its next turn. Default: task. One exception: a reply (--reply-to) marked question whose text has no question mark and is at most 280 characters asks nothing, so it is sent as fyi and the output says so. |
| `--urgent` |  | Deliver at once, into a running turn where the agent host allows it. |
| `--reply-to` | `<number>` | The number of the message in your inbox that this answers. Signed, so the recipient can trust the link. |
| `--relay` | `<number>` | The number of a message in your inbox that this passes on. Signed, so the recipient can trust the link. |
| `--id` | `<key>` | A key of your own. A second send with the same key to the same address within two minutes stores nothing new, so a retry cannot deliver twice. |
| `--file` | `<path>` | Read the message text from a file. Use - for standard input; if nothing arrives there within 15 minutes the command gives up. |

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | Who to send to, as owner.group.name. |
| `message` | no | The text. Leave it out to read the text from --file or from standard input. |

## Examples

```text
mash send acme.demo.alice "lunch at noon?"
mash send acme.demo.alice --urgent --kind question "ready?"
mash send acme.demo.alice --reply-to 2 "yes"
git log -1 | mash send acme.demo.alice --kind fyi --file -
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Sent

Stream: stdout. Exit code: 0.

```text
Mail sent (number 42).
```

### Sent, urgent, with a kind

Stream: stdout. Exit code: 0.

```text
Urgent question mail sent (number 43).
```

### With --detail

Stream: stdout. Exit code: 0.

```text
Mail sent (number 42).
to:      acme.demo.alice
kind:    task
urgent:  no
```

### A reply marked question that asks nothing

Stream: stdout. Exit code: 0.

```text
Mail sent as fyi (number 44): a reply marked question that asks nothing.
```

### Repeated --id

Stream: stdout. Exit code: 0.

```text
Mail already sent; nothing new stored.
```

### An address in another form

Stream: stderr. Exit code: 2.

```text
error: acme_demo_alice is not an address; write it as acme.demo.alice
hint: mash send --help
```

### Nothing arrives on standard input

Stream: stderr. Exit code: 1.

```text
error: no message text arrived on standard input
```

### No such endpoint

Stream: stderr. Exit code: 1.

```text
error: no endpoint has the address acme.demo.alise
hint: mash who
```

### The recipient is suspended or was deleted

Stream: stderr. Exit code: 1.

```text
error: acme.demo.alice is suspended or was deleted; nothing was sent
hint: mash who
```

### No credentials here

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob has no credentials here: ~/.config/mash/acme.demo.bob.creds is missing
hint: mash doctor
```

### Not signed in

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob cannot sign in: the server refused its credentials
hint: mash doctor
```

### A deleted endpoint here

Stream: stderr. Exit code: 1.

```text
error: acme.dev.bob: this endpoint was deleted; its address cannot be used again
hint: mash doctor
```

## JSON

With `--json` the result is this shape. The values are an example.

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

## MCP tool

[`send`](mcp-send.md)

## Notes

send has no --dry-run: sending is the act itself, not a change to the mesh.
The number is the message's number in the recipient's inbox. It is printed
because the sender can find it nowhere else, and needs it: an answer arrives
marked 'reply to: 42'.

## What it replaces

In 0.5.5, the CLI and the MCP tool differed: only the MCP tool had reply-to,
relay and id; only the CLI had --file. The receipt wording differed between
them. The underscore form of an address was accepted in 0.5.5 and is refused
from 0.5.6.
