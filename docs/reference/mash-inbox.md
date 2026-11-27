Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash inbox

List the messages this endpoint has received.

Release: 0.6.1. Group: Everyday.

## Usage

```text
mash inbox [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--new` |  | Only new messages: those that have not yet reached a session. |
| `--limit` | `<n>` | How many to show, newest last. Default 20, at most 100. |
| `--after` | `<number>` | Only messages after this number. |
| `--wide` |  | More columns: the full sender address, trust and size. May pass 80 columns. |

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Examples

```text
mash inbox
mash inbox --new
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
2 messages for acme.demo.alice, 2 new, 1 urgent.

NUMBER  FROM           SENT     STATE  URGENT  KIND      MESSAGE
1       acme.demo.bob  10s ago  new    no      task      lunch at noon?
2       acme.demo.bob  8s ago   new    yes     question  ready?
```

### A refused message

Stream: stdout. Exit code: 0.

```text
3 messages for acme.demo.alice, 1 new, 0 urgent, 1 refused.

NUMBER  FROM           SENT     STATE    URGENT  KIND  MESSAGE
1       acme.demo.bob  2h ago   read     no      task  lunch at noon?
2       acme.demo.bob  10s ago  new      no      task  ready?
3       ?              1h ago   refused  -       -     control character in na…
```

### Every kind side by side

Stream: stdout. Exit code: 0.

```text
17 messages for o.dev.bob, 0 new, 0 urgent, 12 refused.

NUMBER  FROM         SENT    STATE    URGENT  KIND  MESSAGE
1       o.dev.alice  3m ago  read     no      task  verified: real mash send f…
2       UCNY…4UHK    3m ago  read     no      task  claimed: header without a …
3       UCVG…3VVW    3m ago  read     no      task  unknown-signer: signed by …
4       ?            3m ago  read     no      task  absent: no Agent-Sender he…
5       o.dev.frank  3m ago  read     no      task  stale: signed an hour befo…
6       ?            2m ago  refused  -       -     not verified
7       ?            2m ago  refused  -       -     header too long
8       ?            2m ago  refused  -       -     time not in UTC
9       ?            2m ago  refused  -       -     control character in name
10      ?            2m ago  refused  -       -     name too long
11      ?            2m ago  refused  -       -     broken character in name
12      ?            1m ago  refused  -       -     a field appears twice
13      ?            1m ago  refused  -       -     space in subject
14      ?            1m ago  refused  -       -     signature unreadable
15      ?            1m ago  refused  -       -     empty sender header
16      ?            1m ago  refused  -       -     two sender headers
17      ?            1m ago  refused  -       -     unreadable sender header
```

### --json, a refused message

Stream: stdout. Exit code: 0.

```text
{
 "endpoint": "acme.demo.alice",
 "total": 3,
 "new": 1,
 "urgent": 0,
 "refused": 1,
 "exact": true,
 "wake_count": 15,
 "over_wake_count": false,
 "wake_age": "1h",
 "oldest_waiting": "10s",
 "over_wake_age": false,
 "messages": [
  {
   "number": 1,
   "from": "acme.demo.bob",
   "sent": "2026-11-19T07:44:12Z",
   "state": "read",
   "urgent": false,
   "kind": "task",
   "trust": "verified",
   "preview": "lunch at noon?"
  },
  {
   "number": 2,
   "from": "acme.demo.bob",
   "sent": "2026-11-19T09:44:02Z",
   "state": "new",
   "urgent": false,
   "kind": "task",
   "trust": "verified",
   "preview": "ready?"
  },
  {
   "number": 3,
   "from": "?",
   "sent": "2026-11-19T08:44:12Z",
   "state": "refused",
   "urgent": false,
   "kind": "",
   "trust": "refused",
   "preview": "",
   "reason": "control character in name"
  }
 ]
}

```

### A long message, or one of several lines

Stream: stdout. Exit code: 0.

```text
NUMBER  FROM           SENT    STATE  URGENT  KIND    MESSAGE
3       acme.demo.bob  1m ago  new    no      result  The build passed on all …
```

### With --wide

Stream: stdout. Exit code: 0.

```text
2 messages for acme.demo.alice, 2 new, 1 urgent.

NUMBER  FROM           SENT     STATE  URGENT  KIND      TRUST     SIZE  MESSAGE
1       acme.demo.bob  10s ago  new    no      task      verified  14 B  lunch at noon?
2       acme.demo.bob  8s ago   new    yes     question  verified  6 B   ready?
```

### Empty

Stream: stdout. Exit code: 0.

```text
No messages for acme.demo.alice.
```

