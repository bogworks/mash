---
status: accepted
date: 2026-10-28
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Urgent is a separate signed mark, honoured only from verified senders

## Context and Problem Statement

Hook delivery (ADR-0024) holds mail for an idle session until the idle wake (ADR-0028). Some mail cannot wait that
long: the operator asked for urgent mail to be "prioritized on delivery". An urgent flag that anyone can set would let
any sender wake any session at will, and an unsigned header could be added to a message in transit. mash needed an
urgent signal that wakes at once but cannot be forged or borrowed.

## Decision Drivers

* Urgent mail must wake an idle session at once, whatever its kind.
* Only a sender whose identity mash has proven may jump the queue.
* The mark must be bound to one message, so it cannot be copied onto another.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* An unsigned urgent header, honoured from any sender
* An urgent kind alongside the five mail kinds
* A separate urgent mark signed by the sender's key, honoured only when the sender is verified

## Decision Outcome

Chosen option: "a separate signed mark, honoured only from verified senders". The recorded reason for the trust rule:
"Only verified counts ... A claimed sender's urgent mail, even with a mark its key did sign, is normal mail" (PR #23).
Why urgent is a mark separate from the kind is not recorded; the kind was added later (ADR-0027) and is unsigned, so a
signed urgent mark keeps a trust-bearing signal out of an unsigned header (reconstructed).

As built (mash layer, using the sender's NATS key as SAP §13 does):

* The sender sets the `Mash-Urgent` header to a signature by its own key over the message's SAP sender-header fields
  (account, user, subject, timestamp, nonce) under its own domain string, so the mark holds for that message alone
  (`internal/sap/sender.go`, `SignUrgent`, `VerifyUrgent`).
* The receiver treats a message as urgent only when the sender's trust label is `verified` (ADR-0019) and the mark
  verifies (`internal/endpoint/mail.go`).
* Urgent mail of any kind wakes a hooked session at once, each urgent message once; if a drain leaves it held, it may
  ring again. The wake's summary line starts with `urgent,`, and a hook drain shows urgent mail first.
* `mash send --urgent` and the session's `send` tool's `urgent` field set it.

### Consequences

* Good, because an unproven sender cannot wake a session early, even by copying a mark from a real message.
* Good, because urgent works for every kind, including `fyi`.
* Bad, because any verified sender may mark any message urgent; there is no per-sender limit.
* Neutral, because a claimed or unknown sender's urgent mail is still delivered, as normal mail.

### Confirmation

* `internal/sap/sender_test.go`: `TestUrgentMarkRoundTrip`.
* `internal/endpoint/hook_test.go`: `TestHookUrgentWakesAtOnce` (includes the claimed-sender case),
  `TestHookUrgentLeftHeldRingsAgain`.
* `internal/endpoint/kind_test.go`: `TestUrgentFyiStillWakesAtOnce`.
* `cmd/mash/hook_test.go`: `TestSendUrgentFromTheCLI`, `TestHookDrainShowsUrgentFirst`.
* `internal/render/render_test.go`: `TestUrgentLeadsTheLineAndIsMarkedInTheMessage`.
* The release gate (`docs/release.md`) requires live `--urgent` mail within 10 s.

## Pros and Cons of the Options

### Unsigned header from any sender

* Good, because it is the simplest to build.
* Bad, because anyone who can publish to an inbox could wake the session at will.

### An urgent kind

* Good, because it adds no new header.
* Bad, because the kind is unsigned, and urgency would then exclude a kind (an urgent `fyi` or `blocker`).

### Signed mark, verified senders only

* Good, because the mark is as strong as the message signature and tied to one message.
* Bad, because it adds a second signature per urgent message and one more header to parse.

## More Information

* PR #23 ("hook delivery part 3: urgent mail", 2026-10-28). The request: maintainer design discussion, 2026-10-27
  (not public).
* `docs/operations.md`, "Hook delivery".
* Related: ADR-0017 (message signing), ADR-0018 (mash's signed marks), ADR-0019 (trust labels), ADR-0027 (kinds),
  ADR-0028 (wake timing).
