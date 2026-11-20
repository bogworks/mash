Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl init

Set mash up on a NATS server you already have, from a file.

Release: 0.5.6. Group: Mesh.

## Usage

```text
mashctl init [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--config` | `<path>` | The setup file. See Notes for its fields. |
| `--dry-run` |  | Print what would change, and change nothing. |

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Examples

```text
mashctl init --config ~/mash-nats/mash-setup.yaml
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Set up

Stream: stdout. Exit code: 0.

```text
Initialized mash on tls://localhost:4222 for owner acme
```

### Already set up

Stream: stdout. Exit code: 0.

```text
Already initialized on tls://localhost:4222; nothing changed
```

### Cannot reach the server

Stream: stderr. Exit code: 1.

```text
error: cannot reach tls://localhost:4222
hint: mashctl doctor
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "server": [
    "tls://localhost:4222"
  ],
  "owner": "acme",
  "changed": true
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

Safe to run again. THE SETUP FILE is YAML, and holds paths, never secrets:
'servers' (a list of addresses), 'ca' (the certificate authority's
certificate, if the server uses its own), 'owner' (the first owner),
'admin_creds' (the administrator's creds file), 'system_creds' (the system
account's creds file), 'operator_key' (the operator's signing key) and
'account_key' (the account's signing key). The last three are what cancelling
a deleted endpoint's key needs. A hosted NATS where you do not hold the
operator key, such as Synadia Cloud, is not supported by mashctl in 0.5.6;
mash itself works against any conforming server.

## What it replaces

Replaces mashctl adopt, mashctl init and mashctl init --local. mashctl does
not install or start NATS; the NATS quick-start script writes this file as its
last step.
