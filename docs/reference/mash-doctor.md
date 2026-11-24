Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash doctor

Check everything mail depends on for this endpoint, and say how to fix what is
wrong. Changes nothing.

Release: 0.5.8. Group: Everyday.

## Usage

```text
mash doctor [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--strict` |  | Exit 1 on a warning too. |

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Examples

```text
mash doctor
mash doctor --strict
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
claude    ok    2.1.320
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  ok
refused   ok    no unverified mail was refused

No problems.
```

### A problem and a warning, with one hint

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    ok    tls://myserver:4222
sign-in   FAIL  the server refused these credentials
delivery  skip  needs sign-in
refused   warn  3 unverified messages were refused

1 problem, 1 warning.
hint: mash join <file> --replace
```

### Server not answering

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    FAIL  the server did not answer: tls://myserver:4222
sign-in   skip  needs the server
delivery  skip  needs the server
refused   ok    no unverified mail was refused

1 problem.
```

### Claude Code is recent enough

Stream: stdout. Exit code: 0.

```text
claude    ok    2.1.320
```

### Claude Code is too old

Stream: stdout. Exit code: 1.

```text
claude    FAIL  2.1.300 is below the tested 2.1.312
```

### Claude Code is newer than tested

Stream: stdout. Exit code: 0.

```text
claude    warn  2.9.0 is above the tested 2.1
```

### No claude command

Stream: stdout. Exit code: 0.

```text
claude    skip  no claude command found
```

### The version of claude is unreadable

Stream: stdout. Exit code: 0.

```text
claude    warn  the version of claude could not be read
```

### The plugin runs this mash

Stream: stdout. Exit code: 0.

```text
binary    ok    0.5.9
```

### The plugin runs another mash

Stream: stdout. Exit code: 0.

```text
binary    warn  the plugin runs 0.5.8; this mash is 0.5.9: install 0.5.8, or set
                MASH_BIN
```

### The plugin runs no downloaded mash

Stream: stdout. Exit code: 0.

```text
binary    skip  the plugin runs no downloaded mash
```

### MASH_BIN is set

Stream: stdout. Exit code: 0.

```text
binary    skip  MASH_BIN is set; the plugin runs it
```

### This mash has no release version

Stream: stdout. Exit code: 0.

```text
binary    skip  this mash has no release version
```

### Arriving mail cannot be stored here

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  FAIL  cannot write ~/.local/state/mash/acme.demo.bob
refused   ok    no unverified mail was refused

1 problem.
```

### At a shell, where there is no session to deliver to

Stream: stdout. Exit code: 0.

```text
delivery  skip  needs a session
```

### Another session holds the endpoint

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  FAIL  another session holds this endpoint
refused   ok    no unverified mail was refused

1 problem.
```

### In a session whose hooks have not delivered yet

Stream: stdout. Exit code: 0.

```text
delivery  warn  hooks have not run in this session yet; they run with
                the first prompt or tool call
```

### In a session whose hooks are failing

Stream: stdout. Exit code: 1.

```text
delivery  FAIL  hooks are not delivering mail to this session;
                restart the session after reinstalling

1 problem.
hint: claude plugin install mash@mash
```

### A newer session replaced this one

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  FAIL  this session lost this endpoint to a newer one
refused   ok    no unverified mail was refused

1 problem.
```

### The endpoint was deleted, seen from another machine, with one hint

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    ok    tls://myserver:4222
sign-in   FAIL  this endpoint was deleted; its address cannot be used again
delivery  skip  needs sign-in
refused   ok    no unverified mail was refused

1 problem.
hint: mash join <file>
```

### The endpoint was deleted, on the machine that deleted it, with one hint

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     FAIL  this endpoint was deleted; its address cannot be used again
server    ok    tls://myserver:4222
sign-in   skip  needs the creds
delivery  skip  needs sign-in
refused   ok    no unverified mail was refused

