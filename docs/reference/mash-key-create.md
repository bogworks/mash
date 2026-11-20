Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash key create

Make a seed and its public key.

Release: 0.5.6. Group: Setup.

## Usage

```text
mash key create [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--out` | `<path>` | Where to write the seed. The public key goes beside it with .pub added. Default: mash.seed in the current folder. |

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Examples

```text
mash key create --out bob.seed
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Created

Stream: stdout. Exit code: 0.

```text
Created bob.seed and bob.seed.pub.
```

### File exists

Stream: stderr. Exit code: 1.

```text
error: bob.seed already exists; nothing was written
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "seed": "bob.seed",
  "public_key": "bob.seed.pub"
}
```

## MCP tool

None. Setup. An agent does not hold credentials-making commands.

## Notes

Give the .pub file to whoever runs the mesh: mashctl endpoint create
acme.demo.bob --public-key bob.seed.pub

## What it replaces

As built, renamed to NATS's words. Produces the same pair as NATS's own nk
tool.
