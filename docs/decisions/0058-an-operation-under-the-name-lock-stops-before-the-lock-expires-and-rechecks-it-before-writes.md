---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# An operation under the name lock stops before the lock expires and re-checks it before a write

## Context and Problem Statement

[ADR 0048](0048-limit-changes-take-a-per-stream-kv-lock-with-expiry.md) serialises admin changes to one endpoint with a
lock key that expires after 30 seconds, judged by each admin's own clock. It accepted that a critical section that
outlasts the lock "can overlap with another admin's". That was tolerable when the lock guarded a limits update. It is not
now that the same lock guards `suspend`, `resume` and a guarded `deprovision`
([ADR 0057](0057-an-admin-can-suspend-an-endpoints-inbound-mail-by-parking-its-inbox-stream-subjects.md)): `admin_timeout`
can exceed 30 seconds, so one stalled write lets a second admin take the lock and `resume`, and the first command then
deletes an inbox that has taken new mail (issue 161, found in review of the guarded deprovision).

## Decision Drivers

* The guard of a guarded deprovision must hold at the moment of the write, not only when it was read.
* No new service, bucket or server feature: JetStream KV has no fencing token on stream writes.
* A stalled admin must fail clearly and safely, never overlap another.

## Considered Options

* Leave it: document that a stalled admin can overlap.
* Lengthen the lock to `admin_timeout`.
* Bound every operation to the lock's life and re-check the lock before each write that cannot be undone.

## Decision Outcome

Chosen option: bound and re-check. An operation's context ends `lockSafety` (5 seconds) before the lock's own expiry,
measured from a time taken before the lock was acquired, so a write that stalls is cancelled while the lock is still
ours. Immediately before each write the lock protects and that cannot be undone or that another admin could invalidate
(a stream update, a record purge, the lease fence, the stream delete), the operation re-reads the lock key and refuses
unless it still holds the revision it wrote and is inside the bound. The refusal says the lock was lost or is about to
expire, that nothing after that point changed, and to try again. Lengthening the lock is rejected because it makes a
crashed admin block the endpoint longer and still leaves a gap; leaving it is rejected for the reason above.

### Consequences

* Good: a stalled or slow admin can no longer act on a stream after another admin may hold the lock, to within clock skew
  of the safety margin.
* Good: no new state; the check reads the lock key already there.
* Bad: an operation that really needs more than 25 seconds fails with "try again" (limits over many endpoints take the
  lock one endpoint at a time, so the usual case is unaffected).
* Bad: the check and the write are not atomic; the margin, not the check, closes most of the gap. A write that is
  already on the wire when the lock expires can still land, which is why the margin is a few seconds, and admins' clocks
  must still agree to within it.

### Confirmation

* Tests with a shortened lock: a stalled guarded deprovision ends with the clear error and the stream and record intact;
  an admin that takes the expired lock makes the first operation's check refuse before it deletes; happy paths unchanged.

## Pros and Cons of the Options

### Leave it

* Good, because nothing changes.
* Bad, because a guarded deprovision can delete an inbox that took new mail.

### Lengthen the lock

* Good, because a slow operation fits inside it.
* Bad, because a crashed admin blocks the endpoint longer, and a stall can still pass any fixed length.

### Bound and re-check

* Good, because a stalled operation stops while the lock is still ours and refuses before a destructive write.
* Bad, because a check is not atomic with the write and an operation over about 25 seconds fails.

## More Information

* Issue 161; ADR 0048 (amended: its overlap sentence no longer holds).
