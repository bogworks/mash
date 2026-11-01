---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# One inbox stream and one durable pull consumer per endpoint

## Context and Problem Statement

Mail is stored in JetStream, NATS's persistence layer. A JetStream stream stores the messages published on the
subjects it captures; a consumer is a server-side cursor through which a client reads and acknowledges them. mash had
to decide how to lay out streams and consumers: one stream for all mail, or one per endpoint, and how an endpoint
reads its own.

## Decision Drivers

* An endpoint's mail history must be private to that endpoint.
* Each inbox should have its own limits, so one busy endpoint does not consume another's space.
* Retiring an endpoint should be simple.
* Mail must not be lost when a session is slow to acknowledge it.

## Considered Options

* One shared stream for all mail, with one consumer per endpoint (the design-era spike of 2026-10-21)
* One stream per endpoint, `inbox-<name>`, with one durable pull consumer `deliver`

## Decision Outcome

Chosen option: "One stream per endpoint, with one durable pull consumer", because "a per-endpoint stream keeps each
endpoint's history private to that endpoint, gives each inbox its own limits, and makes retiring an endpoint a single
delete" (spec §5.1). The spike's shared stream had shown that "any endpoint can read all history".

The consumer (`internal/store/store.go`, `ConsumerConfig`) is durable (it survives restarts on the server), pulled by
the endpoint, with explicit acknowledgement, an ack wait of 30 s, at most 1000 unacknowledged messages, unlimited
redelivery (MaxDeliver -1), and delivery from the start of the stream. The values 30 s and 1000 were chosen without a
recorded reason. Unlimited redelivery has been set since v0.1; delivery that holds mail and negatively acknowledges it
relies on it so held mail is never dropped (pull request #79).

### Consequences

* Good, because the permission set can grant each endpoint its own stream and nothing else (ADR-0007).
* Good, because each inbox's limits and retention are independent (ADR-0010).
* Good, because deprovisioning deletes one stream.
* Bad, because the number of streams grows with the number of endpoints; an account with a stream cap (a hosted free
  tier allows 5 streams, enough for 3 endpoints, `docs/synadia.md`) limits how many endpoints it can hold.
* Neutral, because there is no group inbox, queue consumer or broadcast; each message goes to one endpoint.

### Confirmation

* `internal/admin/admin_test.go` `TestProvisionCreatesInboxConsumerRecord`: the stream and the consumer's durable name,
  ack policy, ack wait, max ack pending and deliver policy.
* `internal/admin/admin_test.go` `TestDeprovisionRemovesAll`: deprovisioning removes the inbox.
* `internal/admin/admin_test.go` `TestEndpointPermissionDenials`: an endpoint cannot read or fetch from another inbox.
* `internal/endpoint/endpoint_test.go` `TestStoreAndForward`: mail sent while the recipient is offline is delivered
  later.
* No automated test checks MaxDeliver -1 in the provisioned consumer.

## Pros and Cons of the Options

### One shared stream, a consumer per endpoint

* Good, because there is one stream to manage, whatever the number of endpoints.
* Bad, because any endpoint able to read the stream can read all history, unless permissions are scoped per subject
  inside it.
* Bad, because one set of limits covers everyone, so one inbox's traffic evicts another's mail.

### One stream and one durable consumer per endpoint

* Good, because privacy, limits and removal follow the stream boundary.
* Bad, because accounts with stream caps hold fewer endpoints.

## More Information

* Design spec §5.1. Code: `internal/store/store.go` (`InboxConfig`, `ConsumerConfig`), `internal/sap/subjects.go`
  (`InboxStream`).
* Design-era spike: maintainer design discussion, 2026-10-21 (not public).
* Pull request #79 (held mail is negatively acknowledged and redelivered); pull request #130 made the number of
  streams per endpoint a parameter of the hosted-service setup.
* Related: ADR-0007 (permissions), ADR-0010 (inbox limits).
