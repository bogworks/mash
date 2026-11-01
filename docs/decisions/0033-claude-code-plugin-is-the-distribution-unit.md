---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# The Claude Code plugin, in the repo's own marketplace, is the distribution unit

## Context and Problem Statement

A mash session needs three things wired into Claude Code: the MCP server (`mash serve`), the hooks that deliver mail
(ADR-0024), and, for the idle wake, a channel registration. Claude Code packages such wiring as a plugin, published
through a marketplace (a catalogue of plugins). Channels add a constraint: the design-era spike found that
`--channels` "takes only Anthropic-approved plugins", and the development flag that bypasses this asks for
confirmation on every launch. mash had to decide how it reaches a user's sessions.

## Decision Drivers

* Design goal 7: "Ready to open-source ... a Claude Code plugin in its own marketplace" (spec §1.1).
* A channel plugin outside Anthropic's list must be named in a managed-settings allowlist, which needs a stable
  marketplace and plugin name.
* An installed plugin must never block a user's session, whichever mash binary is on `PATH`.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Document manual MCP server and hook configuration
* Publish through a third-party or Anthropic marketplace
* Ship the plugin from the mash repo, which is its own marketplace

## Decision Outcome

Chosen option: "the repo is its own marketplace", because of goal 7 and because the channel allowlist needs a
marketplace mash controls (design-era spike, maintainer design discussion, 2026-10-21, not public; spec §11.2).

As built (mash packaging on the Claude Code host):

* `.claude-plugin/marketplace.json` names marketplace `mash` with plugin `mash` in `plugin/`. The plugin is
  `plugin/.mcp.json` (runs `mash serve` with `MASH_SPOOL_FROM_START=1`), `plugin/hooks/hooks.json` (`PostToolUse` and
  `UserPromptSubmit`, both running `mash hook`) and `plugin/.claude-plugin/plugin.json`.
* The `mash` binary is a separate install and must be on `PATH` (`docs/install.md`).
* `mash plugin export <dir>` (PR #60) writes this build's embedded plugin and a one-entry marketplace into a local
  directory, because "Claude Code can pin a marketplace only by local dir" (PR #60). A consumer needed to pin the
  plugin to the binary's version.
* The hook command always exits 0 and sends `mash hook`'s stderr to a log (PR #71), because an older mash without a
  `hook` command "exits 2, which Claude Code treats as blocking".
* `plugin.json`'s version equals the release version (PR #59).

### Consequences

* Good, because one install wires the server, hooks and environment together, and the release gate tests them as a
  unit (ADR-0034).
* Good, because a missing or old binary degrades to no mail, never a blocked session.
* Bad, because the binary and plugin are installed separately and can drift; an installed plugin runs against the
  current and previous binaries.
* Bad, because the idle wake still needs per-machine channel setup (an allowlist in managed settings, or the
  development flag), which the plugin cannot do.

### Confirmation

* `cmd/mash/plugin_test.go`: `TestPluginManifests`.
* `cmd/mash/plugin_export_test.go`: `TestPluginExport`, `TestPluginExportOverwrites`, `TestPluginEmbedCoversDirectory`.
* `cmd/mash/plugin_hook_test.go`: `TestPluginHook_neverBlocksAndLogsStderr`, `TestPluginCommandsAreFrozen`.
* `internal/release/plugin_test.go`: `TestCheckPluginVersion`, `TestRepo_pluginManifestHasASemverVersion`.

## Pros and Cons of the Options

### Manual configuration

* Good, because it needs no marketplace.
* Bad, because each user hand-edits MCP and hook settings, and a channel allowlist has no plugin to name.

### Someone else's marketplace

* Good, because users may already trust it.
* Bad, because releases and the plugin's name would depend on another party.

### Own marketplace

* Good, because mash controls the name the allowlist uses and ships plugin and code together.
* Bad, because users must add the marketplace and, for channels, allowlist it themselves.

## More Information

* Design spec §1.1 (goal 7) and §11.2 (plugin and marketplace); `docs/install.md`.
* PRs #59, #60 (2026-10-28), #71 (2026-10-28).
* Related: ADR-0022 (one endpoint manager per session), ADR-0024 (hooks), ADR-0032 (host adapter), ADR-0034 (frozen
  plugin commands).
* **Not yet true:** the descriptions in `plugin/.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json`
  still say mail is pushed "through Claude Code channels"; since hook delivery (ADR-0024) the channel only wakes an
  idle session.
