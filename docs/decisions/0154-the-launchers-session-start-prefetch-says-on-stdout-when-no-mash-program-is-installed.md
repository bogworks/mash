---
status: accepted
date: 2026-11-21
decision-makers: Randy Bias (mash maintainer)
---

# The launcher's session-start prefetch says on stdout when no mash program is installed

## Context and Problem Statement

[ADR 0096](0096-the-plugin-launcher-takes-a-verification-setting-and-the-layer-above-owns-profiles.md) left the
launcher's prefetch (the plugin's SessionStart step that warms the managed binary) silent on stdout: "Stdout stays the
binary's." When no mash program is installed, the agent therefore has no mash tools and nothing says why, and the person
is not told to install mash.

The 0.5.6 specification (`spec/mash.json`, the hook's "When mash is not installed" entry) says the launcher, run at
session start, prints one line on stdout, so the agent sees it and can tell the person: "mash is not installed: the mash
plugin found no mash program. Install it: curl -fsSL <release base>/install.sh | bash".

## Decision Drivers

* The specification requires the line, on stdout, at session start.
* The SessionStart hook's stdout is shown to the agent; stderr is not.
* The prefetch must still never fail or block a session.

## Considered Options

* Keep the prefetch silent, as ADR 0096 left it.
* Print the one line on stdout, only when no mash program is available (chosen).

## Decision Outcome

Chosen option: "print the one line", because the specification requires it.

The prefetch writes the not-installed line to stdout when there is no `MASH_BIN` override, no managed copy and no `mash`
on `PATH`; in every other case it writes nothing to stdout, as before. It still exits 0. The line names the project's
own latest-release base; the launcher does not derive it from `MASH_RELEASE_BASE`, which is the base of the versioned
binary downloads and has no `install.sh` under it. This departs from ADR 0096's "Stdout stays the binary's" for the
prefetch only; the verification setting, `MASH_BIN` and the downloads are unchanged.

### Consequences

* Good, because a person with no mash program is told how to install it, in the one place the agent sees.
* Good, because the prefetch still never fails or blocks.
* Bad, because a fork or mirror that wants its own address in the line edits its launcher.

### Confirmation

`plugin/scripts/test-launcher.sh` checks the line exactly, on one line of stdout, for the missing-binary cases, and that
a successful prefetch is silent. `cmd/mash` plugin hook tests check the same sentence.

## Pros and Cons of the Options

### Keep the prefetch silent

* Good, because it changes nothing.
* Bad, because the agent has no mash tools and no reason is given; it departs from the specification.

### Print the one line when no mash program is available

* Good, because it follows the specification and costs one line, only in the broken case.
* Bad, because stdout of the prefetch is no longer always empty.

## More Information

* Departs from the prefetch sentence of [ADR 0096](0096-the-plugin-launcher-takes-a-verification-setting-and-the-layer-above-owns-profiles.md); 0096 is otherwise unchanged and keeps its status.
* Follows [ADR 0147](0147-the-0-5-6-command-line-and-mcp-surface-is-built-from-a-specification-that-supersedes-contract-revision-26.md): the specification.
