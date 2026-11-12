---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# AT-011 on nats-server 2.15.2 gives the same result as 2.15.1

Amends: ADR-0097

## Context and Problem Statement

[ADR 0097](0097-a-template-change-on-a-scoped-signing-key-closes-open-connections-and-binds-the-reconnect.md) records
what a template change on a scoped signing key does to a connection that is open, measured on nats-server 2.15.1, and
`TestAT011TemplateChangeReachesOpenConnections` guards the record: it fails when `server.VERSION` or the measured
outcome classes differ from `internal/policy/testdata/at011-result.json`. The dependency bump to nats-server 2.15.2
(with nats.go 1.55.0) made the test fail on the version. Did the behaviour change?

## Decision Drivers

* Mash may rely only on what a measurement shows on the server version it ships against.
* The guard exists so that a server change is looked at, not waved through.
* ADR 0097 is accepted and is not rewritten; a later measurement is a new record that amends it.

## Considered Options

* Measure on 2.15.2, and when the outcome classes are unchanged, update the record and amend ADR 0097 with this ADR
  (chosen).
* Change the test to ignore the version.
* Leave the record on 2.15.1 and the test failing.

## Decision Outcome

Measured on nats-server 2.15.2 (nats.go 1.55.0), the outcome is the same as on 2.15.1 in all four scenarios (a fixed
template line narrowed and widened, a tag-driven line narrowed and widened):

* The server closes the sender's connection at the push (`EOF`, 0 ms), the client reconnects with the same
  credentials, the new template binds the new connection, and a fresh connection is bound at once.
* The `applied` class is `after_reconnect`, the connection class is `reconnected`, a subject the change did not touch
  lost nothing outside the disconnect window, and a fresh connection is `changed`, in every run.
* The test failed only on the recorded version, not on the outcome classes, before the record was updated.

| Measure (test client with a 10 ms reconnect wait) | nats-server 2.15.1 (ADR 0097) | nats-server 2.15.2 |
| --- | --- | --- |
| Reconnect after the push | 13 to 123 ms | 13 to 112 ms |
| First effect on the new connection | 23 to 123 ms | 24 to 121 ms (123 ms with 12 busy loops under `-race`) |
| Runs | 20 under `-race`, 10 with eight busy loops | 20 under `-race`, 10 with 12 busy loops |
| Outcome classes that differed | none | none |
| Messages lost outside the disconnect | none | none |

The delays are of the same size and depend on the client's reconnect wait, not on the server, as in ADR 0097. In a few
runs one message sent in the instant the server closed the socket was lost, as before.

The guard record `internal/policy/testdata/at011-result.json` now names 2.15.2. Every statement of ADR 0097 holds on
2.15.2, and REQ-012 and REQ-020 are unchanged.

**On each change of the nats-server version, the record is measured again and updated, and an ADR that amends the
last one is written**, whether or not the outcome changed. The test's failure on the version is the prompt for it.

### Consequences

* Good, because the shipped server's behaviour is recorded, and ADR 0097 is left as it was.
* Good, because the guard stays on: a later change in the server's behaviour still fails the test.
* Bad, because each nats-server bump costs a measurement and a short ADR.

### Confirmation

`TestAT011TemplateChangeReachesOpenConnections` passes against `testdata/at011-result.json` on 2.15.2. It was run 20
times under `-race` and 10 times with 12 busy loops running; every run measured the same outcome classes.

## Pros and Cons of the Options

### Measure on 2.15.2, update the record, amend ADR 0097 (chosen)

* Good, because it keeps one record per server version and the history in ADRs.
* Bad, because it is a step on every bump.

### Change the test to ignore the version

* Good, because a bump would not need a step.
* Bad, because the outcome classes would then be the only guard, and a change that the classes do not show would pass
  unseen.

### Leave the record on 2.15.1 and the test failing

* Good, because it needs no work.
* Bad, because CI stays red.

## More Information

* [ADR 0097](0097-a-template-change-on-a-scoped-signing-key-closes-open-connections-and-binds-the-reconnect.md), which
  this record amends; its file is unchanged.
* Contract AT-011, requirements REQ-012 and REQ-020.
