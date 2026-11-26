---
status: superseded by ADR-0131
date: 2026-11-14
decision-makers: Randy Bias (mash maintainer); the bug was found by the mash coordinator preparing the v0.5.0 review
---

# A submission with the content in force is refused as unchanged and uses no generation
Superseded by [0131](0131-mash-stands-alone-everything-works-without-homerange-or-any-upward-dependency-first.md).

## Context and Problem Statement

`listset.Accept` compared only the base of a submission with the generation in force. A list set with the same content
as the one in force was accepted as the next generation: it was signed, pushed, gated and committed, and used up a
generation number for no change. `policy diff` then said "this would change" over an empty plan. What should a
submission do whose content is the content in force?

## Decision Drivers

* A generation number should stand for a change.
* The submitter should get an explicit answer.
* Nothing should be signed or pushed for a change that is not one.

## Considered Options

* Refuse it as unchanged (chosen).
* Skip it and report success.
* Keep accepting it as the next generation.

## Decision Outcome

Chosen option: "Refuse it as unchanged".

`listset.Accept` refuses a submission whose compiled output digests to the `Signed` digest of the record in force with
`ErrUnchanged` (an `*UnchangedError` carrying the generation in force). `Record.Content` cannot be compared: it covers
the base, which differs between any two submissions. The compiled digest leaves the base out and does not depend on
encoding or order, so a reordered resubmission is unchanged too. `Run` turns it into a refused status with one refusal,
stage `unchanged`, "same content as generation N in force; nothing to apply". No generation is used, nothing is signed
or pushed, and the record in force, the pending record and the stored list set and JWT are untouched.

One exception: a submission that starts a planned submitter rotation (it carries a `next_submitter` and none is
pending) or completes one (a rotation is pending and this is the next key's first set) is not refused as unchanged. It
applies as a new generation, signed, pushed, gated and committed, because ADR 0112 completes a rotation only after a
successful apply and the compiled digest does not cover `next_submitter`. The intake sets `Doc.RotationStep` for these
two cases, the applier passes it on as `RunInput.RotationStep`, and `Run` then calls `listset.AcceptRotationStep`, which
makes every check `Accept` makes except the unchanged one.

### Consequences

* Good, because every generation is a signed, pushed and gated change.
* Good, because the submitter is told that nothing happened.
* Bad, because an operator cannot force a re-push by resubmitting.
* Neutral, because two list sets written differently that compile to the same permissions count as unchanged: the
  list set in force keeps its own wording, and the permissions are the same either way.
* Bad, because a submission equal to the content in force while a different generation is pending is refused as
  unchanged. The pending generation is resolved by resubmitting it, or rolled back by Converge (ADR 0100).

### Confirmation

Unit tests: `TestAccept_SameContentRefusedAsUnchanged` and `TestAccept_ChangedContentStillAccepted` in
`internal/listset`, and `TestRunSameContentRefusedAsUnchanged` in `internal/policyapply`. For the rotation exception:
`TestAcceptRotationStep_SameContentAccepted` in `internal/listset`, `TestRunRotationStepAppliesSameContent` in
`internal/policyapply` (same content with `RotationStep` applies as the next generation), and
`TestRotationStepIsMarkedForTheApplier` in `internal/policyintake` (leg 1, leg 2 and a plain document, with the next
submitter recorded and promoted).

## Pros and Cons of the Options

### Refuse as unchanged (chosen)

* Good, because a generation number stands for a change that was signed, pushed and passed the gate.
* Good, because repairing drift (pushing again what is in force) is Converge's job, not a resubmission's.
* Good, because the submitter gets an explicit answer.

### Skip and report success

* Good, because a repeated submission is harmless.
* Bad, because it claims a gate pass for a submission that never ran one.
* Bad, because the submitter cannot tell that nothing was pushed.

### Keep accepting

* Good, because a resubmission re-pushes what is in force.
* Bad, because it uses a generation for no change, and `policy diff` contradicts it.

## More Information

* ADR 0100 (the applied gate and Converge), ADR 0098 (generations).
