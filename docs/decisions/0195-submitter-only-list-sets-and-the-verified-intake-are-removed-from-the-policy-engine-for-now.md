---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# Submitter-only list sets and the verified intake are removed from the policy engine for now

Amends: ADR-0119
Related: ADR-0111 and ADR-0112 (already superseded by ADR-0131)

## Context and Problem Statement

ADR 0111 (an adopted account takes list sets only from its submitter) and ADR 0112 (the intake reads the adoption marker,
verifies the signature, and completes a rotation) belong to the countersigned-submitter design. ADR 0131 superseded
them: mash works alone first and has no upward dependency. This ADR removes the code they left in `policydoc`. The 0.6.0 policy design (pages 1 and 2) has one unsigned
document per owner kept in a file the administrator owns. Is the code for the signed submission, the planned submitter
rotation and the adoption marker kept in `policydoc`?

## Decision Drivers

* mash stands alone: no submitter or countersigning host is assumed.
* Standing order 17: mash is a tenant of the account and does not take over its keys.
* Dead code with a security meaning misleads a reader.

## Considered Options

* Keep the signed-document code unused.
* Remove it from `policydoc`; the intake that consumes it is removed by a separate change.

## Decision Outcome

Chosen option: remove it. `policydoc` decodes plain JSON only; `next_submitter` and a compact JWS are refused. `Diff`
checks no adoption marker and no signer. ADR 0119's rule that unchanged content is refused stays, without the rotation
exception (there is no rotation).

### Confirmation

`TestDecodeRefusesNextSubmitter` and `TestDecodeRefusesJWS` in `internal/policydoc`; the release guard keeps every
policy package out of the released programs, so nothing shipped changes.

### Consequences

* Good, because the engine has one input form and no key-holder it must trust.
* Bad, because the intake in `internal/policyintake` and `internal/policyserve` cannot accept any document until it is
  removed (it is unreachable: no program links it).
* Neutral, because a signed form could return later as a new decision.

## Pros and Cons of the Options

### Keep it unused

* Bad, because untested-by-use security code stays in the tree.

### Remove it

* Good, because the tree says what the design is.

## More Information

Pages 1, 2 and 4 of the 0.6.0 policy spec; ADR 0131, which superseded 0111 and 0112; the 26 Nov assignment that left this code for the rename work.
