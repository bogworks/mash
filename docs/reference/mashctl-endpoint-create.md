Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint create

Create an endpoint and its credentials.

Release: 0.5.9. Group: Endpoints.

## Usage

```text
mashctl endpoint create <address> [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--out` | `<path>` | Write a join file for another machine and install nothing here. |
| `--public-key` | `<path>` | Sign this public key and make no seed. Needs --out. For an endpoint that made its own key with mash key create. |
| `--dry-run` |  | Print what would change, and change nothing. |

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | The endpoint, as owner.group.name. |

## Examples

```text
mashctl endpoint create acme.demo.bob
mashctl endpoint create acme.demo.bob --out acme.demo.bob.join
mashctl endpoint create acme.demo.bob --public-key bob.seed.pub --out acme.demo.bob.join
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Created, credentials installed on this machine

Stream: stdout. Exit code: 0.

```text
Created endpoint acme.demo.bob
Credentials: <config dir>/acme.demo.bob.creds
```

### Created, with a join file

Stream: stdout. Exit code: 0.

```text
Created endpoint acme.demo.bob
Join file: acme.demo.bob.join
```

### With --dry-run

Stream: stdout. Exit code: 0.

```text
Would create endpoint acme.demo.bob
```

### --public-key without --out

Stream: stderr. Exit code: 2.

```text
error: --public-key needs --out, the join file to write
hint: mashctl endpoint create --help
```

### The address exists

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob already exists
```

### The address was deleted before

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob was deleted and its address cannot be used again
```

### --json

Stream: stdout. Exit code: 0.

```text
{
  "address": "acme.demo.bob",
  "installed_here": true,
  "creds": "<config dir>/acme.demo.bob.creds",
  "join_file": null,
  "dry_run": false
}
```

### --json, with a join file and --dry-run

Stream: stdout. Exit code: 0.

```text
{
  "address": "acme.demo.bob",
  "installed_here": false,
  "creds": null,
  "join_file": "acme.demo.bob.join",
  "dry_run": true
}
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "address": "acme.demo.bob",
  "installed_here": true,
  "creds": "/home/ada/.config/mash/acme.demo.bob.creds",
  "join_file": null,
  "dry_run": false
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

With no option, the endpoint can be used on this machine at once: what a solo
developer wants. --out is for another machine. --public-key is for when the
seed must never leave that machine. When mashctl has no certificate authority
file configured, the join file's "ca" is null, and mash then trusts the server
only through the machine's own trusted certificate authorities, as a browser
would.

## What it replaces

In 0.5.5, this took --make-key or --public-key, accepted three address forms,
and printed about ten lines. provision did the same job and is removed.
