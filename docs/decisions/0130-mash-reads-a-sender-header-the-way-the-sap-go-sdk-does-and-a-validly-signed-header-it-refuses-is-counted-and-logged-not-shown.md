---
status: proposed
date: 2026-11-15
decision-makers: Randy Bias (mash maintainer)
---

# mash reads a sender header the way the SAP Go SDK does, and a validly signed header it refuses is counted and logged, not shown

Amends: ADR-0063

## Context and Problem Statement

mash decoded the `Agent-Sender` header with its own parser (`internal/sap`): a JSON map decode that took any RFC 3339
timestamp, took the last of a repeated member, had no size cap and accepted any name. The SAP Go SDK's `identity`
package parses the same header more strictly (SAP section 13.5). v0.6.0 moves mash's signing and verification onto the
SDK, which raises one question: does mash keep reading stored and live headers its own way, or the SDK's way?

The maintainer ruled (decision 229): mash reads SAP headers the way the SAP family does, on both the live
and the stored path. This ADR records what mail that changes, and what mash does so that no mail is lost without a
trace.

## Decision Drivers

* One reading of a header across the family: a header that another SAP implementation reads one way must not be read
  another way by mash (a repeated member is a parser differential, because a map decode takes the last value and a
  stricter reader refuses it).
* mash's own headers must be unaffected.
* A validly signed message that the stricter parser refuses must leave a trace an operator can see.
* A refused message must not reach the session: the refusal exists to keep hostile header text from being displayed.

## Considered Options

* Keep mash's lenient parser for stored mail and use the SDK only for live requests.
* Adopt the SDK's parser on both paths, refuse what it refuses, and make every refusal visible (chosen).
* Adopt the SDK's parser and keep a refused message in the spool with a `malformed` label.

## Decision Outcome

Chosen option: "adopt the SDK's parser on both paths, and make every refusal visible".

`VerifyStored` and `VerifyLive` in `internal/sap` read the header with `identity.ParseHeader` first (so a refused message
keeps the account, user and name it claimed, for the log) and then verify with `identity.VerifyStored` or
`identity.VerifyLive`. The labels, the `Mash-*` signatures (instance, urgent, in-reply-to, via), the trust refinements
`unknown-signer` and `stale` and the token rules stay mash's own.

### The header classes now refused

A header with a valid signature that is now `malformed` (it used to be `verified`, then shown):

| Class | What is refused |
|---|---|
| `size` | a value over 2048 bytes |
| `encoding` | a value that is not UTF-8, or not a JSON object |
| `duplicate-member` | a member name that appears twice (mash took the last) |
| `timestamp` | a `ts` that is not RFC 3339 UTC ending in `Z` (an offset such as `+01:00` is refused) |
| `name` | a `name` with a control character, a lone surrogate escape, or more than 64 UTF-16 code units |
| `subject` | a `sub` that is not a NATS subject (for example one with a space) |
| `nonce` | a `nonce` outside `[A-Za-z0-9_-]{1,64}` |
| `version-or-field` | `v` missing or not a number, or a required member missing or not a string |

Also changed: a `sig` that is not base64url without padding of 64 bytes is now `malformed` (it used to be `forged`).
A signature that decodes but does not verify, a `sub` that is not the arrival subject, a replayed nonce and a `ts`
outside the 30 second window stay `forged`.

mash's own headers are unaffected: it writes `ts` as `2006-01-02T15:04:05Z`, a 22 character nonce, the endpoint name as
`name` and the arrival subject as `sub`. The compat gate (`internal/sap/compat_corpus_test.go`) lists each changed
case with its old and new label.

### What mash does with a refused message

* It is not shown and is not written to the spool (showing it is what the refusal prevents).
* It is acknowledged as before. The inbox stream keeps it (limits retention: 30 days or 16 MiB), so it can be read.
* The endpoint logs one line per message: the stream reference, the label (`malformed` or `forged`), the reason class
  from the table above, and the arrival subject. For a forged message (whose header parsed) it also names the claimed user key. Never the body, the header text or the claimed name.
* The endpoint counts refusals by reason class. The `status` tool inside `mash serve` reports the count when it is not zero (the count is per delivery and per session; the command `mash status` from a shell has no live endpoint and shows nothing).
* `mashctl inbox inspect` shows the reason class of a `malformed` or `forged` message (text and JSON). `mashctl audit` reads only stream and consumer state, not message headers, so it is unchanged: counting there would mean scanning every message.

A message that mash will not show is therefore never lost silently: it is counted and logged when it arrives, and it
stays in the stream for the length of the retention window.

### The test that changed

`TestUnsafeSenderNameIsReplaced` sent an unsigned header whose `name` held a line break and a fake `[verified]` line and
expected the message to be shown as `[claimed]` under a replaced name. The SDK parser refuses a control character in
`name`, so the message is now refused and not shown at all; the test expects that (no event, and the refusal counted).
The display-injection it guards is prevented more strongly, not less. A new test, `TestLabelLikeSenderNameIsReplaced`,
keeps the case that is still parsed: a name that only looks like a label (no control character) is still replaced by
a safe form and the message still reads `[claimed]`.

### Consequences

* Good, because mash and the other SAP implementations now agree on every header, and the repeated-member differential
  is closed.
* Good, because a refused message is visible to an operator the moment it arrives.
* Bad, because a message whose header is refused is no longer delivered to the session, even though its signature is
  valid. Only foreign or hand-made headers are affected; a header mash wrote is not.
* Neutral, because the refused message is kept in the stream but not in the spool: reading it is an operator action
  (`mashctl inbox inspect`), not something a session does.

### Confirmation

`internal/sap/compat_corpus_test.go` (every case, old and new label), `internal/sap/sender_oracle_test.go` (byte identity
of the headers mash writes), the reason-class table test in `internal/sap`, the refusal tests in `internal/endpoint`
(counted by class, not shown, still in the stream), the status line test, and the two sender-name tests above.

## Pros and Cons of the Options

### Keep the lenient parser for stored mail

* Good, because nothing mash accepted yesterday is refused today.
* Bad, because the same header reads two ways in one process, and the repeated-member differential stays.

### Adopt the SDK parser and keep a refused message in the spool

* Good, because nothing is dropped from the operator's point of view.
* Bad, because the spool is what the session shows; keeping hostile header content there, even labelled, is a path for
  it to reach the model, and it adds state to a delivery path that is hard to change. The inbox stream already keeps the
  message.

## More Information

Plan: the v0.6.0 SAP Go SDK adoption plan, step P1 (2). Related: [ADR 0063](0063-in-reply-to-is-signed-and-a-sender-can-declare-a-relay-with-a-signed-mash-via-both-are-shown-only-when-they-verify.md) (the signed marks mash keeps), and the SDK's `identity` package at v0.1.0.
