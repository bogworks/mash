---
status: accepted
date: 2026-11-14
decision-makers: Randy Bias (mash maintainer); from the review of the REQ-015 ordering change, contract mash-messaging-policy draft-3
---

# An old credential the target template cannot expand is proven refused by its revocation

## Context and Problem Statement

ADR 0100 has the applied gate prove a removed reach refused by a probe credential minted with the sender's old name and
old tags under the new template. A target can add a template line that needs a tag the old credential lacks, such as a
new tag-driven deny line. The server cannot expand the target template for a user without that tag, so it refuses such
a credential at connect. A probe with the old name and tags then fails at connect, which proves nothing about the
reach. How does the gate show that such a sender's old credential is refused?

## Decision Drivers

* Any check the gate cannot make counts as failed, never as passed (ADR 0100).
* The gate must not rely on a server behaviour it does not check.
* The evidence must hold on every server.

## Considered Options

* Count every reach of such a sender as credential-only: require its old keys revoked on every server and probe the
  target credential (chosen).
* Count the refusal at connect as the evidence.
* Probe with the old name and tags anyway.

## Decision Outcome

Chosen option: "Count every reach as credential-only".

This departs from ADR 0100's rule to probe the old name and tags for a removed reach, for a sender the target template
cannot expand. The gate, in `internal/policyapply` (`NewExpect`), counts every reach of such a sender as
credential-only. It requires each of its old keys, from the old-key inventory, to be revoked on every server
(`old_key_not_revoked` otherwise), and it probes the target credential. The revocation is the evidence that the old
credential is refused. The server's refusal at connect is not relied on.

### Consequences

* Good, because the evidence is a revocation the gate checks on every server, not an untested connect refusal.
* Good, because a sender with no old-key inventory fails the gate, as ADR 0100 requires for any credential-only reach.
* Bad, because such a change needs every affected sender's old keys revoked, which REQ-014's rotation provides, before
  it can count as applied.

### Confirmation

`TestNewOrderAT014` in `internal/policyapply` adds a tag-driven deny line that the sender's old tags cannot expand,
and checks that every reach of that sender is credential-only. The gate's old-key tests (ADR 0100) fail a credential-only
sender whose old keys are not revoked on every server. AT-014 runs the case live and requires the old key revoked.

## Pros and Cons of the Options

### Credential-only, with revocation (chosen)

* Good, because it uses the same evidence as a tag-only narrowing (ADR 0100).
* Bad, because it forces a rotation for such a sender.

### The connect refusal as evidence

* Good, because it needs no rotation.
* Bad, because the gate would rely on a server behaviour it does not check, and a server that expanded the template
  differently would pass unseen.

### Probe the old name and tags anyway

* Bad, because the probe fails at connect, so a pass would mean nothing.

## More Information

* ADR 0100 (the applied gate), ADR 0102 (templates and tags).
* Contract mash-messaging-policy draft-3, REQ-011, REQ-014, REQ-015.
