---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# `mail` is a declared SAP verb, not a registered micro-service endpoint

## Context and Problem Statement

SAP agents are NATS micro services (NATS's request/reply service framework): each verb such as `prompt` or `status`
is normally a registered endpoint that subscribes to its subject and answers requests. SAP allows custom verbs (SAP
§2) and extra metadata keys (§3.2). mash adds a `mail` verb whose subject is captured by a JetStream stream, so a
publish to it is answered by JetStream with a storage acknowledgement. The question was how to make `mail` visible to
SAP tools without breaking that acknowledgement.

## Decision Drivers

* A sender must receive JetStream's publish acknowledgement, which is how it knows the mail was stored.
* SAP tools should be able to see that an agent accepts mail, and on which subject.
* No change to SAP itself.

## Considered Options

* Register `mail` as a micro-service endpoint
* Declare `mail` in the service metadata and the heartbeat, without registering it

## Decision Outcome

Chosen option: "Declare `mail` in the service metadata and the heartbeat", because "a registered endpoint would
subscribe to the mail subject and answer the JetStream publish acknowledgement's reply subject, which would break the
publisher's acknowledgement" (spec §3).

The declaration has two parts:

* The SAP service registration carries the metadata key `mail_subject` (extra keys are allowed by SAP §3.2).
* The signed heartbeat carries `endpoints.mail` = `{"subject": …, "metadata": {"durable": "true"}}` (unknown entries
  are tolerated by SAP §8.3).

### Consequences

* Good, because a publish to an inbox gets exactly one answer, JetStream's acknowledgement, so `send` reports success
  only after the mail is stored.
* Good, because SAP tools that read metadata or heartbeats can find the mail subject.
* Bad, because a SAP tool that only lists registered endpoints does not see `mail`.
* Neutral, because SAP's own SDKs add custom verbs the same way (for example `agents.spawn.…`), so the pattern is
  not specific to mash.

### Confirmation

* `internal/endpoint/service_test.go` `TestRegistrationMetadata`: the registration carries `mail_subject`, and the test
  fails if a `mail` endpoint is registered ("mail must be declared, not registered (it would answer the JetStream ack)").
* `internal/endpoint/service_test.go` `TestHeartbeatSigned`: the heartbeat's `endpoints.mail` names the mail subject.

## Pros and Cons of the Options

### Register `mail` as a micro-service endpoint

* Good, because every SAP tool that lists endpoints would see it.
* Bad, because the endpoint would answer the publisher's acknowledgement subject, so the publisher could no longer tell
  whether JetStream stored the message.

### Declare `mail` in metadata and the heartbeat

* Good, because JetStream remains the only responder on the mail subject.
* Good, because it uses SAP's own extension points.
* Bad, because discovery of the mail subject depends on tools reading metadata.

## More Information

* Design spec §3 ("mail is declared, not registered").
* Code: `internal/endpoint/service.go` (service metadata `mail_subject`; heartbeat `endpoints.mail` with
  `durable: "true"`).
* Related: ADR-0003 (mash is SAP plus one verb), ADR-0005 (subjects), ADR-0011 (senders find the mail subject in the
  directory, since the recipient may be offline).
