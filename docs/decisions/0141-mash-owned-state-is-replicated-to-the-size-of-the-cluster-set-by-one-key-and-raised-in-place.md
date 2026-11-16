---
status: proposed
date: 2026-11-16
decision-makers: Randy Bias (mash maintainer)
---

# Mash-owned state is replicated to the size of the cluster, set by one key, and raised in place

Amends: ADR-0088

## Context and Problem Statement

Every stream and bucket mash owns is made with one replica (R1): the inbox, issue-record and adoption streams set it in
code, and the directory, lease and retired-name buckets leave it unset, which JetStream reads as 1. JetStream places each
R1 object on one node, so on a three-node cluster the objects spread over the nodes and every node holds a write path
mash needs. A live run of three nodes showed that any one node down stopped `add`, `reissue`, `audit` and `issued`.
Existing objects are never altered: the retired bucket "is never altered" ([ADR 0088](0088-a-retired-new-form-name-is-burned-in-a-durable-bucket-by-a-two-phase-marker-that-is-never-deleted.md)),
and an existing inbox that differs from the wanted shape is refused, replicas included, because changing an inbox could
discard stored mail. So a deployment could not be raised, and a raised one would be refused as drift. How many replicas
should mash-owned state have, who decides it, and how does an existing deployment get there?

## Decision Drivers

* One node of three down must not stop writes (a quorum of two is enough).
* A single server must stay as simple as it is, with no new setting to understand.
* One place decides the number, so no object is made with another count by accident.
* An existing deployment must be raised without losing a message, a key or a lease, and without a rebuild.
* Nothing about the protocol changes; no layer above mash is named or assumed.

## Considered Options

* One `replicas` key (`auto`, `1`, `3`), decided from the declared server count, and an in-place raise (chosen).
* Always three replicas.
* A replica count per object kind.
* Leave R1 and document that one node down stops writes.

## Decision Outcome

Chosen: the first option.

1. The setting `replicas` is `auto` (default), `1` or `3` (`config.json` `"replicas"`, `MASH_REPLICAS`, and `--replicas N` on `init` and `adopt`). `auto` is 3 when the declared server count (`servers`, `MASH_SERVERS`) is 3 or more, else 1. `3` on fewer than three declared servers is refused, because it could not be placed. Every other value is refused naming `auto`, `1` and `3`; 5 is allowed by NATS but is not tested here and is not offered.
2. `store.Replicas(setting, servers)` is the only place the number is decided. Every config function (directory, lease, retired names, issue record, adoption, inbox) takes the count; the unset field and the fixed `1` go. A solo server, and `init --local`, stay at one replica.
3. `adopt` writes `servers` (the servers that answered the system account ping) and `replicas` into the config it writes, and creates what it creates at that count. `init` uses the configuration. `init` never changes an existing object's replica count.
4. `store.RaiseReplicas` raises an existing deployment in place, while it serves: directory, lease, issue record, adoption, retired names, then every inbox. For each it reads the stream's configuration, skips an object already at the target, and otherwise updates the stream with only `Replicas` changed (every other field written back as read), then waits, bounded and with the lag shown, until every replica is current. It writes no key, keeps rollup off on the lease bucket, is idempotent and resumable, never lowers, refuses a target above the declared server count, and changes nothing when the cluster cannot place the replicas. The command name is decided separately.
5. This amends [ADR 0088](0088-a-retired-new-form-name-is-burned-in-a-durable-bucket-by-a-two-phase-marker-that-is-never-deleted.md): the retired bucket's replica count, and only that, may be raised, by the raise above; no marker is written or removed. It also relaxes the rule that an existing inbox is never changed, for the replica count only.
6. The checks accept a raised count: an inbox, the retired bucket, the issue record and the adoption bucket may have more replicas than wanted and never fewer. Nothing else about them changes.
7. `audit` and `issued check` give one finding when the setting wants 3 and an object has fewer ("raise the replica count of mash-owned state to 3"), and none when `auto` resolves to 1.

### Consequences

* Good, because one node of three down no longer stops directory, lease, issue-record and inbox writes after a leader election.
* Good, because a single server is unchanged, and an existing deployment is raised without a rebuild or a lost message.
* Bad, because storage is three times what R1 used, per object, on the nodes that hold them.
* Bad, because the write gate of `retire`, `reissue` and `add` wanted every declared server to answer when this was decided, so working through an outage meant lowering `MASH_SERVERS`, and `auto` then resolved to the lower count. [ADR 0142](0142-a-revocation-write-needs-a-quorum-of-the-declared-servers-and-a-server-that-missed-it-takes-it-from-the-resolver.md) replaces that gate with a write quorum.
* Neutral, because a raise needs every node up and with room, and says so before it changes anything.

### Confirmation

* A live test on three routed nodes: objects made R3 by the normal path, one node stopped, then `add`, a send and receive, and `retire` work (with the declared count lowered to the nodes up, the gate above), the node returns and catches up and `audit` is clean.
* A live test that starts at R1, writes a directory record, an issue record, a lease and mail, raises to R3 while a sender runs, and checks the message count, the lease, that rollup stays refused, that every object is R3 and current, and that the retired bucket keeps its markers.
* Unit tests for `store.Replicas`, each config function at R1 and R3, `RaiseReplicas` (idempotent, resumable, refuses above the server count, never lowers, changes only the replica count) and each check (accepts more, refuses fewer).
* The standing-order guard test (`TestNoWordOfALayerAboveInShippedCodeAndDocs`) passes.

## Pros and Cons of the Options

### One `replicas` key and an in-place raise (chosen)

* Good, because the solo case needs no setting and the cluster case needs the declared count mash already asks for.
* Good, because one function decides the number and one function raises it.
* Bad, because the raise is a cluster operation: every node must be up and have room.

### Always three replicas

* Good, because there is no setting.
* Bad, because a single server cannot place three replicas, so the solo case would break.

### A replica count per object kind

* Good, because an operator could keep the large inboxes at one replica.
* Bad, because it adds a setting per kind to get wrong, and the directory and lease must be replicated for any write to survive a node down.

### Leave R1

* Good, because nothing changes.
* Bad, because one node down stops every write path, which is the problem.

## More Information

* Design note: `docs/plans/2026-11-16-replicas-for-mash-owned-state.md`.
* [ADR 0088](0088-a-retired-new-form-name-is-burned-in-a-durable-bucket-by-a-two-phase-marker-that-is-never-deleted.md), [ADR 0031](0031-send-at-most-once-with-an-idempotency-key.md).