### The oldest has waited longer than the wake age

Stream: stdout. Exit code: 0.

```text
3 messages for acme.demo.alice, 3 new, 0 urgent.
The oldest has waited 1h 12m.

NUMBER  FROM           SENT  STATE  URGENT  KIND  MESSAGE
...
```

### More waiting than the wake count

Stream: stdout. Exit code: 0.

```text
17 messages for acme.demo.alice, 17 new, 0 urgent.
More than 15 are waiting.

NUMBER  FROM  SENT  STATE  URGENT  KIND  MESSAGE
...
```

### A sender outside your group

Stream: stdout. Exit code: 0.

```text
NUMBER  FROM            SENT    STATE  URGENT  KIND  MESSAGE
3       acme.ops.carol  2m ago  new    no      fyi   deploy finished
```

### A deleted endpoint here

Stream: stderr. Exit code: 1.

```text
error: acme.dev.bob: this endpoint was deleted; its address cannot be used again
hint: mash doctor
```

### Counts include mail read out of order, with --detail

Stream: stdout. Exit code: 0.

```text
About 2 messages for acme.demo.alice, about 1 new, about 1 urgent.
exact: no (mail read out of order may still be counted as urgent)

NUMBER  FROM           SENT     STATE  URGENT  KIND      MESSAGE
1       acme.demo.bob  10s ago  new    no      task      lunch at noon?
2       acme.demo.bob  8s ago   new    yes     question  ready?
```

### Counts without serve running

Stream: stdout. Exit code: 0.

```text
About 2 messages for acme.demo.alice, about 2 new, about 1 urgent.

NUMBER  FROM           SENT     STATE  URGENT  KIND      MESSAGE
1       acme.demo.bob  10s ago  new    no      task      lunch at noon?
2       acme.demo.bob  8s ago   new    yes     question  ready?
```

### Counts are capped

Stream: stdout. Exit code: 0.

```text
About 1000 messages for acme.demo.alice, about 90 new, about 4 urgent.
```

### Counts without serve running, with --detail

Stream: stdout. Exit code: 0.

```text
About 2 messages for acme.demo.alice, about 2 new, about 1 urgent.
exact: no (mash serve is not running, so unverified mail is counted)

NUMBER  FROM           SENT     STATE  URGENT  KIND      MESSAGE
1       acme.demo.bob  10s ago  new    no      task      lunch at noon?
2       acme.demo.bob  8s ago   new    yes     question  ready?
```

### Counts are capped, with --detail

Stream: stdout. Exit code: 0.

```text
About 1000 messages for acme.demo.alice, about 90 new, about 4 urgent.
exact: no (only the oldest 1000 messages were checked)

NUMBER  FROM           SENT     STATE  URGENT  KIND      MESSAGE
1       acme.demo.bob  10s ago  new    no      task      lunch at noon?
2       acme.demo.bob  8s ago   new    yes     question  ready?
```

### Counts are capped and serve is not running, with --detail

Stream: stdout. Exit code: 0.

```text
About 1000 messages for acme.demo.alice, about 90 new, about 4 urgent.
exact: no (only the oldest 1000 messages were checked)
exact: no (mash serve is not running, so unverified mail is counted)

NUMBER  FROM           SENT     STATE  URGENT  KIND      MESSAGE
1       acme.demo.bob  10s ago  new    no      task      lunch at noon?
2       acme.demo.bob  8s ago   new    yes     question  ready?
```

### The class strings

Stream: stdout. Exit code: 0.

```text
not verified
header too long
time not in UTC
control character in name
name too long
broken character in name
a field appears twice
space in subject
signature unreadable
empty sender header
two sender headers
unreadable sender header
header is not a JSON object
version missing
version is not a number
field is not text
account or user not valid
signed header incomplete
message id not valid
time not valid
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "endpoint": "acme.demo.alice",
  "total": 2,
  "new": 2,
  "urgent": 1,
  "refused": 0,
  "exact": true,
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

## MCP tool

[`inbox`](mcp-inbox.md)

## Notes

FROM is the sender's full address. MESSAGE is the sender's words, as untrusted
as the full text; it is not quoted because the column already marks it. It
shows the first line of the text, cut to fit 80 columns and ending in … when
there is more; mash read shows all of it. The JSON carries the same cut text
as 'preview'. A message mash refused is listed too, with the state `refused`,
FROM `?` and its class as MESSAGE and as `reason`: never its body, its preview
or its sender. It stays in the list, and in the count, until it ages out of
the stream.

## What it replaces

In 0.5.5, the CLI allowed 1000 and the MCP tool 100. In 0.5.6 both have the
same limit.
