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
* Also order earlier violations ahead of a send with a flush barrier after a send that ended in a timeout.

## Decision Outcome

Chosen option: signal only when exactly one send is waiting, because it is the only case where the subject identifies the
send. With several waiting, none is signalled; the sends keep waiting for their ack or deadline and end as a send timeout.
That timeout says, with a clause, that a permission violation was seen while several sends were in flight and could not be
attributed. One waiter still does not prove the violation is its own: a violation delayed from an earlier send that ended
in a timeout or cancellation can arrive after the next send registers. So a send that ends that way marks its subject dirty,
and the next send on a dirty subject runs a flush barrier before it registers. The connection is ordered, and the server
writes the -ERR for a publish before the PONG for a PING sent after it, so when the flush returns every earlier violation
has been delivered and dropped (no waiter yet). The barrier is bounded by the caller's context and 2 seconds; if it fails,
that send is unattributable: it never reports a definite refusal and keeps timeout semantics. A send that ended by ack or
by a definite refusal marks nothing. The flag lives as long as the waiter set and goes when it empties, so it never reaches a later send.

The refusal text keeps its first sentence and adds that, if the policy file allows the pair or lists the recipient at all,
the credentials may be older than the rule: the admin runs `mashctl policy drift`, then `mashctl reissue`.

### Consequences

* Good, because `refused by policy` is only said when it is attributable.
* Good, because the message points at the stale-credentials cause.
* Good, because a violation delayed from an earlier timed-out send cannot be reported as the current send's refusal, so a
  caller does not retry without its idempotency key and duplicate the message.
* Bad, because the next send after a timeout costs one extra round trip.
* Bad, because two overlapping sends to one denied subject now both wait for their timeout instead of failing fast.

### Confirmation

Tests in `internal/endpoint/refusal_test.go`: one waiter, several waiters, the flag clearing, a later sole send, the timeout
clause, an allowed send acked after a stray violation, a delayed violation delivered during the barrier not refusing the next send,
a failed barrier never giving a definite refusal, and settled sends (ack, refusal) not triggering a barrier.

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
* Limit: nats.go runs the connection's error handler on its own goroutine, so the flush barrier can return before the handler for a delayed violation has run. The barrier makes a false attribution require that lag; it does not rule it out. A caller that gets a refusal for a send it made with an idempotency key can still retry with the same key (the send tool prints it), which is at most once within the duplicate window.
