Generated from spec/*.json by scripts/refgen. Do not edit.

# mash serve

Run as an MCP server for an agent host. Started by the host, not by hand.

Release: 0.5.6. Group: Integration.

Visible and documented for anyone wiring mash into an agent host other than
Claude Code.

## Usage

```text
mash serve [options]
```

## Options

| Option | Argument | Description | Environment |
| --- | --- | --- | --- |
| `--wake-count` | `<n>` | Wake the session when more than this many messages are waiting. Default 15; 0 turns it off. | MASH_WAKE_COUNT, or "wake_count" |
| `--wake-age` | `<duration>` | Wake the session when the oldest waiting message is older than this. Default 1h; 0 turns it off. | MASH_WAKE_AGE, or "wake_age" |
| `--accept-unverified` |  | Deliver mail that is not verified, with its trust label. Default: off; such mail is refused. | MASH_ACCEPT_UNVERIFIED, or "accept_unverified" |

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Examples

```text
mash serve
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Started by an agent host

Stream: stdout. Exit code: 0.

```text
(speaks MCP on standard input and output; prints nothing for a person)
```

### Run by hand in a terminal

Stream: stderr. Exit code: 2.

```text
error: mash serve is started by an agent host, not by hand
hint: mash serve --help
```

## MCP tool

None. Integration. This is how an agent host runs mash; it is not something an
agent calls.

## Notes

Which endpoint. serve never picks an endpoint by itself, even when only one is
installed: it acts as the one named by --endpoint, MASH_ENDPOINT or the
configuration file, and with none named it offers only status, doctor and
version, whose answer is 'no endpoint is chosen for this session' with the
hint 'start the session with MASH_ENDPOINT=<address>'. Otherwise every agent
session on a machine with one endpoint installed would become that endpoint by
accident, and two sessions would fight over it. Waking for a backlog. Whatever
the kinds of the messages, serve wakes the session when more than --wake-count
are waiting or the oldest has waited longer than --wake-age, at most once
every 10 minutes and not in the first 10 minutes after it starts. mash inbox
says so on its second line. Refusing unverified mail. Only mail whose trust is
'verified' reaches the session, the inbox listing and mash read. Mail that is
unsigned, signed by an unknown key, claimed but unproven, or stale is dropped
and counted; mash doctor shows the count. The sender is not told.
--accept-unverified delivers it instead, labelled.

## What it replaces

As built, plus the two wake settings (Randy, 2026-11-19) and the refusal of
unverified mail. It holds the connection to the server, receives mail in the
background, wakes an idle session and offers the MCP tools.
