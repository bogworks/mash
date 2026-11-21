Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint permissions

Print the NATS permissions an endpoint's user needs, for someone who runs
their own NATS.

Release: 0.5.6. Group: Endpoints.

## Usage

```text
mashctl endpoint permissions <address> [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | The endpoint, as owner.group.name. |

## Examples

```text
mashctl endpoint permissions acme.demo.bob
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Permissions

Stream: stdout. Exit code: 0.

```text
(the same JSON as --json: this command prints data,
 for pasting into a NATS configuration)
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "pub": {
    "allow": [
      "agents.mail.acme.>",
      "_INBOX.acme.>"
    ],
    "deny": []
  },
  "sub": {
    "allow": [
      "_INBOX.acme.acme_demo_bob.>"
    ],
    "deny": []
  }
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

The exact subjects are the code's; the example shows the shape, which is the
permissions object of a NATS user JWT, with NATS's own key names: pub and sub,
each with allow and deny.
