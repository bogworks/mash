---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (mash maintainer)
---

# Config and state directories are relocatable by MASH_CONFIG_DIR and MASH_STATE_DIR

## Context and Problem Statement

The XDG change (pull request 65) made mash honour an absolute `XDG_CONFIG_HOME`, with `~/.config/mash` as the legacy fallback. That still gives no way to name a config directory exactly, and the state directory (`spool/` and `hook.log`) has only `XDG_STATE_HOME` as its parent. The hook command builds the state path by hand, and `quickstart-synadia.sh` can only honour a `--config-dir` by rewriting `HOME` (public-readiness audit findings M14 and M15).

## Decision Drivers

* Let a deployment or a test put config and state at any absolute path without faking `HOME`.
* Build on the XDG behaviour and its legacy fallback; with no new variable set, behaviour and paths stay as they were.

## Considered Options

* Keep `HOME` and the XDG variables as the only anchors, and rewrite `HOME` where another location is needed.
* Add `MASH_CONFIG_DIR` and `MASH_STATE_DIR` on top of the XDG behaviour.

## Decision Outcome

Chosen option: "add `MASH_CONFIG_DIR` and `MASH_STATE_DIR`", because it relocates each directory exactly and leaves the XDG behaviour untouched.

An absolute `MASH_CONFIG_DIR` is the config directory and takes precedence over everything: `XDG_CONFIG_HOME` and the legacy `~/.config/mash` are not consulted, because it is an explicit choice. The state directory, which holds `spool/` and `hook.log`, is `$MASH_STATE_DIR`, else `$XDG_STATE_HOME/mash` (absolute only), else `~/.local/state/mash`. A relative `MASH_*` directory is an error naming the variable. `config.Config` carries `StateDir` (replacing `StateHome`, the parent of `mash`), `spool.Dir` takes it, the plugin's hook command uses the same state order, and the quickstart script runs mash with `MASH_CONFIG_DIR` set instead of rewriting `HOME`.

### Consequences

* Good, because a config or state location can be any absolute path, and the quickstart no longer needs `--config-dir` to end in `.config/mash`.
* Neutral, because a relative `XDG_STATE_HOME` was used as given before; it is now ignored, as the XDG specification says.

### Confirmation

`internal/config` tests pin the resolution order and the relative-path errors; `cmd/mash` and `scripts` tests cover the spool under `MASH_STATE_DIR` and a quickstart into a config directory not named `.config/mash`.

## Pros and Cons of the Options

### Keep HOME and the XDG variables as the only anchors

* Good, because nothing changes.
* Bad, because relocating means faking `HOME`, which breaks everything else that reads it, and the state directory cannot be named.

### Add the MASH variables

* Good, because each directory can be named exactly, and one variable names the state directory for the hook command.
* Bad, because there are several ways to set each directory.

## More Information

* Builds on pull request 65 (XDG_CONFIG_HOME with the legacy fallback). Related: ADR-0025 (the spool). Code: `internal/config/config.go`, `internal/spool/spool.go`, `plugin/hooks/hooks.json`.
