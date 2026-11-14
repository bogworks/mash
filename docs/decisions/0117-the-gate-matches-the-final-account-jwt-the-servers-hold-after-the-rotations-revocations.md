---
status: accepted
date: 2026-11-14
decision-makers: Randy Bias (mash maintainer); from the review of the policy apply interface, contract mash-messaging-policy draft-3
---

# The gate matches the final account JWT the servers hold after the rotation's revocations

## Context and Problem Statement

ADR 0114 has the rotation step (`AfterPush`) revoke keys after the target is pushed. The servers then hold an account
JWT re-signed with those revocations, not the signed target, so ADR 0114 has the gate read the target by content
(templates, signers and revocations) instead of by the signed target's claims ID. Reading by content cannot tell the
target from another JWT with the same content. A retry push in the gate's convergence would also resend the
pre-revocation target, and the ledger would store a JWT the servers do not hold. Which JWT does a run treat as the
target once the rotation has revoked keys?

## Decision Drivers

* A revocation is never lowered or removed (ADR 0114).
* A server is on the target only if it holds exactly the JWT mash pushed (ADR 0100).
* The ledger stores the JWT the servers hold, so a later run and a rollback build on it.

## Considered Options

* The rotation step returns the final account JWT, which becomes the target from then on (chosen).
* Read the target by content after the rotation (ADR 0114).

## Decision Outcome

Chosen option: "The final account JWT becomes the target".

This departs from ADR 0114's reading by content. In `internal/policyapply`, `AfterPush` returns the final account JWT the
servers hold after its revocations. Before using it, Run checks that it carries the target generation, keeps every
revocation in force, and fits the account JWT size limit. It then becomes the target for the rest of the run:

* The gate's `TargetJWT` is the final JWT, so a server counts as on the target only when it holds exactly that token.
* A retry push in the convergence pushes it, never the pre-revocation target.
* The commit stores it.

A rotation step that pushed revocations but returns no final JWT fails the run: nothing is committed and the
generation stays pending.

### Consequences

* Good, because the gate matches the servers' JWT exactly again, as ADR 0100 intends.
* Good, because no retry can push a JWT that lacks the rotation's revocations.
* Bad, because a custom `AfterPush` must return the final JWT whenever it revokes.

### Confirmation

Unit tests in `internal/policyapply` (`rollback_test.go`): `TestCommitStoresFinalJWTWithRevocations`,
`TestFinalJWTMissingRevocationIsNotCommitted` and `TestRevokedKeysWithoutFinalJWTIsNotCommitted`.

## Pros and Cons of the Options

### The final JWT becomes the target (chosen)

* Good, because the target, the retry, the commit and the rollback base are one token.
* Bad, because the rotation step's interface grows by one value.

### Read by content (ADR 0114)

* Good, because the rotation step need not return a JWT.
* Bad, because a retry could resend the pre-revocation target, and a JWT equal in content passes as the target.

## More Information

* ADR 0100 (the applied gate), ADR 0114 (rollback and the rotated inventory).
* Contract mash-messaging-policy draft-3, REQ-011, REQ-014.
