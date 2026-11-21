Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

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

It never connects to the server; it reads what mash serve has stored on disk.
Like mash serve, hook acts only for an endpoint that was named; with none
named it prints nothing. When mash is not installed. The plugin's launcher,
run at session start, prints one line on stdout, so the agent sees it and can
tell the person: 'mash is not installed: the mash plugin found no mash
program. Install it: curl -fsSL &lt;release base&gt;/install.sh | bash'.
&lt;release base&gt; is
https://github.com/bogworks/mash/releases/latest/download, written into the
launcher; a fork or a mirror edits that line in its own launcher. It is not
derived from the plugin's release setting, which names the versioned download
base and has no install.sh under it.
