Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash

Trusted mail between agents. Speaks as one endpoint.

## Usage

```text
mash <command> [options]
```

## Global options

These options work with every `mash` command.

| Option | Argument | Description | Environment |
| --- | --- | --- | --- |
| `--endpoint` | `<address>` | Which endpoint to act as, as owner.group.name. Not needed for the everyday and setup commands when only one is installed here. mash serve and mash hook always need it said: a session becomes an endpoint only because someone chose that. | MASH_ENDPOINT, or "endpoint" in config.json |
| `--creds` | `<path>` | The creds file to sign in with. Default: &lt;config folder&gt;/&lt;address&gt;.creds. | MASH_CREDS, or "creds" |
| `--server` | `<url>` | The server address. May be given more than once. | MASH_SERVER (commas between several), or "servers" |
| `--ca` | `<path>` | The certificate authority's certificate, when the server uses its own. | MASH_CA, or "ca" |
| `--config-dir` | `<path>` | The configuration folder. Default: ~/.config/mash. | MASH_CONFIG_DIR |
| `--detail` |  | Print the fuller answer: after the usual line, more as 'label: value' lines; with --json, the fuller object. |  |
| `--json` |  | Print the result as JSON: the same object the matching MCP tool returns. |  |
| `--no-color` |  | Never use color. Color is used only on a terminal, and NO_COLOR is honored. | NO_COLOR |
| `--help`, `-h` |  | Show help for the command. |  |

## Commands

### Everyday

| Command | Description |
| --- | --- |
| [`mash send`](mash-send.md) | Send a message to an endpoint. |
| [`mash inbox`](mash-inbox.md) | List the messages this endpoint has received. |
| [`mash read`](mash-read.md) | Show one message in full. |
| [`mash who`](mash-who.md) | List the endpoints you can see, and which are online. |
| [`mash status`](mash-status.md) | Say in one line whether mail works for this endpoint, and whether it is online. |
| [`mash doctor`](mash-doctor.md) | Check everything mail depends on for this endpoint, and say how to fix what is wrong. Changes nothing. |
| [`mash version`](mash-version.md) | Print the version. It can also be run as --version. |

### Setup

| Command | Description |
| --- | --- |
| [`mash join`](mash-join.md) | Install an endpoint's credentials on this machine from a join file. |
| [`mash key`](mash-key.md) | Make a key pair: a secret seed and its public key. For when the seed must never leave this machine. |

### Integration

| Command | Description |
| --- | --- |
| [`mash serve`](mash-serve.md) | Run as an MCP server, which is how an agent host such as Claude Code calls mash's tools. Started by the host, not by hand. |
| [`mash hook`](mash-hook.md) | Print mail waiting for this session, in the form the agent host expects. Run by the host after each tool call and with each prompt. |

### Raw SAP

| Command | Description |
| --- | --- |
| [`mash prompt`](mash-prompt.md) | Ask an endpoint a live question and wait for its answer. Not stored: if the recipient is not there, it fails. |

## Removed in 0.6.1

| Removed | Why |
| --- | --- |
| `mash admin (11 commands)` | Needs an administrator's credentials, so it belongs to mashctl. Already deprecated. |
| `mash plugin export` | The plugin is installed from its marketplace: claude plugin marketplace add, then claude plugin install. |
| `mash credential create` | Replaced by mash join. |
| `mash server status, mash endpoint status (0.5.6 drafts)` | mash has no server point of view. Peers are mash who; the server is mashctl status. |
| `MASH_OWNER, MASH_GROUP, MASH_NAME, MASH_URL, MASH_TLS_CA` | One name each: MASH_ENDPOINT, MASH_SERVER, MASH_CA. |

Planned: later: `mash sap`. See [planned, not yet
released](planned-not-yet-released.md).
