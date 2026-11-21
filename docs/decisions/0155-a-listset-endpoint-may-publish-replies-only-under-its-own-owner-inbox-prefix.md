---
status: accepted
date: 2026-11-21
decision-makers: Randy Bias (mash maintainer)
---

# A listset endpoint may publish replies only under its own owner's inbox prefix

## Context and Problem Statement

[ADR 0007](0007-one-nats-user-per-endpoint-least-privilege.md) gives each endpoint the right to publish to `_INBOX.>`,
so it can answer a caller. `mashctl`'s admin permissions already narrowed that to the endpoint's own owner
(`_INBOX.<owner>.>`, `internal/admin/permissions.go`). The listset compiler (`internal/listset`) still granted the whole
`_INBOX.>`, and its check required it. An endpoint made through a listset could therefore publish into another owner's
inbox prefix (security read 2, finding R1-4).

## Decision Drivers

* Least privilege: a reply right should reach only the callers the endpoint may serve.
* The listset and admin permission sets should say the same thing about replies.
* Cross-group replies, for example a prompt answered across two owner groups of one range, must keep working.

## Considered Options

* Keep `_INBOX.>` (the grant in ADR 0007).
* Bind the grant to the endpoint's own inbox prefix in listset, as admin does.

## Decision Outcome

Chosen option: bind it. The compiled publish line is `_INBOX.<rng>.>` and the subscribe line is
`_INBOX.<rng>.<flat>.>`, where `<rng>` is the first token of the endpoint's flat key (`range_owner_name`), which is
admin's owner (admin's owner, group and instance are this package's range, owner and name). The check requires exactly
those lines and refuses a template that still carries `_INBOX.>`.

Binding to the middle token (this package's owner) was tried and refused: a reply across owner groups of one range was
denied (`TestAT019LivePromptGetsItsDirectAnswer`, `TestAT005DenyAllSenderIsRefused`,
`TestAT020OpenReplyAndPlumbingSubjectsArePinned`).

### Confirmation

`TestPublishInboxBoundToOwner` in `internal/listset` checks that a compiled template allows `_INBOX.<its range>.x` and
refuses `_INBOX.<another range>.x`, and that `Check` refuses a template carrying `_INBOX.>`. The live tests AT-019, AT-005
and AT-020 check that cross-group replies still work.

### Consequences

* Good, because an endpoint cannot publish into another range's inbox prefix, and the two permission sets agree.
* Good, because replies across owner groups of one range still work.
* Neutral, because ADR 0007's publish line `_INBOX.>` is narrowed here for listset endpoints; the rest of ADR 0007 stands.

## Pros and Cons of the Options

### Keep `_INBOX.>`

* Good, because it is what ADR 0007 says and it never blocks a reply.
* Bad, because an endpoint can publish into any other range's inbox prefix, which admin already forbids.

### Bind to the endpoint's own inbox prefix

* Good, because it is least privilege and matches admin.
* Bad, because the listset template and its check change together, and golden files and the policy document move with them.

## More Information

Security read 2, finding R1-4. The permission set is described in [the policy enforcement document](../policy-enforcement.md).
