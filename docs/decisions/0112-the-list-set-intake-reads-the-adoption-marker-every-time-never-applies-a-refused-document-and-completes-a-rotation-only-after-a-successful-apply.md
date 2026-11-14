---
status: accepted
date: 2026-11-13
decision-makers: Randy Bias (mash maintainer)
---

# The list-set intake reads the adoption marker every time, never applies a refused document and completes a rotation only after a successful apply

## Context and Problem Statement

[ADR 0111](0111-an-adopted-account-takes-its-list-sets-only-from-its-submitter-and-mashctl-refuses-every-other-policy-change.md)
made an adopted account take its list sets only from one registered submitter, and left the part that takes them
out of the policy bucket to later work. That intake has to decide, for each value the bucket delivers, whether to
hand it to the apply stack, what to report when it does not, how a restart behaves, and how the submitter key is
rotated without a window in which the wrong key is accepted or a rotation is lost. The apply stack is built
separately and is not merged, so the intake must not depend on it.

## Decision Drivers

* A revoked or rotated key must be refused at once, not after a cache expires.
* A refused document must never reach the applier, and its report must not leak the document or a key.
* A restart replays the latest value; it must not turn an applied document into a refusal.
* A planned rotation must not strand the account with a key that was never recorded, nor accept the new key before
  its first list set is applied.
* The intake must be testable and buildable without the apply stack.

## Considered Options

* Read the marker for every revision, check the in-force digest before the signature, record a rotation only after
  the apply succeeds, and put decoding, applying, status and reporting behind small interfaces (chosen).
* Cache the marker and refresh it on a timer.
* Record the next submitter when the document is accepted, before it is applied.
* Import the apply stack's packages directly.

## Decision Outcome

The intake (`internal/policyintake`) watches `listset.<account>` in the range's policy bucket and handles each
delivered revision, one at a time and in order.

* **The marker is read every time.** Never cached. A read error other than not-found is not a refusal: it is retried
  with a doubling wait (2 seconds to a minute) until it works, a newer revision arrives or the intake stops. No
  marker is a refusal at stage `adoption`: an account without the marker is not governed by list sets.
* **Refusals never apply.** A bad or missing signature, a signer other than the expected key, a malformed or
  unknown-schema document, a document that names another account or range, and an unacceptable `next_submitter` are
  each reported with a stage and a short detail, and the applier is not called. A detail never holds the document and
  never more than the first 8 characters of a key. The expected key is the pending next submitter if there is one,
  so the old key is refused from then on.
* **A replay is skipped by digest.** If the SHA-256 of the raw bytes equals the digest of the document in force, the
  revision is skipped silently. The check comes before the signature check, so the replay of an applied document
  signed by a key a rotation has since retired is not a refusal that overwrites the applied status.
* **A rotation completes only after a successful apply.** A document signed by the submitter that carries
  `next_submitter` has the key recorded in the marker (action `rotate`) after it is applied. The first applied
  document signed by that key promotes it. Both writes are a compare-and-set on the marker's last revision, are
  retried until they land, are logged loudly when they fail, and report nothing: the applied status stands. A
  `next_submitter` that a promoting document carries is ignored; it starts a rotation only on the next document.
* **Concurrency: the intake holds the account lock.** It takes the account lock of
  [ADR 0111](0111-an-adopted-account-takes-its-list-sets-only-from-its-submitter-and-mashctl-refuses-every-other-policy-change.md)
  around the whole processing of one revision (read the marker with its revision, verify, apply, write the
  rotation), so a recovery adopt waits for an apply in flight. A busy lock is waited for with a doubling backoff and
  never a refusal. As a second guard, before the apply the intake checks that it still holds the lock and that the
  marker is still at the revision it verified under; if not it applies nothing and processes the revision again from
  the top, where the new marker decides. The rotation writes are a compare-and-set on that revision and fail with
  `ErrMarkerMoved` when it differs (they accept a marker already in the end state only at that revision or at the
  revision of the intake's own earlier write); the intake then logs loudly and abandons the rotation, never
  retrying blindly, so the key a recovery adopt set stays the expected signer.
* **The apply is guarded before each write.** The same check (the lock still held, the marker read afresh and still at
  the revision verified under, bounded by a timeout) is handed to the applier as a `Guard`; the applier calls
  `Before(ctx, step)` ahead of each write it cannot undo and aborts with the error. A lock that expires during a long
  apply therefore cannot let the old signer's document keep writing after a recovery adopt: the intake logs the step
  and processes the revision again, and the new marker decides.
* **A next submitter mash holds is refused.** A `next_submitter` that is any key the key store holds (operator,
  system, account and zone keys, and the admin user), as `policy adopt` already refuses for a submitter, is refused at
  stage `document` before the apply and again at the marker write. The held keys come from one shared helper
  (`ctlkeys.Authority.HeldKeys`). A key store that cannot be read refuses (fail closed).
* **An ambiguous marker write is read back.** If the write fails without a definite refusal (a timeout, no answer, a
  broken connection), the marker is read: when it is the intended end state at exactly the revision after the one
  written against, the write was our own and counts as done; otherwise the marker moved and the rotation is abandoned.
* **The collaborators are interfaces.** Decoding and verifying (`Decode`), reading what is in force (`InForce`),
  applying (`Applier`), reporting (`Reporter`) and the marker (`MarkerStore`) are left open, and an adapter over the
  apply stack is written when it lands. The real `MarkerStore` is over the admin connection.

### Consequences

* Good, because a rotated or recovered key is refused on the very next revision.
* Good, because a refusal and a replay cannot disturb an applied status.
* Good, because the intake is built and fully tested with fakes for the unmerged parts.
* Bad, because the marker is read for every revision, one more read each time; list sets are rare.
* Bad, because a process that stops between an applied document and the marker write loses the pending rotation; the
  operator adopts again to put the marker right.
* Bad, because a revision abandoned for a newer one while the marker could not be read is not reported.

### Confirmation

Tests run the intake against an in-process JetStream server with the real marker store and real signatures:
a good document applied once and in order; each refusal stage never applying; the old submitter refused after a
recovery adopt; a restart replaying an applied document; marker and in-force read errors retried; apply errors
reported only when no status was written; the planned rotation end to end, with the marker's history; a failing
marker write retried; deletes and purges ignored; a cancelled context ending the goroutines; and the reporter and log
never seeing raw bytes or a full key.

## Pros and Cons of the Options

### Read every time, digest before signature, rotate after apply, interfaces (chosen)

* Good, because each of the drivers is met directly.
* Bad, because of the costs under Consequences.

### Cache the marker and refresh it on a timer

* Good, because it reads less.
* Bad, because a recovered key would still be accepted until the refresh.

### Record the next submitter before the apply

* Good, because it has no window after the apply.
* Bad, because a document that fails to apply would leave the old key refused and the new key unproven.

### Import the apply stack's packages directly

* Good, because there is no adapter.
* Bad, because the intake could not be built or merged before the stack, and its tests would need the whole stack.

## More Information

* Related: [ADR 0111](0111-an-adopted-account-takes-its-list-sets-only-from-its-submitter-and-mashctl-refuses-every-other-policy-change.md), the adoption marker.
