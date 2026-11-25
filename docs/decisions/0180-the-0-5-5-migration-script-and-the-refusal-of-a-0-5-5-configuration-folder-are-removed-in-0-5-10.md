---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer)
---

# The 0.5.5 migration script and the refusal of a 0.5.5 configuration folder are removed in 0.5.10
Supersedes [0170](0170-the-0-5-5-migration-script-and-the-refusal-of-a-0-5-5-configuration-folder-ship-again.md).

## Context and Problem Statement

[ADR 0152](0152-the-release-carries-a-one-time-0-5-5-migration-script.md) made the release carry
`mash-migrate-0.5.5.sh`, a one-time script that rewrites the old `config.json` keys (`url` became `servers`, `tls_ca`
became `ca`), and made both tools refuse a configuration folder that still holds the old keys, with a hint that names
the script. [ADR 0170](0170-the-0-5-5-migration-script-and-the-refusal-of-a-0-5-5-configuration-folder-ship-again.md)
kept both after the removal in pull request #1185 was reverted, and said the removal would be proposed again for 0.5.10
(#1446).

Releases 0.5.8 and 0.5.9 shipped the script. The 0.5.10 proposal removes it. The hint also tells a reader to run
`curl | bash` on a script that nothing checks before it runs (#1458). Should 0.5.10 still carry the script, the
refusal and the hint?

## Decision Drivers

* A reader who ran 0.5.5 has had two releases to migrate; the script is one more asset to build, attest and verify.
* The refusal and its hint only make sense with the script they name, so they go together.
* The hint is an unchecked `curl | bash` command (#1458).
* A decision record is never deleted or rewritten: a changed decision gets a new record
  ([ADR 0002](0002-record-architecture-decisions-as-madr-records.md)).

## Considered Options

* Remove the script, the refusal and its hint together in 0.5.10.
* Keep all three.

## Decision Outcome

Chosen option: "Remove the script, the refusal and its hint together in 0.5.10", as the 0.5.10 proposal says.

* `scripts/mash-migrate-0.5.5.sh` and its tests are removed, and a release from 0.5.10 on carries two scripts,
  `install.sh` and `mash-local-nats-setup.sh`. The release check expects the migration script in releases up to and
  including 0.5.9, which carried it and are still verified, and in no release from 0.5.10 on.
* The detection of a 0.5.5 configuration folder (the keys `url` and `tls_ca`), its refusal in both tools and its hint
  are removed, together with `cliframe.ReleaseBase`, the specification text that described them (including the
  `curl | bash` migration command, #1458) and the regenerated reference pages.
* A 0.5.5 configuration folder is then not specially detected: the tools read it as any other folder, and the old keys
  are ignored.
* This record supersedes ADR 0170. ADR 0170 stays in the history with the status "superseded by ADR-0180".

### Consequences

* Good, because the release carries one asset fewer and the code no longer reads `config.json` for the old keys before
  every command.
* Good, because no printed line tells a reader to pipe an unchecked download into a shell.
* Bad, because a machine still on 0.5.5 gets no named error and no script; it starts from the 0.5.6 configuration keys
  by hand.

### Confirmation

The release check tests in `internal/release` and `scripts/releasecheck` hold 0.5.8 and 0.5.9 to three scripts and 0.5.10
and later to two; the workflow tests hold `release.yml` to the two scripts.

## Pros and Cons of the Options

### Remove the script, the refusal and its hint together in 0.5.10 (chosen)

* Good, because it ends the unchecked command and the extra asset.
* Bad, because a leftover 0.5.5 folder is no longer named.

### Keep all three

* Good, because a leftover 0.5.5 folder is named, with the script that rewrites it.
* Bad, because it keeps code, an asset and an unchecked command for a version nobody is known to run.

## More Information

Supersedes [ADR 0170](0170-the-0-5-5-migration-script-and-the-refusal-of-a-0-5-5-configuration-folder-ship-again.md).
Issues #1446 and #1458.
