Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash prompt

Ask an endpoint a live question and wait for its answer. Not stored: if the
recipient is not there, it fails.

Release: 0.5.9. Group: Raw SAP.

## Usage

```text
mash prompt <address> <text> [options]
```

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | Who to ask. |
| `text` | yes | The question. |

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### As built

Stream: stdout. Exit code: 0.

```text
(what it prints today; unchanged in 0.5.6)
```

### mash prompt --help

Stream: stdout. Exit code: 0.

```text
Ask an endpoint a live question and wait for its answer. Not stored: if the
recipient is not there, it fails.

Usage:
  mash prompt <address> <text>

Arguments:
  address  Who to ask.
  text     The question.

Options:
  --endpoint <address>  Which endpoint to act as, as owner.group.name. Not
                        needed when only one is installed here.
  --creds <path>        The creds file to sign in with. Default: <config
                        folder>/<address>.creds.
  --server <url>        The server address. May be given more than once.
  --ca <path>           The certificate authority's certificate, when the server
                        uses its own.
  --config-dir <path>   The configuration folder. Default: ~/.config/mash.
  -h, --help            Show help for the command.
```

## MCP tool

[`reply`](mcp-reply.md)

## What it replaces

Unchanged from 0.5.5. Only a verified sender that is a known endpoint is
accepted.
