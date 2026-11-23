---
status: accepted
date: 2026-11-23
decision-makers: Randy Bias (mash maintainer)
---

# The policy document's range field, flag and placeholder are renamed prefix, with no old name accepted

## Context and Problem Statement

The policy code in the tree (the policy document, the intake, the serve and apply paths, the list set) named the first
part of an address "range": the JSON field `range` of the policy document, the `--range` flag, the `{range}` placeholder
of the policy bucket pattern, and `StageRange` with the stage string "range". The standing order for mash says mash knows
no name of a layer above it, and "range" is a term of the layer above. The 0.5.7 final gate recorded the field as a
departure (#1262) and the lead left it for 0.5.8, because renaming a field of a wire format is a format change
(contract REQ-001 forbids one except by a named exception). The maintainer named this one (the standing order "range
leaves"; planner #2923).

## Decision Drivers

* mash stands alone first and does not carry the vocabulary of a layer above it.
* No program released as 0.5.x contains this code, so no stored state or wire message of a released program carries the field.
* A shim that accepts both names would keep the old name in the format for good.

## Considered Options

* Leave `range` and record the departure.
* Rename with a shim that accepts both names for a release.
* Hard cut: rename everywhere, accept no old name, give a migration note.

## Decision Outcome

Chosen option: the hard cut, by the maintainer's ruling and the lead's confirmation of the name `prefix` (lead #262).

* The policy document field is `prefix`; the flag is `--prefix`; the bucket placeholder is `{prefix}`; the refusal stage
  and its entry are "prefix" (`StagePrefix`); `ErrRangeNeeded` is `ErrPrefixNeeded`.
* A document that still carries a `range` field is refused; a bucket pattern with `{range}` is refused. There is no shim
  and no tolerance of old state.
* The plan line for a document naming another prefix names the field (issue #1258).
* The 0.5.8 release notes carry the migration note: regenerate any policy document written for an earlier release.

### Where the field appears

The field is in the policy document (a signed JSON message submitted to the policy intake, a wire format), in the stored
list set and its explanation output, and in the policy bucket name pattern. All of this is code that is in the tree and
tested but linked into no released program: `go list -deps` of `cmd/mash` and `cmd/mashctl` at the 0.5.7 candidate lists
none of policydoc, policyserve, policyapply, policyintake, policyview, listset, reconcile or issue. A 0.5.7 program
therefore reads and writes no document with this field, and no 0.5.7 command prints it.

### Consequences

* Good: the vocabulary of the layer above leaves the format before anything is released that depends on it.
* Bad: a policy document built from the tree before this change is refused; nothing released produces one.
* Neutral: "range" remains in prose about the first address part in docs/policy-enforcement.md and in the wording of
  internal/issue and ctlkeys; a separate cleanup issue covers it.
