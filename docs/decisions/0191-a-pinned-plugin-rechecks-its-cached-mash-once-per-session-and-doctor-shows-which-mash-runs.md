---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# A pinned plugin re-checks its cached mash once per session, and doctor shows which mash runs

Amends: ADR-0096

## Context and Problem Statement

[ADR 0096](0096-the-plugin-launcher-takes-a-verification-setting-and-the-layer-above-owns-profiles.md) gives the
launcher one setting, `MASH_PLUGIN_REQUIRE_VERIFIED`, off by default. Off, the launcher runs an already cached managed
`mash` without hashing it again and can fall back to a `mash` on the `PATH` after a failed download. So the pins in
`mash.pins` do not stop a replaced cached binary or an older `PATH` binary from running (issue #1290, found by the
security read of #1283). Should the default change?

## Decision Drivers

* Randy's ruling (planner-relayed, "Concur." on option 1): verification stays off by default.
* A replaced cached binary should not run for a whole series of sessions unseen.
* The fallback to `PATH` is wanted by people who work offline, and must not vanish silently.
* Hooks have a 10 s budget and must stay fast.

## Considered Options

* Verified execution by default, no unverified fallback.
* Leave it off and only state the limit.
* Stay off, re-check the cache once per session, and show a `PATH` fallback in `mash doctor` (chosen).

## Decision Outcome

Chosen: with verification off, a pinned plugin hashes its cached managed `mash` once per session, at `__prefetch` and
at `serve` (serve's start is the session boundary) and never in hook mode. A mismatch moves the file aside to
`<binary>.bad-<epoch>` and fetches a fresh verified copy. The fallback to a `mash` on the `PATH` is not removed. The
launcher exports `MASH_LAUNCHED_FROM` (`managed`, `path` or `override`) before it runs `mash`, and `mash doctor` shows a
`launcher` row, only when that variable is set: ok for the pinned copy or `MASH_BIN`, a warning for `PATH`. With
`MASH_PLUGIN_REQUIRE_VERIFIED` on, nothing changes: it re-hashes on every run.

### Consequences

* Good, because a replaced cache is replaced again at the next session start, and a `PATH` mash is visible.
* Bad, because a binary swapped after the session starts still runs until the next one; only the on setting closes that.
* Neutral, because `MASH_LAUNCHED_FROM` is a documented variable the launcher sets, not configuration read from a layer
  above.

### Confirmation

`plugin/scripts/test-launcher.sh` (tampered cache replaced at serve and prefetch, not in hook mode, the three
`MASH_LAUNCHED_FROM` values) and `cmd/mash/launchercheck_test.go`.

## Pros and Cons of the Options

### Verified execution by default

* Good, because the pins become binding.
* Bad, because it goes against Randy's ruling and breaks the offline fallback people rely on.

### Leave it off and state the limit

* Good, because nothing changes.
* Bad, because a replaced cache keeps running unseen.

### Stay off, re-check once per session, show `PATH` in doctor

* Good, because it is cheap, keeps hooks fast and makes the fallback visible.
* Bad, because a swap after the session starts is not caught.

## More Information

Issue #1290; [ADR 0096](0096-the-plugin-launcher-takes-a-verification-setting-and-the-layer-above-owns-profiles.md).
