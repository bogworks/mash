---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer)
---

# The publish grant on `_INBOX.>` and `$SRV.>` stays, and is stated as outside the mail policy

Amends: ADR-0007

## Context and Problem Statement

[ADR 0007](0007-one-nats-user-per-endpoint-least-privilege.md) gives every endpoint a publish grant on `_INBOX.>` and `$SRV.>`
so a replier can answer a caller whose reply subject it cannot know in advance (`fixedPub` in
`internal/admin/permissions.go`). Issue #218 found that this grant is a publish path the mail policy does not govern:
an endpoint allowed no mail, prompt or status at all can still publish to `_INBOX.bill.spoof`, and any subscriber on
that prefix receives the bytes. Should the grant be kept, replaced, or scoped?

## Decision Drivers

* A reply subject is chosen by the caller, so a fixed per-replier scope does not fit without a protocol change.
* Real request inboxes carry 128 random bits, so forging a reply to a live request means guessing the token.
* The prompt reply path and `$SRV` answers must keep working.

## Considered Options

* Keep the grant and state in the security documents that it is outside the mail policy.
* Replace the grant with NATS response permissions (`allow_responses`).
* Scope the grant per endpoint.
* Drop the grant from endpoints that never serve prompts or services.

## Decision Outcome

Chosen option: keep the grant and state it. `_INBOX.>` and `$SRV.>` are outside the mail policy; they carry replies
only, and the covert publish path between endpoints that already know each other's inbox prefixes is accepted.
`SECURITY.md` (Known limits) says so.

* Response permissions are revisited in the 0.6.0 SDK move, which changes the reply-subject layout; it is not decided
  here.
* Per-endpoint scoping is out: it needs a protocol change.

### Consequences

* Good, because there is no change in behaviour and no re-test of the chunked prompt reply path.
* Bad, because a covert, unpoliced publish path remains between endpoints of one account; the limit is documented, not
  closed.
* Neutral, because the same grant is part of the starting point the 0.6.0 policy work narrows.

### Confirmation

* `internal/admin/permissions_test.go` `TestPermissionsExact` keeps the exact publish list, which still includes
  `_INBOX.>` and `$SRV.>`.
* `SECURITY.md` (Known limits) states the limit.

## Pros and Cons of the Options

### Keep the grant and state it

* Good, because no behaviour changes.
* Bad, because the covert publish path stays.

### Response permissions

* Good, because a reply could only go to a request the endpoint received.
* Bad, because every reply path changes and chunked prompt replies need re-testing.

### Scope the grant per endpoint

* Bad, because callers build the reply prefix, so it needs a protocol change.

### Drop the grant for endpoints that serve nothing

* Good, because it narrows the path for some endpoints.
* Bad, because it needs a role or capability that does not exist.

## More Information

* Issue #218. The planner's ruling of 2026-11-25 chose option 1.
