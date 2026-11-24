Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# MCP tool doctor

Check everything mail depends on for this endpoint, and say how to fix what is
wrong. Changes nothing.

Release: 0.5.8. Command line equivalent: `mash doctor`.

## Parameters

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `strict` | boolean | no | Exit 1 on a warning too. |

## Structured result

The structured content of a successful result. The values are an example.

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

## Text result

### All well

The call returns this text.

```text
config    ok
claude    ok    2.1.320
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  ok
refused   ok    no unverified mail was refused

No problems.
```

## Errors

### A problem and a warning, with one hint

The call returns this text with `isError` set.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    ok    tls://myserver:4222
sign-in   FAIL  the server refused these credentials
delivery  skip  needs sign-in
refused   warn  3 unverified messages were refused

1 problem, 1 warning.
hint: mash join <file> --replace
```

### Server not answering

The call returns this text with `isError` set.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    FAIL  the server did not answer: tls://myserver:4222
sign-in   skip  needs the server
delivery  skip  needs the server
refused   ok    no unverified mail was refused

1 problem.
```

### In a session whose hooks are failing

The call returns this text with `isError` set.

```text
delivery  FAIL  hooks are not delivering mail to this session;
                restart the session after reinstalling

1 problem.
hint: claude plugin install mash@mash
```

### The endpoint was deleted, seen from another machine, with one hint

The call returns this text with `isError` set.

```text
config    ok
claude    skip  no claude command found
creds     ok
server    ok    tls://myserver:4222
sign-in   FAIL  this endpoint was deleted; its address cannot be used again
delivery  skip  needs sign-in
refused   ok    no unverified mail was refused

1 problem.
hint: mash join <file>
```

### The endpoint was deleted, on the machine that deleted it, with one hint

The call returns this text with `isError` set.

```text
config    ok
claude    skip  no claude command found
creds     FAIL  this endpoint was deleted; its address cannot be used again
server    ok    tls://myserver:4222
sign-in   skip  needs the creds
delivery  skip  needs sign-in
refused   ok    no unverified mail was refused

1 problem.
hint: mash join <file>
```

### In a session with no endpoint chosen

The call returns this text with `isError` set.

```text
No endpoint is chosen for this session.
```
