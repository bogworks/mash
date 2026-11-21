Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl

Administers a mash mesh through a server.

## Usage

```text
mashctl <command> [options]
```

## Global options

These options work with every `mashctl` command.

| Option | Argument | Description | Environment |
| --- | --- | --- | --- |
| `--server` | `<url>` | The server address. May be given more than once. | MASH_SERVER (commas between several), or "servers" |
| `--creds` | `<path>` | The administrator's creds file. | MASH_ADMIN_CREDS, or "admin_creds" |
| `--ca` | `<path>` | The certificate authority's certificate, when the server uses its own. | MASH_CA, or "ca" |
| `--config-dir` | `<path>` | The configuration folder. Default: ~/.config/mash. | MASH_CONFIG_DIR |
| `--detail` |  | Print the fuller answer: after the usual line, more as 'label: value' lines; with --json, the fuller object. |  |
| `--json` |  | Print the result as JSON. |  |
| `--no-color` |  | Never use color. | NO_COLOR |
| `--help`, `-h` |  | Show help for the command. |  |

## Commands

### Mesh

| Command | Description |
| --- | --- |
| [`mashctl init`](mashctl-init.md) | Set mash up on a NATS server you already have, from a file. |
| [`mashctl status`](mashctl-status.md) | Say in one line whether the server is working, and how many endpoints it has. |
| [`mashctl doctor`](mashctl-doctor.md) | Check the server, mash's storage on it and every endpoint's records, and say how to fix what is wrong. Changes nothing. |
| [`mashctl limits`](mashctl-limits.md) | Show or apply the storage limits. |
| [`mashctl version`](mashctl-version.md) | Print the version. |

### Endpoints

| Command | Description |
| --- | --- |
| [`mashctl endpoint`](mashctl-endpoint.md) | Create, change and remove endpoints. |

Planned for 0.6.0: `mashctl owner`. See [coming in 0.6.0](coming-in-0-6-0.md).
