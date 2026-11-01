---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# A lease KV entry allows one live session per name (TTL 90 s, refresh 30 s)

## Context and Problem Statement

Nothing in NATS stops two processes from connecting with the same credentials. If two sessions ran under one endpoint
name, both would pull from the same inbox consumer and each would see only part of the mail. The design-era spike
found exactly this: "Two sessions under one name would split mail … Production needs a lease, such as an atomic KV
create with a TTL." mash needed a way to guarantee at most one live session per name, which frees itself when a
session dies.

## Decision Drivers

* Mail for one name must go to one session, never split between two.
* "Unique, verifiable identities" (spec §1.1 goal 3).
* A dead session's claim must expire without an operator.
* The server may have no memory store, so the lease must use file storage.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* A lease in a KV bucket with a time-to-live, taken by atomic create and renewed by revision-checked update
* An exclusive NATS consumer or subscription that only one client can hold
* No lease; rely on launchers never starting two sessions with one name

## Decision Outcome

Chosen option: "A lease in a KV bucket with a time-to-live", because the spike found that two sessions under one name
split mail and named a KV create with a TTL as the fix.

* The `mash-leases` bucket (file storage, history 1) has a time-to-live of 90 s; each key is an endpoint name.
* A session takes its name with a KV create, which fails if the key exists, and renews it every 30 s with an update
  that succeeds only if the key's revision is unchanged (compare-and-set).
* A session that loses its lease after starting stops consuming, reports it and exits non-zero.
* A session releases its lease when it exits on SIGTERM, SIGHUP or SIGINT (added after the first live run,
  commit `d71f4f3`).
* Each endpoint may write only its own lease key (ADR-0007).

The value 90 s (time-to-live) was chosen without a recorded reason; it equals the window in which a heartbeat counts
as online. The value 30 s (refresh) was chosen without a recorded reason.

### Consequences

* Good, because mail for a name is never split between two live sessions.
* Good, because a crashed session's name frees itself within 90 s with no operator action.
* Bad, because a restarted session can find its own dead predecessor's lease; ADR-0014 records how that is handled.
* Neutral, because the lease value also carries the holder's host, pid and start time, which later decisions build
  on (ADR-0014, ADR-0016).

### Confirmation

* `internal/endpoint/endpoint_test.go` `TestLeaseRefusesSecond`, `TestLeaseRefreshAfterLostReply`,
  `TestLeaseRefreshTransportErrorIsNotLoss`, `TestCloseReleasesALeaseWithAStaleRevision`.
* `cmd/mash/main_test.go` `TestServeLeaseConflict` and `TestServeExitsWhenLeaseLost`.
* `cmd/mash/signal_test.go` `TestServeReleasesLeaseOnSignal`.
* `internal/admin/admin_test.go` `TestEndpointPermissionDenials`: another endpoint's lease cannot be written.

## Pros and Cons of the Options

### KV lease with a time-to-live

* Good, because create and compare-and-set make taking and keeping the lease atomic on the server.
* Good, because expiry is handled by the bucket, not by the session.
* Bad, because a dead holder blocks its name until the time-to-live runs out, unless handled separately.

### Exclusive consumer or subscription

* Good, because it would need no separate bucket.
* Bad, because it does not record who holds the name, so a refused session cannot say which host and process hold it.

### No lease

* Good, because there is nothing to build.
* Bad, because one mistaken launch splits an endpoint's mail without any error.

## More Information

* Design spec §5.2 and §7.1 step 4. Code: `internal/store/store.go` (`LeaseTTL`, `LeaseConfig`),
  `internal/endpoint/endpoint.go` (`LeaseRefresh`, `takeLease`), `internal/endpoint/directory.go` (`OnlineWindow`).
* Design-era spike: maintainer design discussion, 2026-10-21 (not public).
* `docs/evidence/2026-10-22-live-e2e.md` (the first live run, including a lease conflict).
* Related: ADR-0007 (permissions), ADR-0014 (waiting and takeover), ADR-0015 (deprovision fence), ADR-0016 (lease as a
  health record).
* **Not yet true:** spec §1.1 goal 3 and §7.1 step 4 still say a second session under a held name refuses to start;
  since 2026-10-23 it waits and retries instead (ADR-0014).
