Generated from spec/*.json by scripts/refgen. Do not edit.

# mash doctor

Check everything mail depends on for this endpoint, and say how to fix what is
wrong. Changes nothing.

Release: 0.5.6. Group: Everyday.

## Usage

```text
mash doctor [options]
```

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Examples

```text
mash doctor
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### All well

Stream: stdout. Exit code: 0.

```text
config    ok
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  ok
refused   ok    no unverified mail was refused

No problems.
```

### A problem and a warning

Stream: stdout. Exit code: 1.

```text
config    ok
creds     ok
server    ok    tls://myserver:4222
sign-in   FAIL  the server refused these credentials
delivery  skip  needs sign-in
refused   warn  3 unverified messages were refused

1 problem, 1 warning.
hint: ask whoever runs your mesh to run: mashctl endpoint reissue acme.demo.bob
```

### A check that could not run

Stream: stdout. Exit code: 1.

```text
config    ok
creds     ok
server    FAIL  the server did not answer: tls://myserver:4222
sign-in   skip  needs the server
delivery  skip  needs the server
refused   ok    no unverified mail was refused

1 problem.
```

### Arriving mail cannot be stored on this machine

Stream: stdout. Exit code: 0.

```text
delivery  FAIL  mail is held: its folder cannot be written:
                ~/.local/state/mash/acme.demo.bob
```

### At a shell, where there is no session to deliver to

Stream: stdout. Exit code: 0.

```text
delivery  skip  needs a session
```

### The server's certificate is not trusted

Stream: stdout. Exit code: 0.

```text
server    FAIL  the server's certificate is not trusted: tls://myserver:4222
```

### The server does not offer encryption

Stream: stdout. Exit code: 0.

```text
server    FAIL  the server did not offer TLS: tls://myserver:4222
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "endpoint": "acme.demo.bob",
  "problems": 1,
  "warnings": 1,
  "checks": [
    {
      "name": "config",
      "state": "ok",
      "detail": null,
      "fix": null
    },
    {
      "name": "creds",
      "state": "ok",
      "detail": null,
      "fix": null
    },
    {
      "name": "server",
      "state": "ok",
      "detail": "tls://myserver:4222",
      "fix": null
    },
    {
      "name": "sign-in",
      "state": "fail",
      "detail": "the server refused these credentials",
      "fix": "mashctl endpoint reissue acme.demo.bob"
    },
    {
      "name": "delivery",
      "state": "skip",
      "detail": "needs sign-in",
      "fix": null
    },
    {
      "name": "refused",
      "state": "warn",
      "detail": "3 unverified messages were refused",
      "fix": null
    }
  ]
}
```

## MCP tool

[`doctor`](mcp-doctor.md)

## Notes

doctor is the one everyday command that prints many lines, as brew doctor and
flutter doctor do. It reports on stdout and exits 1 when it found a problem.
Every row is always printed, and 'checks' in the JSON always lists every row,
each with a state of ok, warn, fail or skip; a row is skipped when an earlier
failure means it cannot be run. The delivery row says whether mail that
arrives can be stored on this machine for the session; it is checked only
inside a session and skipped at a shell. The refused count is kept on this
machine, so its row shows even when the server is down. The 'refused' row
counts unverified mail that was refused (see mash serve).

## What it replaces

New. It takes over the rows mash status prints today. It has no plugin or
channel row in 0.5.6: neither can be checked truthfully from a shell today,
and the command and the tool must answer alike.
