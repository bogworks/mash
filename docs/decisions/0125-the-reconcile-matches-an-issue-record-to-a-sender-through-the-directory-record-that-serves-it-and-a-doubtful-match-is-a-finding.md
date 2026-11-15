---
status: accepted
date: 2026-11-15
decision-makers: the mash coordinator (mash-lead); the mapping agreed with the core lane, which owns the inbox check
---

# The reconcile matches an issue record to a sender through the directory record that serves it, and a doubtful match is a finding

## Context and Problem Statement

[ADR 0121](0121-an-adopted-account-is-reconciled-by-an-operator-command-after-its-first-list-set-dry-run-first.md) says
that the reconcile revokes "the recorded issued users whose sender is not in the list set in force". It does not say how
an issue record is matched to a sender. The planner matched the record's `address` with the list set's sender address
(`range.owner.name`). An issue record holds the endpoint's directory name and its owner. For an endpoint made by
`mashctl add` that is the bare name (`l1`, owner `h1`), and for a new-form endpoint it is the flat key. So neither kind
ever matched, and the reconcile planned to revoke endpoints that the list set names.

The apply had already accepted these same endpoints. The inbox check behind it (`admin.CheckInbox`, ADR 0122) resolves
a sender's address to the directory record that serves it: the record of its own, or else the legacy record of the same
owner and name. The two halves of one adoption disagreed about which endpoint a sender is.

## Decision Drivers

* The reconcile and the apply must agree on which endpoint a sender is.
* Nothing is revoked on a doubt (ADR 0121).
* Legacy names are not unique across owners.

## Considered Options

* Match through the directory record that serves each sender, with the same resolution as the inbox check (chosen).
* Match on the record's name alone.
* Keep the exact match, and tell operators to reissue legacy endpoints in the new form before adopting.

## Decision Outcome

Chosen option: "Match through the directory record that serves each sender".

* The planner resolves every sender of the list set with `admin.ServingIdentity`. That function is the record
  resolution of `CheckInbox`, which now calls it, so the two cannot differ.
* An issue record is a sender's when its directory name and owner are those of the record serving that sender. A legacy
  record is matched on name and owner together. The exact address match is kept as well.
* Any doubt is a finding, never a revocation. A finding is made when:
  * the record has no owner and carries a sender's name;
  * resolving a sender with the record's name failed;
  * the list set names a sender of the record's name and owner, but no directory record serves it (for example after
    `deprovision --no-revoke`); removing the sender from the list set is what lets the reconcile revoke the record;
  * one record serves several senders. That record counts as in the list set, and the finding names the senders.

This amends ADR 0121's revocation rule: "whose sender is not in the list set" means that no sender of the list set is
served by the record. To its list "What keeps a live endpoint's key from being revoked" it adds: a record that may be a
sender's, but cannot be shown to be one, is a finding.

### Consequences

* Good, because an account of `mashctl add` endpoints can be adopted and reconciled without losing the endpoints its
  list set names.
* Good, because the inbox check and the reconcile share one function.
* Bad, because the planner reads the directory once per sender.

### Confirmation

* Unit tests in `internal/reconcile/plan_test.go` cover:
  * a legacy record kept, and the same name under another owner revoked;
  * a new-form record kept;
  * a resolver error made a finding;
  * an ownerless record made a finding;
  * two senders on one record;
  * the plan digest covering the findings.
* `TestPolicyReconcileKeepsALegacyEndpointOfAListSetSenderLive` adopts an account with a `mashctl add` endpoint, applies
  generation 1 through the production Applier, and checks that the dry run plans no revocation for it.

## Pros and Cons of the Options

### Through the serving record (chosen)

* Good, because it is the apply's own answer.
* Bad, because it needs the directory, which the reconcile already reads over the admin connection.

### Name alone

* Good, because it is simple.
* Bad, because a legacy name repeats across owners, so it would keep a record that belongs to another owner.

### Exact match, and reissue first

* Good, because it changes no code.
* Bad, because an operator who misses the step loses every legacy endpoint at `--yes`, and the apply had told them the
  endpoints were fine.

## More Information

* Amends ADR-0121, its revocation rule for recorded users. Related: ADR-0122 (the inbox check) and ADR-0124.
