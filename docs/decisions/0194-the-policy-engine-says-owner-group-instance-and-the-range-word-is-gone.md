---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# The policy engine says owner, group and instance; the word `range` is gone from it

Replaces: the unmerged `prefix` rename of the held pull request #1286 (planned as ADR 0162, never merged; this record stands in for it)

## Context and Problem Statement

The policy engine (`internal/listset`, `policydoc`, `policyapply`, `policyview`) was written when an address was
`range.owner.name` and the first part was fixed per account. An address is now `owner.group.instance` (ADR 0145), and
mash has no `range` word (standing order 3 for mash). Issues #1262 and #1258 asked for the document field and the plan
line to change. #1286 would have renamed `range` to `prefix`, which would have to be renamed again.

## Decision Drivers

* mash has no `range` word; an address is owner.group.instance.
* One rename, not two.

## Considered Options

* Rename `range` to `prefix` (the plan of #1286).
* Rename by position to owner, group and instance.
* Keep `range` inside the engine.

## Decision Outcome

Chosen option: rename by position.

Rename by position, once, with no shim: the engine's `range` is `owner`, its `owner` is `group` (the group is
`owner.group`), its `name` is `instance`.

* The policy document field `range` is `owner`; a document that still has `range` is refused as an unknown field.
* `ListSet.Range`, `Compiled.Range` and the explain output field are `Owner` and `"owner"`; `StageRange` is
  `StageOwner`; `ErrRangeNeeded` is `ErrOwnerNeeded`; the bucket-name placeholder `{range}` is `{owner}`.
* The responder subject is `agents.policy.<owner>`; `policyview.Options.Range` is `Options.Owner`.
* The plan line names the field: the document's `owner` field is X and the selected owner is Y (#1258).
* The four verbs, the compiled templates and the digest byte layout do not change. The unstated-verb behaviour is not
  part of this decision.

Nothing reaches a released binary: the release guard (standing order 16, #1542) still refuses every one of these
packages in `mash` and `mashctl`, so no stored state of a released program carries the old field. Out of scope: the
dropped packages (`policyintake`, the intake wiring in `policyserve`, `requesterreg`, `issue`, `reconcile`), edited only
where the build needed it.

### Confirmation

The renamed packages' own tests and goldens, and `TestP12_ReleaseGuardUnlinkedEveryTarget` for the guard.

### Consequences

* Good, because the engine says what mash says and one rename replaces two.
* Neutral, because any list set or document written with `range` must be rewritten; none exists outside tests.

## Pros and Cons of the Options

### `prefix`

* Bad, because it renames twice and still is not a mash word.

### Owner, group, instance

* Good, because it is the words of ADR 0145.
* Bad, because it touches every engine package once.

### Keep `range`

* Bad, because it breaks the standing order that mash has no `range` word.

## More Information

Issues #1262 and #1258; ADR 0145; the 0.6.0 policy design page 1, section 3.
