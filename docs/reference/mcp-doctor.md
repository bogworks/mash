Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# MCP tool doctor

Check everything mail depends on for this endpoint, and say how to fix what is
wrong. Changes nothing.

Release: 0.5.6. Command line equivalent: `mash doctor`.

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

## Text result

### All well

The call returns this text.

```text
config    ok
creds     ok
server    ok    tls://myserver:4222
sign-in   ok
delivery  ok
refused   ok    no unverified mail was refused

No problems.
```

## Errors

### A problem and a warning

The call returns this text with `isError` set.

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

The call returns this text with `isError` set.

```text
config    ok
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
