---
status: accepted
date: 2026-11-21
decision-makers: Randy Bias (mash maintainer)
---

# 0.5.7 removes the 0.5.5 migration script and the refusal of a 0.5.5 configuration folder

## Context and Problem Statement

Mash 0.5.6 renamed two keys in `config.json` (`url` became `servers`, `tls_ca` became `ca`). It shipped a one-time
migration script, `mash-migrate-0.5.5.sh`, as a release asset
([ADR 0152](0152-the-release-carries-a-one-time-0-5-5-migration-script.md)), and every command of both tools refused a
configuration folder that still held the old keys, naming the script in its hint.

The maintainer has since ruled that nobody ran 0.5.5 and that it was not really released. Should mash keep carrying a
migration path for a version no one used?

## Decision Drivers

* Mash keeps no old state: a change is a hard cut, with no tolerance for what an earlier version left behind.
* The refusal runs before every command in both tools, and the script is one more asset to build, attest, list in
  `SHA256SUMS` and verify, all for a machine that does not exist.
* A migration is a script, never a command-line option, and a script no one needs is dead weight.

## Considered Options

* Remove the script and the refusal in 0.5.7.
* Keep both for a few more releases.
* Keep the refusal and drop the script.

## Decision Outcome

Chosen option: "Remove the script and the refusal in 0.5.7", because the premise of
[ADR 0152](0152-the-release-carries-a-one-time-0-5-5-migration-script.md) (that machines exist that ran 0.5.5) was
ruled false, and the hard-cut, no-old-state rule leaves nothing for either to do.

* `scripts/mash-migrate-0.5.5.sh` and its test are deleted, and a release no longer carries it: the release scripts are
  `install.sh` and `mash-local-nats-setup.sh`, so a release has twenty assets, not twenty-one.
* The detection of a 0.5.5 configuration folder (the keys `url` and `tls_ca`) and its refusal are removed from both
  tools, together with `cliframe.ReleaseBase`, which only wrote the refusal's hint.
* The specification text that described the refusal, the script and the comparison with 0.5.5 is removed, and the
  reference pages are regenerated.

### Consequences

* Good, because every command starts without first reading `config.json` for keys no release uses, and the release has
  one asset fewer to build, attest and verify.
* Bad, because a configuration folder that still holds `url` or `tls_ca` is no longer detected: it is read as a current
  folder, and the old keys are not what 0.5.7 reads.
* The script stays a release asset of 0.5.6 and earlier releases; only releases from 0.5.7 omit it.
* [ADR 0152](0152-the-release-carries-a-one-time-0-5-5-migration-script.md) is superseded by this decision.

### Confirmation

Tests in `internal/release` and `scripts/releasecheck` require exactly two release scripts, their `SHA256SUMS` lines
and their attestations, and the workflow tests require the same two in the sums, attest, draft and dry-run-verify jobs.
The `mash` and `mashctl` command tests no longer expect a refusal for a folder holding `url` or `tls_ca`.

## Pros and Cons of the Options

### Remove the script and the refusal in 0.5.7 (chosen)

* Good, because it follows the hard-cut rule and leaves no code for a version nobody ran.
* Bad, because a folder that does hold the old keys gets no help.

### Keep both for a few more releases

* Good, because a machine that did run 0.5.5 would still be told what to do.
* Bad, because the ruling is that there is no such machine, and each release would keep building and verifying the script.

### Keep the refusal and drop the script

* Good, because a stray old folder is still named.
* Bad, because the refusal's hint points at a script that no release carries.

## More Information

Supersedes [ADR 0152](0152-the-release-carries-a-one-time-0-5-5-migration-script.md). The maintainer's ruling is
recorded in the 0.5.7 code cut.
