Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash key

Make a key pair: a secret seed and its public key. For when the seed must
never leave this machine.

Release: 0.5.9. Group: Setup.

## Usage

```text
mash key <command> [options]
```

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Commands

| Command | Description |
| --- | --- |
| [`mash key create`](mash-key-create.md) | Make a key pair: a secret seed and its public key. For when the seed must never leave this machine. |
