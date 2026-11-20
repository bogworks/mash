Generated from spec/*.json by scripts/refgen. Do not edit.

# mash prompt

Ask an endpoint a live question and wait for its answer. Not stored: if the
recipient is not there, it fails.

Release: 0.5.6. Group: Raw SAP.

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

## Examples

```text
mash prompt acme.demo.alice "are you there?"
```

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

### Added: every use, on stderr

Stream: stderr. Exit code: 0.

```text
note: 'mash prompt' moves to 'mash sap prompt' in 0.6.0
```

## MCP tool

[`reply`](mcp-reply.md)

## What it replaces

As built, kept unchanged in 0.5.6 at your word. Only a verified sender that is
a known endpoint is accepted.
