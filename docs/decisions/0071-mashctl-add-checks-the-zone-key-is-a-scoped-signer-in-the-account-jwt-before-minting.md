---
status: proposed
date: 2026-11-03
decision-makers: Randy Bias (mash maintainer)
---

# `mashctl add` checks that the zone key is a scoped signer in the account JWT before minting

## Context and Problem Statement

A user minted under a zone's scoped signing key carries empty permission limits and takes the key's template
([ADR 0052](0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md)). If that same key is published in the
account JWT as a plain signing key instead (not a `UserScope`), the minted user has empty limits and no template: it is
unrestricted. `policy apply` only publishes scoped signers and refuses signing keys it did not create, so mashctl never
causes this itself; it needs the account JWT edited by another tool. `mint.Mint` refuses a key that is not an account
key or is the account identity key, and checks the scope only when its key source can see the account JWT. The
production key source (`ctlkeys.Authority`) holds seeds, not the account JWT, so the check never runs in `mashctl add`
and `reissue` (issue #220, codex review of #217).

The account JWT is read through the system account (a lookup on `$SYS.REQ.ACCOUNT.<account>.CLAIMS.LOOKUP`), which
`policy apply`, `drift`, `retire` and a narrowing `reissue` (ADR 0067 (in review in #241)) already use. `add` today needs only the admin creds.

## Decision Drivers

* A zone key that would mint an unrestricted user must be refused, not warned about.
* The check must read the account JWT as the servers hold it, and verify it as `policy apply` does
  ([ADR 0059](0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md)): valid signature, our account,
  our operator or declared signing key.
* No new verb or flag (the CLI freeze stands).
* No change to the directory, the permission model or the JWT format.

## Considered Options

* Leave it: mint checks the scope only where the key source can see the JWT.
* Keep a copy of the last applied account JWT in the key directory and check against it.
* `add` reads the account JWT through the system account and refuses unless the zone key is a scoped signer.
* Warn when the system account creds are absent and mint anyway.

## Decision Outcome

Chosen option: `mashctl add`, for a zone that already has endpoints, looks up the account JWT with the system account
creds (`MASH_SYS_CREDS`), verifies it, and refuses to mint unless the zone's signing key is a `UserScope` signer in it.
It does this before any key, directory record or creds file is written. A new zone is not checked: its key is made by
that same command and is published by the `policy apply` that must follow. Without system account creds `add` refuses
for an existing zone and says what to set.

A cached copy goes stale as soon as someone else changes the account JWT, which is the case being defended against. A
warning that mints anyway is the hole. The cost is one more credential to hold for `add`; every other account-changing
mashctl command already needs it. `reissue` takes the same check once ADR 0067 (in review in #241) lands.

### Consequences

* Good, because a plain signing key in the account JWT can no longer produce an unrestricted user through `add`.
* Good, because it reuses the lookup and verification `policy apply` uses; no new trust path.
* Bad, because `add` into an existing zone now needs the system account creds and a reachable server, where it needed
  the admin creds and a reachable server before.
* Bad, because a clustered account whose servers disagree is judged by the one answer the lookup returns; the full-set
  rule of `apply` is not applied to a read.

### Confirmation

* A test that `add` into an existing zone whose key the verifier refuses writes no creds file and no directory record.
* A test that a new zone is not checked and that a scoped key passes.
* A test of the verification on account JWTs built with a scoped signer, a plain signing key and no key.

## Pros and Cons of the Options

### Leave it

* Good, because nothing changes for operators.
* Bad, because the guard in mint can never fire in production.

### Cache the last applied JWT

* Good, because `add` stays on the admin creds.
* Bad, because the copy is stale exactly when another tool changed the JWT.

### Read it through the system account and refuse

* Good, because it checks the live JWT.
* Bad, because it requires the system account creds.

### Warn and mint anyway

* Good, because it never blocks.
* Bad, because the unrestricted user is still issued.

## More Information

* Issue #220; [0052](0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md),
  [0059](0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md).
* Code: `internal/mint/mint.go` (`ScopeVerifier`, `checkZoneKey`), `cmd/mashctl/add.go`, `cmd/mashctl/scopecheck.go`.
