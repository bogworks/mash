---
status: proposed
date: 2026-11-16
decision-makers: Randy Bias (mash maintainer)
---

# A revocation write needs a quorum of the declared servers, and a server that missed it takes it from the resolver

Amends: ADR-0067

## Context and Problem Statement

`mashctl add`, `reissue` and `retire` read the account JWT from the servers, and `retire` and a rotating `reissue` push a
revision of it that carries a revocation. Each of those steps asked that every declared server answer: the server set
had to be complete (`ServerSet.Complete`), every expected server had to answer the claims lookup, and every one had to
acknowledge the push ([ADR 0067](0067-a-reissue-that-narrows-an-endpoint-rotates-its-user-key-and-revokes-the-old-one.md)
records the read for `reissue`; `RevokeUsers` and `KeyRevoked` apply it to the others). `add` and `reissue` also read the
account JWT once more, before they minted anything, to refuse while a zone policy of an older release was applied.

That is fail-closed, and it is also an outage. Spike 10 ran three nodes with `MASH_SERVERS=3` and replicas 3, so that
every stream and bucket survives one node down ([ADR 0141](0141-mash-owned-state-is-replicated-to-the-size-of-the-cluster-set-by-one-key-and-raised-in-place.md)),
and stopped one node: `add`, `reissue` and `retire` were all refused, because one declared server did not answer. The
only way through was to lower `MASH_SERVERS` to 2, which also changes what `replicas auto` means for objects made while
it is lowered. Spike 7 had shown that the other half already works: a revocation pushed to the servers that are up
reaches the server that was down once it rejoins, because every server's resolver syncs the account JWTs with its peers
and keeps the newer one.

How many servers must answer for these verbs to read or change the account JWT, without letting a revocation be lost?

## Decision Drivers

* One node of three down must not stop `add`, `reissue` or `retire`.
* No acknowledged revocation may be lost, and a revoked credential must never be accepted again because a read missed the
  copy that holds the revocation.
* A server that did not answer must stay visible until it holds the revocation, never be ignored.
* Every check that is not about completeness stays: the JWT verifies against the operator, the signing keys agree, no
  gateways or conflicting clusters, the size cap.
* A single server and a pair stay exactly as strict as they are.
* Nothing about the protocol changes; no layer above mash is named or assumed.

## Considered Options

* Every declared server must answer (the status quo).
* A quorum of the declared servers (chosen).
* Best effort: one answering server is enough.

## Decision Outcome

Chosen: a quorum.

1. **The setting.** `write_quorum` (`config.json` `"write_quorum"`, `MASH_WRITE_QUORUM`) is `auto` (default) or a positive whole number. `auto` is a majority of the declared servers (`servers` / `MASH_SERVERS`), `declared/2 + 1`, when 3 or more are declared, and every server otherwise (1 of 1, 2 of 2). `config.WriteQuorum` is the only place the number is decided. A number above the declared count is refused, since no write could reach it. With 3 or more declared servers a number below a majority is refused, since a quorum of 1 of 3 would let two disjoint groups each take a write and miss the other's. A number needs the server count declared.
2. **What it governs.** `add`, `reissue` and `retire`, wherever they read the account JWT or push a claims update: the revocation lookup of `reissue`, and the lookup, the push and the read-back of a revocation (`retire`, a rotating `reissue`, `mashctl revoke`, `deprovision`). They need `quorum` answering servers instead of all.
3. **Reads.** The base of a write is the newest verified copy (highest issued-at) among the answers, as it already was. A quorum read overlaps every quorum that acknowledged a write, so the newest copy among the answers holds every acknowledged revocation. A key is revoked if any answering copy names it.
4. **Writes.** A push stands when at least `quorum` distinct known servers acknowledged it with code 200 and the answering servers hold the pushed JWT on read-back. A server that answers with any other code is still a failure.
5. **The declared count is the denominator.** A server that did not answer is pending, never dropped from the count; the cluster reporting more servers than are declared refuses, because the majority would no longer overlap.
6. **`retire` and the pending servers.** The revocation reaches the servers that answer and a server that is down gets it from its resolver when it rejoins. `retire` exits 0 when the quorum acknowledged, prints one line `pending_servers: ...` that says which servers have not acknowledged and that they converge when they rejoin, and prints how many hold the revocation. Below the quorum it refuses as before: nothing is revoked or removed, and the message says how many answered of how many, what the quorum is and that a server may be down.
7. **`audit` shows it until it is true.** A key the issue record (`revoked_at`) or the retired-name bucket marks revoked that some server's account JWT does not list as revoked is a `revocation_pending` finding, as is a server that did not answer while such a key exists. It is derived on each run from those records and the servers' copies; nothing is stored, so it clears when every server holds the revocation. It counts for `--strict`. A key that no server lists as revoked (retired with `--no-revoke`) is not pending. `audit` checks only with the system credentials and the key store; without them it says nothing.
8. **The zone check of `add` and `reissue` is removed.** Zones are gone ([ADR 0091](0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md)); the check read the account JWT from every server for a scoped signing key and was the only reason `add` read it at all. `unzone` keeps its own check, unchanged, and shares `zoneGuard` and `apply.RefuseZonePolicy` as before. What `add` and `reissue` no longer have from it: `add` needs no system credentials (`reissue` still does, to read revocations), neither fails when an older release's zone policy is in the account JWT, and neither reads the JWT to find a scoped signing key (the revocation push still refuses a signing key mashctl did not create).

