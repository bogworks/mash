---
status: superseded by ADR-0131
date: 2026-11-14
decision-makers: the mash coordinator (mash-lead), for the mechanism; the signer change agreed with the core lane, which owns `policy serve`
---

# The reconcile's --yes acts only on the plan the person read, and an adopted account's first apply drops the signing keys mash did not make
Superseded by [0131](0131-mash-stands-alone-everything-works-without-homerange-or-any-upward-dependency-first.md).

## Context and Problem Statement

[ADR 0121](0121-an-adopted-account-is-reconciled-by-an-operator-command-after-its-first-list-set-dry-run-first.md) made
`mashctl policy reconcile` a dry run whose `--yes` computes the plan again under the lock and carries it out, and it said
the first applied list set removes every signing key that is not mash's. Two things were left open. A plan computed again
under the lock may differ from the one the person read, and `--yes` is one-way in this release (a revoked credential
cannot be reissued on an adopted account). And the signer `policy serve` uses refused an account JWT holding a signing
key mash did not make, as [ADR 0059](0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md)
asked of `policy apply`: mash never carries a signing key it did not create. Refusing means an adopted account with such a
key can never take its first list set, and AT-015 asks that adopting it removes the key.

## Decision Drivers

* A one-way step acts on exactly what a person read, or not at all.
* mash must not lock out its own admin user or an exempt user.
* An adopted account must be able to take its first list set.

## Considered Options

* `--yes` takes the digest of the dry run's plan and refuses any other plan; the first apply drops non-mash keys once the
  issuer check passes (chosen).
* `--yes` acts on whatever plan it computes under the lock; the signer keeps refusing non-mash keys.

## Decision Outcome

Chosen option: the first.

* **`--yes --plan DIGEST`.** The dry run prints `plan: sha256:<hex>`, the digest of the plan's canonical JSON, and says to
  run again with `--yes --plan` that digest. `--yes` needs `--plan`; it takes the account lock and the ledger lease,
  computes the plan again, and refuses, changing nothing, when its digest is not the one given ("the plan changed since
  the dry run: run it again"). A `--listset` preview is never executable.
* **The first apply drops the non-mash signing keys.** For an adopted account, the account JWT signer (`policyserve.Issuer`)
  no longer refuses a signing key that is not one of the key store's owner group keys. It first runs
  `admin.CheckIssuersKept` with the admin user's issuer and the issuer of every connection of an exempt user, against the
  keys that stay (the account key, the owner group keys of other ranges the key store holds, and the target's signers);
  when that passes it drops the other keys, and when it fails it refuses with the check's error, naming each user and
  key. Without the admin and exempt issuers configured, the signer keeps refusing as before, so nothing is dropped
  blind.

This amends ADR 0059's rule for this one case: mash still never carries a signing key it did not create, and it now
removes one at an adopted account's apply instead of refusing, after showing that no user it must keep was signed by it.

### Consequences

* Good, because the revocations `--yes` makes are exactly the ones the person read.
* Good, because an adopted account can take its first list set, and AT-015's "removes the second" holds.
* Bad, because a credential signed by a dropped key by any tool stops working at the first apply, unless it is the admin
  or an exempt user (those refuse the apply instead). The reconcile's dry run with `--listset` lists the connected users
  each such key signed before the apply.
* Bad, because a dry run that is not re-run after a change must be run again before `--yes`.

### Confirmation

The signer tests next to `internal/policyserve/signer_test.go` (dropped when the check passes, refused when the admin or
an exempt user's issuer would go, refused as before when the issuers are not configured), the command tests of
`--yes` (a stale `--plan` refused and nothing changed), and `TestPolicyReconcileAT015Live`, which applies the first list
set through the production signer and runs `policy reconcile --yes --plan`.

## Pros and Cons of the Options

### A digest of the plan read, and the first apply drops non-mash keys (chosen)

* Good, because a one-way revocation happens only for the plan a person read.
* Good, because the account can take its first list set.
* Bad, because a stale dry run has to be run again.

### Act on the plan computed under the lock; keep refusing non-mash keys

* Good, because it needs no digest.
* Bad, because `--yes` can revoke a key the person never saw in a plan.
* Bad, because an account holding a key mash did not make can never take its first list set.

## More Information

* Amends ADR-0121 (`--yes`) and ADR-0059 (signing keys mashctl did not create, for an adopted account's apply). Related: ADR-0122 (`policy serve`'s inbox rule).
* AT-015 (contract mash-messaging-policy).
