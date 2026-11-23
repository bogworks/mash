---
status: accepted
date: 2026-11-23
decision-makers: mash-lead (0.5.8 plan, #1246), under the maintainer's release delegation
---

# config.json records the configured signing key's public key, and a lost signing key is never replaced by the identity key

Amends: ADR-0158

## Context and Problem Statement

ADR 0158 and #1243 let a person give `mashctl` an account signing key (`signing_key`) so users are signed with it, which a strict
operator needs. The key store records the choice in `signing.pub`, so losing `signing.nk` alone makes `mashctl` refuse. If both
`signing.nk` and `signing.pub` are lost, nothing says a signing key was ever configured, and `mashctl` signs with the identity key
again: on a strict server the new users are refused, and on any server the endpoints are signed by a key the person meant to
stop using (issue #1246, known issue of 0.5.7).

## Decision Drivers

* A lost signing key must refuse, loudly, and name the fix; it must never turn into identity signing.
* The record must live outside the key store, so one loss cannot erase both the key and the memory of it.
* The record is a trust anchor: it must hold no secret, and a wrong value must be refused, not trusted.

## Considered Options

* Keep the record in the key store only (0.5.7): a loss of the folder's two signing files falls back to the identity key.
* Also record the configured signing key's public key in `config.json` as `signing_key_pub`, and check it against the key store.

## Decision Outcome

Chosen option: the second.

* **The field.** `init` writes `signing_key_pub` (the public key only, never a seed) to `config.json` when the setup names
  `signing_key`, or, on a rerun that finds the key store already holding a signing key, that key's public key. An existing
  `config.json` without the field is completed; a different value is refused ("was set up with a different signing key").
* **The check.** Every place `mashctl` opens the key store passes the expected key (`ctlkeys.Open(dir, expectSigningPub)`; the parameter is required, so a new caller cannot forget it). `Signing()` then
  refuses with `ErrSigningKeyLost` when the expected key is set and both files are gone, when `signing.pub` is gone but `signing.nk`
  is present, or when the stored record or key differs from the expected one. The message names `mashctl init --config <file>` and the same `signing_key`.
  Revoke, apply and the policy paths get the refusal because they open the store the same way.
* **No field, no change.** A configuration without `signing_key_pub` (no signing key ever configured, or a 0.5.7 one) signs with
  the identity key as before; a rerun of `init` adds the field, from the given `signing_key` or, when the setup names none, from the key the store already holds.

### Consequences

* Good: losing the key-store files can no longer turn a configured signing key into identity signing.
* Good: the field holds a public key, so the config file stays free of secrets.
* Bad: `config.json` is now part of the trust record; replacing it by hand with an older copy drops the protection.
* Bad: a person who sets `signing_key` and later wants identity signing back must also remove `signing_key_pub` from `config.json`.

### Confirmation

* `cmd/mashctl/signing_lost_both_test.go`: both files gone with the field refuses `endpoint create` and records nothing; a
  configuration without the field still signs with the identity key; a rerun restores; a different value is refused.
* `internal/ctlkeys` tests of `Signing()` with an expectation: both gone, one gone, a different stored key.
* `cmd/mashctl` `TestEndpointDeleteSigningLostBothRefuses`, `internal/apply` `TestAccountFindingsRefusesWhenBothSigningFilesAreGone` and
  `internal/policyserve` `TestSigningPubRefusesWhenBothSigningFilesAreGone`: delete, apply and the policy applier refuse when both files are gone.
* `internal/ctlkeys` `TestSigningRefusalTypes`: each refusal has its own type, is an `ErrSigningKeyLost`, names the key folder and gives the fix as a hint line.
* The expected key is a required parameter of `ctlkeys.Open`, so a call site cannot omit it: the compiler enforces it.

## Pros and Cons of the Options

### Key store only

* Good, because it adds no field.
* Bad, because one folder loss erases both the key and the record, and `mashctl` then signs with the wrong key.

### Also record the public key in config.json

* Good, because the key and the record are in different places and the refusal is explicit.
* Bad, because `config.json` becomes a trust anchor that must be kept.

## More Information

Amends [ADR 0158](0158-mashctl-carries-the-accounts-plain-signing-keys-and-still-refuses-scoped-ones.md) (its accepted-risk note on
both files lost). Issue #1246.
