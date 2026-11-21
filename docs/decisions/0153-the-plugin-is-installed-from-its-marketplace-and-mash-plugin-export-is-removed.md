---
status: accepted
date: 2026-11-21
decision-makers: Randy Bias (mash maintainer)
---

# The plugin is installed from its marketplace, and `mash plugin export` is removed

## Context and Problem Statement

[ADR 0033](0033-claude-code-plugin-is-the-distribution-unit.md) made the Claude Code plugin, in the repo's own
marketplace, the distribution unit, and added `mash plugin export <dir>` (PR #60) so a consumer could write the binary's
embedded plugin and a one-entry marketplace into a local directory and pin the plugin to the binary's version.

The 0.5.6 specification ([ADR 0147](0147-the-0-5-6-command-line-and-mcp-surface-is-built-from-a-specification-that-supersedes-contract-revision-26.md))
says the plugin is installed from its marketplace: `claude plugin marketplace add`, then `claude plugin install`. It
lists no `plugin export` command. Keeping the command means the binary carries a second way to install the plugin that
no document asks a person to use.

## Decision Outcome

Remove `mash plugin export` and the embedded copy of the plugin it wrote. The plugin comes from its canonical
marketplace (this repository's `.claude-plugin/marketplace.json`) through the two `claude plugin` commands in
`docs/install.md`. This supersedes the `mash plugin export` part of ADR 0033; the rest of 0033 (the repo is its own
marketplace, the hook always exits 0, `plugin.json`'s version equals the release version) stands.

A fork that publishes its own plugin edits its own marketplace and docs.

### Consequences

* Good, because there is one way to install the plugin, and it is the one the docs and the specification name.
* Good, because the binary no longer embeds a copy of the plugin that can drift from the repository's.
* Bad, because pinning the plugin to a binary's exact build by a local directory is gone; the plugin runs against the
  current and previous binaries as 0033 already said.
