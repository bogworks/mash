---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (project owner)
---

# mash reads Claude Code's managed settings, read-only, for diagnosis only

## Context and Problem Statement

mash delivers mail through hooks and uses a channel only to wake an idle session (ADR-0024). A channel registers only
if the plugin is on Claude Code's channel allowlist, a managed-settings key (`allowedChannelPlugins`). The install
guide tells every user to write that key into the local managed-settings file. In an organisation that uses
server-managed settings, Claude Code "uses the highest-ranked source that delivers at least one policy key and
ignores the rest rather than merging them", and server-managed settings rank first
([managed settings](https://code.claude.com/docs/en/managed-settings#how-claude-code-combines-managed-sources)). The
local file is then ignored without a word: the channel never registers, idle sessions are not woken, and mash's
`status` tool, which only counts the notifications it wrote, cannot say why. The same organisations may also set
`CLAUDE_CODE_MCP_ALLOWLIST_ENV`, which limits an MCP server's environment, so `MASH_*` variables never reach `mash
serve`. mash had to decide whether, and how far, to look at Claude Code's settings.

## Decision Drivers

* A user who sees "mash half works" needs the reason and the next step from mash itself.
* mash must never change a policy file it does not own, and must never print what is in one.
* The files, paths and rules are Claude Code internals that can move; a wrong "OK" is worse than no answer.
* The reading is advice: Claude Code's own `/status` is the authority.

## Considered Options

* Say nothing; document the server-managed case in the install guide only
* Read the managed settings files, read-only, and report the result in `status` and at `serve` start
* Run `claude doctor` and parse it
* Also write the allowlist entry where mash can

## Decision Outcome

Chosen option: "read the managed settings files, read-only, and report", because it names the cause where the user
already looks and costs no privilege.

* `internal/claudesettings` reads the server-managed cache (`~/.claude/remote-settings.json`) and the endpoint-managed
  file with its `managed-settings.d` drop-ins, in Claude Code's rank order, and applies the first-source-wins rule and
  the `managedSourcesBehavior: "merge"` opt-in. Every path and key name is in that one file, with a link to the docs.
* It never writes, never logs file contents, and returns only key names and booleans. `status` shows one line,
  `channel allowlist:`, as OK, SHADOWED, MISSING, OFF or UNKNOWN, and `serve` prints it once to stderr when it is not
  OK. When it cannot read or parse a file it says UNKNOWN, never OK.
* `status` and `serve` also report `environment: STRIPPED` when `CLAUDE_CODE_MCP_ALLOWLIST_ENV` is `1` in a managed
  source's `env`, in `~/.claude/settings.json`, or in serve's own environment.
* mash does not act on the reading: delivery never depends on it.
* Not read: the macOS configuration profile and the Windows registry (no documented file), so a machine with an MDM
  policy can rank a source above what mash sees. A stale `remote-settings.json` can give a false SHADOWED. Both are why
  the line says to confirm with `/status`.

### Consequences

* Good, because the common failure in managed organisations gets a named cause and a next step.
* Good, because nothing is written and nothing sensitive is printed.
* Bad, because Claude Code can change the paths or rules; the reading then goes UNKNOWN or stale until updated.
* Neutral, because the allowlist line is advice and can disagree with `/status`.

### Confirmation

* `internal/claudesettings/claudesettings_test.go`: `TestServerManagedShadowsLocalFile`,
  `TestMergeOptInUnionsSources`, `TestLocalFileWinsWithoutServerSettings`, `TestDropInsMergeAlphabetically`,
  `TestUnreadableOrBadJSONIsUnknown`.
* `cmd/mash/status_tool_test.go`: `TestStatusAllowlistShadowed`, `TestStatusAllowlistOK`,
  `TestStatusAllowlistMissing`, `TestStatusEnvironmentStripped`.

## Pros and Cons of the Options

### Say nothing

* Good, because there is no code to keep in step with Claude Code.
* Bad, because the failure stays silent, as it was.

### Read the files, read-only

* Good, because it explains the failure inside mash with no privilege.
* Bad, because it depends on Claude Code internals and cannot see MDM policy.

### Run `claude doctor`

* Good, because Claude Code reports its own state.
* Bad, because it needs the `claude` binary on the path, is slow, and its output is not a stable interface.

### Write the entry

* Good, because it could fix the local-file case.
* Bad, because managed settings are an administrator's, not mash's (ADR-0008 in spirit), and a write cannot beat a
  higher-ranked source.

## More Information

* [Managed settings](https://code.claude.com/docs/en/managed-settings),
  [server-managed settings](https://code.claude.com/docs/en/server-managed-settings),
  [channels](https://code.claude.com/docs/en/channels#restrict-which-channel-plugins-can-run).
* Related: ADR-0012 (the endpoint name is chosen per launch), ADR-0024 (mail by hook, channel only wakes),
  ADR-0034 (status text is a compatibility contract).
