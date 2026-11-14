---
status: proposed
date: 2026-11-14
decision-makers: Randy Bias (mash maintainer)
---

# Applying a list set checks that an added address has its inbox and makes nothing

## Context and Problem Statement

A list set can add an address, and applying it runs two steps for each new address: create its inbox and issue its
credential. ADR 0104 says credentials come from the issue service for a public key a wrangler countersigned, and that mash
never makes, receives or stores an endpoint's private key. A list set carries no public key. So `mashctl policy serve`
could do neither step, and its Provisioner failed closed: any change that added an address was refused. That left no way to
apply the first list set of an adopted account at all. What should the two steps mean when a list set is applied?

## Decision Drivers

* No private key is made, received or logged on the apply path (ADR 0104).
* An apply must not leave an address in a list set whose mail has nowhere to land.
* The check must be safe to run before anything is pushed, and again later, with no side effect.
* An account's existing endpoints, made with `mashctl add`, must be usable. `add` makes a legacy endpoint: the inbox
  stream is `inbox-<name>`, filtered on `agents.mail.*.<owner>.<name>`. A list-set address `range.owner.name` is a new-form
  identity whose own inbox would be `inbox-<range>_<owner>_<name>`. ADR 0074 refuses to admit both, because the legacy
  filter already matches the new-form subject.

## Considered Options

* Check only: an added address must already have its inbox, and the apply makes nothing (chosen).
* Make the inbox from the list set, with a placeholder user key.
* Keep refusing every added address.

## Decision Outcome

Chosen option: "Check only".

**Create inbox** means verify. The service looks at the address with a read-only call, `Admin.CheckInbox`, which reads the
directory record and the stream and the durable consumer and creates nothing and writes no record. An address has its inbox
when its own record and `inbox-<flat>` exist, or, when it has no record of its own, when the legacy endpoint of the same
owner and name has its record and `inbox-<name>`. In both cases the stream and the `deliver` consumer must have the
configuration mail depends on, the same fields `EnsureInbox` checks; one that differs is an error naming the field.

**Issue credential** means check that the address can be issued: it is in the list set in force or the target and its owner
group's scoped key exists in the key store. It issues nothing, as before; the credential still comes from the issue service
or from `mashctl add` (ADR 0104).

**Where it is decided.** Before it signs or pushes anything, the Applier checks every address the change adds. If any has no
inbox the change is refused, and the status names the address (up to three, then a count) and says in plain words that it
has no inbox yet and that its endpoint must be provisioned, with a credential from the issue service, first. An address that
has its inbox is applied. A stream that differs from the configuration refuses the change with the field named. The same
check runs again as the create-inbox step inside the apply, so the two cannot disagree about an inbox that was there a
moment ago.

The existing rule that a change which narrows an existing sender by tags is refused is unchanged, because that needs a new
credential for the sender.

### Consequences

* Good, because the first list set of an account whose endpoints were made with `mashctl add` is applied with no test
  provisioner, and nothing is made that mash cannot undo.
* Good, because a list set can no longer name an address whose mail would be dropped.
* Bad, because the operator provisions endpoints before the list set that names them, and `mashctl add` is refused once an
  account is adopted (ADR 0111), so those endpoints are made before adoption or through the issue service.
* Bad, because a stream whose limits were changed since it was made is refused until it is put back.
* Not decided here: how an endpoint of an already adopted account is provisioned after adoption, and how a rotated sender
  receives its new credential.

### Confirmation

Unit tests: `TestCheckInboxFindsAnInboxAndCreatesNothing` and `TestCheckInboxReportsAMissingConsumerAndAChangedStream` in
`internal/admin`; `TestCreateInboxOnlyChecksAndNamesWhatIsMissing`, `TestNoInboxDetailIsPlainAndShort`,
`TestApplierRefusesAnAddressWithNoInboxBeforeAnythingIsPushed` and `TestApplierAppliesAnAddressWhoseInboxExists` in
`internal/policyserve`. End to end, with no test provisioner: `TestPolicyServeAppliesAFirstListSetOfAddMadeEndpoints` and
`TestPolicyServeRefusesAnAddressWithNoInbox` in `cmd/mashctl`.

## Pros and Cons of the Options

### Check only (chosen)

* Good, because nothing is made that needs a key mash does not hold.
* Good, because it works for the endpoints `mashctl add` already made.
* Bad, because the operator must provision before submitting.

### Make the inbox from the list set, with a placeholder user key

* Good, because a list set would be self-contained.
* Bad, because the record would hold a key that is not the endpoint's, and replacing it later is a second provisioning path.

### Keep refusing every added address

* Good, because it changes nothing.
* Bad, because no first list set could ever be applied.

## More Information

Follows ADR 0104 (credentials come from the issue service), ADR 0074 (the legacy and new-form identities and their overlap)
and ADR 0111 (an adopted account takes list sets only through the list-set path).
