Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# MCP tool version

Print the version.

Release: 0.5.6. Command line equivalent: `mash version`.

## Structured result

The structured content of a successful result. The values are an example.

```json
{
  "version": "0.5.6",
  "commit": "3f9a41c2e0b7d5a1c4e08b2d77f3a95e1b6c4098",
  "plugin": "0.5.6"
}
```

## Text result

### Version

Stream: stdout. Exit code: 0.

```text
mash v0.5.6 (3f9a41c)
```
