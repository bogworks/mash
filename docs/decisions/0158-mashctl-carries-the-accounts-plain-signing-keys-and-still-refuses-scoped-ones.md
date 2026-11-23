---
status: accepted
date: 2026-11-22
decision-makers: Randy Bias (mash maintainer)
---

# mashctl carries the account's plain signing keys and still refuses scoped ones

Amends: ADR-0059

## Context and Problem Statement

ADR 0059 made `apply`, `repair` and `--dry-run` refuse any signing key in the fetched account JWT that mashctl did not
create, "scoped or plain", because mashctl re-signs the whole JWT with the operator key and would carry the foreign key
into a JWT it vouches for. Issue #1239 (decision 289, the maintainer's ruling) needs mash to work on a strict operator
(`strict_signing_key_usage: true`), where users must be signed by an account signing key, not the identity key. The
account's own signing key is a plain signing key that mashctl did not create, so the 0059 refusal would stop every
revocation and policy apply on such an account.

## Decision Drivers

* A strict operator is unusable without a plain account signing key, and the person who sets mash up already holds it.
* A scoped signing key changes what users it signs may do; a plain key does not narrow anything, so the two carry different risk.
* The exposure must be stated, with the ruling that accepts it, rather than left in the code.

## Considered Options

* Keep ADR 0059 as written (refuse every foreign signing key).
* Carry plain foreign signing keys unchanged; refuse scoped ones; name the configured `signing_key` as the one mashctl signs with.

## Decision Outcome

Chosen option: the second, by the maintainer's ruling on #1239 (decision 289).

* **Plain keys are carried.** `apply`, `repair`, `--dry-run`, the policy server and `policy apply` keep every plain signing
  key of the fetched account JWT unchanged when they re-sign it. The configured `signing_key` (`signing.nk` and its record
  `signing.pub`) is one of them and is required to stay present and plain (`Candidate.RequiredPlain`); a revocation proceeds
  with a warning when it is absent from the JWT and refuses before any push when it has become scoped.
* **Scoped foreign keys are still refused.** A scoped signing key mashctl did not create, or a scoped configured key, is
  refused as ADR 0059 says, naming it by public key and role; `diff` and `drift` report it. [ADR 0124](0124-the-reconciles-yes-acts-only-on-the-plan-the-person-read-and-an-adopted-accounts-first-apply-drops-the-signing-keys-mash-did-not-make.md)'s first-generation drop of a foreign scoped key stands unchanged: 0.5.7 never carries a scoped key it did not make.
* **Everything else in ADR 0059 stands:** the issuer, subject and signature checks, and TLS for admin and system connections.
  Only the sentence "any other signing key ... scoped or plain, is refused" is replaced.

### Accepted risk

mashctl signs, with the operator key, an account JWT that holds a plain signing key it did not create. Whoever added that key
to the account (it must already be in the operator-issued JWT) can mint users for the account, and mashctl will not remove
it. The maintainer accepts this: the key is already in a JWT the operator signed, mashctl only preserves it, and the person
running mashctl is the account's owner. Owner: the mash maintainer. If both `signing.nk` and `signing.pub` are lost, mint
falls back to identity signing (known issue #1246, milestone 0.5.8).

### Consequences

* Good: mash works on a strict operator, and revocation and policy apply keep the account's signing key.
* Bad: an account with a plain signing key from another tool keeps it through mashctl's re-signing; remove it with that tool.
* Bad: a person who adds `signing_key` and later wants identity signing back must remove both `signing.nk` and `signing.pub`
  (`docs/operations.md`).

### Confirmation

* `internal/apply` tests (`signing_scope_test.go`): plain foreign keys carried, scoped refused, configured key scoped or absent.
* `internal/policyapply` and `internal/policyserve` tests that carry every plain key; `cmd/mashctl/strict_signing_test.go` on a strict fixture.
* `internal/ctlkeys` tests that a lost `signing.nk` refuses and that removing both files returns to identity signing.

## Pros and Cons of the Options

### Keep ADR 0059 as written

* Good, because mashctl never re-signs a key it did not create.
* Bad, because a strict operator then cannot be used at all, which #1239 rules out.

### Carry plain keys, refuse scoped ones

* Good, because the account's own signing key survives and the scoped boundary (the one that narrows permissions) holds.
* Bad, because mashctl now vouches, by re-signing, for plain keys it did not create (the accepted risk above).

## More Information

Amends [ADR 0059](0059-mashctl-checks-who-issued-the-account-jwt-carries-only-its-own-signing-keys-and-requires-tls-for-admin-connections.md)
(its signing-key paragraph only). Issue #1239; maintainer decision 289; known issue #1246 (both signing files lost). The 0.5.7
release notes state the exposure.
