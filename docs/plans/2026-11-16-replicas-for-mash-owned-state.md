# Replicas for mash-owned buckets and streams (design note)

Lane core, 2026-11-16. Source: spike 10 (a 3-node cluster, a central `mashctl`): every mash-owned bucket and stream is R1, so with any one node down every write fails (`add`, `reissue`, `audit`, `issued`). Docs only; no code yet.

## Facts (read in the code)

- Every mash-owned JetStream object is created R1: the inbox, issue-record and adoption streams set `Replicas: 1` in code, and the directory, lease and retired bucket configs leave it unset, which JetStream reads as 1: the directory (`store.DirectoryConfigFor`), the lease bucket (`store.LeaseConfigFor`, hardened in `lease_harden.go`, which already passes `cfg.Replicas` through), the retired-names bucket (`store.RetiredConfig`, ADR 0088), the issue record (`store.IssuedConfig`, `issued.go`), the adoption bucket (`adoption.go`), the policy ledger (`mash-policy`, #676), and each endpoint's inbox stream (`store.inboxStreamConfig`). The durable consumer config sets no replica count (it follows its stream).
- JetStream places each R1 object on one node, so a 3-node cluster spreads them (leases and retired on one node, the directory on another, the issue record and an inbox on a third) and loses a write path per node down.
- mash already knows the server count: `MASH_SERVERS` (declared; `adopt` counts the servers that answered the system ping, and every cluster verb checks it). Replica counts do not need a new probe.
- Existing objects are never altered: `Init` keeps a larger `MaxBytes` and updates the rest; the retired bucket "is never altered" (ADR 0088); an existing inbox stream that differs from the wanted shape is refused by `checkInboxStream`, replicas included ("mash does not change an existing inbox, because that could discard stored mail"). So a deployment cannot be raised today, and a raised one would be refused as drift.
- JetStream can raise a stream's replica count in place (a stream update, 1 to 3) while it serves, without losing messages; the new replicas catch up and the stream info reports each replica's lag. Lowering is also possible but is not needed here.

## Decision

1. **One setting, `replicas`.** Config key `replicas` (env `MASH_REPLICAS`, and a `--replicas N` flag on `init` and `adopt`), values `auto` (default), `1` or `3`. `auto` means 3 when the declared server count is 3 or more, else 1. A value of 3 on a declared count below 3 is refused (it could not place). Other numbers are refused: 1 and 3 are the supported shapes (5 is allowed by NATS but untested here and left out).
2. **One function.** `store.Replicas(setting string, servers int) (int, error)` is the only place the number is decided; every `*ConfigFor` takes it as a parameter and the fixed value (or the unset field) goes. The solo single-server default stays R1 (`servers = 1` gives 1).
3. **`init` and `adopt`.** `init --local` creates a one-server setup: R1, as today. `adopt` of a cluster reads the count it already finds, writes `servers` and `replicas` into the config it writes, and creates the buckets at that count. A plain `init` against an existing cluster uses `MASH_SERVERS`.
4. **Raising an existing deployment, live.** One engine call `store.RaiseReplicas(ctx, js, target)` and a `mashctl` face (name is ctl's): for each mash-owned object it reads the stream's current replica count and, below the target, updates the stream's `Replicas`, then waits until every replica reports current (bounded, with the lag shown) before the next object. Order: directory, lease, issue record, adoption, policy ledger, retired, then inbox streams. It is idempotent and resumable (an object already at the target is skipped), changes nothing else (every other field of the config is read back and written unchanged), refuses a target above the declared server count, and never lowers. Each step prints the object and its replica state; a partial run is safe because R1 and R3 objects can coexist. The lease bucket goes through its stream update with rollup kept off, as `initLease` does.
5. **Drift checks accept a raised count.** `checkInboxStream`, `VerifyRetiredStatus` and the issue/adoption verifiers compare `replicas` to the wanted count and accept a larger one that was raised on purpose (never smaller than wanted). Nothing else about them changes; the retired bucket is still never altered except by the raise, which writes no key.
6. **Reporting.** `audit` and `issued check` gain one finding when `replicas` wants 3 and an object is R1 ("raise it"), and the wording of the down-node refusals (spike 10's messages) names the server holding the object: that is a separate small change, listed here so one note covers the cluster story.
7. **Docs.** The replicas key and what R1 and R3 mean for a down node are documented here; the "Several servers" section itself is rewritten in the cluster-setup change, and this change follows it once that merges.

## What R3 does and does not give

- One of three nodes down: writes keep working after leader election (seconds). Two down: no quorum, writes fail as an outage, which is correct.
- A send in flight at election time may time out; the idempotency key (ADR 0031) makes the retry safe.
- Storage is three times what R1 used. The limits (`InboxMaxBytes` and the bucket caps) stay per object, so a node holds up to three copies of what it did.
- Raising is a cluster operation: all three nodes must be up and have room. The call checks first and changes nothing if a node is missing.

## Risks

- A raise during an outage of the one node that holds an R1 object cannot proceed (the object is unavailable); it reports the object and waits for the node.
- Lowering the count by hand outside mash is not detected as drift when it equals the wanted count.
- The lease bucket is the most sensitive object; the raise reuses its hardened update path and the live test must check rollup stays refused.

## Live proof (after the maintainer's go)

Three real nodes, `adopt`, R3 objects; stop one node; `add`, a send and `retire` still work and `audit` is clean; bring it back and everything catches up. A second run starts R1, writes a directory record, an issue record, a lease and mail, raises to R3 while a sender is running, and loses nothing (message count and the lease unchanged, rollup still refused).

## ADR

One ADR is needed: mash-owned state is replicated to the cluster's size (R3 at three nodes or more, R1 solo), set by one key, and raised in place. It amends ADR 0088 (the retired bucket's replica count may be raised) and the inbox rule of "never change an existing inbox" (replicas only).
