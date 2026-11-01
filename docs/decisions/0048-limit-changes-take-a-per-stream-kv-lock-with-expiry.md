---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (mash maintainer)
---

# Limit changes take a per-stream KV lock with expiry

## Context and Problem Statement

`mash admin limits` and `mash admin provision` read an inbox stream's caps, check that the change would not lower them
(ADR-0010, the "never lower a limit" rule), and then write the stream with an update. JetStream has no
compare-and-set on a stream's configuration, so two admins changing the limits of one inbox at the same moment can
both pass the check against the old caps and then write in turn, and the second write can lower a cap the first just
raised. A lower cap can discard stored mail. The first answer (issue #27, deferred from #18) was a docs line: run
`mash admin limits` from one admin at a time. This ADR replaces that line with a mechanism.

## Decision Drivers

* Two admins must not be able to leave a stream with a cap lower than either of them asked for, or a mix of the two.
* mash holds no server of its own: the only shared state is JetStream, and only admins have a connection that can
  write to it beyond an endpoint's own keys (ADR-0007).
* A crashed admin must not block a stream for ever.
* An endpoint must never be able to read, take or release the lock (ADR-0007: its permissions are the security model).
* No new bucket if an existing one will do (hosted accounts count streams).

## Considered Options

* Keep the docs line: one admin at a time.
* A per-stream lock key with an expiry, in the lease bucket, taken with a KV create (compare-and-set on an absent key).
* The same lock in the directory bucket.
* A dedicated lock bucket, created by `mash admin init`.
* Optimistic: read, update, read back, and repair if the caps are lower than wanted.

## Decision Outcome

Chosen option: "a per-stream lock key in the lease bucket", because KV create and update are real compare-and-set
operations, the lease bucket needs no new stream, and an endpoint cannot reach a key there that is not its own name.

* **Where.** The key is `limits-lock.<name>` in the lease bucket. An endpoint may publish to and read exactly
  `$KV.mash-leases.<its own name>` (`internal/admin/permissions.go`), and a name holds no dot (ADR-0036), so no endpoint's
  grant matches a lock key: it can neither read nor write one. The directory bucket was rejected because endpoints may
  read all of it and `mash admin list` enumerates its keys. Nothing enumerates the lease bucket's keys. A dedicated
  bucket would work as well, but costs a stream in the account's limit and a step in `mash admin init` that older
  deployments have not run. If the lease bucket does not exist, taking the lock creates it with the configured limits.
* **Value.** `{"owner": "<random id>", "until": "<RFC 3339 time>"}`. `until` is the admin's own clock plus 30 s.
* **Acquire.** KV create. If the key exists, read it. If `until` has passed (or the value cannot be read), take it
  over with a KV update against the revision just read: of several takers exactly one wins, and the others look again.
  Otherwise wait, polling every 200 ms plus up to 200 ms of random jitter, for up to 20 s, then fail with
  `mash: another admin is changing the limits of <name> (lock held until <time>); try again`.
* **Release.** A purge guarded by the revision this admin wrote (`LastRevision`), so a lock that expired and was taken
  over by another admin is never removed by the first one. A failed release is ignored: the lock expires.
* **Where it is held.** Before the read of the stream and through its write, in `applyInboxLimits`, in `Provision`
  (the create-or-update of the inbox stream) and in `EnsureInbox` (find, create or check). `ApplyLimits` over many names
  takes each name's lock in turn, never several at once, so there is no lock ordering and no deadlock.
* **Time.** Expiry is judged by each admin's own clock, so admins' clocks must agree to within a few seconds; 30 s is
  generous for that. The lease bucket's own 90 s TTL removes a lock nobody released. A critical section that outlasts the
  30 s (a stream update that hangs) can overlap with another admin's; that one change then has the old risk.

### Consequences

* Good, because two admins changing one inbox's limits are serialised: the second sees the first's caps and is refused
  if it would lower them, and neither can leave a mix.
* Good, because a crashed admin blocks a stream for at most 30 s.
* Good, because no endpoint permission changes and no bucket is added.
* Bad, because a second admin can wait up to 20 s, and fails with an error naming the holder's expiry.
* Bad, because the expiry depends on admins' clocks being roughly right; a clock more than 30 s fast on one admin can
  make it take over a live lock.
* Bad, because the lock is advisory: it protects admins that use it from each other, not from a tool that updates the
  stream directly.
* Neutral, because `mash admin limits` and `provision` write a lock key and a purge marker to the lease bucket for each
  inbox they touch.

### Confirmation

`internal/admin/lock_test.go`, against the in-process NATS server with the production permissions:
`TestConcurrentLimitsNeverLeaveALowerCap` and `TestProvisionRacingProvisionNeverLowersCaps` (a seam between the read
and the write forces the interleaving; both fail with the lock disabled), `TestExpiredLockIsTakenOver`,
`TestLiveLockMakesTheSecondAdminWaitThenTimeOut` (the exact error), `TestWaiterGetsTheLockWhenItIsReleased`,
`TestReleaseNeverDeletesSomeoneElsesLock`, `TestProvisionWithApplyLimitsOnANewName`, `TestEndpointCannotReachALockKey`
(an endpoint user can use its own lease key and can neither read, write nor purge a lock key) and
`TestLockCreatesTheLeaseBucketWhenMissing`.

## Pros and Cons of the Options

### Keep the docs line

* Good, because nothing changes.
* Bad, because nothing stops the race, and the cost of losing it is discarded mail.

### Lock key in the lease bucket

* Good, because create and update are compare-and-set, no new stream is needed, and endpoints cannot reach the key.
* Bad, because expiry rests on admins' clocks.

### Lock key in the directory bucket

* Good, because the admin already owns that bucket.
* Bad, because endpoints can read it, and `mash admin list` would see lock keys as endpoints.

### Dedicated lock bucket

* Good, because the lock is isolated from the leases.
* Bad, because it costs a stream and needs `mash admin init` on every existing deployment first.

### Optimistic read-back and repair

* Good, because it needs no lock.
* Bad, because the lower cap is in force, and may already have evicted mail, before the repair runs.

## More Information

* Issue #27 (deferred from #18). Builds on ADR-0010 (limits never lower) and ADR-0007 (permissions); the key's
  isolation relies on ADR-0036 (names hold no dot).
* Docs: `docs/operations.md` "Changing limits later". Code: `internal/admin/lock.go`, `internal/admin/admin.go`.
