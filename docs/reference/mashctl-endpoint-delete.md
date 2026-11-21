Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint delete

Delete an endpoint, its inbox and its credentials. The address can never be
used again.

Release: 0.5.6. Group: Endpoints.

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
This deletes acme.demo.bob and the 1 message in its inbox. It cannot be undone.
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
error: deleting needs a terminal to confirm, or --yes
hint: mashctl endpoint delete --help
```

### A server did not confirm the key was cancelled: nothing is deleted

Stream: stderr. Exit code: 1.

```text
error: acme.demo.bob was not deleted: 1 of 2 servers did not confirm
       its key was cancelled
hint: mashctl endpoint delete acme.demo.bob
```

### Cancelled everywhere, then a later step failed (0.5.6 as built)

Stream: stderr. Exit code: 1.

```text
error: deleted acme.demo.bob, but PARTIAL: acme_demo_bob's user key UBIDWPW3 was revoked in the account JWT, so its credentials no longer work, but the removal did not complete: mash: lease: nats: nats: API error: code=400 err_code=10109 description=invalid operation on sealed stream; run the same command again (revoking again is safe)
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
&lt;address&gt;.creds.deleted, so mash no longer counts it as installed; the
path is predictable and nothing prints it. Nothing else on any machine is
touched. THE LATE-FAILURE LINE in 0.5.6 is not how it should read: it carries
an internal word (PARTIAL), the flat name with underscores, a key id and the
server's raw error. Two parts of it vary: the eight-character key id after
'user key', and the cause between 'did not complete: ' and '; run the same
command again'. From 0.5.7 one line replaces it: 'error: deleted
acme.demo.bob, but the removal did not finish: &lt;cause in plain words&gt;'
with the same hint; its credentials already no longer work, and running the
command again is safe.

## What it replaces

Typing the name to confirm is what gh repo delete does.
