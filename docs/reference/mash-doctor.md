Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash doctor

Check everything mail depends on for this endpoint, and say how to fix what is
wrong. Changes nothing.

Release: 0.5.9. Group: Everyday.

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
mash doctor --endpoint acme.demo.alice
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
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  ok
refused   ok    no mail was refused

No problems.
```

### Mail was refused

Stream: stdout. Exit code: 0.

```text
config    ok
claude    ok    2.1.320
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  ok
refused   ok    4 messages were refused
                2 not verified
                1 time not in UTC
                1 two sender headers

No problems.
```

### Several endpoints installed here and none chosen, all well

Stream: stdout. Exit code: 0.

```text
Endpoints installed here: 2.

ENDPOINT         PROBLEMS  WARNINGS  FIRST ISSUE
acme.demo.alice  0         0
acme.demo.bob    0         0

No problems.
```

### Several endpoints installed here, one with a problem

Stream: stdout. Exit code: 1.

```text
Endpoints installed here: 2.

ENDPOINT         PROBLEMS  WARNINGS  FIRST ISSUE
acme.demo.alice  0         0
acme.demo.bob    1         0         sign-in: the server refused these credentials

1 problem. See it: mash doctor --endpoint acme.demo.bob
```

### Several endpoints installed here, --json

Stream: stdout. Exit code: 1.

```text
{"endpoints":[{"endpoint":"acme.demo.alice","problems":0,"warnings":0,"checks":[...],"hint":null},{"endpoint":"acme.demo.bob","problems":1,"warnings":0,"checks":[...],"hint":null}],"problems":1,"warnings":0,"hint":null}
```

### A problem, with one hint

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    ok    tls://myserver:4222
sign-in   FAIL  the server refused these credentials
delivery  skip  needs sign-in
refused   ok    3 messages were refused
                2 not verified
                1 time not in UTC

1 problem.
hint: mash join <file> --replace
```

### Server not answering

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    FAIL  the server did not answer: tls://myserver:4222
sign-in   skip  needs the server
delivery  skip  needs the server
refused   ok    no mail was refused

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

### Started by the plugin's launcher, running the pinned mash

Stream: stdout. Exit code: 0.

```text
launcher  ok    pinned mash
```

### Started by the plugin's launcher, running a mash from PATH

Stream: stdout. Exit code: 0.

```text
launcher  warn  mash from PATH, not the pinned one
```

### The stored-mail check was skipped

Stream: stdout. Exit code: 0.

```text
config       ok
claude       ok    2.1.320
creds        ok
server       ok    tls://myserver:4222
sign-in      ok
delivery     ok
stored mail  skip  not checked: ~/.local/state/mash/acme.demo.bob is not writable
refused      ok    no mail was refused

No problems.
```

### Arriving mail cannot be stored here

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  FAIL  cannot write ~/.local/state/mash/acme.demo.bob
refused   ok    no mail was refused

1 problem.
```

### At a shell, where there is no session to deliver to

Stream: stdout. Exit code: 0.

```text
delivery  skip  checked only inside a Claude Code session
```

### At a shell, when the state folder cannot be created

Stream: stdout. Exit code: 1.

```text
config       ok
claude       skip  no claude command found
creds        ok
server       ok    tls://myserver:4222
sign-in      ok
delivery     FAIL  cannot write ~/.local/state/mash/spool/acme/acme.demo.bob
stored mail  skip  not checked: ~/.local/state/mash/spool/acme/acme.demo.bob is not writable
refused      ok    no mail was refused

1 problem.
```

### At a shell, with no spool setting, when the state folder cannot be created

Stream: stdout. Exit code: 0.

```text
config       ok
claude       skip  no claude command found
creds        ok
server       ok    tls://myserver:4222
sign-in      ok
delivery     warn  cannot write
                   ~/.local/state/mash/spool/acme/acme.demo.bob
                   plugin sessions get no mail until fixed
stored mail  skip  not checked: ~/.local/state/mash/spool/acme/acme.demo.bob is not writable
refused      ok    no mail was refused

1 warning.
```

### Another session holds the endpoint

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  FAIL  another session holds this endpoint
refused   ok    no mail was refused

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

### While stored mail is checked before delivery

Stream: stdout. Exit code: 1.

```text
delivery  FAIL  mail is waiting while stored mail is checked

1 problem.
```

### A newer session replaced this one

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  FAIL  this session lost this endpoint to a newer one
refused   ok    no mail was refused

1 problem.
```

### The endpoint was deleted, seen from another machine, with one hint

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    ok    tls://myserver:4222
sign-in   FAIL  this endpoint was deleted; its address cannot be used again
delivery  skip  needs sign-in
refused   ok    no mail was refused

