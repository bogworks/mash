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
| [`mashctl version`](mashctl-version.md) | Print the version. It can also be run as --version. |

### Endpoints

| Command | Description |
| --- | --- |
| [`mashctl endpoint`](mashctl-endpoint.md) | Create, change and remove endpoints. |

## Removed in 0.6.1

| Removed | Why |
| --- | --- |
| `adopt, init --local, init --start` | One setup command: mashctl init --config. mashctl does not install or start NATS. |
| `provision, deprovision, list` | Duplicates of endpoint create, endpoint delete and endpoint list. |
| `set-meta, label, unlabel` | Replaced by endpoint set and endpoint unset. |
| `suspend, resume, permissions (top level)` | Moved under endpoint, where every other endpoint command is. |
| `suspended` | endpoint show says whether an endpoint is suspended. |
| `audit, issued list, issued check` | The checks are mashctl doctor; the records are in endpoint show. |
| `inbox inspect` | Now endpoint inbox. |
| `add, reissue, retire (messages only)` | Left over from 0.5.5's renames; nobody uses 0.5.5. |

Planned: later: `mashctl owner`. See [planned, not yet
released](planned-not-yet-released.md).
