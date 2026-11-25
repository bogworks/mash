---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer)
---

# From 0.6.0 the pre-tag gate requires the upgrade-proof receipts, and `publish` takes them

Amends: ADR-0001

## Context and Problem Statement

[ADR 0001](0001-publishing-is-one-resumable-command.md) makes publishing one resumable command whose first step is the
pre-tag gate. The SDK adoption contract (REQ-019, AT-019) says the gate of the switch-on release refuses unless the
upgrade proofs (AT-011 on every platform and executable, AT-012 on every platform) have receipts that match the
candidate, and that the check fails closed. The contract names no switch-on version. The planner's ruling on decision 300
makes 0.6.0 the SDK move. How does that change the one command?

## Decision Drivers

* A release must not be tagged on a proof that did not run, or ran on other bytes.
* The release before the switch-on must behave as it does today.
* `publish` must stay one command; a gate that `publish` cannot satisfy would block every release.

## Considered Options

* Require receipts from 0.6.0; below it, check them only when `--receipts` is given.
* Require receipts on every release from now on.
* Leave receipts to a separate command that `publish` does not run.

## Decision Outcome

Chosen option: require receipts from 0.6.0. `release.SwitchOnVersion` is the one constant. For a version at or above it,
the pre-tag gate refuses without `--receipts <dir>`, and with a receipt set that is incomplete or does not match the
gate's own build on every field. Below it, the receipts are checked only when `--receipts` is given, and then as
AT-011 alone. `release-check publish` takes `--receipts <dir>` and passes it to the pre-tag gate, so the single
resumable command is unchanged for a release that has its receipts. The 0.5.x releases that give no `--receipts` are
gated exactly as before.

### Consequences

* Good, because the switch-on release cannot be tagged on a missing, failed or mismatched proof.
* Good, because earlier releases and their command lines do not change.
* Bad, because `publish` of a 0.6.0 or later release fails until the proof has run and its receipts exist.

### Confirmation

`scripts/releasecheck/receipts_test.go` and `receipts_gate_test.go` run AT-019's cases on both lines;
`internal/release/switchon_test.go` pins the boundary.

## Pros and Cons of the Options

### Require receipts on every release now

* Good, because one rule for every release.
* Bad, because the proofs are not defined for releases before the SDK move.

### A separate command

* Good, because `publish` is untouched.
* Bad, because nothing forces it to run, which is the gap AT-019 closes.

## More Information

* Amends ADR 0001 for the pre-tag gate's inputs only.
* Contract `0.6.0-sdk-adoption`: REQ-019, AT-019, DEC-001.
