---
status: accepted
date: 2026-11-24
decision-makers: Randy Bias (mash maintainer)
---

# The 0.5.5 migration script and the refusal of a 0.5.5 configuration folder ship again

## Context and Problem Statement

[ADR 0152](0152-the-release-carries-a-one-time-0-5-5-migration-script.md) made the release carry
`mash-migrate-0.5.5.sh`, a one-time script that rewrites the old `config.json` keys (`url` became `servers`, `tls_ca`
became `ca`), and made both tools refuse a configuration folder that still holds the old keys, naming the script in the
hint. [ADR 0161](0161-0-5-8-removes-the-0-5-5-migration-script-and-the-refusal-of-a-0-5-5-configuration-folder.md)
removed both (pull request #1185), on the ground that nobody ran 0.5.5.

Release 0.5.8 shipped the script. Pull request #1185 merged to main afterwards, during 0.5.9, against the 0.5.9
contract's requirement REQ-028, which reserves that change to the maintainer, and the release check still requires the
script among a release's assets. The planner ruled on 2026-11-24 that the requirement stands. Which record governs
now?

## Decision Drivers

* The release check and the requirement REQ-028 say the script is a release asset; the code and the check must agree.
* A decision record is never deleted or rewritten: a changed decision gets a new record
  ([ADR 0002](0002-record-architecture-decisions-as-madr-records.md)).
* The refusal and its hint only make sense with the script they name.

## Considered Options

* Revert pull request #1185 and record the reversal in a new ADR.
* Keep the removal and change the release check and the requirement instead.

## Decision Outcome

Chosen option: "Revert pull request #1185 and record the reversal in a new ADR", because the planner's ruling keeps
REQ-028 and the release check as they are.

* `scripts/mash-migrate-0.5.5.sh` and its tests are restored, and a release carries the script again, as an attested
  asset listed in `SHA256SUMS`: the release scripts are `install.sh`, `mash-local-nats-setup.sh` and
  `mash-migrate-0.5.5.sh`.
* The detection of a 0.5.5 configuration folder (the keys `url` and `tls_ca`), its refusal in both tools and its hint
  naming the script return, together with `cliframe.ReleaseBase`, the specification text that described them and the
  regenerated reference pages.
* This record supersedes ADR 0161 and reinstates ADR 0152. ADR 0161 stays in the history with the status
  "superseded by ADR-0170"; ADR 0152's status says it is accepted again.

### Consequences

* Good, because the release check, the requirement and the code agree again.
* Good, because a configuration folder left by 0.5.5 is named for what it is, with the script that rewrites it.
* Bad, because every command of both tools reads `config.json` for the old keys before it starts, and the release has
  one more asset to build, attest and verify.
* No release ever went out without the script: 0.5.8 carried it, and the removal never reached a release.
* The removal is proposed again for 0.5.10 (#1446), as its own decision.

### Confirmation

The tests restored with the revert check the refusal in `cmd/mash`, `cmd/mashctl` and `internal/cliframe`, the three
release scripts in `internal/release` and `scripts/releasecheck`, and the same three in the workflow tests.

## Pros and Cons of the Options

### Revert pull request #1185 and record the reversal in a new ADR (chosen)

* Good, because it meets REQ-028 and keeps the history of both decisions readable.
* Bad, because it restores code that the project had already decided to remove, until 0.5.10 takes that up again.

### Keep the removal and change the release check and the requirement

* Good, because it keeps the smaller release.
* Bad, because the planner ruled against it.

## More Information

Supersedes [ADR 0161](0161-0-5-8-removes-the-0-5-5-migration-script-and-the-refusal-of-a-0-5-5-configuration-folder.md)
and reinstates [ADR 0152](0152-the-release-carries-a-one-time-0-5-5-migration-script.md). Requirement: REQ-028; the
planner's ruling of 2026-11-24.
