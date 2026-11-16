---
status: proposed
date: 2026-11-16
decision-makers: Randy Bias (mash maintainer)
---

# A legacy endpoint is governed in place: its endpoint tag is its name, and `add` refuses under an applied list set

Amends: ADR-0132

## Context and Problem Statement

[ADR 0132](0132-policy-scope-is-the-account-and-an-owner-is-the-group-range-is-removed.md) gives a credential an endpoint
tag `ep:<flat key>` so a template can name the sender's inbox, lease and reply prefix, and refuses a legacy identity
because its flat key has no owner. A solo developer's endpoints are made by `mashctl add` and are legacy, so the
credential primitive ([ADR 0136](0136-mashctl-signs-a-credential-for-a-public-key-the-endpoint-made-and-never-sees-a-private-key.md))
cannot govern them, and `add` and `reissue` still mint untagged owner-wide credentials, so an applied list set changes
nothing for them. How does a legacy endpoint come under a policy?

## Decision Drivers

* A solo developer must be able to apply a policy to the endpoints `add` made, without migrating them.
* A legacy endpoint's inbox, streams and mail must not move.
* An endpoint tag must never point a credential at another owner's inbox.
* A policy and `add` must not contradict each other silently.
* The choice of a default agent token, and of what `add` produces, is the maintainer's and is not made here.

## Considered Options

* Govern the legacy endpoint in place (chosen).
* `add` makes new-form endpoints by default (rejected for now: needs a default agent token and still needs a migration of every existing endpoint).
* Migrate legacy endpoints to the new form first (rejected for now: the longest path, for no gain the first option lacks).

## Decision Outcome

Chosen: govern in place.

1. The endpoint tag of a legacy identity is `ep:<name>`, its flat key. The templates are unchanged: a legacy and a new-form flat key use the same lines, and they cannot collide (a new-form flat key has underscores, a legacy name has none).
2. The issuer issues a legacy tag only when the directory holds a legacy record named `<name>` under the address's owner. A missing record, another owner's, a new-form record at that name, or a failed read refuses `endpoint_mismatch` and issues nothing.
3. `mashctl add` and `reissue` refuse while a list set is in force, before any key, record or file is made, with an error that names the governed path. This is the applied-policy refusal of ADR 0091 generalized from a zone policy to a list set.
4. Credentials issued before a policy stay owner-wide until reissued through the primitive. The apply's plan names them.

No verb is added or renamed here. What the governed path is called, and whether `add` calls the primitive itself, wait for the maintainer's answers on the policy verbs.

### Consequences

* Good, because an `add` endpoint is governed with no migration and no moved mail.
* Good, because a policy and `add` cannot silently disagree.
* Bad, because `add` and `reissue` stop working on an account with a list set until the CLI face of the governed path exists.
* Neutral, because it amends one refusal of ADR 0132 and adds one directory read to the issuer.

### Confirmation

* A live two-server test with endpoints made by `add`: apply, credential through the primitive, x reaches y and z does not.
* A unit test that a legacy tag is refused for a missing, foreign or new-form record and on a read error.
* A test that `add` and `reissue` refuse under a list set and change nothing.

## Pros and Cons of the Options

### Govern in place (chosen)

* Good, because nothing moves and it needs one tag rule and one directory check.
* Bad, because the legacy tag rests on a directory read.

### `add` makes new-form endpoints by default

* Good, because new endpoints are in the form the policy was designed for.
* Bad, because it needs a default agent token and does nothing for existing endpoints.

### Migrate to the new form first

* Good, because one form remains.
* Bad, because it is a new identity, inbox and alias window per endpoint before any policy works.

## More Information

Design note: `docs/plans/2026-11-16-governing-an-add-endpoint.md`. Related: ADR 0074, 0076, 0091, 0132, 0136.
