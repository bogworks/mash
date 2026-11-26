Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint inbox

List what is in an endpoint's inbox, without showing any message's text.

Release: 0.5.9. Group: Endpoints.

## Usage

```text
mashctl endpoint inbox <address> [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--limit` | `<n>` | How many, newest last. Default 20. |
| `--after` | `<number>` | Only messages after this number. The same option as mash inbox. |

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | The endpoint, as owner.group.name. |

## Examples

```text
mashctl endpoint inbox acme.demo.bob
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Messages

Stream: stdout. Exit code: 0.

```text
2 messages for acme.demo.bob, 1 not delivered.

NUMBER  FROM   SENT    TRUST     DELIVERED  KIND  SIZE  REASON
7       alice  1m ago  verified  yes        task  48 B
8       ?      1m ago  absent    no         task  61 B  not verified
```

### A refused message

Stream: stdout. Exit code: 0.

```text
2 messages for acme.demo.bob, 1 not delivered, 1 refused as malformed.

NUMBER  FROM   SENT    TRUST     DELIVERED  KIND  SIZE  REASON
7       alice  1m ago  verified  yes        task  48 B
8       ?      1m ago  refused   no         -     61 B  two sender headers
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "address": "acme.demo.bob",
  "total": 2,
  "undelivered": 1,
  "refused": 1,
  "messages": [
    {
      "number": 7,
      "from": "acme.demo.alice",
      "trust": "verified",
      "delivered": true,
      "kind": "task",
      "size": 48,
      "when": "2026-11-26T15:03:05Z"
    },
    {
      "number": 8,
      "from": "?",
      "trust": "absent",
      "delivered": false,
      "kind": "task",
      "size": 61,
      "when": "2026-11-26T15:04:05Z",
      "reason": "not verified"
    }
  ]
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

An administrator sees everything stored. FROM is the address a message claims,
and '?' when it names no sender at all (the same '?' in the JSON); messages
are delivered in order, so a delivered message never follows an undelivered
one; TRUST says whether that claim was proven, in mash's own labels: verified,
unknown-signer, claimed, absent, stale, forged or refused. `refused` is a
message with a header fault: its REASON is the class (the same words as mash
inbox shows), and the JSON has the same class as 'reason', present on a
refused message and on a message that was not verified, absent on the others.
'refused' in the JSON object counts the messages mashctl itself finds
malformed that are still in the stream, as the header does; a message that was
not verified is not in it, keeps its trust label and gets `not verified` as
its reason. DELIVERED is yes or no, and means only that the endpoint took the
message off the server. Whether the endpoint then showed it or refused it is
decided on the endpoint's own machine and is not something mashctl can see: by
default an endpoint refuses everything that is not verified, so a 'yes' beside
any other trust label most likely means it was refused. In --json, `when` is the time the message was
stored, in RFC 3339 and UTC, with a fraction of a second when the server
stored one (for example 2026-11-26T15:04:05Z or 2026-11-26T15:04:05.12Z).

## What it replaces

In 0.5.5, this was mashctl inbox inspect, with --from and --to. Those are
replaced by --after, the option mash inbox has.
