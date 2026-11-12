---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# The lease bucket does not allow rollup writes

## Context and Problem Statement

The shared lease bucket (`mash-leases`, stream `KV_mash-leases`) holds each endpoint's lease and the admin operations'
locks. A JetStream key-value bucket made with the client library's default configuration allows rollup writes, and every
endpoint's credentials can write to the bucket's subjects. Mash did not set the stream's rollup setting, so an old
deployment's bucket allows it. Should the bucket refuse rollup writes, and how does a deployment that already has the
bucket get there?

## Decision Drivers

* The bucket must not accept a write that removes keys the writer does not own.
* No operation mash needs may be blocked by the change.
* A deployment that upgrades must not be left silently unhardened, and an admin operation that relies on the bucket
  must not proceed on one that is.
* The change must not require a new bucket or a migration of live leases.

## Considered Options

* Set `AllowRollup=false` on the lease stream, at creation and on init, verify it at each admin lock, and report an
  unhardened bucket in the audit (chosen).
* Keep the default and rely on per-key subject permissions.
* Move the admin locks to a separate bucket.
* Also set `DenyPurge=true`.

## Decision Outcome

The lease bucket's stream is configured with `AllowRollup=false`. `mash admin init` creates a new bucket that way, and
updates an existing bucket's stream in place (keeping its keys and never lowering its size cap), then verifies the result
and fails if the setting did not take. Every admin lock checks the setting before it takes the lock: a bucket that
still allows rollup is hardened in place, and the lock is refused when that cannot be done or the configuration cannot
be read, so it fails closed.

Mash removes a lease key with a delete on the key's revision, never with a purge, because a purge is a rollup write. A
revision that moved since the read means a live endpoint wrote the key, and the delete is retried or refused.

`DenyPurge` stays false. The server refuses a stream that denies purge while allowing rollup, and with rollup refused a
deny-purge would block no operation mash needs; the setting that matters is `AllowRollup`.

`mashctl audit` reports a lease bucket that still allows rollup writes as a problem, `lease bucket allows rollup writes;
run "mash admin init" to harden it`, and one whose stream configuration cannot be read as a problem too. An absent bucket
is not a problem. A problem counts toward the audit's findings and `--strict` exit status.

Re-running `mash admin init` after upgrading is a required step, and the operations guide says so. A deployment that
upgrades without it keeps its old bucket until an admin lock runs (which hardens it) or the audit is read.

### Consequences

* Good, because the bucket refuses rollup writes, and no key mash needs to remove is removed by anything other than a
  revision-aware delete.
* Good, because a deployment that missed the upgrade step is told by the audit, and its next admin lock hardens the
  bucket, so it is not left silent.
* Good, because init hardens an existing bucket in place, with its keys kept.
* Bad, because an upgraded deployment is unhardened until `mash admin init` is run or an admin lock runs.
* Bad, because every admin lock reads the lease stream's configuration, one more request each time.

### Confirmation

Tests cover init creating the bucket with rollup refused, init hardening a bucket made by a plain key-value create and
keeping its keys and cap, the admin lock hardening a bucket that allows rollup and refusing one it cannot harden or whose configuration cannot be read,
lease keys removed by a revision-aware delete with a moved revision retried, and the audit reporting an unhardened bucket
as a problem (text, JSON and exit status), reporting none after init and none when the bucket is absent.

## Pros and Cons of the Options

### Set AllowRollup=false, verify at each lock, report in the audit (chosen)

* Good, because it closes the write at the stream, whatever the subject permissions.
* Good, because it needs no new bucket and no migration.
* Bad, because it depends on a re-run of init after upgrading.

### Keep the default and rely on per-key subject permissions

* Good, because it changes nothing.
* Bad, because a subject permission names a key's subject, and a rollup write on one key's subject removes the other
  keys under it: per-key permissions are not enough.

### Move the admin locks to a separate bucket

* Good, because locks and leases would be isolated.
* Bad, because the lease bucket would still allow rollup, so the leases stay unprotected, and a second bucket counts
  against hosted accounts' stream limits.

### Also set DenyPurge=true

* Good, because it would also refuse a purge.
* Bad, because the server refuses it with rollup allowed, and with rollup refused it blocks nothing mash needs.

## More Information

* Related: [ADR 0092](0092-a-new-serve-of-the-same-session-takes-over-the-endpoint-lease.md).
