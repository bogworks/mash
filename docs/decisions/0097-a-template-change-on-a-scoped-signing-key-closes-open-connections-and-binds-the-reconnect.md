---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# A template change on a scoped signing key closes open connections, and the reconnect is bound by the new template

## Context and Problem Statement

REQ-012 asks whether narrowing a scoped signing key's permission template, then pushing the new account JWT, reaches a
client connection that is already open. It matters because the applied gate and every narrowing rest on it. A narrowing
that left open connections on the old template would leave a sender with a permission the policy had withdrawn, for as
long as that connection lived, and an endpoint's connection lives for days. The contract text assumes the NATS
documentation's reading, that a template change applies "with no creds re-issued", and a figure of about 0.5 s measured
for a zone key. Neither says whether the connection that is already open is the one that changes. Does it, how fast,
and what is lost on the way?

AT-011 measures it against a real nats-server (the version in `go.mod`, in process, operator mode, a full account
resolver, the kind mashctl pushes to) and records the result so that a server version that changes it fails a test.

## Decision Drivers

* A narrowing must take effect on connections that are open, not only on new ones.
* Mash must state only what a measurement shows, on the server version it ships against.
* A change in the server's behaviour must be caught before release, not found in use.
* The test must be fast, and its assertions must be outcome classes, not exact timings.

## Considered Options

* Measure it, record the outcome classes and the server version, and fail the test when either differs (chosen).
* Rely on the NATS documentation's wording.
* Measure it once and record it in prose only.

## Decision Outcome

Measured on nats-server 2.15.1 (nats.go 1.54.0), the sender under key K stays connected, an observer subscribes to the
subjects, and the new account JWT is pushed as mashctl pushes it: a request on `$SYS.REQ.CLAIMS.UPDATE` from a system
account user, to a full directory resolver, answered by the server.

**A template change does not apply to the connection that is open. The server closes that connection when it takes the
update, and the client's reconnect with the same credentials is bound by the new template.**

* The server closes the sender's connection at the push: the client sees an end of stream (`EOF`, now and then
  `connection reset by peer`) at 0 ms, with no error message and no permission violation first.
* The nats.go client, with reconnect enabled, reconnects with the same credentials and no re-issue. The reconnect took
  13 to 123 ms after the push in the runs measured, with a 10 ms reconnect wait.
* The first refused publish to the narrowed subject (or the first delivered publish to the widened one) came on the new
  connection, 23 to 123 ms after the push: the maximum over the measured runs was 123 ms, 123 ms again with eight busy
  loops running on a 16-core host under `-race`. Four scenarios were measured, a fixed template line removed
  (narrow) and added (widen), and a tag-driven line `{{tag(allow)}}` removed and added with the sender's JWT tags
  unchanged, and they behaved alike.
* The subject that the change did not touch stayed allowed throughout: a stream of one message every 2 ms (about 150 to
  200 messages across the transition) to a kept subject arrived in order, except that in a few runs one or two messages
  sent in the instant the server closed the socket were lost. Messages sent while the client was disconnected were held
  by the client and delivered after the reconnect. Loss is therefore possible for the messages in flight at the close,
  and nowhere else.
* A fresh connection opened right after the push was bound by the new template at once, in all four scenarios.

The measured delay depends on the client's reconnect wait, not on the server: the test's client waits 10 ms, and a mash
endpoint waits 2 s (`internal/endpoint`), so a mash endpoint should be expected to be bound by a change about 2 s after
the push, plus the reconnect itself. That figure is inferred from the wait, not measured. The about 0.5 s that mash
measured for a zone key is consistent with a reconnect that began soon after the push and with a short reconnect wait; it
does not show that a connection stayed open, and the claim that a template change applies to an open connection that
stays open is not what this server does.

The test, `TestAT011TemplateChangeReachesOpenConnections` in `internal/policy`, runs the four scenarios on every `go
test`. It asserts the outcome classes, not exact times:

* the change shows within a named bound, `at011Bound` of 3 s (measured maximum 123 ms), or the outcome is
  `not_applied`;
* whether it showed on the open connection (`open_connection`) or only after the server closed it and the client
  reconnected (`after_reconnect`), and the connection class (`kept`, `reconnected`, `closed`);
* no loss to a subject the change did not touch, except messages sent after the push and before the client reconnected;
* a fresh connection is bound by the new template.

The classes and the server version are recorded in `internal/policy/testdata/at011-result.json`. The test fails, with a
message that says so, when `server.VERSION` differs from the recorded version or the measured classes differ from the
recorded ones. The maintainer then runs `MASH_AT011_UPDATE=1 go test -run TestAT011 ./internal/policy/`, commits the new
record, and updates this record, or supersedes it with a new one, and REQ-012 and REQ-020 if the outcome changed.

### Consequences

* Good, because a narrowing is bound on every connection once the server has closed it and the client has reconnected,
  with no credentials re-issued and no action by the endpoint.
* Good, because the result is recorded per server version and guarded on every `go test`: a NATS upgrade that makes a
  change reach open connections without closing them, or stop reaching them at all, fails CI before release.
* Good, because a widening is bound the same way, so a withdrawn permission and a granted one share one mechanism.
* Bad, because a narrowing takes effect only when the client reconnects, so for a client that does not reconnect it
  does not take effect at all (the server closes it, so it is closed, not left on the old template). Mash may rely on
  the change binding a connection only for a client that reconnects with the same credentials, as the endpoint's does
  (`MaxReconnects(-1)`).
* Bad, because the window of the change is the client's reconnect wait (2 s for an endpoint), not the 123 ms measured
  with a short wait.
* Bad, because a message in flight at the close can be lost. Mash may not rely on exactly-once delivery across a push;
  mail is durable in the inbox stream and a publisher that needs the message to arrive confirms it.
* Mash may not claim that a template change applies to a connection that stays open, and may not claim a delay below
  the client's reconnect wait plus the reconnect.

### Confirmation

`TestAT011TemplateChangeReachesOpenConnections` is the confirmation. It runs four scenarios (fixed line narrowed,
fixed line widened, tag-driven line narrowed, tag-driven line widened) against an in-process nats-server and compares the
outcome classes and the server version with `internal/policy/testdata/at011-result.json`. It takes about 2 s and was run
20 times under `-race` with no failure, and 10 more times with eight busy loops running.

## Pros and Cons of the Options

### Measure it, record the outcome classes and the server version, fail on a difference (chosen)

* Good, because it states what is true of the shipped server and catches a change in it.
* Good, because it asserts classes and a generous bound, so it does not flake on a loaded host.
* Bad, because a server upgrade needs the record rerun and this record updated.

### Rely on the NATS documentation's wording

* Good, because it costs nothing.
* Bad, because "no creds re-issued" is true and silent about the connection that stays open, which is what the gate
  depends on, and the behaviour is the server's to change.

### Measure it once and record it in prose only

* Good, because it needs no test.
* Bad, because nothing notices when the server changes the behaviour.

## More Information

* Contract AT-011, requirements REQ-012 and REQ-020.
* The push is the path `mashctl policy apply` uses (`internal/apply`, `$SYS.REQ.CLAIMS.UPDATE`).
* Related: [ADR 0007](0007-one-nats-user-per-endpoint-least-privilege.md).
