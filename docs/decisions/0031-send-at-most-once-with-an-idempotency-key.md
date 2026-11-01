---
status: accepted
date: 2026-10-30
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# A send can be made at-most-once with a key, within the stream's duplicate window

## Context and Problem Statement

A send is a publish to the recipient's inbox stream, which JetStream (NATS's persistence layer) acknowledges once the
message is stored. If that acknowledgement is lost, for example to a network fault, the sender cannot tell whether
the message was stored. Before this decision every send got a fresh SAP nonce, so "a send that timed out might have
landed already. Re-running the same command then delivered it a second time" (PR #122). mash needed a safe retry.

## Decision Drivers

* A retried send must not deliver the same message twice.
* Use what NATS already offers rather than a new protocol.
* Keep SAP's per-message signature and replay protection intact.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Leave retries to the caller and accept duplicates
* Deduplicate in the recipient only, by message content
* A caller-supplied idempotency key that fixes the message's nonce and JetStream message id

## Decision Outcome

Chosen option: "an idempotency key", because a timed-out send could otherwise be delivered twice (PR #122). The
alternatives were not weighed in the record.

As built:

* NATS layer: JetStream drops a publish whose `Nats-Msg-Id` it has seen within the stream's duplicate window. mash's
  inbox window is 2 minutes (`internal/store/store.go`); the value was chosen without a recorded reason.
* mash layer: every send carries a key, the caller's (`mash send --id KEY`, or the `send` tool's `id`) or a random
  one. The SAP nonce, which is also the `Nats-Msg-Id`, is the first 128 bits (32 hex characters) of SHA-256 over a
  fixed label, the sender, the recipient and the key, each length-prefixed (`SendNonce` in
  `internal/endpoint/mail.go`). The same key from the same sender to the same recipient gives the same nonce; a key
  is 1 to 128 printable ASCII characters.
* A re-send inside the window stores nothing new; the send reports the original `#seq` and says it was a duplicate.
  A send whose acknowledgement times out returns an error naming the key to retry with and the window.
* Recipient side (PR #134): the recipient remembers the stream sequences of signed mail it has shown for the whole
  session, so a redelivery is never shown twice even after its `(user, nonce)` entry has expired; a second message
  with the same `(user, nonce)` inside the window is treated as a replay and dropped.

### Consequences

* Good, because a caller can retry a timed-out send safely within 2 minutes.
* Good, because the SAP signature still covers the nonce, so the key cannot be swapped onto another message.
* Bad, because protection ends with the window: a retry after 2 minutes may store a second copy, which the recipient
  shows as new mail.
* Neutral, because a send without `--id` behaves as before: each is a new message.

### Confirmation

* `internal/endpoint/send_id_test.go`: `TestSendIDTwiceLandsOnce`, `TestSendIDDoesNotCollideAcrossSendersOrRecipients`,
  `TestSendIDRepeatedAfterTheWindowIsShownAgain`, `TestSendIDValidation`,
  `TestRedeliveryOfAPrunedMessageIsDroppedAndAcked`.
* `internal/endpoint/hook_test.go`: `TestSeenSetWindowAndSequence`.
* `cmd/mash/send_id_test.go`: `TestCLISendIDRerunLandsOnce`, `TestCLISendIDFlagsAndValidation`, `TestServeSendToolID`.

## Pros and Cons of the Options

### Accept duplicates

* Good, because nothing is built.
* Bad, because a network fault turns into a repeated instruction to the recipient.

### Recipient dedupe by content

* Good, because senders change nothing.
* Bad, because two genuine, identical messages would be merged, and the duplicate is still stored.

### Idempotency key

* Good, because the stream refuses the duplicate before it is stored, using a NATS feature.
* Bad, because it is bounded by the duplicate window and needs the caller to reuse the key.

## More Information

* PR #122 (2026-10-30), PR #134 (2026-10-31). A consumer's review of network faults found the problem.
* `docs/install.md` (the `send --id` section).
* Related: ADR-0009 (inbox stream), ADR-0010 (inbox limits), ADR-0017 (signing and nonces), ADR-0025 (at-least-once
  delivery to the session).
