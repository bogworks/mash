---
status: proposed
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# `mash serve` asks who is online when someone asks, instead of listening to every heartbeat

## Context and Problem Statement

Every `mash serve` endpoint publishes a signed heartbeat every 30 seconds and, as long as it runs, holds a standing
subscription to every other endpoint's heartbeat (`agents.hb.*.<owner>.*`,
`internal/endpoint/directory.go`, `watchPresence`). With N endpoints that is N squared over 30 deliveries per second:
33,333 deliveries per second and 10 MB per second of server output at 1,000 endpoints (measured,
`docs/evidence/2026-11-02-heartbeat-fanout/`). Since the directory poll was fixed
([ADR 0065](0065-a-directory-poll-reloads-only-when-the-directory-stream-moved-and-is-jittered.md)) this is the whole
of the servers' remaining load (0.52 cores of 16, three servers together). Each endpoint also verifies N/30 signatures
per second (33 per second at 1,000; computed, not measured).

What the standing subscription feeds is small: the last-seen times behind `Endpoint.Who()`, which is the `who` tool
inside a session and nothing else. The command-line `mash who` already does without it: it asks every agent for its
registration (a request to `$SRV.INFO.agents`, answered by every agent) and waits until the answers go quiet. Most
sessions never call `who`.

## Decision Drivers

* An endpoint that nobody asks about should cost the server nothing for presence.
* No change to the permission set and none to the protocol: SAP agents keep publishing their heartbeats, whoever
  listens (other SAP tools may).
* `who` stays correct and fast enough for a person or a model to wait for.
* The worst case (every endpoint asking all the time) must not cost more messages than today.

## Considered Options

* Keep the standing subscription.
* Lengthen the heartbeat interval.
* Drop the standing subscription; `who` asks on demand, with a short cache and a minimum gap between probes.
* A presence summary written by one aggregator.

## Decision Outcome

Chosen option: `mash serve` no longer holds the heartbeat subscription. `Endpoint.Who()` inside a session refreshes
presence on demand with the probe the command line already uses (a request to `$SRV.INFO.agents`; a registration counts
only when its identity signature verifies and its key is the one the directory has for that name, as today), waits for
the answers to go quiet (`PresenceQuiet`, 300 ms, capped at `PresenceCap`, 2 s), records them as seen now, and serves
the result from memory for 30 seconds: another `who` inside that time does not probe again. Online means answered the
last probe, which is more current than the 90-second window of a heartbeat. Heartbeats are still published every 30
seconds. The permission set is unchanged: publishing `$SRV.>` and reading one's own inbox prefix are already granted;
the heartbeat subscribe grant simply goes unused.

Measured on a three-server cluster with simulated endpoints (`scripts/dirbench -hb-mode`): at 1,000 endpoints dropping
the subscription takes server output from 33,657 to 258 messages per second, 10.2 to 0.14 MB per second, and server
CPU from 0.52 to 0.06 cores; a probe costs N replies. If every endpoint ran `who` every five minutes the cost would be
12,306 messages per second (37 percent of today's) and 0.175 cores; every minute it would be 61,099, more than today.
The 30-second minimum gap bounds the worst case at N squared over 30 replies per second, today's message rate. A
probe takes about 312 ms until quiet (300 ms of it the quiet window); a burst of 500 endpoints asking in the same
second received all 250,000 replies within 375 ms, with no loss.

### Consequences

* Good, because presence costs nothing while nobody asks: at 1,000 endpoints the servers carry 130 times fewer messages
  and no client verifies heartbeats.
* Good, because it needs no new permission and no protocol change, and the existing one-shot path is reused.
* Good, because `who` reports who answers now, not who was heard in the last 90 seconds.
* Bad, because `who` takes about a third of a second instead of reading memory, and each `who` costs N replies.
* Bad, because the `last seen` figure is the time of the last probe an answer came to, not the last heartbeat: an
  endpoint that was online and silent between probes shows as seen at the last probe.
* Bad, because an endpoint that only answers `$SRV.INFO` late (slower than the 2-second cap) can look offline for that
  `who`; the same holds for the command line today.
* Heartbeats keep going out, so a tool that listens to them is unaffected. A later record may lengthen their interval.

### Confirmation

* An endpoint test counts the messages an idle endpoint receives over several heartbeat intervals and asserts none:
  no standing subscription.
* An endpoint test calls `Who()` twice within the cache time and asserts one probe, then once after it and asserts a
  second; a test that an endpoint that answers is online and one that does not is offline.
* A test that a registration with a bad identity signature, or a key that is not the directory's, is not counted.
* `scripts/dirbench -hb-mode` is rerun against the change.

## Pros and Cons of the Options

### Keep the standing subscription

* Good, because `who` reads memory.
* Bad, because the cost is N squared whether or not anyone asks: 33,333 deliveries per second at 1,000 endpoints.

### Lengthen the heartbeat interval

* Good, because it is one constant.
* Bad, because the interval is part of an agent's registration metadata that other SAP tools read, the cost is still N
  squared, and online detection gets slower.

### Ask on demand with a cache and a minimum gap

* Good, because the idle cost is zero and the worst case equals today's message rate.
* Bad, because each `who` costs N replies and a third of a second.

### A presence aggregator

* Good, because it could answer `who` with one message.
* Bad, because it is a new component that must run and be trusted, and mash adds no service of its own.

## More Information

* The measurement, method and limits: `docs/evidence/2026-11-02-heartbeat-fanout/README.md`; harness `scripts/dirbench`.
* Code: `internal/endpoint/directory.go` (`watchPresence`, `Who`), `internal/endpoint/client.go` (`startPresence`,
  `awaitPresence`, `Refresh`), `cmd/mash/serve.go` (the `who` tool).
* Follows [ADR 0065](0065-a-directory-poll-reloads-only-when-the-directory-stream-moved-and-is-jittered.md) (#43).
