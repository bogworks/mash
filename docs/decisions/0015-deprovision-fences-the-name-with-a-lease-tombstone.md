---
status: accepted
date: 2026-10-27
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Deprovisioning fences the name with a lease tombstone before removing anything

## Context and Problem Statement

`mash admin deprovision <name>` removes an endpoint: its directory record and its inbox stream. A running session
holds its name through a lease, an entry in a NATS KV bucket (NATS's key-value store) that it refreshes every 30
seconds (ADR-0013). Deprovision used to purge the directory record and the lease, then delete the inbox. Nothing
stopped a session that was using the name: a running session's next refresh found the lease key gone and created it
again, and a session starting up could read the directory before the purge and take the lease after it. Either way the
deprovisioned name "was held again" until that session exited (issue #10, PR #11).

## Decision Drivers

* A deprovisioned name must stay free: no live or starting session may take it back.
* A name that is provisioned again must work at once, without waiting for a timer.
* The race was found in mash's own tests, so the fix had to be testable without timing luck.

## Considered Options

Both options were recorded in issue #10:

* (a) `Open` re-checks the directory after taking the lease, and gives the lease back if the record is gone.
* (b) Deprovision fences the name: it writes a tombstone to the lease key, which `Open` and the refresh treat as
  permanent, and provisioning clears it.

## Decision Outcome

Chosen option: both, with the tombstone fence as the main fix, because "(b) closes both cases in one place. (a) also
catches a directory record removed by other means. Doing both is cheap." (issue #10).

As built (mash layer):

* `Deprovision` first writes `{"deprovisioned":true,"since":...}` (`store.LeaseTombstone`) to the lease key, and only
  then purges the directory record and deletes the inbox stream.
* A lease refresh or an `Open` that meets the tombstone fails with the permanent error `ErrDeprovisioned`, and the
  session stops instead of taking the name.
* After taking the lease, `Open` reads the directory record again; if it is gone, it releases the lease and fails.
* The tombstone expires with the lease bucket's TTL (90 seconds, ADR-0013). `Provision` purges the lease key, so a
  name provisioned again works at once.

### Consequences

* Good, because a running session stops at its next refresh (within 30 seconds) and a starting one is refused.
* Good, because the fence lives in one place, the lease key, which every session already reads.
* Bad, because for up to 90 seconds the lease key holds a value that is not a lease. Any tool that lists leases must
  skip entries with `"deprovisioned":true`; a consumer's lease listing failed for that window when this shipped (PR #11).
* Neutral, because deprovision still does not revoke the endpoint's NATS user (ADR-0008). Its credentials keep
  connecting until the account owner revokes them, and its mail arrives labelled `[unknown-signer]` (ADR-0019).

### Confirmation

* `internal/endpoint/endpoint_test.go`: `TestDeprovisionedLiveSessionDoesNotRetakeTheLease`,
  `TestOpenRefusesADeprovisionedName`, `TestReprovisionClearsTheTombstone`,
  `TestOpenRechecksTheDirectoryAfterTakingTheLease`.
* `internal/admin/admin_test.go`: `TestDeprovisionRemovesAll`; `internal/admin/meta_test.go`: `TestDeprovisionWithMeta`.

## Pros and Cons of the Options

### (a) Open re-checks the directory

* Good, because it also catches a directory record removed by other means.
* Bad, because alone it leaves a race window between the re-check and the purge, and the refresh path needs its own copy.

### (b) Tombstone fence

* Good, because one write closes both the running and the starting case.
* Bad, because the lease bucket now holds two kinds of value, which every reader must tell apart.

## More Information

* Issue #10 ("Deprovision can be undone by a live or starting session"), PR #11.
* Code: `internal/admin/admin.go` (`Deprovision`, `Provision`), `internal/store/store.go` (`LeaseTombstone`),
  `internal/endpoint/endpoint.go` (`fenced`, `ErrDeprovisioned`).
* Docs: `docs/operations.md`, "Removing an endpoint"; `SECURITY.md` (deprovision does not revoke the user).
* Related: ADR-0008 (mash never mints or revokes NATS users), ADR-0011 (the directory), ADR-0013 (the lease).
