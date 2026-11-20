Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mash join

Install an endpoint's credentials on this machine from a join file.

Release: 0.5.6. Group: Setup.

## Usage

```text
mash join <file> [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--key` | `<path>` | Your own seed file, when the join file was made for a public key you supplied. |
| `--replace` |  | Replace credentials already installed here for the same address. |
| `--dry-run` |  | Print what would change, and change nothing. |

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `file` | yes | The join file whoever runs the mesh gave you. |

## Examples

```text
mash join acme.demo.bob.join
mash join acme.demo.bob.join --key bob.seed
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Joined

Stream: stdout. Exit code: 0.

```text
Joined as acme.demo.bob.
```

### Already installed

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is already installed here
hint: mash join acme.demo.bob.join --replace
```

### This folder already belongs to another mesh

Stream: stderr. Exit code: 1.

```text
error: this configuration folder is set up for tls://other:4222
hint: mash join acme.demo.bob.join --config-dir <another folder>
```

### With --dry-run

Stream: stdout. Exit code: 0.

```text
Would join as acme.demo.bob.
```

### The join file needs your seed

Stream: stderr. Exit code: 2.

```text
error: this join file holds no seed; it was made for your own key
hint: mash join acme.demo.bob.join --key <your seed file>
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "endpoint": "acme.demo.bob",
  "servers": [
    "tls://myserver:4222"
  ],
  "creds": "~/.config/mash/acme.demo.bob.creds"
}
```

## MCP tool

None. Setup. An agent does not hold credentials-making commands.

## Notes

It asks nothing: the file came from whoever runs your mesh. How it works.
Whoever runs the mesh runs 'mashctl endpoint create acme.demo.bob --out
acme.demo.bob.join' and gets one text file. They get that file to the other
machine by any route they trust. There, 'mash join acme.demo.bob.join' copies
its three parts into the configuration folder: the server address or
addresses, the certificate authority's certificate if the server uses its own,
and a NATS creds file (a user JWT and its seed). After that, mash status
works. When the endpoint made its own key, the join file holds no seed and
--key supplies it. In 0.6.0, mash join also gains a way to ask to join and
wait for approval (board card 210). THE JOIN FILE is JSON, written with mode
600, with these fields: "mash_join": 1 (the format's version); "address";
"servers": a list of addresses; "ca": the certificate authority's certificate
as PEM text, or null; and either "creds": the whole text of a NATS creds file,
or, when the endpoint made its own key, "user_jwt": the user JWT alone. One
configuration folder serves one mesh; a join file for another mesh needs
another folder.

## What it replaces

New. Replaces mash credential create (0.5.5) and the unbuilt mash credential
install (0.5.6 contract).
