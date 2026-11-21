Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint inbox

List what is in an endpoint's inbox, without showing any message's text.

Release: 0.5.6. Group: Endpoints.

## Usage

```text
mashctl endpoint inbox <address> [options]
```

## Options

| Option | Argument | Description |
| --- | --- | --- |
| `--limit` | `<n>` | How many, newest last. Default 20. |
| `--after` | `<number>` | Only messages after this number. The same option as mash inbox. |

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Arguments

| Argument | Required | Description |
| --- | --- | --- |
| `address` | yes | The endpoint, as owner.group.name. |

## Examples

```text
mashctl endpoint inbox acme.demo.bob
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## Output

### Messages

Stream: stdout. Exit code: 0.

```text
2 messages for acme.demo.bob, 1 not delivered.

NUMBER  FROM   WHEN    TRUST     DELIVERED  KIND  SIZE
7       alice  2m ago  verified  no         task  48 B
8       ?      1m ago  absent    yes        task  61 B
```

## JSON

With `--json` the result is this shape. The values are an example.

```json
{
  "address": "acme.demo.bob",
  "total": 2,
  "undelivered": 1,
  "messages": [
    {
      "number": 7,
      "from": "acme.demo.alice",
      "trust": "verified",
      "delivered": false,
      "kind": "task",
      "size": 48
    },
    {
      "number": 8,
      "from": null,
      "trust": "absent",
      "delivered": true,
      "kind": "task",
      "size": 61
    }
  ]
}
```

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Notes

An administrator sees everything stored. FROM is the address a message claims,
and '?' when it names no sender at all (null in the JSON); TRUST says whether
that claim was proven, in mash's own labels: verified, unknown-signer,
claimed, absent, stale or forged. DELIVERED is yes or no, and means only that
the endpoint took the message off the server. Whether the endpoint then showed
it or refused it is decided on the endpoint's own machine and is not something
mashctl can see: by default an endpoint refuses everything that is not
verified, so a 'yes' beside any other trust label most likely means it was
refused.

## What it replaces

Today mashctl inbox inspect, with --from and --to; those are replaced by
--after, the option mash inbox has.
