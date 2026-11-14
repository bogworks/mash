---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# The plugin launcher takes a verification setting and the layer above owns profiles

## Context and Problem Statement

A pinned plugin carries the release version and the sha256 of each platform binary in `mash.pins`, and its launcher
downloads and verifies that binary. When the download fails, the hash does not match, there is no pinned hash for the
platform, or the downloaded binary does not run or reports another version, the launcher falls back to whatever `mash`
is on the `PATH`. Two other paths also run a binary nothing has verified: an unpinned plugin uses `mash` on the `PATH`,
and a binary already cached from an earlier download is run without checking it again, so a file replaced after the
download is run. The pin is therefore a preference and not a guarantee. Some deployments want that flexibility because an
old install keeps the plugin working offline; others want the pin to be binding. How does the launcher serve both, and
who decides which a deployment gets?

## Decision Drivers

* A deployment that needs the pin to be binding must be able to have it, with no path that runs an unverified binary.
* A deployment that relies on the fallback must keep working unchanged.
* mash must not interpret named security profiles: what a profile means is a decision for the layer above mash, which
  knows the deployment.
* Each behaviour is one concrete setting, read in one place and documented with its default.
* A failure must say what failed, in either case.

## Considered Options

* One concrete boolean setting, `MASH_PLUGIN_REQUIRE_VERIFIED`, defaulting to off (chosen).
* Named profiles (a single word that mash would expand into settings) that mash reads and interprets.
* Always verify.
* Never verify, as before.
* A separate setting per failure type.

## Decision Outcome

The launcher has one concrete setting, `MASH_PLUGIN_REQUIRE_VERIFIED`, and no notion of a named profile. The layer above
mash decides what a profile means for a deployment and sets this and any other concrete settings; mash only reads them.

When it is on, the launcher never runs a binary it has not verified against `mash.pins`. It refuses, printing what
failed and how to turn the setting off to stderr and exiting 127, in these cases: the plugin is unpinned (`mash.pins` is
missing, empty, unreadable or has no valid `version=` line; the error names the file); the cached managed binary's sha256
does not match `mash.pins` (it is hashed on every run, with no re-fetch and no fallback to the `PATH`; it is also refused
when there is no pinned sha256 for the platform or no hashing tool); and the pinned binary cannot be fetched or verified
(a failed download, a sha256 mismatch, no pinned sha256 for the asset, an unsupported OS or architecture, no hashing
tool, a downloaded binary that does not run or reports the wrong version, or a hook skipping a download that failed
recently). It never runs a `mash` from the `PATH`.

When it is off, which is the default, behaviour is what it was before: a failed pinned download falls back to `mash` on
the `PATH` with a one-line warning naming what failed; an unpinned plugin uses `mash` on the `PATH` with a warning naming
the pins file; and the cached managed binary is used as it is, without being hashed again.

Values: `1`, `true`, `yes` and `on` are on; `0`, `false`, `no` and `off` are off; unset takes the default (off); any
other value, including the empty string, fails closed: it is treated as on, with a note on stderr naming the value.

The setting is read in one function, `require_verified`, and the default is the single assignment in it, so the name, the
source and the default can change without touching the rest of the launcher. The name and the source, an environment
variable, are provisional.

Hooks keep their download budget and failure marker. When the setting is on, a hook after a failed download refuses for
the next 300 s without trying again; that is by design, since the alternative is running an unverified binary. The setting
covers what mash fetches; an operator's `MASH_BIN` is trusted as given and is never verified, whether the setting is on
or off. A successful verified download is unchanged, and so is the prefetch. Stdout stays the binary's.

### Consequences

* Good, because a deployment can make the pin binding, and the layer above mash can choose that per deployment.
* Good, because mash carries no profile vocabulary to keep in step with the layers above it.
* Good, because the default keeps working as before.
* Good, because a cached binary replaced after its download is caught when the setting is on, at the cost of hashing it
  on every run.
* Good, because the setting can be renamed or moved by editing one function.
* Bad, because the default leaves the pin bypassable until a deployment turns the setting on.
* Bad, because when it is on a hook or server fails when the network is down and no copy has been downloaded, when the
  plugin is unpinned, and for 300 s after a hook's failed download.
* Bad, because an environment variable is something the process's parent controls, so it does not stop an attacker who
  controls that environment; that is a reason the source is provisional.
* Accepted: there is a small gap between hashing the cached binary and executing it, so a file replaced in that interval
  is run. Closing it would need an executable descriptor, which a shell launcher cannot hold.
* Accepted: `mash.pins` takes the first matching line for the version and for each asset, so a duplicate later in the file
  is ignored.
* Accepted: the prefetch exits 0 without hashing a binary that is already cached; the next run verifies it when the
  setting is on.

### Confirmation

The launcher tests cover the setting on and off with a failed download, a hash mismatch and a success (on refuses with
exit 127 and runs nothing from the `PATH`; off falls back with a warning naming the cause; success runs the pinned binary
with no warning), the default equal to off, each accepted value, an invalid value and an empty value treated as on, a hook
skipping a recent failure, an unpinned plugin and one with a malformed `mash.pins` (on refuses and names the file, off
warns and uses the `PATH`), a cached binary that matches (runs) and one that was changed (on refuses, also in hook mode,
and does not run it; off runs the cached copy as before), and `MASH_BIN` and the prefetch unchanged.

## Pros and Cons of the Options

### One concrete boolean setting, default off (chosen)

* Good, because it serves both kinds of deployment with one switch, and the layer above decides which.
* Good, because the default changes nothing.
* Bad, because the default is the weaker choice.

### Named profiles that mash reads and interprets

* Good, because one word would set several behaviours.
* Bad, because mash would then decide what a profile means, which is the layer above's decision; rejected under the
  maintainer's ruling that mash exposes concrete settings only.

### Always verify

* Good, because the pin always binds.
* Bad, because it breaks an offline or air-gapped install that relies on a `mash` on the `PATH`.

### Never verify

* Good, because it is the current behaviour and never blocks.
* Bad, because the pin can always be bypassed.

### A separate setting per failure type

* Good, because each failure could be handled differently.
* Bad, because a hash mismatch and an unreachable host are both a pinned binary that could not be verified or fetched, and
  more settings are more to get wrong.

## More Information

* Related: [ADR 0033](0033-claude-code-plugin-is-the-distribution-unit.md).
