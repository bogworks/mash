---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# A directory poll reloads only when the directory stream moved, and is jittered

## Context and Problem Statement

Every running endpoint re-reads the whole directory every 15 seconds (`DirectoryPoll`, `internal/endpoint/directory.go`):
a stream-info request filtered by subject to list the keys, then one direct get per record
(`internal/endpoint/client.go`). Each endpoint does this, so the load on the NATS server grows with the square of the
endpoint count (issue #43). Heartbeats add a second quadratic term: every endpoint subscribes to every heartbeat.

It was measured on a three-server cluster with simulated endpoints doing exactly that request pattern
(`docs/evidence/2026-11-02-directory-polling/`): requests per second were 673 at 100 endpoints, 16,700 at 500 and 66,700
at 1,000, matching N(N+1)/15 to a fraction of a percent; the three servers together used 0.05, 0.77 and 3.2 cores, with
no failures or timeouts. Endpoints that start in the same second made each load slow and bursty (median 599 ms against
8 ms at 500 endpoints). The servers cope into the hundreds of endpoints; what scales badly is the request count itself,
which is what a metered hosted account would bill, and the reply volume of the filtered stream info (it lists every key).
The directory almost never changes: a record is written when an endpoint is provisioned, changed or retired.

## Decision Drivers

* The idle cost of the directory should not depend on the square of the endpoint count.
* The permissions of an endpoint stay as they are: it may read stream info and do direct gets on the directory, and may
  not create consumers on it (an ephemeral consumer could exhaust or delete another endpoint's resources).
* A changed directory must still reach every endpoint within the poll interval, and the read must never serve a
  directory older than it can notice.
* No new moving part: no cache server, no index service.

## Considered Options

* Keep the poll as it is.
* Jitter the interval and lengthen it, still reloading everything every time.
* A KV watch (an ephemeral consumer per endpoint).
* A core subscription to the directory's subjects for change events, with a slow poll as the safety net.
* Poll the stream's state cheaply and reload only when it moved, with jitter.

## Decision Outcome

Chosen option: poll the stream's state, and reload only when it moved. Each poll is one stream-info request with no
subject filter, whose reply is a few hundred bytes: it carries the stream's creation time, message count, last sequence
and last time. When none of them differs from what the endpoint last loaded, the poll ends there. When any differs,
the endpoint does today's full reload (the filtered stream info and the direct gets) and records the new state. A
reload is also forced on a slow schedule (every 20 minutes) whatever the state says, so a missed signal cannot leave a
stale directory for long. The interval is jittered: the first poll starts at a random offset inside the interval, and
each later wait is the interval plus or minus 25 percent, so endpoints that start together drift apart instead of
landing a wave of requests at once. The 15-second interval is kept, so a changed directory is noticed within about 19
seconds in the worst case.

With the numbers above, the idle request rate falls from N(N+1)/15 to N/15 per second: 67 instead of 66,700 at 1,000
endpoints, and 7 instead of 673 at 100. A directory change costs every endpoint one full reload inside one interval, the
same as one old poll, so a change at 1,000 endpoints is about 67,000 requests spread over 15 seconds, once.

A watch is not chosen: it needs a consumer on the directory stream, which the permission set deliberately withholds
([ADR 0007](0007-one-nats-user-per-endpoint-least-privilege.md)). A core subscription to the directory's subjects would
give pushed changes with no consumer, but it widens the permission set (a subscribe grant on the bucket's subjects) and
adds a second path to keep correct; it is left for a later record if a change must be seen faster than one interval.

Heartbeat fan-out (each endpoint receives every heartbeat: N squared over 30 seconds, 33,000 deliveries per second at
1,000 endpoints) is a separate cost and is not decided here.

### Consequences

* Good, because the idle directory cost is linear in the endpoint count and the server and the account meter see
  hundreds of requests, not tens of thousands, at 1,000 endpoints.
* Good, because the permission set, the record format and the in-memory directory are unchanged.
* Good, because jitter removes the synchronised-start wave measured at 500 endpoints.
* Bad, because a change is noticed within up to about 19 seconds rather than 15 (the jitter), and a directory read can
  now be as old as the forced 20-minute reload if the stream state is ever wrong; the state check compares four fields,
  including the creation time, so a stream recreated with the same sequence is caught.
* Bad, because every directory change makes all endpoints reload together (jittered over the interval): the cost of a
  change is unchanged, only the idle cost drops.
* The heartbeat fan-out is left as it is.

### Confirmation

* An endpoint test counts the requests an endpoint makes over an idle stretch of several polls (with a short test
  interval) and asserts one stream-info request per poll and no direct gets.
* An endpoint test writes a record, deletes one and recreates the stream, and asserts each is seen by the next poll (and
  that a poll after no change makes no direct gets).
* A test of the forced reload and of the jitter bounds (a fake clock or an injectable random source).
* `scripts/dirbench` is rerun against the change: requests per second at 100, 500 and 1,000 endpoints.

## Pros and Cons of the Options

### Keep the poll as it is

* Good, because it is the simplest and has worked at today's scale.
* Bad, because the request count grows with the square of the endpoint count, 66,700 per second at 1,000.

### Jitter and lengthen the interval

* Good, because it is a one-line change and removes the synchronised waves.
* Bad, because the quadratic growth stays, and a longer interval slows every change.

### A KV watch

* Good, because updates are pushed and an idle directory costs nothing.
* Bad, because it needs a consumer on the directory stream, which endpoints may not create.

### A core subscription for change events

* Good, because changes arrive at once with no consumer.
* Bad, because it widens the permission set and needs the slow poll as well, so there are two paths.

### Poll the stream state and reload only on change

* Good, because it keeps the permissions and the read path, and cuts the idle cost to one small request per poll.
* Bad, because a change makes everyone reload, and the freshness depends on four stream fields staying meaningful.

## More Information

* Issue #43; the measurement, method and limits: `docs/evidence/2026-11-02-directory-polling/README.md`; the harness:
  `scripts/dirbench`.
* Directory poll: `internal/endpoint/directory.go`; the key list and record reads: `internal/endpoint/client.go`.
* Heartbeat fan-out options for a later record: a presence summary written by one aggregator, heartbeats to a subject
  subset, or presence read from the lease bucket on demand.
* Implemented in `internal/endpoint/dirpoll.go` (`DirPoller`, `DirectoryFullReload`) and `pollDirectory`; `scripts/dirbench -mode new` runs the same `DirPoller`.
