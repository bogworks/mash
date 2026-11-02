---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# A publish violation is a definite refusal only when one send is waiting on the subject

## Context and Problem Statement

A publish the server refuses by permissions gets no reply: the only trace is a "Permissions Violation for Publish" error on
the connection, naming the subject. `mash send` registers for that subject and reports `refused by policy` when the error
arrives. The error cannot be attributed to one send, only to a subject, and every waiter on the subject was told. A delayed
violation from an earlier send, or two overlapping sends across a permission change, could therefore give a later send a
definite refusal while its delivery was in fact unknown (issue #182). The text also said `refused by policy` even when the
policy file allows the pair and the sender's credentials simply predate the rule.

## Decision Drivers

* A definite "refused" must be true: the message was not stored and a retry with the same credentials cannot succeed.
* When attribution is unclear the honest answer is the existing one, "may or may not have been delivered" (timeout).
* The sender cannot read the policy file, so the message cannot say which cause applies.

## Considered Options

* Keep signalling every waiter on the subject.
* Signal only when exactly one send is waiting on the subject; with several, signal none and flag the set ambiguous.
* Match each violation to a send by timing or sequence number.

## Decision Outcome

Chosen option: signal only when exactly one send is waiting, because it is the only case where the subject identifies the
send. With several waiting, none is signalled; the sends keep waiting for their ack or deadline and end as a send timeout.
That timeout says, with a clause, that a permission violation was seen while several sends were in flight and could not be
attributed. The flag lives as long as the waiter set and goes when it empties, so it never reaches a later send.

The refusal text keeps its first sentence and adds that, if the policy file allows the pair or lists the recipient at all,
the credentials may be older than the rule: the admin runs `mashctl policy drift`, then `mashctl reissue`.

### Consequences

* Good, because `refused by policy` is only said when it is attributable.
* Good, because the message points at the stale-credentials cause.
* Bad, because two overlapping sends to one denied subject now both wait for their timeout instead of failing fast.

### Confirmation

Tests in `internal/endpoint/refusal_test.go`: one waiter, several waiters, the flag clearing, a later sole send, the timeout
clause, and an allowed send acked after a stray violation.

## Pros and Cons of the Options

### Keep signalling every waiter on the subject

* Good, because concurrent sends to a denied subject fail fast.
* Bad, because a stray or delayed violation can label a send refused when its delivery is unknown.

### Signal only when exactly one send is waiting

* Good, because a refusal is only reported when the subject identifies the send.
* Bad, because overlapping sends to a denied subject time out instead of failing fast.

### Match each violation to a send

* Good, because it would be exact.
* Bad, because the server's error carries only the subject; nothing in it identifies the send.

## More Information

* Issue #182. Code: `internal/endpoint/refusal.go`, `internal/endpoint/mail.go`.
