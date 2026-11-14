---
status: accepted
date: 2026-11-14
decision-makers: the mash coordinator (mash-lead), for the mechanism, under the maintainer's adoption plan in ADR 0111; built by the sdk lane
---

# An adopted account is reconciled by an operator command after its first list set, dry run first

## Context and Problem Statement

AT-015 asks that "adopting an account that holds an untracked legacy credential and a non-mash signing key revokes the
first and removes the second". [ADR 0111](0111-an-adopted-account-takes-its-list-sets-only-from-its-submitter-and-mashctl-refuses-every-other-policy-change.md)
recorded the exempt keys for that step and planned it, without building it: remove signing keys that are not mash's,
revoke recorded issued users that are not in the first list set and connected keys that are neither recorded nor exempt,
report what cannot be named as `reconciled: partial (N unknown)`, print a dry run, and act only with `--yes`. When does it
run, what counts as untracked, and how does it avoid revoking a live endpoint's key when the issue record is incomplete?

## Decision Drivers

* Adopting an account changes nothing in it (ADR 0111). Applying a list set and revoking keys are separate steps, and a
  person reads the plan before each.
* A key is revoked only when mash can show it is not one it issued under the list set; a doubt is a finding, never a
  revocation.
* Revocations must survive later list sets: the next run keeps every revocation in force (ADR 0117 and the
  never-lower rule).
* mash must not lock itself out of the account.

## Considered Options

* An operator command, `mashctl policy reconcile`, run after the first applied list set, dry run by default (chosen).
* An automatic step of the intake after the first successful apply.
* A step of `mashctl policy adopt`.

## Decision Outcome

Chosen option: "An operator command after the first list set, dry run first".

`mashctl policy reconcile` prints the plan and changes nothing. With `--yes` it computes the plan again under the
account lock and the ledger lease and carries it out. The two halves:

* **Signing keys.** A signing key is mash's when it is a scoped key whose public key the key store holds as an owner
  group key, of this range or any other; the key store is the authority, and a key that only carries a group's role is
  not mash's. The first applied list set removes every other signing key: the gate accepts an account JWT only when it
  holds exactly the expected scoped keys (ADR 0100, step 2), and the apply path's signer drops them once the issuer check
  below passes. The reconcile
  therefore pushes nothing for them. It reports a non-mash key still present in the JWT in force as a finding, and with
  `--listset FILE`, before the first apply, each key that apply would remove, with the connected users each one signed.
  A user signed by a removed key stops working with it. So the reconcile refuses (exit 1) when the admin user's issuer,
  or a connected exempt user's issuer, is a key that would be removed: reissue those users under the account key
  first. The same check, `admin.CheckIssuersKept`, is for the apply path to make before the first push.
* **Revocations.** The plan revokes the recorded issued users whose sender is not in the list set in force, and the
  connected user keys that are neither in the issue record, nor exempt, nor issued under the list set. They are revoked
  by signing the account JWT in force with the added revocations, pushing it, gating it on every server and committing
  it as the JWT in force, so a later run keeps them; each key is then marked revoked in the issue record. A revocation's
  time is the later of now and the connected credential's issue time.

What keeps a live endpoint's key from being revoked:

* A recorded key is revoked only when its history in the issue record is shown complete (ADR 0115) and no revision is
  dated in the future. Otherwise it is a finding and is left alone.
* A connected key with no record whose credential is signed by one of the list set's scoped keys is a list-set
  credential that lost its record: a finding, never revoked as untracked.
* A key with no record counts as untracked only when the issue record's bucket shows that no record was lost (deletes
  and purges denied, and its history metadata `full` or `raised:N:S`). Otherwise every unrecorded connected key is a
  finding.
* Exempt keys (the admin user, the system user, each `--exempt` key) are never in any action.

Every revocation in the plan names its key, the sender or connection it belongs to and why, so a reader sees a live
endpoint at a glance. Any finding makes the result `reconciled: partial (N unknown)`.

Until the account JWT signer the apply path uses is configured for the command, `--yes` refuses with "no signer
configured: run without --yes to see the plan" and changes nothing.

### Consequences

* **A recorded user whose sender is not in the list set in force is revoked, and in this release there is no way back
  inside mash.** Once an account is adopted and its first list set applied, every credential mash issued to a sender
  the list set leaves out stops working when the operator runs `mashctl policy reconcile --yes`. An endpoint the outside
  system forgot to list, or a credential revoked by mistake, loses its credential for good: an adopted account refuses
  `mashctl reissue`, `mashctl policy serve` issues nothing, and the issue service is for test accounts only (ADR 0104).
  So `--yes` is one-way. The dry run names each credential first and ends with "a revoked credential cannot be reissued
  on an adopted account in this release: --yes is one-way".
* Good, because the account ends with only mash's scoped keys and the credentials the list set accounts for.
* Good, because nothing is revoked on a doubt: an incomplete record, an unprovable bucket or a scoped-key credential
  without a record is reported, not revoked.
* Good, because mash cannot remove the key its own admin user was signed by.
* Bad, because the account is not fully reconciled until an operator runs the command; an automatic step would not
  wait.
* Bad, because `--yes` waits for the apply path's signer.

### Confirmation

Unit tests in `internal/reconcile` (`plan_test.go`), one per rule: `TestRecordedSenderNotInTheListSetIsRevoked`,
`TestConnectedUnrecordedIsRevokedAsUntracked`, `TestRecordedKeyWithAnUnprovenOrFutureHistoryIsAFindingAndUntouched`,
`TestUnrecordedKeyIssuedByAScopedKeyIsAFindingNotARevocation`, `TestWithoutBucketProofEveryUnrecordedConnectedKeyIsAFinding`,
`TestExemptKeysAreInNoActionAndNoFinding`, `TestRevocationTimeIsTheLaterOfNowAndTheJWTIssueTime`,
`TestAnotherRangesHeldScopedKeyIsKept`, `TestScopedKeyNotHeldIsNonMash` and `TestAdminUserUnderAKeyThatLeavesIsAnIssuerProblem`;
`admin.CheckIssuersKept`'s tests; the executor's tests (`exec_test.go`); the command's dry run text and JSON goldens and
`--yes` refusing (`cmd/mashctl/policy_reconcile_test.go`). `TestPolicyReconcileAT015Live` runs AT-015 on a test account:
a legacy user signed by the account key and a plain signing key, adoption, a first list set applied, the plan revoking
the user with the key gone, then the executor revoking it and the user's reconnect refused.

## Pros and Cons of the Options

### An operator command after the first list set (chosen)

* Good, because a person reads the plan before anything is revoked.
* Good, because it keeps the intake to one job: applying list sets.
* Bad, because it is one more step to run.

### Automatic after the first apply

* Good, because nothing is left to remember.
* Bad, because revocations would happen with nobody reading what they hit.

### At adopt

* Good, because it is early.
* Bad, because there is no list set yet to tell recorded users in it from those out of it, and adopting must change
  nothing (ADR 0111).

## More Information

* AT-015 (contract mash-messaging-policy). ADR 0111 (adoption and its planned reconcile), ADR 0112 (the intake), ADR
  0115 (complete history), ADR 0100 (the gate), ADR 0117 (the final JWT).