1 problem.
hint: mash join <file>
```

### The endpoint was deleted, on the machine that deleted it, with one hint

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     FAIL  this endpoint was deleted; its address cannot be used again
server    ok    tls://myserver:4222
sign-in   skip  needs the creds
delivery  skip  needs sign-in
refused   ok    no mail was refused

1 problem.
hint: mash join <file>
```

### The certificate is not trusted

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    FAIL  the server's certificate is not trusted: tls://myserver:4222
sign-in   skip  needs the server
delivery  skip  needs the server
refused   ok    no mail was refused

1 problem.
```

### The server does not offer TLS

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    FAIL  the server did not offer TLS: tls://myserver:4222
sign-in   skip  needs the server
delivery  skip  needs the server
refused   ok    no mail was refused

1 problem.
```

### A creds file is missing

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     FAIL  ~/.config/mash/acme.demo.bob.creds is missing
server    ok    tls://myserver:4222
sign-in   skip  needs the creds
delivery  skip  needs sign-in
refused   ok    no mail was refused

1 problem.
hint: mash join <file>
```

### The server refused the credentials

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    ok    tls://myserver:4222
sign-in   FAIL  the server refused these credentials
delivery  skip  needs sign-in
refused   ok    no mail was refused

1 problem.
hint: mash join <file> --replace
```

### The creds file is not a creds file

Stream: stdout, then the hint on stderr. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     FAIL  ~/.config/mash/acme.demo.bob.creds is not a creds file
server    ok    tls://myserver:4222
sign-in   skip  needs the creds
delivery  skip  needs sign-in
refused   ok    no mail was refused

1 problem.
hint: mash join <file> --replace
```

### The creds file can be read by others

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     FAIL  ~/.config/mash/acme.demo.bob.creds can be read by others
server    ok    tls://myserver:4222
sign-in   skip  needs the creds
delivery  skip  needs sign-in
refused   ok    no mail was refused

1 problem.
```

### The server address is not tls://

Stream: stdout. Exit code: 1.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    FAIL  the server address is not tls://: nats://myserver:4222
sign-in   skip  needs the server
delivery  skip  needs the server
refused   ok    no mail was refused

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
 "warnings": 0,
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
   "state": "ok",
   "detail": "3 messages were refused",
   "fix": null
  }
 ],
 "refused": 3,
 "refused_by_class": {
  "not verified": 2,
  "time not in UTC": 1
 },
 "hint": "mash join <file> --replace"
}
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "endpoint": "acme.demo.bob",
  "problems": 1,
  "warnings": 0,
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
      "state": "ok",
      "detail": "3 messages were refused",
      "fix": null
    }
  ],
  "refused": 3,
  "refused_by_class": {
    "not verified": 2,
    "time not in UTC": 1
  },
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
arrives can be stored on this machine for the session; it is checked inside a
session and skipped at a shell, except that a shell fails it when the state
folder cannot be created and MASH_SPOOL_FROM_START (which the plugin sets) or
MASH_SPOOL_REQUIRED is set, so that mail waits for the spool, and without
those settings a shell warns when the folder cannot be created; it finds the
folder by asking the system without writing anything. In a session that has
just started, the row is 'warn' until the first prompt or tool call, so
'doctor --strict' exits 1 until then: a program that waits for a session to be
ready repeats the check. The 'hooks not run yet' warning has no hint, because
no command fixes it. Whether storage is failing is decided by whoever asks, by
trying the folder, not from a record kept in it. An agent host that has no
hooks would warn for as long as it runs; only Claude Code is in scope for
0.5.6. The refused count is kept on this machine, so its row shows even when
the server is down. The 'refused' row counts the refused messages the inbox
still holds, for any reason: a message whose sender header was refused, and
mail that was not verified on arrival (see mash serve); a message on both
counts once. Its state is always ok: nothing a person can do removes a refused
message before it ages out. Mail that was verified on arrival and whose sender
was deleted afterwards is not counted: mash read shows it as refused, and that
is where it is seen. The JSON adds refused (an integer, 0 when none) and
refused_by_class (an object with the count of each class, {} when none); the
classes are in refused_by_class only, and the refused entry of checks has no
more than the usual four keys. While the first start's check of the stored
mail is skipped because the state directory cannot be written, and only then,
a row named `stored mail` with state skip follows the delivery row; it changes
neither the problem count nor the exit status. The launcher row, which says
which mash the plugin's launcher started, reports how the mash it runs in was
launched: it shows in a session's own doctor (the MCP tool), and a mash doctor
typed in a shell has no launcher row.

## What it replaces

New in 0.5.6. It takes over the rows mash status printed in 0.5.5. It has no
plugin or channel row: neither can be checked truthfully from a shell, and the
command and the tool must answer alike.
