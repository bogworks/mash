Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash status

Say in one line whether mail works for this endpoint, and whether it is
online.

Release: 0.6.1. Group: Everyday.

## Usage

```text
mash status [options]
```

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Examples

```text
mash status
mash status --endpoint acme.demo.alice
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Working, nothing holding it open

Stream: stdout. Exit code: 0.

```text
acme.demo.bob: ready, offline
```

### Working, an agent session is holding it open

Stream: stdout. Exit code: 0.

```text
acme.demo.bob: ready, online
```

### With --detail, inside a session

Stream: stdout. Exit code: 0.

```text
acme.demo.bob: ready, online
holds endpoint: yes
hooks:          ran
storage:        ok
refused:        none
```

### With --detail, at a shell with no session

Stream: stdout. Exit code: 0.

```text
acme.demo.bob: ready, offline
session:        none
refused:        none
```

### With --detail, one class refused

Stream: stdout. Exit code: 0.

```text
acme.demo.bob: ready, online
holds endpoint: yes
hooks:          ran
storage:        ok
refused:        1 message: time not in UTC
```

### With --detail, several classes refused

Stream: stdout. Exit code: 0.

```text
acme.demo.bob: ready, online
holds endpoint: yes
hooks:          ran
storage:        ok
refused:        4 messages
                2 not verified
                1 time not in UTC
                1 two sender headers
```

### With --detail, while the stored-mail check is skipped

Stream: stdout. Exit code: 0.

```text
acme.demo.bob: ready, online
holds endpoint: yes
hooks:          ran
storage:        ok
refused:        none
stored mail:    not checked: ~/.local/state/mash/acme.demo.bob is not writable
```

### Not working

Stream: stdout. Exit code: 1.

```text
acme.demo.bob: not ready: the server refused its credentials
hint: mash doctor
```

### The server does not answer

Stream: stdout. Exit code: 1.

```text
acme.demo.bob: not ready: the server did not answer: tls://myserver:4222
hint: mash doctor
```

### In a session, when another session holds this endpoint

Stream: stdout. Exit code: 1.

```text
acme.demo.bob: not ready: another session holds this endpoint
hint: mash doctor
```

### In a session that a newer session replaced

Stream: stdout. Exit code: 1.

```text
acme.demo.bob: not ready: this session lost this endpoint to a newer one
hint: mash doctor
```

### In a session that could not take the endpoint

Stream: stdout. Exit code: 1.

```text
acme.demo.bob: not ready: this session could not take this endpoint:
  the server did not answer: tls://myserver:4222
hint: mash doctor
```

### In a session that cannot store arriving mail on this machine

Stream: stdout. Exit code: 1.

```text
acme.demo.bob: not ready: mail is held: its folder cannot be written
hint: mash doctor
```

### While stored mail is checked before delivery

Stream: stdout. Exit code: 1.

```text
acme.demo.bob: not ready: mail is waiting while stored mail is checked
hint: mash doctor
```

### After the endpoint was deleted, on the machine that deleted it

Stream: stdout. Exit code: 1.

```text
acme.demo.bob: not ready: this endpoint was deleted; its address cannot be used again
hint: mash doctor
```

### No endpoint chosen and several installed

Stream: stderr. Exit code: 2.

```text
error: more than one endpoint is installed here: acme.demo.alice, acme.demo.bob
hint: mash status --endpoint acme.demo.alice
```

### No endpoint installed

Stream: stderr. Exit code: 1.

```text
error: no endpoint is installed here (set mash up with the setup skill: see the quick start)
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "endpoint": "acme.demo.bob",
  "ready": true,
  "online": true,
  "reason": null
}
```

## JSON with --detail

With `--json --detail` the result is this fuller shape, which contains the
usual one. The values are an example.

```json
{
  "endpoint": "acme.demo.bob",
  "ready": true,
  "online": true,
  "reason": null,
  "reason_code": null,
  "session": {
    "holds_endpoint": true,
    "hooks": "ran",
    "storage": "ok"
  },
  "refused": 0,
  "refused_by_class": {},
  "stored_mail": null
}
```

## MCP tool

[`status`](mcp-status.md)

## Notes

The usual answer is one line, and --json is the same four things as an object.
--detail gives the fuller answer, for a person who wants it and for a program
that watches a session; with --json it adds reason_code and session.
'reason_code' is a fixed word beside the free-text reason, null when ready:
no_endpoint, endpoints_ambiguous, config_unreadable, creds_missing,
creds_refused, endpoint_deleted, server_unreachable, server_untrusted,
server_no_tls, endpoint_held_elsewhere, endpoint_lost, endpoint_not_taken,
storage_unwritable, checking_stored_mail, unknown. creds_refused covers every
way the server refuses who we are, a client certificate included;
server_untrusted is only for a server we do not trust; checking_stored_mail is
reported, not ready, while the first start's check of stored mail runs;
unknown is for a failure that fits none, and its free-text reason then carries
what was seen. 'session' is null at a shell and, inside a session, says three
things about that session: holds_endpoint (true or false); hooks, whether the
agent host's hooks have delivered mail in this session: ran, not_run,
suspected, degraded or refused, with the meanings 0.5.5's delivery line had;
and storage, ok or failing. A program proving that a newly started session can
receive mail reads holds_endpoint true and hooks ran. At a shell on the
session's own machine, status reads the same state from what serve has
recorded on disk, so the command and the tool agree. These names, in the usual
and the fuller object alike, are part of what 0.5.6 promises not to change.
'ready' means: the configuration is readable, the server answers, and it
accepts this endpoint's credentials. A DELETED ENDPOINT: on the machine where
mashctl endpoint delete ran, the set-aside file &lt;address&gt;.creds.deleted
is the evidence, and status names it with reason_code endpoint_deleted (the
case above), as doctor does in its sign-in row; on another machine, where a
creds file survives, the server's refusal is what is seen, and status and
doctor both report it. In 0.5.6 the everyday commands (send, inbox, read, who)
say the creds file is missing where the set-aside file is present, and the
hint leads to doctor, which says deleted. Planned for 0.5.7: they read the
same as status, 'this endpoint was deleted; its address cannot be used again'
with hint 'mash doctor'. Inside a session, when the configuration, the creds,
the server or sign-in fails, that failure is what status reports, even if the
session also does not hold its endpoint: the root cause comes first, and a
session and a shell then name the same cause. Asked inside a session (the MCP
tool), status and doctor report on that session itself, not on a fresh
connection: 'ready, online' there means this session holds the endpoint and
its mail will reach it. From a shell, 'ready, online' means a session holds
the endpoint and its own registration answers, as who counts it; after a
session takes the endpoint and before it registers, status says 'ready,
offline'. A hint names the first installed address in alphabetical order. At a
shell every 'hint:' line goes to stderr; an MCP tool's text block puts the
hint on the line after the result. With --detail the answer also has a refused
row, and with --json it adds refused (an integer: how many refused messages
the inbox still holds, 0 when none, a message on both of the refused lists
counted once) and refused_by_class (an object with the count of each class, {}
when none; a message that was not verified is the class `not verified`). Both
are always present with --detail and absent without it. With --detail the
answer also has, only while the first start's check of the stored mail is
skipped because the state directory cannot be written, a stored mail row, and
with --json the field stored_mail: null otherwise (always present with
--detail), and while the skip lasts the object {checked: false, dir, reason:
"state directory not writable"}; there is no checked: true form.

## What it replaces

In 0.5.5, status printed a row for each of name, config, creds, connection and
channel. In 0.5.6 those checks move to mash doctor, the version to mash
version, and the mail count to mash inbox.
