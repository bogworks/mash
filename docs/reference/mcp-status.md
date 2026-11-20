Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# MCP tool status

Say in one line whether mail works for this endpoint, and whether it is
online.

Release: 0.5.6. Command line equivalent: `mash status`.

## Structured result

The structured content of a successful result. The values are an example.

```json
{
  "endpoint": "acme.demo.bob",
  "ready": true,
  "online": false,
  "reason": null
}
```

## Text result

### Working, nothing holding it open

Stream: stdout. Exit code: 0.

```text
acme.demo.bob: ready, offline
```

## Errors

### Not working (the first line on stdout, the hint on stderr)

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

### In a session that cannot store arriving mail on this machine

Stream: stdout. Exit code: 1.

```text
acme.demo.bob: not ready: mail is held: its folder cannot be written
hint: mash doctor
```

### No endpoint chosen and several installed

Stream: stderr. Exit code: 2.

```text
error: more than one endpoint is installed here: acme.demo.alice, acme.demo.bob
hint: mash status --endpoint acme.demo.alice
```

### No endpoint installed

Stream: stderr. Exit code: 2.

```text
error: no endpoint is installed here
hint: mash join <file>
```

### As an MCP tool in a session with no endpoint named

Stream: stdout. Exit code: 1.

```text
No endpoint is chosen for this session.
hint: start the session with MASH_ENDPOINT=<address>
```
