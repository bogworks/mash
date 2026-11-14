---
status: accepted
date: 2026-11-14
decision-makers: Randy Bias (mash maintainer)
---

# A revocation needs the issue record to show a key's whole history, and the bucket says so in its stream metadata

Amends: ADR-0101

## Context and Problem Statement

NATS honours a user revocation only for credentials issued at or before the revocation time, so a credential dated in
the future stays valid after a revocation. `mashctl deprovision` therefore reads every retained issue time of the key
in the issue record (`mash-issued`) before it revokes, and refuses when one is later than the revocation time. The
record keeps a bounded number of revisions of a key. A key at the bucket's history limit may have lost its oldest
revisions, and the first version of the bucket kept 8, which `mash admin init` raises to 64.

Two gaps remained. First, the check decided whether older revisions were lost from the bucket's current history limit.
A bucket created with a limit of 8 that dropped a future-dated revision and was later raised to 64 looks complete, so a
revocation could pass while that credential stayed valid. Second, only `deprovision` made the check: `mashctl retire`
and a rotating `mashctl reissue` revoked without it. How can the record show that a key's history is complete, and
where must the check run?

## Decision Drivers

* A revocation that leaves a credential valid must be refused, never reported as done.
* The evidence must not be something a data write can hide or forge by accident.
* Every command that revokes a user key by key and time must make the same check.
* A refusal must change nothing.

## Considered Options

* Mark the bucket in its stream metadata when it is created at the full limit or raised to it, and treat a key as
  complete only when the marker proves it (chosen).
* Judge completeness from the current history limit alone (the behaviour before this decision).
* Refuse every key of a bucket that was ever raised.
* Keep a per-key counter of every write in a data entry.

## Decision Outcome

Chosen option: "Mark the bucket in its stream metadata".

The issue record's stream carries the metadata key `mash_issued_history`, which a data write cannot change:

* A bucket created at the full limit (64) has the value `full`.
* When `mash admin init` raises a bucket from a per-key history of N to 64, it writes `raised:<N>:<S>`, where S is the
  stream's last sequence. It raises the limit first (one stream update) and reads the last sequence after that update,
  then writes the marker in a second update. Both updates copy the whole stream configuration, so rollup, delete and
  purge stay refused. If the second step fails, the limit stays raised, no marker is written, `init` returns the error,
  and the legacy rule (no marker) applies until `init` is run again. A raise never replaces an existing marker and never turns `full` into anything else,
  and a second `init` changes nothing.
* A bucket with no marker is a legacy one: nothing is known about what it dropped.

Whether the retained history of one key is shown complete is decided from the marker:

* `full`: complete, unless the key is at the 64-revision cap (the existing rule).
* `raised:N:S`: complete when fewer than N of the key's retained revisions have a stream sequence at or below S. At a
  limit of N, a key with fewer than N revisions up to S had none dropped. Revisions after S were kept at 64.
* no marker: complete when the key has fewer than 8 retained revisions (the old limit), otherwise not shown.
* A marker value that is neither form is not shown complete.

A key whose history is not shown complete is refused with the existing wording that a future-dated credential cannot
be ruled out and nothing was changed, plus the way forward: revoke the key by hand in the account JWT, only after
checking where it was issued.

The same check, `ctl.CheckNotFutureIssued` with the exact time handed to the revocation, now also runs in
`mashctl retire` (inside the guarded removal, before the revocation) and in a rotating `mashctl reissue` (before
anything is staged or revoked). A refusal changes nothing there either.

### Consequences

* Good, because a bucket raised from 8 can no longer look complete when a revision was lost before the raise.
* Good, because the marker is in the stream configuration, which the issue record's data writers cannot reach.
* Good, because `deprovision`, `retire` and a rotating `reissue` make one check with one clock reading.
* Bad, because a key of a legacy bucket with 8 or more revisions, or one that had 8 up to a raise, is refused until it
  is revoked by hand.
* Good, because S is read after the raise: a write that lands under the old limit has a sequence at or below S, so a
  concurrent write cannot evict a revision unseen. S can only be larger than strictly needed, which refuses more.
* Bad, because the raise and the marker are two updates: a failure between them leaves a raised bucket with no marker,
  which is judged by the legacy 8-revision rule (conservative) until `init` is run again.

### Confirmation

`internal/admin` tests cover each marker state (full; raised and provable; raised with exactly N revisions before S;
no marker with 7 and with 8 revisions; an unreadable marker), that creation writes `full`, and that a raise writes
`raised:8:S` once, keeps the hardening and is idempotent, that S is the last sequence after the raise, and that a fault between the two
updates leaves the limit raised with no marker and the legacy rule in force. `cmd/mashctl` tests show that `retire` and a rotating
`reissue` refuse on an unprovable history with nothing changed, and proceed on a provable one.

## Pros and Cons of the Options

### The marker in stream metadata (chosen)

* Good, because it records what only the raise knew, at the moment it knew it.
* Bad, because a bucket raised before this decision has no marker and is judged by the 8-revision rule.

### The current history limit alone

* Bad, because a raise hides earlier loss.

### Refuse every key of a raised bucket

* Good, because it is simple.
* Bad, because it refuses keys that provably lost nothing.

### A counter in a data entry

* Bad, because a data write can change or lose it, and it would need a write on every issue.

## More Information

* ADR 0101 (the issue record).
