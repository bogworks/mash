Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint delete

Delete an endpoint, its inbox and its credentials. The address can never be
used again.

Release: 0.5.9. Group: Endpoints.

## Usage

```text
mashctl endpoint delete <address> [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--yes` |  | Do not ask. |
| `--dry-run` |  | Print what would change, and change nothing. |

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | The endpoint, as owner.group.name. |

## Examples

```text
mashctl endpoint delete acme.demo.bob
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Asked, then deleted

Stream: stdout. Exit code: 0.

```text
This deletes acme.demo.bob and the 1 message in its inbox.
Mail that arrives before it is gone is lost too. It cannot be undone.
Type the address to confirm: acme.demo.bob
Deleted endpoint acme.demo.bob
```

### With --yes

Stream: stdout. Exit code: 0.

```text
Deleted endpoint acme.demo.bob
```

### Wrong text typed

Stream: stderr. Exit code: 1.

```text
error: that was not the address; nothing was deleted
```

### No terminal and no --yes

Stream: stderr. Exit code: 2.

```text
error: mashctl endpoint delete needs a terminal to type the address
hint: mashctl endpoint delete --help
```

### A server did not confirm the key was cancelled: nothing is deleted

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob was not deleted: 1 of 2 servers did not confirm
       its key was cancelled
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled, then a later step failed: a. its lock was lost

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is partly deleted: its lock was lost
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled, then a later step failed: b. its record could not be read

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is partly deleted: its record could not be read
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled, then a later step failed: c. its record changed meanwhile

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is partly deleted: its record changed meanwhile
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled, then a later step failed: d. record removal not confirmed

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is partly deleted: record removal not confirmed
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled, then a later step failed: e. its name was not freed

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is partly deleted: its name was not freed
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled, then a later step failed: f. its address was not closed

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is partly deleted: its address was not closed
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled, then a later step failed: g. its inbox is still there

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is partly deleted: its inbox is still there
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled, then a later step failed: h. an earlier removal is stuck

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is partly deleted: an earlier removal is stuck
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled, then a later step failed: i. its creds file was not moved

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is partly deleted: its creds file was not moved
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled, then a later step failed: j. its creds path is not a file

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob is partly deleted: its creds path is not a file
hint: mashctl endpoint delete acme.demo.bob
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "address": "acme.demo.bob",
  "deleted": true,
  "messages_deleted": 1
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

Order. The endpoint's key is cancelled on every server first; only when every
server confirms is anything removed, so a failure there leaves the endpoint
whole. If this machine holds the endpoint's creds file, it is set aside as
&lt;address&gt;.creds.deleted, a marker that keeps the endpoint's name and its
JWT only, no key, so mash no longer counts it as installed; the path is
predictable and nothing prints it. Nothing else on any machine is touched. If
a step after the cancellation fails, the error is one line naming the address
and the cause in plain words, with the command to run again; the key is
already cancelled, so its credentials no longer work, and running the command
again is safe.

## What it replaces

In 0.5.5, this was also reachable as deprovision, which is removed. Typing the
name to confirm is what gh repo delete does.
