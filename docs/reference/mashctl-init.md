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

### This folder was set up for a different server

Stream: stderr. Exit code: 1.

```text
error: ~/.config/mash was set up with a different operator key;
       nothing was changed
hint: mashctl init --config <file> --config-dir <a new folder>
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

mashctl does not install or start NATS; the NATS quick-start script writes
this file as its last step. Safe to run again with the same settings. It never
replaces what a folder already holds: if the folder was set up with a
different operator key, account key, server, system creds, admin creds or
owner, it refuses and changes nothing, in one line of the same form naming
which ('... was set up with a different owner; nothing was changed'), and the
way on is a new configuration folder (--config-dir) or moving the old folder
aside yourself. Someone who removes a script-made server and sets one up again
meets this every time; it is a known issue for 0.5.6 with that workaround. THE
SETUP FILE is YAML, and holds paths, never secrets: 'servers' (a list of
addresses), 'ca' (the certificate authority's certificate, if the server uses
its own), 'owner' (the first owner), 'admin_creds' (the administrator's creds
file), 'system_creds' (the system account's creds file), 'operator_key' (the
operator key that signs the account: its identity key when the server was set
up with the nats command, or a signing key if your operator uses one) and
'account_key' (the account's signing key). The last three are what cancelling
a deleted endpoint's key needs. Know what this means: with a server set up by
the script, mashctl's folder holds the operator's identity key, the strongest
key there is, with which the operator itself can be re-issued. It is protected
only as your own files are. A hosted NATS where you do not hold the operator
key, such as Synadia Cloud, is not supported by mashctl in 0.5.6; mash itself
works against any conforming server.
