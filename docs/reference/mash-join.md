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
| `--replace` |  | Replace the creds already installed here for the same address. It replaces nothing else: not the server, not the certificate authority. |
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

### With --detail

Stream: stdout. Exit code: 0.

```text
Joined as acme.demo.bob.
servers: tls://myserver:4222
creds:   ~/.config/mash/acme.demo.bob.creds
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
error: ~/.config/mash was set up with a different server;
       nothing was changed
hint: mash join acme.demo.bob.join --config-dir <another folder>
```

### This folder trusts a different certificate authority

Stream: stderr. Exit code: 1.

```text
error: ~/.config/mash was set up with a different ca;
       nothing was changed
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

### The join file can be read by others

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob.join can be read by others; a join file that holds a seed must be readable by you alone
hint: chmod 600 acme.demo.bob.join
```

### The join file is not a plain file of your own

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob.join is not a plain file owned by you; nothing was installed
hint: copy it into a file of your own, then: chmod 600 <that file>
```

### The folder cannot be written

Stream: stderr. Exit code: 1.

```text
error: cannot write into ~/.config/mash: permission denied; nothing was installed
hint: mash doctor
```

### Could not finish and could not undo

Stream: stderr. Exit code: 1.

```text
error: could not finish and could not undo: ~/.config/mash/acme.demo.bob.creds left; old creds kept at ~/.config/mash/acme.demo.bob.creds.old
hint: mash doctor
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "endpoint": "acme.demo.bob"
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
another folder. The certificate authority's certificate is kept in the folder
as ca.pem, and every endpoint in the folder trusts it; a join file that brings
a different one, or none where the folder has one, or one where the folder has
none and already holds an endpoint, is refused, with or without --replace. The
first join into an empty folder sets it; a folder that already holds an
endpoint and has no ca.pem refuses a join file that brings one, with the same
'different ca' line (settled 2026-11-20, security read 2). A JOIN FILE THAT
HOLDS A SEED IS A SECRET FILE, and so is the file --key names: the file --key
names is checked before it is read, and a join file is opened to see whether
it holds a seed and then checked before anything is installed: it must be a
plain file, owned by you, readable by you alone, or join refuses with the two
cases above. A join file that holds only a user JWT is not checked for owner
or mode, but any join file that is not a plain file (a directory, a FIFO, a
device) gets the 'not a plain file of your own' line (settled 2026-11-20,
security read 2; wording 2026-11-21). A join interrupted after the creds were
installed but before the server addresses and ca.pem were written leaves a
folder that refuses a plain retry and that --replace does not finish, because
--replace touches only the creds; it is a known issue for 0.5.6, with the
workaround: move the configuration folder you joined into (~/.config/mash, or
the folder MASH_CONFIG_DIR or --config-dir names) aside and join again. In
0.6.0 a retry finishes a half-done join when the creds match the join file
(settled 2026-11-20).

## What it replaces

New. Replaces mash credential create (0.5.5) and the unbuilt mash credential
install (0.5.6 contract).
