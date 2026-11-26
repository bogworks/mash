---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# The withdrawn policy adoption and intake code is removed from main

## Context and Problem Statement

[ADR 0129](0129-the-policy-commands-are-not-part-of-v0-5-0-and-the-engine-stays-internal-until-it-works-standalone.md)
keeps the policy engine internal until it works standalone. The contract that described adopting existing accounts
and taking policy requests in through an intake (issuing credentials for requesters, reconciling a live account to a
document) was withdrawn on 15 November. Its code stayed on main, unreachable from any released binary, and
staticcheck v0.8.2 (ADR 0190) now fails lint on the parts nothing calls.

## Decision Drivers

* Lint must pass, and main should not carry code for a withdrawn design.

## Considered Options

* Keep the code until the policy work renames its packages.
* Remove it now (chosen).

## Decision Outcome

Chosen option: "remove it now", because nothing calls it and lint fails on it.

The change removes the code of the withdrawn contract, and the adoption code that no command calls: `internal/policyintake`, `internal/requesterreg`, `internal/issue` and
`internal/reconcile`; in `internal/policyserve` the adapters, the applier with its tests, and the helpers only they
used (`settled.go` and its test helpers). `internal/policyserve` stays with the signer, the server limits and the
inbox and credential provisioning. Two comments that named `policyintake` are corrected.

Not changed here: the policy work is renaming `listset`, `policydoc`, `policyapply`, `policy` and `policyview`
(owner, group, instance) and decides what else of the adoption design leaves those packages (`Submitter`,
`NextSubmitter`, `adoptionUnchecked` in `policydoc`) and whether `policyserve` stays.

### Consequences
* Good, because main carries no code for a design that was withdrawn, and lint is clean.

* Good, because the merge-queue exception rule of ADR 0182 and the release check's list of policy packages
  (`scripts/releasecheck/unlinked.go`) name only packages that exist. This decides that change to the gate: the
  paths of the deleted packages are not kept as a guard, because bringing one back is a new reviewed pull request
  anyway (the maintainers' ruling, 26 November).
* Bad, because a package restored at one of those paths is no longer recognised by name by those two gates.

### Confirmation

* `go build ./...`, `go vet ./...` and staticcheck pass; the full test suite passes in a container.

## Pros and Cons of the Options

### Keep the code

* Good, because the policy rename touches fewer files at once.
* Bad, because lint fails on the unused symbols.

## More Information

* ADRs 0104, 0111, 0112, 0119, 0121 and 0124 are already superseded by ADR 0131 and are not touched.
* Related: [ADR 0129](0129-the-policy-commands-are-not-part-of-v0-5-0-and-the-engine-stays-internal-until-it-works-standalone.md),
  ADR 0190.
