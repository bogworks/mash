---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer); the probe approach was approved by the mash coordinator and agreed by the planner for contract mash-messaging-policy draft-3, REQ-011
---

# The applied gate proves old credentials refused by a probe credential and by revocation

## Context and Problem Statement

Contract mash-messaging-policy REQ-011 counts a list set generation as applied only when every server has taken the
push, every revoked key is refused on every server, and a send to a removed receiver is refused on every server, both
with each old credential and from a connection that was open before the change. AT-011 (ADR 0097) showed that a
template push closes every connection under the changed scoped key, and that a client that reconnects is bound by the
new template. mash never holds a sender's private key: the key pair is made on the sender's host (D201). So mash cannot
send with a sender's real old credential. How does the gate show that the old credential is refused?

## Decision Drivers

* The gate must not need a sender's seed, and must not create a credential that outlives it.
* A probe must never reach more than the sender's own credential could.
* The evidence must hold on every server, not only the one mash talks to.
* Any check the gate cannot make counts as failed, never as passed.

## Considered Options

* A probe credential minted by mash with the sender's old name and tags, plus every server's account JWT revoking the
  old key (chosen).
* Ask each sender's host to send a test message with its real old credential.
* Count the push acknowledgements alone as proof.

## Decision Outcome

Chosen option: "A probe credential minted by mash, plus revocation".

The permissions of a user signed by an owner group's scoped key come only from the key's template, expanded with the
user's name and tags (ADR 0102). A user that mash mints under the same scoped key with the sender's old name and old
tags therefore has exactly the permissions the sender's old credential has under the new template. The gate, in
`internal/policyapply`, checks in this order and reports every failure, not only the first:

1. The server set is complete (`internal/apply`'s rules).
2. Every server holds the target generation. A server's generation is read from its account JWT by content: the
   templates on its scoped keys, the public keys of those scoped keys and its revocations match the target or the
   previous generation, or it is unknown. The caller passes the expected scoped signing key of each owner group; the
   JWT must hold exactly those and no other signing key, scoped or plain, so a JWT with another or an extra signer is
   never read as a generation. The gate fails closed (a `generation` failure, "no expected signing keys") when the
   target has none.
3. Every revoked key is revoked in every server's account JWT. This is the evidence that the real old key is refused.
   The caller gives an inventory of each sender's old keys (credentials issued before the change, from the issue
   record). For every sender with a tag-only narrowing, and every sender the change removes, each old key must be among
   the revoked keys, else the gate fails with `old_key_not_revoked` naming the sender and key. A sender with no
   inventory entry fails too: the inventory is required, so a narrowing cannot count as applied while the real old
   credential still reaches the removed receiver.
4. Every connection opened before the push (a witness) was closed by the server.
5. On every server, a reconnecting probe with the old name and tags is refused each receiver the change removed from
   it. When the removal is by tags only, the new template still grants the old tags that receiver, so the old
   credential is refused only by its revocation (step 3, which REQ-014's rotation provides), and the gate probes the
   sender's new credential instead.

So "a send with each old credential is refused" is met by the probe plus the revocation, because mash holds no sender
seed (D201).

Probes follow these rules: each has a fresh key pair and an expiry of minutes; it carries no tag the list set does not
give that sender; it is recorded in the issue record (REQ-018) with purpose `probe`; and it is revoked when the gate
ends, pass or fail.

When servers hold different generations, mash pushes the target again, and if that does not bring every server to it,
pushes the previous generation again. Only then does it report a policy in force. A generation is committed as the one
in force only after the gate passes, with a compare-and-set on its base, so of two submissions on one base exactly one
wins.

When the target JWT was pushed and the gate does not pass, but not every server is back on the previous generation (for
example every server holds the target and a probe or witness check fails), the target is recorded as a pending
generation in the ledger. While one is pending, a submission with different content is refused with
`ErrPendingGeneration` before anything is signed or pushed, so the same base cannot be accepted while the failed change
is visible. The operator resolves it by resubmitting the same list set: it is pushed and gated again, and when applied
it is committed and the pending record cleared. Rollback is by Converge, which clears the record when every server is
back on the previous generation.

The ledger's lock is mandatory. A run holds it across reading the record in force, accepting, signing, pushing, gating
and committing, and reads the record in force after taking it. A persistent ledger MUST implement it as a cross-process
lock (a lease in the shared store); the in-memory ledger's lock is per process. A run also refuses, before any push, a
signed account JWT larger than `policy.AccountJWTLimit` (1 MiB), naming its size and the limit.

### Consequences

* Good, because the gate needs no sender seed and works on every server.
* Good, because a probe never holds more than the sender's own credential.
* Bad, because each gate run mints and revokes short-lived credentials, which adds revocations to the account JWT.
* Bad, because a generation is read from the JWT by content: two generations with equal templates are told apart only
  by their revocations.
* Bad, because a server reports an account's JWT only once the account is loaded there (a connection under it). A server
  where it is not loaded reads as an unknown generation and fails the gate; the witnesses, opened first, load it.
* Neutral, because the in-memory ledger is per process; the persistent compare-and-set store follows the homerange-mash
  seam's status key.

### Confirmation

Unit tests in `internal/policyapply` run the gate against fake servers: applied on two servers, a server held back
(partial, named), convergence by re-push and by pushing the previous generation, a missing revocation, an open witness,
a removed receiver still reached, a tag-only narrowing with and without its old keys revoked, a JWT with another or an
extra signer, a pending generation, an oversized JWT, and two concurrent runs on one base. AT-010 runs it against a
live two-server cluster.

## Pros and Cons of the Options

### A probe credential plus revocation (chosen)

* Good, because it needs nothing from the sender's host.
* Bad, because the real old key is shown refused by the account JWT, not by a live send.

### A test send from each sender's host

* Good, because it uses the real credential.
* Bad, because it needs every host to answer, and a host that is down blocks the gate.

### Push acknowledgements alone

* Good, because it is cheap.
* Bad, because an acknowledgement does not show that a send is refused, which REQ-011 asks for.

## More Information

* Contract mash-messaging-policy draft-3, REQ-011, REQ-012, REQ-014, AT-010, AT-011.
* ADR 0102 (templates and tags), ADR 0097 (AT-011's result), ADR 0098 (generations).
