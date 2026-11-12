---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# The plugin launcher has a provisional security stance setting

## Context and Problem Statement

A pinned plugin carries the release version and the sha256 of each platform binary in `mash.pins`, and its launcher
downloads and verifies that binary. When the download fails, the hash does not match, there is no pinned hash for the
platform, or the downloaded binary does not run or reports another version, the launcher falls back to whatever `mash`
is on the `PATH`. The pin is therefore a preference and not a guarantee: anyone who can make the download fail or the
hash differ, or who can put a `mash` on the `PATH`, gets their binary run in place of the pinned one. Some deployments
want that fallback because an old install keeps the plugin working offline; others want the pin to be binding. Which
should the launcher do?

## Decision Drivers

* A deployment that needs the pin to be binding must be able to have it.
* A deployment that relies on the fallback must keep working unchanged.
* The setting's name and source are not settled, so they must be easy to change.
* A failure must say what failed, in either case.

## Considered Options

* One setting with two values, `strict` and `relaxed`, defaulting to `relaxed` for now (chosen).
* Always strict.
* Always relaxed, as before.
* A separate setting per failure type.

## Decision Outcome

The launcher has one security stance with two values. `strict` refuses when the pinned binary cannot be fetched or
verified: it prints what failed, that the stance is strict and how to relax it, to stderr, and exits 127, and it never
runs a `mash` from the `PATH`. `relaxed` falls back to `mash` on the `PATH` as before and prints a one-line warning on
stderr that names what failed. The default is `relaxed` for now, so behaviour does not change until a deployment opts in.

The setting is read in one function, `security_stance`, and the default is one assignment in it, so the name, the
source and the default can change without touching the rest of the launcher. The name `MASH_SECURITY_STANCE` and its
source, an environment variable, are provisional. An unrecognised value fails closed: it is treated as `strict`, with a
note on stderr that names the value.

The stance applies only to a pinned plugin whose pinned binary could not be fetched or verified. `$MASH_BIN`, the managed
copy, a successful download, the unpinned path (nothing to verify) and the prefetch are unchanged. Stdout stays the
binary's.

### Consequences

* Good, because a deployment can make the pin binding.
* Good, because the default keeps current behaviour, and the fallback is now announced with its cause.
* Good, because the setting can be renamed or moved by editing one function.
* Bad, because the default leaves the pin bypassable until a deployment sets `strict`.
* Bad, because under `strict` a hook or server fails when the network is down and no copy has been downloaded.
* Bad, because an environment variable is something the process's parent controls, so it does not stop an attacker who
  controls that environment; that is a reason the source is provisional.

### Confirmation

The launcher tests cover both stances with a failed download, a hash mismatch and a success (strict refuses with exit
127 and runs nothing from the `PATH`; relaxed falls back with a warning naming the cause; success runs the pinned binary
with no warning), the default equal to relaxed, an invalid value treated as strict, a hook skipping a recent failure,
and the override, managed copy, unpinned path and prefetch unchanged.

## Pros and Cons of the Options

### One setting, strict or relaxed, default relaxed for now (chosen)

* Good, because it serves both kinds of deployment with one switch.
* Good, because the default changes nothing.
* Bad, because the default is the weaker stance.

### Always strict

* Good, because the pin always binds.
* Bad, because it breaks an offline or air-gapped install that relies on a `mash` on the `PATH`.

### Always relaxed

* Good, because it is the current behaviour and never blocks.
* Bad, because the pin can always be bypassed.

### A separate setting per failure type

* Good, because each failure could be handled differently.
* Bad, because a hash mismatch and an unreachable host are both a pinned binary that could not be verified or fetched, and
  more settings are more to get wrong.

## More Information

* Related: [ADR 0033](0033-claude-code-plugin-is-the-distribution-unit.md).
