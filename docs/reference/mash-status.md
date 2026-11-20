Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash status

Say in one line whether mail works for this endpoint, and whether it is
online.

Release: 0.5.6. Group: Everyday.

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

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "endpoint": "acme.demo.bob",
  "ready": true,
  "online": false,
  "reason": null
}
```

## MCP tool

[`status`](mcp-status.md)

## Notes

'ready' means: the configuration is readable, the server answers, and it
accepts this endpoint's credentials. A hint names the first installed address
in alphabetical order. At a shell every 'hint:' line goes to stderr; an MCP
tool's text block puts the hint on the line after the result.

## What it replaces

Today status prints a row for each of name, config, creds, connection and
channel. Those checks move to mash doctor; the version to mash version; the
mail count to mash inbox.
