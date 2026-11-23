---
status: superseded by ADR-0166
date: 2026-11-23
decision-makers: Randy Bias (mash maintainer)
---

# Doctor takes keys issued elsewhere from an external_keys allowlist in config.json
Superseded by [0166](0166-doctor-health-covers-mashs-own-users-account-audit-is-explicit-audit-users.md).

## Context and Problem Statement

`mashctl doctor` fails the `keys` check for every connected key that the issue record does not hold. On an account shared with other tools, those tools issue users of their own, so doctor fails on keys that are expected. The user needs a way to say which foreign keys are known, without making doctor blind to an unknown one (issue #1300).

## Decision Drivers

* A key nobody declared must still fail, with and without `--strict`.
* The smallest change that fixes the shared-account case.
* No new verb and no change to what is stored on the server.

## Considered Options

* A. An allowlist `external_keys` of exact user public keys in `config.json`.
* B. A verb `mashctl user register` that writes a new record kind in the mash-issued bucket.
* C. Report keys not issued here as a warning.

## Decision Outcome

Chosen option: A. `config.json` takes `external_keys`, an array of exact user public keys (`U...`). A connected key that is listed and not in the issue record is not a finding, and the `keys` line says how many were external. A key the issue record holds keeps its rules, so a revoked key that is still connected fails even when it is listed. A key that is not listed and not issued here still fails. An entry that is not a user public key is a configuration error that names the entry.

### Confirmation

`TestDoctorKeysExternalKeys` and its neighbours in `cmd/mashctl` pin the listed, unlisted, revoked and malformed cases; `internal/config` pins the validation.

### Consequences

* Good: the smallest change; no issue-bucket schema change and no new verb.
* Good: every undeclared key still fails.
* Bad: the list is kept per admin machine, not shared through the server.
* Neutral: a stale entry is harmless, because it only matters while that key is connected.
* Neutral: entries are exact keys; there are no patterns.
* Neutral: `external_keys` is a config.json-only setting, with no flag and no environment variable. It is an exception to "every setting three ways", as the encryption switch is (that one is environment only).
* Neutral: the field is validated by the shared config loader, so a bad entry (not a user public key, surrounding whitespace, a duplicate) also stops `mash serve`, not only `mashctl doctor`. An error names an entry only if it starts with `U`, so a seed is never echoed.

## Pros and Cons of the Options

### A. Allowlist in config.json

* Good, because it needs no server change and no new command.
* Bad, because each machine that runs doctor needs the list.

### B. `mashctl user register` with a new record kind in the mash-issued bucket

* Good, because the list would be shared by every admin machine.
* Bad, because it adds a verb, changes the bucket schema, and every reader of the bucket must understand the new kind. It can be built later and feed the same list.

### C. Report foreign keys as a warning

* Good, because it needs no configuration.
* Bad, because doctor could not tell a known tool from an attacker's key, and `--strict` would still fail.

## More Information

* Issue #1300.
* `docs/doctor.md`, section keys.
