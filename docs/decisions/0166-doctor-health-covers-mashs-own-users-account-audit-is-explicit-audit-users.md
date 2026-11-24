---
status: accepted
date: 2026-11-23
decision-makers: Randy Bias (mash maintainer)
---

# Doctor health covers mash's own users; account audit is explicit (--audit-users)

## Context and Problem Statement

`mashctl doctor` failed the `keys` check for every connected key that the issue record does not hold. On a NATS account shared with other tools, those tools issue users of their own, so doctor failed on keys that are expected (issue #1300). [ADR 0165](0165-doctor-takes-keys-issued-elsewhere-from-an-external-keys-allowlist-in-config-json.md) answered with an `external_keys` allowlist in `config.json`. It was never released. This ADR supersedes it.

Randy Bias, as relayed by the planner, asked of that first design: "Do you think that's how the admin tool should assess \"health\"?" and, asked whether to change it, said "Yes." His ruling: "A NATS account is the operator's and may be shared; mash is one tenant in it."

## Decision Drivers

* Mash is a tenant of the account, not its owner. Health covers mash's own things.
* An operator who wants to know whether every connected user was issued by mashctl still needs a way to check it, and to fail on it. The check sees only the users connected now.
* No new setting, no new verb, no change to what is stored on the server.
* `external_keys` never shipped, so it can be removed with no tolerance for old state.

## Considered Options

* A. Keep the `external_keys` allowlist of ADR 0165. Rejected by Randy: it makes doctor judge users that are not mash's.
* B. A verb that registers a foreign user as known (a new record kind in the mash-issued bucket).
* C. Report foreign users as a plain WARN.
* D. Health ignores foreign users; an explicit `mashctl doctor --audit-users` fails on them.

## Decision Outcome

Chosen option: D. `mashctl doctor`, with or without `--strict`, never fails because a connected user was not issued by mashctl. The `keys` row stays ok and adds one clause: `keys       ok    every connected user was issued by mashctl` when there are no other users, or `keys       ok    mashctl's users ok; 2 other users, not issued by mashctl` (singular: `1 other user`). The new flag `mashctl doctor --audit-users` is the only thing that fails on an unknown user, and `--strict` does not imply it. It prints `keys       FAIL  2 connected users were not issued by mashctl:`, each key (up to 3, then `and N more`) on its own line at column 17, and `hint: revoke it with the tool that issued it (mashctl revokes only its own)`. A key the issue record holds keeps its rules, so a revoked key that is still connected still fails, and when it does the count of other users stays on a continuation line of the failure. `external_keys` is removed entirely.

### Confirmation

`TestDoctorKeysForeignUserIsAClauseNotAFailure`, `TestDoctorAuditUsersFailsOnAForeignUser`, `TestDoctorAuditUsersManyKeysOnLinesOfTheirOwn`, `TestDoctorForeignUserCountText`, `TestDoctorRevokedStillConnectedFailsWithoutAudit`, `TestDoctorOnlyMashUsersPassesInEveryMode`, `TestDoctorKeysFailsWhenTheIssueRecordIsMissing`, `TestDoctorKeysCountsAConnectionWithNoKey` and `TestDoctorAuditUsersRevokedKeyLinesFit80Columns` in `cmd/mashctl` pin each case: a foreign user (ok, with the count clause), `--strict` with a foreign user (still ok), `--audit-users` (fails, names the keys, caps at 3 with `and N more`, gives the hint, keeps every line within 80 columns), an account with only mash's users (the plain line, in every mode, and with a 0.5.7 `config.json` unchanged), a missing issue record (the keys row fails with `no issue record; mashctl cannot tell its users from others`, with or without `--audit-users`), and a connection that reports no key (counted as a user mashctl did not issue, shown as `(no key reported)`).

### Consequences

* Good: doctor passes on a shared account with no configuration.
* Good: the audit is explicit, so a failure always means the operator asked for it.
* Good: a 0.5.7 `config.json` needs no change, and no setting is added or left behind.
* Neutral: `--strict` does not fail on a foreign user; use `--audit-users`.
* Neutral: revoking a foreign user is outside mashctl. It did not issue the user, so it cannot revoke it; the tool that issued it can.

## Pros and Cons of the Options

### A. `external_keys` allowlist

* Good, because every undeclared key still fails.
* Bad, because the list is kept per admin machine, and it asks doctor to judge users that are not mash's.

### B. A verb that registers foreign users

* Good, because the list would be shared by every admin machine.
* Bad, because it adds a verb and a bucket schema change, for users mash does not own.

### C. A plain WARN

* Good, because it needs no configuration.
* Bad, because every shared account would warn on every run, and there would still be no way to fail on an audit.

### D. Ignore foreign users in health; explicit `--audit-users`

* Good, because health stays about mash, and the audit is available when wanted.
* Bad, because a foreign user is only reported, not failed, unless the operator asks.

## More Information

* Issue #1300.
* Requirement: contract 0.5.8 revision 5, REQ-A (mash #1300, as corrected by Randy on 2026-11-23).
* Approval: Randy Bias's ruling of 2026-11-23, quoted under Context and Problem Statement, recorded in REQ-A of the contract above (decision-makers: Randy Bias).
* Supersedes [ADR 0165](0165-doctor-takes-keys-issued-elsewhere-from-an-external-keys-allowlist-in-config-json.md).
* `docs/doctor.md`, section keys.
