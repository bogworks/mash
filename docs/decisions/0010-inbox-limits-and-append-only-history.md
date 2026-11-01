---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Inboxes keep 30 days or 16 MiB, drop the oldest, and deny delete and purge

## Context and Problem Statement

Each endpoint's inbox is a JetStream stream (ADR-0009). A stream needs limits: how long messages are kept, how much
space they may use, how large one message may be, and what happens when a limit is reached. JetStream can also deny
deleting single messages or purging the stream. mash had to choose these settings for every inbox.

## Decision Drivers

* "Durable for 30 days. History stays readable by its owner for reports." (spec §1.1 goal 4)
* History should not be quietly rewritten, even by someone holding the account's admin credentials.
* The stored subject must stay as published, so a stored signature still verifies (SAP §13.8).
* The NATS server used at the start had no memory store.
* A consumer needed mail kept for 30 days.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Bounded age and size, discard the oldest, deny delete and purge (as built)
* Bounded age and size, refuse new mail when full (discard new)
* Allow delete and purge, so an endpoint or admin can clean its inbox

## Decision Outcome

Chosen option: "Bounded age and size, discard the oldest, deny delete and purge". The reasons on record:

* 30 days follows spec goal 4. A design-era spike used 90 days; the change to 30 days is not explained.
* Deny delete and purge, "so even the account admin cannot quietly rewrite history; deleting the whole stream
  (deprovisioning) is the only way to remove it" (`docs/operations.md`, Inbox limits).
* File storage, because the server had no memory store (`docs/evidence/2026-10-22-live-e2e.md`).
* No subject transform, because SAP §13.8 forbids one: a stored signature must verify against the stored subject.

The value 16 MiB (inbox size) was chosen without a recorded reason. The value 1 MiB (largest message) was chosen
without a recorded reason. The value 2 minutes (duplicate window) was chosen without a recorded reason. No reason was
recorded for discarding the oldest mail rather than refusing new mail.

Later additions: a send is capped at the smaller of 1 MiB and the server's `max_payload` (512 KiB on one hosted free
tier), and the two KV buckets are capped at 16 MiB (directory) and 4 MiB (leases), because a hosted account rejects a
stream configuration with no maximum size (pull request #127, 2026-10-30).

### Consequences

* Good, because mail survives sessions being offline for weeks.
* Good, because no one with endpoint or account-admin rights can remove a single message; only deleting the stream does.
* Bad, because a flood from any endpoint of the same owner evicts older mail, including unread mail (`SECURITY.md`,
  Known limits), and no endpoint can purge the flood.
* Bad, because nothing keeps mail past 30 days.
* Neutral, because limits are fixed in code, the same for every inbox.

### Confirmation

* `internal/admin/admin_test.go` `TestProvisionCreatesInboxConsumerRecord`: file storage, limits retention, 30 days,
  16 MiB, 1 MiB, discard old, 2-minute duplicate window, deny delete, deny purge, direct get, no subject transform.
* `internal/admin/admin_test.go` `TestEndpointPermissionDenials`: message delete, purge and stream delete are denied.
* `internal/endpoint/endpoint_test.go` `TestSendTooLarge`; `internal/endpoint/maxpayload_test.go`
  `TestSendFollowsTheServersMaxPayload` and `TestSendKeepsTheOneMiBCapOnTheDefaultServer`.
* `internal/admin/maxbytes_test.go` `TestInitFreshBucketsHaveMaxBytes` and `TestInitOnServerThatRequiresMaxBytes`.

## Pros and Cons of the Options

### Discard oldest, deny delete and purge

* Good, because new mail always lands and history is append-only.
* Bad, because a full inbox loses its oldest mail, read or not.

### Discard new when full

* Good, because stored mail, including unread mail, is never evicted by size.
* Bad, because a full inbox refuses all new mail until messages age out.

### Allow delete and purge

* Good, because an inbox could be cleaned after a flood.
* Bad, because history could be rewritten without a trace.

## More Information

* Design spec §1.1 (goal 4) and §5.1. Code: `internal/store/store.go` (`InboxConfig`, bucket caps),
  `internal/endpoint/mail.go` (send size check).
* Design-era spike: maintainer design discussion, 2026-10-21 (not public).
* Pull request #127 (bucket caps and `max_payload`).
* Related: ADR-0009 (inbox per endpoint), ADR-0031 (duplicate window and send keys), ADR-0037 (files by reference).