### What was fail-closed before

Every write needed every server: the complete server set, every server answering the lookup, every server acknowledging the push, and every server holding the pushed JWT on read-back. One server down was a refusal of `add`, `reissue` and `retire`, and a refusal that told the operator to lower `MASH_SERVERS`.

### What is still fail-closed

* Below the quorum: nothing is pushed, nothing is revoked and nothing is removed.
* Trust and shape: every copy must verify against the operator; two different copies with the same issued-at are refused as ambiguous; a signing key mashctl did not create refuses the revocation push; gateways, conflicting clusters and a cluster larger than declared refuse; the account JWT size cap holds.
* A server that refuses the push with any code other than 200.
* The policy apply gate ([ADR 0100](0100-the-applied-gate-proves-old-credentials-refused-by-probe-and-revocation.md), [ADR 0113](0113-an-old-credential-the-target-template-cannot-expand-is-proven-refused-by-its-revocation.md)) and the issue service keep asking every server: they prove a state, not take a write, and they are not changed.
* `unzone`, which still refuses while a zone policy is applied or the servers cannot be asked.

### What now converges

A server that was down when a revocation was pushed takes the account JWT from the account resolver when it rejoins: each server's resolver asks its peers for the account JWTs on a timer (a minute by default) and keeps the newer one. Until then it still accepts the revoked credential: the window is the time to its next sync, and the servers that answered refuse the credential at once. `retire` says which servers are pending and `audit` reports `revocation_pending` until they hold it, so the window is visible and ends by itself. A server that is up but unreachable from where mashctl runs is in the same state and is reported the same way.

### Consequences

* Good, because one node of three down no longer stops `add`, `reissue` or `retire`, with `MASH_SERVERS` still 3.
* Good, because no acknowledged revocation is lost: a quorum read always includes a server that holds it.
* Good, because a revocation that has not reached a server is reported until it has.
* Bad, because a server that missed a revocation accepts the revoked credential until its next resolver sync after it rejoins, or for as long as it is partitioned. `pending_servers` and `revocation_pending` name the exposure; bringing the server back, or taking it out of service, closes it.
* Bad, because the account claims lookup does not say which server replied, so a pending server is shown by count and as "a server that did not answer" unless the ping saw it by name.
* Neutral, because an operator who wants the old behaviour sets `write_quorum` to the declared count.

### Confirmation

* A live test on three routed nodes with `MASH_SERVERS=3`, replicas auto and `write_quorum` auto: one node stopped, then `add`, a send and receive, `reissue` and `retire` succeed; `retire` reports the stopped node pending; `audit` reports `revocation_pending`; the node is restarted, takes the revocation, refuses the retired credential, and `audit` is clean.
* A live test with two nodes down: the real verbs change nothing, and the revocation asked of the node that is left is refused with the quorum message before anything is pushed.
* Unit tests for the quorum value (parsing, the refusals, `auto` at 1, 2, 3 and 5 servers), the server set check, the newest copy among a quorum of answers, acknowledgement counting and the pending list, `retire`'s pending line and exit status, the `audit` finding (present with a lagging server, gone when all hold it), and that `unzone` still refuses.
* The standing-order guard test (`TestNoWordOfALayerAboveInShippedCodeAndDocs`) passes.

## Pros and Cons of the Options

### Every declared server must answer (the status quo)

* Good, because a write is never made from a copy a silent server might have improved on, and a revocation is on every server before the command returns.
* Bad, because one node down stops `add`, `reissue` and `retire`, which defeats the replicated state of ADR 0141; the only way through is to lie about the server count.

### A quorum of the declared servers (chosen)

* Good, because any two quorums share a server, so a read always sees the newest acknowledged write and no revocation is lost.
* Good, because it needs no new mechanism: the resolver's own sync brings a returning server up to date.
* Good, because pending servers stay in the count and are reported until they hold the write.
* Bad, because a pending server accepts a revoked credential until it syncs.

### Best effort: one answering server is enough

* Good, because it never refuses while any server is up.
* Bad, because two groups of servers that cannot see each other can each accept a write, and a later read from one group misses the other's revocation: a revoked key can be re-signed or its revocation overwritten. That is split-brain, and the refusal of a minority quorum exists to prevent it.

## More Information

* Spike 7 (revocations converge across servers) and spike 10 (one node down blocked `add`, `reissue` and `retire`).
* [ADR 0141](0141-mash-owned-state-is-replicated-to-the-size-of-the-cluster-set-by-one-key-and-raised-in-place.md) noted the write gate as outside its decision; this is that decision.
* Amends [ADR 0067](0067-a-reissue-that-narrows-an-endpoint-rotates-its-user-key-and-revokes-the-old-one.md): its revocation read no longer needs every server, only a quorum. ADR 0059 is not amended: it concerns the issuer, the signing keys and TLS, which are unchanged here. ADR 0091 is not amended: it names `serve`, `apply` and `unzone` as the commands that refuse on a zone policy.
