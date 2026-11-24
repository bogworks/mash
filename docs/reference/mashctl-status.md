Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl status

Say in one line whether the server is working, and how many endpoints it has.

Release: 0.5.9. Group: Mesh.

## Usage

```text
mashctl status [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Examples

```text
mashctl status
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Working

Stream: stdout. Exit code: 0.

```text
tls://localhost:4222: ok, 3 endpoints, 1 online
```

### Several servers: one line each

Stream: stdout. Exit code: 1.

```text
tls://a.example:4222: ok, 3 endpoints, 1 online
tls://b.example:4222: not ok: the server did not answer
hint: mashctl doctor
```

### Not working (the line on stdout, the hint on stderr)

Stream: stdout. Exit code: 1.

```text
tls://localhost:4222: not ok: the server did not answer
hint: mashctl doctor
```

### No admin credentials

Stream: stdout. Exit code: 1.

```text
tls://localhost:4222: admin credentials are not set: set MASH_ADMIN_CREDS, or "admin_creds" in config.json
```

### No server is configured

Stream: stderr. Exit code: 1.

```text
error: no server is configured
hint: mashctl init --config <file>
```

### --json

Stream: stdout. Exit code: 0.

```text
{
  "server": "tls://localhost:4222",
  "ready": true,
  "endpoint_count": 3,
  "online_count": 1,
  "reason": null
}
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "server": "tls://localhost:4222",
  "ready": true,
  "endpoint_count": 3,
  "online_count": 1,
  "reason": null
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## What it replaces

New in 0.5.6.
