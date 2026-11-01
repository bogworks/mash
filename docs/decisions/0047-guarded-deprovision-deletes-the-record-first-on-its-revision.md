---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (mash maintainer)
---

# A guarded deprovision deletes the record first, against its revision, and fences the lease after

## Context and Problem Statement

`quickstart-synadia.sh` cleans up a failed provision by listing the directory, checking that the record for the name
carries the key this run created, and then running `mash admin deprovision <name>`. Another admin could replace the
record between the two calls, and the cleanup would remove their record and inbox (issue #9). The fix is
`mash admin deprovision <name> --user <public-key>`: remove the endpoint only if the record's user is still the key.

ADR-0015 makes `Deprovision` write the lease tombstone first, then remove the record and the inbox. A guard that may
refuse cannot follow that order: a refused call would already have fenced the lease of an endpoint that stays.

## Decision Drivers

* A refused or raced guarded call must change nothing.
* A name must still not be retakeable by a live or starting session (ADR-0015).

## Considered Options

* (a) Fence first as ADR-0015 does, then check and delete.
* (b) Check and delete the record first, with a revision guard, then fence and delete the inbox.

## Decision Outcome

Chosen option: (b), for the guarded form only. Plain `Deprovision` keeps ADR-0015's order.

* `DeprovisionIfUser` reads the record (value and revision), refuses unless its user equals the key, then purges it
  with `jetstream.LastRevision(revision)`. A failed purge (revision conflict) does not refuse at once: it re-reads the
  record, as `SetMeta` does (up to 5 tries). If the user still equals the key (another admin changed only metadata,
  which bumps the revision), the purge is retried on the new revision, so a metadata-only change never refuses. If the
  user now differs, the record vanished after the read, or the tries run out, it refuses with "the record for <name>
  changed; left in place" and touches nothing.
* The quickstart script treats "belongs to another user key" as a genuinely foreign record and still deletes its own
  never-used NATS user. For "changed; left in place" (and for an unknown failure) a record for the name may still
  use this run's user, so the script keeps the NATS user and its creds file and says so.
* Every refusal happens before anything is written: no tombstone, no stream delete. With no record it refuses even if
  the inbox stream exists, because nothing proves who owns it.
* After the record is gone it writes the tombstone and deletes the inbox stream.
* The gap between the record delete and the tombstone is safe. `Open` takes the lease and then reads the directory
  record again; with the record gone it gives the lease back and fails (ADR-0015). A session that already holds the
  lease is stopped by the tombstone at its next refresh, as before. So a record-less name cannot be held.
* `DeprovisionIfUser`, plain `Deprovision` and `Provision` each run under the per-name lock of ADR-0048
  (`limits-lock.<name>` in the lease bucket) for their whole body: the guarded call holds it across the read, the
  record delete, the fence and the inbox delete, and `Provision` across its record check, lease clear, stream and
  record write. Another admin who provisions or deprovisions the same name meets the lock and waits (up to 20 s, then
  fails with the lock's "try again" message), then acts on the final state. This closes the interval between the
  record delete and the fence, in which a second admin could provision the name and have its new lease fenced and
  its new inbox deleted by the first call. A Provision that waited for a deprovision finds no record and no stream,
  clears the tombstone as it always does, and starts clean.
* If the call fails between the record delete and the fence, the name has no record, an inbox and no tombstone. A plain
  `mash admin deprovision <name>` finishes it.
* Residual risk: the lock expires after 30 s (and on the admins' clocks). A deprovision or provision that outlasts
  that, or an admin running a mash without the lock, can still interleave with another; the revision guard on the
  record delete still protects the record in that case, but not the fence or the inbox.

### Consequences

* Good, because a refused or raced call changes nothing.
* Good, because the quickstart's cleanup is one call.
* Bad, because the guarded form has a different order from the plain one, and a crash between its steps leaves a
  half-removed endpoint that needs a plain deprovision.

### Confirmation

* `internal/admin/deprovision_user_test.go`: a matching user is removed, another user is refused with the record,
  inbox and lease untouched, a record changed or replaced between the read and the delete survives, and a missing
  record with an existing inbox is refused. `TestDeprovisionIfUserHoldsNameAcrossPurgeAndFence` starts a second
  admin's Provision of the same name between the record delete and the fence and asserts it does not complete until
  the call returns, and that the new record, inbox and mail then exist. The replaced-record test writes the
  replacement directly, as an admin without the lock would.
* `cmd/mash/main_test.go` (`TestAdminCLI`): bad keys are usage errors and no key longer than 8 characters is printed.
* `scripts/quickstart_synadia_test.go`: the cleanup is one guarded call and maps each refusal to its message.

## Pros and Cons of the Options

### (a) Fence first

* Good, because it is the same order as the plain form.
* Bad, because a refused call would already have fenced an endpoint that stays.

### (b) Guard and record delete first

* Good, because a refusal touches nothing.
* Bad, because a crash between its steps leaves a half-removed endpoint.

## More Information

* Issue #9; ADR-0015; ADR-0048; code in `internal/admin/admin.go` (`DeprovisionIfUser`).
