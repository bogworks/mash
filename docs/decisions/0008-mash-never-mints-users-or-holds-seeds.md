---
status: superseded by ADR-0052
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# mash never mints NATS users or holds operator or account seeds; credentials stay 0600 on the endpoint's host
Superseded by [0052](0052-mashctl-mints-nats-users-and-holds-the-account-signing-key.md).

## Context and Problem Statement

In a NATS server run in operator mode, identities form a chain: an operator key signs accounts, and an account key
(its seed, the private half) signs users. Whoever holds an account seed can issue any user in that account, and so
any mash identity. Each endpoint also has a credentials file (its user JWT and user seed). mash had to decide whether
it creates users itself, which secrets it ever holds, and where an endpoint's credentials live.

## Decision Drivers

* Keep the secret that can forge every identity out of mash.
* Let operators issue users with the tooling they already use (`nsc`, or any JWT issuer).
* An endpoint's own credentials should be readable only by the account that runs it, on its own host.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* mash prints the permission template; the operator's tooling mints the users
* mash holds the account seed and mints users itself

## Decision Outcome

Chosen option: "mash prints the permission template; the operator's tooling mints the users", because "whoever holds
those [operator or account seeds] can issue any identity" (`SECURITY.md`, Out of scope). Keeping minting outside mash
bounds what a compromised or buggy mash can leak (reconstructed).

* `mash admin permissions <name>` prints an endpoint's permissions as JSON or as `nsc` flags; the operator's tooling
  issues the user, and `mash admin provision` creates the stream, consumer and directory record behind it.
* An endpoint's credentials file lives only on the endpoint's host, at mode 0600 (spec §4).
* Since 2026-10-31 (pull request #168), mash refuses a credentials file that its group or other users can read; this
  was the maintainer's directive for the 0.3.0 release.

### Consequences

* Good, because no mash command or file holds a key that can create identities.
* Good, because operators keep control of user lifetimes, expiry and revocation in their own tooling.
* Bad, because setting up an endpoint takes two tools: the issuer, then `mash admin provision`.
* Bad, because deprovisioning an endpoint does not revoke its NATS user; that stays with the operator's tooling.
* Neutral, because the hosted-service setup (`mash init synadia`, ADR-0035) asks the hosted service's API to create
  users and still never holds an account seed.

### Confirmation

* `internal/admin/permissions_test.go` `TestNSCFlags`: the template renders as `nsc` flags.
* `internal/config/credsmode_test.go` `TestCheckCredsMode`: group- or other-readable credentials are refused.
* `internal/endpoint/credsmode_test.go` `TestOpenRefusesCredsReadableByOthers`: an endpoint does not start with such a
  file.
* No automated test confirms that no code path holds a seed; it is a property of the code's structure.

## Pros and Cons of the Options

### Operator's tooling mints users

* Good, because the most sensitive key never leaves the operator's control.
* Good, because it works with any NATS issuer.
* Bad, because first-time setup needs NATS knowledge mash cannot supply.

### mash mints users

* Good, because setup would be one command.
* Bad, because mash, or the host it runs on, would then hold a key that can forge every endpoint.

## More Information

* Design spec §4 and §11.1 ("mash never mints NATS users and never holds an account or operator seed").
* `docs/operations.md` (setup); `SECURITY.md` (Out of scope).
* Code: `internal/admin/permissions.go`, `internal/config/credsmode.go`.
* Pull request #168 (refuse a readable credentials file); pull request #130 (`mash init synadia`).
* Related: ADR-0007 (permissions), ADR-0035 (hosted-service setup).
* **Not yet true:** `SECURITY.md` (Known limits) still says "mash never checks the credentials file's mode", which pull
  request #168 made false. `mash init synadia` creates one NATS user per endpoint through the hosted service's API, with
  a temporary admin user it deletes afterwards, so the statement that mash never mints users holds only for the
  self-hosted path.
