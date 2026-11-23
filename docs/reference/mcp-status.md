Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# MCP tool status

Say in one line whether mail works for this endpoint, and whether it is
online.

Release: 0.5.8. Command line equivalent: `mash status`.

## Parameters

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `detail` | boolean | no | Return the fuller object. Default false: the usual, minimal one. |

## Structured result

The structured content of a successful result. The values are an example.

```json
{
  "endpoint": "acme.demo.bob",
  "ready": true,
  "online": true,
  "reason": null
}
```

## Structured result with --detail

With the `detail` parameter set, the structured content is this fuller shape,
which contains the usual one. The values are an example.

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
  }
}
```

## Text result

### Working, nothing holding it open

The call returns this text.

```text
acme.demo.bob: ready, offline
```

## Errors

### Not working

The call returns this text with `isError` set.

```text
acme.demo.bob: not ready: the server refused its credentials
hint: mash doctor
```

### The server does not answer

The call returns this text with `isError` set.

```text
acme.demo.bob: not ready: the server did not answer: tls://myserver:4222
hint: mash doctor
```

### In a session, when another session holds this endpoint

The call returns this text with `isError` set.

```text
acme.demo.bob: not ready: another session holds this endpoint
hint: mash doctor
```

### In a session that a newer session replaced

The call returns this text with `isError` set.

```text
acme.demo.bob: not ready: this session lost this endpoint to a newer one
hint: mash doctor
```

### In a session that could not take the endpoint

The call returns this text with `isError` set.

```text
acme.demo.bob: not ready: this session could not take this endpoint:
  the server did not answer: tls://myserver:4222
hint: mash doctor
```

### In a session that cannot store arriving mail on this machine

The call returns this text with `isError` set.

```text
acme.demo.bob: not ready: mail is held: its folder cannot be written
hint: mash doctor
```

### After the endpoint was deleted, on the machine that deleted it

The call returns this text with `isError` set.

```text
acme.demo.bob: not ready: this endpoint was deleted; its address cannot be used again
hint: mash doctor
```

### No endpoint chosen for this session

The call returns this text with `isError` set.

```text
No endpoint is chosen for this session.
hint: start the session with MASH_ENDPOINT=<address>
```
