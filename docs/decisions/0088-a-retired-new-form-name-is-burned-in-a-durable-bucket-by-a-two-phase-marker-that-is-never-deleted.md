---
status: accepted
date: 2026-11-11
decision-makers: Randy Bias (mash maintainer)
---

# A retired new-form name is burned in a durable bucket by a two-phase marker that is never deleted

## Context and Problem Statement

The naming plan's invariant INV-002 says a retired agent/owner/name is never assigned again. The only fence a
deprovisioned endpoint had was the lease tombstone, which expires with the lease bucket's TTL (90 seconds), so a name
could come back after a minute. [ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md)
gave a new-form endpoint a key of its own, its flat form `agent_owner_name`, and
[ADR 0076](0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md) lets such a record answer
to a legacy identity as an alias. Provisioning and deprovisioning the new form (naming plan 1.6) therefore needs a
record of retirement that lasts, and one that cannot be half-written.

Retiring a name is several steps on a shared server (revoke the user key, purge the record, fence the lease, delete the
inbox), any of which can stop part-way. A marker written only at the end would let a crash reopen the name; a marker
written first and never cleaned up would burn a name that was never retired.

## Decision Drivers

* A retired new-form name must stay refused for good, across server restarts and lease expiry (INV-002).
* A crash at any step must leave the name refused, and running the same command again must finish the job.
* A mistyped name, or a refused removal, must not burn a name.
* An endpoint must not be able to read, write or delete a marker.
* The marker store must not drop a marker when it is full.

## Considered Options

* A key in a new `mash-retired` KV bucket with no TTL, written in two phases (pending, then final).
* Lengthen the lease tombstone's TTL.
* A tombstone in the directory bucket itself.

## Decision Outcome

Chosen option: the `mash-retired` bucket with two-phase markers, because it is the only option that is durable, shows a
half-finished retirement and keeps endpoint users out of it.

* One key per retired identity, its flat form, in the bucket `mash-retired`: no TTL, history 1, file storage (a memory-backed bucket is refused), and a
  size cap that refuses new markers when full rather than evicting old ones. Its value is the three tokens, the retired
  user public key, the time and the state, never a seed.
* **Two phases.** Deprovision writes the marker `pending` before it touches anything, purges, then makes it `final`
  (a revision-checked update). A `pending` marker blocks provisioning exactly like a `final` one. Rerunning the same
  command finishes a stopped retirement. The command deletes a `pending` marker only when the retirement definitively did
  not happen: a check that refused the removal before the purge, or the server refusing the revision-guarded purge, or a
  live record that carries a different user key (the old retirement was superseded). An answer that does not say whether
  the server acted (a timeout, a lost connection) leaves it `pending`. This is the two-phase ruling made in the review of the pull request that introduced it.
* **A `final` marker is never deleted.** Nothing in mash deletes one and there is no release command. A release would
  need a maintainer ruling and an audit record; neither exists, so none is offered.
* **Where it is checked.** Provisioning reads the marker while it holds the directory-wide admission lock, before
  writing anything. The bucket is verified wherever it is opened and refuses a TTL, another history, an evicting stream,
  memory storage (a server restart would erase every marker and free every retired name), or no room for one more marker.
* **Scope.** The invariant covers new-form names. A deprovisioned legacy name can still be provisioned again; no marker
  is written for it.
* **Trust boundary of an alias.** A new-form record may name a legacy identity as its alias. mash verifies the alias by
  the legacy record existing in the same directory when the new record is written, under the alias name's lock. That is
  not proof that the new-form endpoint's holder controls the legacy identity: the one who can provision in this directory
  can create the pair. A legacy endpoint that a new-form record names cannot be deprovisioned until that record is retired
  or re-pointed.
* **Size and its alarm.** The bucket holds many markers at a few hundred bytes each; its cap is 64 MiB. A full bucket
  makes provisioning refuse new identities for good, so an operator must see it coming: watch the bucket with
  `nats kv status mash-retired` and treat 50% of the cap as the alarm. Listing the markers and their count in
  `mashctl audit` is a follow-up. The cap cannot be changed on a live bucket by mash, which never alters an existing
  bucket; raising it means recreating the bucket with its markers copied.

### Confirmation

* `internal/admin` tests with a real server: a retired name is refused, also after the lease expires; a crash after the
  marker or after the purge leaves it `pending` and a rerun finishes it; a definitive refusal deletes only a `pending`
  marker; a superseded `pending` marker is replaced; a `final` marker under a different live user key refuses; an alias is
  verified under the alias name's lock; two provisions of the same alias at once leave exactly one winner; an endpoint
  user cannot reach the bucket.
* `internal/store` tests: the bucket's configuration, and the fail-closed check for a TTL, another history, an evicting
  stream, memory storage or a full bucket.

### Consequences

* Good, because a retired name stays refused for good and a crash cannot reopen it.
* Good, because a half-finished retirement is visible (a `pending` marker) and the same command finishes it.
* Good, because endpoint users have no permission on the bucket.
* Bad, because a burned name can never be recovered by mash, and a mistake in a retirement of a live name can only be
  fixed outside mash.
* Bad, because the bucket only grows, and a full bucket stops provisioning until an operator acts.
* Bad, because the alias is not proof of control (see the trust boundary above).

## Pros and Cons of the Options

### A longer lease tombstone

* Good, because it needs no new bucket.
* Bad, because any TTL lets a name return; INV-002 says never.

### A tombstone in the directory bucket

* Good, because there is one store less.
* Bad, because it mixes a fence with the live records every endpoint reads, and an endpoint user can read that bucket.

## More Information

Implemented in `internal/admin` (provisioning, `beginRetire`, `finalizeRetire`, `abortRetire`) and `internal/store`
(`RetiredConfig`, `VerifyRetiredStatus`, `Retired`). Operator view: `docs/operations.md`, "A retired agent/owner/name is
never reassigned". The naming plan is `docs/plans/2026-11-10-naming-grammar-in-mash.md`.
