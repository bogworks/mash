---
status: proposed
date: 2026-11-16
decision-makers: Randy Bias (mash maintainer)
---

# `mashctl` signs a credential for a public key the endpoint made, and never sees a private key

## Context and Problem Statement

`mashctl add` makes each endpoint's private key where the signing keys are and puts it into a creds file, so a seed
travels to the endpoint. The issue service ([ADR 0104](0104-an-issue-service-signs-a-credential-for-a-public-key-a-wrangler-countersigned.md))
signs for a public key instead, but only for a countersigned request from an outside requester, which a solo developer
does not have ([ADR 0131](0131-mash-stands-alone-everything-works-without-homerange-or-any-upward-dependency-first.md)).
How does a person, or a program wrapping `mashctl`, get a credential for a key the endpoint made itself?

## Decision Drivers

* No private key is made, received, stored or logged by the signing side.
* Nothing above mash is needed: the holder of the operator keys is enough.
* The same refusals as the issue service, so a person and a service meet one vocabulary, and one signing path to test.
* Reissue and audit work from the public key alone.
* Fail closed: no list set in force, an unpublished or mismatched signing key, a revoked or bound key, all issue nothing.
* No default for a first policy is presumed (open on #681).

## Considered Options

* Share the issue service's signing core; add a request-less entry for the holder of the keys (chosen).
* Run the issue service locally with a requester registered for the operator's own key (rejected: it makes the standalone path depend on a registry, a proof and a resident service it does not need).
* Keep `mashctl add` and make it print the public half (rejected: the seed is still made on the signing side).

## Decision Outcome

Chosen: the first option.

1. `internal/credential` gives `Issue(request) → result` in JSON: address `owner.name`, the endpoint, a user public key and a
   purpose go in; a signed JWT and the issue record's facts come out. The caller's trust is holding the owner group's scoped
   signing key. There is no requester, registry, proof or nonce on this path.
2. The signing steps after authentication move out of `issue.Issuer` into one shared `issue.Authority.Sign`; the service and the primitive both call it. The service's behaviour does not change.
3. The JWT carries no permissions of its own (the scoped key's template applies), the sender's tags and the `ep` tag mash computes
   from the endpoint ([ADR 0132](0132-policy-scope-is-the-account-and-an-owner-is-the-group-range-is-removed.md)). The issue record is written, as a compare-and-set, before the JWT is returned.
4. The endpoint makes its key and assembles its creds file itself (`NewKey`, `Assemble`); a JWT whose subject is not the seed's public key is refused.
5. Reissue is the same call for the same key. Rotation is a call for a new key followed by the existing revoke of the old one by public key; the call itself never revokes.
6. `Show(public key)` reports the record, its history, whether the account JWT revokes it and whether it is connected, from the public key alone.
7. With no list set in force, `Issue` refuses `no_list_set`. This decides nothing about the first policy.

`mashctl add` and `reissue` are unchanged. No command is wired; the CLI face is a later change.

### Consequences

* Good, because a seed is never made or seen by `mashctl` on this path, and a person and a program use the same call.
* Good, because the service and the primitive share one signing path and one refusal list.
* Bad, because there are two ways to get a credential until `add` is retired; which survives is the maintainer's choice, later.
* Neutral, because it adds a refusal-only dependency on a list set in force, which the direct driver provides.

### Confirmation

* A live test over a two-server cluster, no service running: a key made by the endpoint, a credential signed by the group key, connection and isolation on the other server, `Show` by public key, revoke, and a refused re-issue.
* A test that a seed in any request field is refused and never reaches a log or a result.
* The issue service's existing tests pass unchanged on the shared core.
* The standing-order guard test (`TestNoWordOfALayerAboveInShippedCodeAndDocs`) passes.

## Pros and Cons of the Options

### Share the signing core with a request-less entry (chosen)

* Good, because one signing path and one refusal list serve both callers.
* Bad, because the shared core must be extracted from a service that is already tested.

### Run the issue service locally with the operator registered

* Good, because no code changes.
* Bad, because it needs a registry, a proof, a nonce set and a resident process for one person.

### Keep `mashctl add` and print the public half

* Good, because it is the smallest change.
* Bad, because the private key is still made on the signing side, which is what this decision removes.

## More Information

Design note: `docs/plans/2026-11-16-credential-for-a-public-key.md`. Related: ADR 0052, 0067, 0101, 0104, 0114, 0131, 0132.
