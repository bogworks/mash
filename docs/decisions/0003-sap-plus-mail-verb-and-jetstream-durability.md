---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# mash is SAP 0.3 plus one verb and JetStream durability; it invents no protocol

## Context and Problem Statement

Agent sessions need to send each other messages that wait while the recipient is busy or not running, and to know who
sent each message. The Synadia Agent Protocol (SAP) 0.3 already defines how agents on NATS are named, discovered and
prompted live, and how a sender proves its identity (SAP §13). It has no durable mailbox. The question was how much
of mash should be new protocol and how much should be borrowed.

## Decision Drivers

* Any SAP tool should see a mash session as an ordinary SAP agent ("Pure SAP", spec §1.1 goal 5).
* As little new code as possible: "One Go binary, plus conventions" (spec §1.1 goal 6).
* Mail must survive the recipient being offline for days (spec §1.1 goal 4).
* A consumer needed a durable mailbox between agent sessions on different machines, without a shared filesystem.

## Considered Options

* SAP 0.3 on NATS, with one added verb (`mail`) and JetStream, NATS's persistence layer, for durability
* A mailbox on S3-compatible object storage, which was designed the same day and paused
* A transport of mash's own (not recorded as weighed; listed for completeness)

## Decision Outcome

Chosen option: "SAP 0.3 on NATS, with one added verb and JetStream for durability", because it keeps every mash
session a plain SAP agent and adds only what SAP lacks: a durable inbox. The maintainer chose it over the S3 mailbox
design on 2026-10-21. No reason was recorded for choosing NATS over S3 or any other transport.

What each layer provides:

* NATS: the connection, subjects, user credentials and permissions.
* JetStream (NATS): streams that hold mail, and KV buckets (NATS's key-value store) for the directory and leases.
* SAP: the subject hierarchy, `$SRV` service discovery, live prompts, heartbeats and §13 sender signatures.
* mash: the `mail` verb, the inbox layout, delivery into the agent session, and trust labels.

### Consequences

* Good, because SAP tools discover, probe and prompt mash sessions without knowing about mash.
* Good, because identity and signing come from SAP §13 and NATS user keys instead of a new scheme.
* Bad, because mash depends on a NATS server with JetStream enabled.
* Neutral, because mash departs from SAP in two places, each recorded separately: senders address mail through mash's
  directory rather than `$SRV.INFO` (ADR-0011), and mash shows two trust labels SAP does not define.

### Confirmation

* `internal/sap/sender_test.go` `TestSignKnownAnswerB13`: signing matches SAP's published known-answer vector B.13
  byte for byte.
* `internal/endpoint/service_test.go` `TestRegistrationMetadata` and `TestHeartbeatSigned`: the SAP registration and
  heartbeat carry `protocol_version` "0.3".
* `internal/endpoint/service_test.go` `TestDiscoverAndPromptReply`: a session is discovered and prompted as a SAP agent.

## Pros and Cons of the Options

### SAP on NATS plus `mail` and JetStream

* Good, because the protocol, discovery and identity are already specified and have SDKs.
* Good, because JetStream gives durable storage, acknowledgement and limits without new code.
* Bad, because the protocol's evolution is outside mash's control (mash targets SAP 0.3.0 at a fixed commit).

### Mailbox on S3-compatible object storage

* Good, because object storage is simple to host and cheap to keep.
* Bad, because it offers no live presence, no push delivery and no per-message sender identity without building them.

### A transport of mash's own

* Bad, because every piece SAP and NATS provide would have to be designed, built and secured again.

## More Information

* Design spec `docs/specs/2026-10-21-mash-design.md` §1 and §1.1 (first version: commit `5ab1e4b`).
* `internal/sap/subjects.go`: `ProtocolVersion = "0.3"`, service name and queue group `agents`.
* The S3 mailbox design: maintainer design discussion, 2026-10-21 (not public).
* The first README (commit `922b893`) parked an MCP transport over SAP and bridges to ACP and A2A for later research.
* NATS was already deployed before mash: the first live run removed an earlier proof-of-concept stream from the
  account (`docs/evidence/2026-10-22-live-e2e.md`).
* Related: ADR-0004 (the `mail` verb), ADR-0005 (subjects), ADR-0011 (directory addressing).
