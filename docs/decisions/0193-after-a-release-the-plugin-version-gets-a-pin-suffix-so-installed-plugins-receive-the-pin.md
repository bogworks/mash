---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# After a release the plugin version gets a `-pin.1` suffix, so installed plugins receive the pin

Amends: ADR-0153

## Context and Problem Statement

[ADR 0153](0153-the-plugin-is-installed-from-its-marketplace-and-mash-plugin-export-is-removed.md) keeps from
[ADR 0033](0033-claude-code-plugin-is-the-distribution-unit.md) that "`plugin.json`'s version equals the release
version". The plugin's launcher downloads a `mash` binary only when `plugin/scripts/mash.pins` names a version and the
sha256 of each platform binary, and those sums exist only after the release is published (`docs/release.md`, step 8
and "Pinning the plugin after a release").

Claude Code updates an installed plugin only when its version changes. A pins commit that leaves `plugin.json` at the
release version would never reach a person who already installed that version. Since 0.5.7 each pin commit has
therefore set `plugin.json` to `<version>-pin.1` (for example `0.5.9-pin.1`, commit c99a246e; `0.5.10-pin.1`, #1791),
which departs from "equals the release version" and is written only in `docs/release.md`.

## Decision Drivers

* An installed plugin must receive the pins file without waiting for the next release.
* The pins can only be computed from the published binaries, so they cannot be in the release commit.
* The next release's gate should still see the plain version.

## Considered Options

* Keep `plugin.json` at the release version after the pin commit (installed plugins never receive the pins).
* Bump to `<version>-pin.1` in the post-release pin commit (chosen).
* Publish a patch release for every pin.

## Decision Outcome

Chosen option: "bump to `<version>-pin.1`", because it is the smallest change that makes Claude Code update the plugin.

The post-release pin commit written by `plugin/scripts/pin-plugin` sets `plugin/.claude-plugin/plugin.json`'s `version`
to `<version>-pin.1` and changes nothing else in the manifest. At the tagged release commit `plugin.json`'s version
still equals the release version, as ADR 0153 says; the suffix exists only on `main` after the release. The next
release's pre-tag edit sets the plain new version again, which the gate requires. This amends the sentence of ADR 0153
(carried from ADR 0033) that the version equals the release version; the rest of both stands.

### Consequences

* Good, because installed plugins update and receive the pins right after a release.
* Good, because the release commit and tag are unchanged.
* Bad, because `plugin.json` on `main` between a release and the next is not the release version.

### Confirmation

* `docs/release.md` step 8 and "Pinning the plugin after a release" describe the suffix.
* `plugin/scripts/pin-plugin` writes it, and the pre-tag gate (`internal/release`, `TestCheckPluginVersion`) requires
  the plain version at the tag.

## Pros and Cons of the Options

### Keep `plugin.json` at the release version

* Good, because it matches ADR 0153 as written.
* Bad, because installed plugins never receive the pins.

### Publish a patch release for every pin

* Good, because every version is a release version.
* Bad, because a release for a two-file bookkeeping commit is far heavier than the suffix.

## More Information

* Amends [ADR 0153](0153-the-plugin-is-installed-from-its-marketplace-and-mash-plugin-export-is-removed.md).
* Related: [ADR 0033](0033-claude-code-plugin-is-the-distribution-unit.md).
* Raised by the review of #1791 (M1).
