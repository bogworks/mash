---
status: accepted
date: 2026-11-14
decision-makers: Randy Bias (mash maintainer); from the review of the REQ-014 rotation change, contract mash-messaging-policy draft-3
---

# A rollback never un-revokes a key, and a rotated sender's revoked keys join the old-key inventory

## Context and Problem Statement

REQ-014 rotates a narrowed sender to a new key and revokes its old keys, and revokes every key of a removed sender. The
rotation runs after the target is pushed and before the gate (ADR 0100), so revocations are pushed during the run. ADR
0100 rolls a split cluster back by pushing the previous generation's JWT, which the caller supplies. A rollback JWT
signed before the run does not carry the run's revocations, so pushing it would make a revoked key valid again. ADR
0100 also takes the old-key inventory from the caller, who could omit the keys the rotation just revoked. How does a
run keep every revocation it made, and make the gate check them?

## Decision Drivers

* A revocation is never lowered or removed.
* The gate must check the keys the run revoked, whatever inventory the caller gives.
* Any check the gate cannot make counts as failed, never as passed (ADR 0100).

## Considered Options

* The rollback is signed on request with every key the run revoked, and is verified before it is pushed; the rotation's
  revoked keys are merged into the inventory (chosen).
* Keep a caller-supplied rollback JWT and trust the caller to include the revocations.
* Never roll back once a revocation has been pushed.

## Decision Outcome

Chosen option: "Sign the rollback on request, verify it, and merge the revoked keys".

In `internal/policyapply`:

* `RunInput.SignPrevious` replaces the caller-supplied previous JWT. Run calls it only when a rollback is needed, with
  the union of the run's revoked keys and the keys the rotation step (`AfterPush`) revoked. `Converge` decodes the
  result and pushes it only when it carries the previous generation and every one of those keys revoked. Otherwise the
  rollback is not pushed, the servers stay split, and the change stays partial with a rollback failure.
* The keys `AfterPush` reports for each sender address are merged into that sender's old-key inventory and into the
  gate's revoked keys, so a caller cannot omit them, and the gate still checks each is revoked on every server.
* Once `AfterPush` has revoked a key, the servers hold an account JWT re-signed with those revocations, not the signed
  target. The gate then reads the target by content (templates, signers and revocations), not by the signed target's
  claims ID.
* A run where a rotated sender has not connected with its new key by the deadline is partial. It commits nothing,
  provisions nothing, and leaves its generation pending, with the old keys already revoked, so only a resubmission of
  the same content may follow.

This departs from ADR 0100's caller-supplied rollback JWT and caller-only old-key inventory.

### Consequences

* Good, because a rollback never un-revokes a key.
* Good, because the gate checks every key the run revoked, whatever inventory the caller gives.
* Bad, because a rollback that cannot be signed with every revocation leaves the cluster split, partial, until an
  operator resubmits.

### Confirmation

Unit tests in `internal/policyapply`: `TestRollbackKeepsAfterPushRevocations`, `TestRollbackDroppedRevocationIsNotPushed`,
`TestRollbackSignErrorAndWrongGeneration`, `TestRunRotationDerivesOldKeys`, `TestRunRotationNotConnectedIsPartialAndNotCommitted`
and `TestRunRotationNotConnectedIsPending`. AT-013 runs the rotation live.

## Pros and Cons of the Options

### Sign on request and verify (chosen)

* Good, because the check is made by mash before the push, not left to the caller.
* Bad, because the caller must be able to sign the previous generation during the run.

### A caller-supplied rollback JWT

* Good, because it is simple.
* Bad, because a JWT signed before the run cannot carry the run's revocations.

### No rollback after a revocation

* Good, because no revocation can be lost.
* Bad, because a split cluster stays split even when a safe rollback exists.

## More Information

* ADR 0100 (the applied gate), ADR 0113.
* Contract mash-messaging-policy draft-3, REQ-011, REQ-014.
