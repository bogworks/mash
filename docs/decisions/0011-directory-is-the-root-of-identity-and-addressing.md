---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# The directory KV bucket is the root of identity and addressing

## Context and Problem Statement

A sender needs two things about a recipient: the subject to publish its mail to, and, for incoming mail, which
endpoint name a signing NATS user belongs to. SAP says callers "MUST learn endpoint subjects from `$SRV.INFO`", the
live discovery of running services, and lets a receiver judge a sender against "a registry it trusts" (SAP §13.7.2).
mash mail must reach endpoints that are not running, so live discovery alone cannot address it.

## Decision Drivers

* Mail must be addressable while the recipient is offline.
* A receiver must map a verified NATS user key to exactly one endpoint name.
* Only the admin who provisions endpoints may change who is who.
* Endpoints must not need rights to create consumers on shared state (ADR-0007).

## Considered Options

* Address through SAP's `$SRV.INFO` discovery
* A directory in a KV bucket (NATS's key-value store), read through a KV watch
* A directory in a KV bucket, re-read by polling

## Decision Outcome

Chosen option: "A directory in a KV bucket, re-read by polling". Senders resolve names through the directory "not
through `$SRV.INFO`, because the recipient may be offline" (spec §3), and the directory is the "registry it trusts" of
SAP §13.7.2 (spec §4). A watch was the first design; it was replaced by polling the same night because "endpoints may
not create or delete directory consumers" (commit `9a82418`), and a watch needs a consumer.

* `mash-directory` maps each name to a record: name, owner, agent, agent token, account, user key, mail subject,
  created, and an optional note. Only the admin writes it.
* Endpoints read it at start and every 15 s, from stream info and direct gets, with no consumer. The value 15 s was
  chosen without a recorded reason.
* `mash serve` refuses to run if its own record does not match the user in its credentials.
* A validly signed message from a user the directory does not know, or that names a different endpoint, is labelled
  `unknown-signer`, not `verified`.
* Optional metadata (`role`, `host` and free-form `labels`) is "identity on record, not live state"
  (`docs/operations.md`); the lease is the live view. A consumer needed to record what an endpoint is for and where it
  runs (pull request #104, 2026-10-30). Pull request #170 (2026-10-31) replaced a single grouping field with generic
  labels; a value in that older field is read as a label.

### Consequences

* Good, because mail can be sent to an endpoint that has never been online.
* Good, because identity has one source, written by one party.
* Bad, because this bends a SAP "MUST": a pure SAP tool does not know to read the directory.
* Bad, because a newly provisioned endpoint is visible to running endpoints only after their next poll (up to 15 s).

### Confirmation

* `internal/endpoint/endpoint_test.go` `TestIdentityMismatchRefuses`, `TestDirectoryLiveUpdate`, `TestUnknownSigner`,
  `TestNameClaimMismatchIsUnknownSigner`, `TestSendUnknownName`, `TestSendReceiveVerified`.
* `internal/admin/admin_test.go` `TestEndpointPermissionDenials`: endpoints cannot write the directory or create or
  delete consumers on it.
* `internal/store/store_test.go` `TestParseRecordWithAndWithoutMeta`, `TestValidLabels`, and the test of an older record's grouping field;
  `internal/admin/meta_test.go` `TestSetMetaLabels`.

## Pros and Cons of the Options

### `$SRV.INFO` discovery

* Good, because it is what SAP prescribes.
* Bad, because an offline endpoint does not answer, so it could not be mailed.

### KV directory with a watch

* Good, because changes arrive at once.
* Bad, because a watch creates a consumer, which endpoints are not allowed to do.

### KV directory with polling

* Good, because it needs only read rights (stream info and direct get).
* Bad, because changes arrive up to one poll interval late.

## More Information

* Design spec §3 (addressing), §4 (who a verified user is), §6 (directory).
* Code: `internal/store/store.go` (`Record`, `DirectoryConfig`), `internal/endpoint/directory.go` (`DirectoryPoll`).
* Commit `9a82418` (poll instead of watch); pull requests #104 and #170 (metadata, labels).
* Related: ADR-0004 (the mail subject is declared), ADR-0007 (permissions), ADR-0019 (trust labels).
