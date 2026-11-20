Generated from spec/*.json by scripts/refgen. Do not edit.

# mash hook

Print mail waiting for this session, in the form the agent host expects. Run
by the host after each tool call and with each prompt.

Release: 0.5.6. Group: Integration.

Visible and documented for anyone wiring mash into an agent host other than
Claude Code.

## Usage

```text
mash hook [options]
```

The global options of `mash` also apply: see [mash](mash.md#global-options).

## Examples

```text
mash hook
```

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Always, so that a hook can never block a session. |

## Output

### Mail waiting

Stream: stdout. Exit code: 0.

```text
(the waiting messages, as context for the agent)
```

### Nothing waiting

Stream: stdout. Exit code: 0.

```text
(nothing)
```

## MCP tool

None. Integration. This is how an agent host runs mash; it is not something an
agent calls.

## Notes

Like mash serve, hook acts only for an endpoint that was named; with none
named it prints nothing. When mash is not installed. The plugin's launcher,
run at session start, prints one line on stdout, so the agent sees it and can
tell the person: 'mash is not installed: the mash plugin found no mash
program. Install it: curl -fsSL <release base>/install.sh | bash'. <release
base> is filled in from the plugin's own release setting, the one its launcher
already uses to find the program, so a fork or a mirror points at itself; in
the project's own plugin it is
https://github.com/bogworks/mash/releases/latest/download. Today nothing is
said and the agent simply has no mash tools.

## What it replaces

As built. It never connects to the server; it reads what mash serve has stored
on disk.