1 problem.
hint: mash join <file>
```

### The certificate is not trusted

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    FAIL  the server's certificate is not trusted: tls://myserver:4222
sign-in   skip  needs the server
delivery  skip  needs the server
refused   ok    no unverified mail was refused

1 problem.
```

### The server does not offer TLS

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    FAIL  the server did not offer TLS: tls://myserver:4222
sign-in   skip  needs the server
delivery  skip  needs the server
refused   ok    no unverified mail was refused

1 problem.
```

### A creds file is missing

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     FAIL  ~/.config/mash/acme.demo.bob.creds is missing
server    ok    tls://myserver:4222
sign-in   skip  needs the creds
delivery  skip  needs sign-in
refused   ok    no unverified mail was refused

1 problem.
hint: mash join <file>
```

### The server refused the credentials

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    ok    tls://myserver:4222
sign-in   FAIL  the server refused these credentials
delivery  skip  needs sign-in
refused   ok    no unverified mail was refused

1 problem.
hint: mash join <file> --replace
```

### The creds file is not a creds file

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     FAIL  ~/.config/mash/acme.demo.bob.creds is not a creds file
server    ok    tls://myserver:4222
sign-in   skip  needs the creds
delivery  skip  needs sign-in
refused   ok    no unverified mail was refused

1 problem.
hint: mash join <file> --replace
```

### The creds file can be read by others

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     FAIL  ~/.config/mash/acme.demo.bob.creds can be read by others
server    ok    tls://myserver:4222
sign-in   skip  needs the creds
delivery  skip  needs sign-in
refused   ok    no unverified mail was refused

1 problem.
```

### The server address is not tls://

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
binary    skip  the plugin's mash is not visible from a shell
creds     ok
server    FAIL  the server address is not tls://: nats://myserver:4222
sign-in   skip  needs the server
delivery  skip  needs the server
refused   ok    no unverified mail was refused

1 problem.
```

### In a session with no endpoint chosen

Stream: the doctor tool's result, an error. Exit code: 1.

```text
No endpoint is chosen for this session.
```

### --json: the one hint is a field

Stream: stdout. Exit code: 1.

```text
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
   "name": "claude",
   "state": "ok",
   "detail": "2.1.320",
   "fix": null
  },
  {
   "name": "binary",
   "state": "skip",
   "detail": "the plugin's mash is not visible from a shell",
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
   "fix": "mash join <file> --replace"
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
 ],
 "hint": "mash join <file> --replace"
}
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
      "name": "claude",
      "state": "ok",
      "detail": "2.1.320",
      "fix": null
    },
    {
      "name": "binary",
      "state": "skip",
      "detail": "the plugin's mash is not visible from a shell",
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
      "fix": "mash join <file> --replace"
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
  ],
  "hint": "mash join <file> --replace"
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
inside a session and skipped at a shell. In a session that has just started,
the row is 'warn' until the first prompt or tool call, so 'doctor --strict'
exits 1 until then: a program that waits for a session to be ready repeats the
check. The 'hooks not run yet' warning has no hint, because no command fixes
it. Whether storage is failing is decided by whoever asks, by trying the
folder, not from a record kept in it. An agent host that has no hooks would
warn for as long as it runs; only Claude Code is in scope for 0.5.6. The
binary row compares the mash the plugin runs (the copy
$CLAUDE_PLUGIN_DATA/bin/mash-&lt;version&gt;, where &lt;version&gt; is the one
in $CLAUDE_PLUGIN_ROOT/scripts/mash.pins) with the mash running doctor; it
warns when they differ and is skipped at a shell, where CLAUDE_PLUGIN_DATA is
not set. Every skip is one short line, and the warn names no path. The refused
count is kept on this machine, so its row shows even when the server is down.
The 'refused' row counts unverified mail that was refused on arrival (see mash
serve). Mail that was verified on arrival and whose sender was deleted
afterwards is not counted: mash read shows it as refused, and that is where it
is seen.

## What it replaces

New in 0.5.6. It takes over the rows mash status printed in 0.5.5. It has no
plugin or channel row: neither can be checked truthfully from a shell, and the
command and the tool must answer alike.
