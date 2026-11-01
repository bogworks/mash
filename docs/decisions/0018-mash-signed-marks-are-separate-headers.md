---
status: accepted
date: 2026-10-25
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# mash's own signed marks (urgent, sender instance) are separate headers; kind and in-reply-to are unsigned

## Context and Problem Statement

SAP's `Agent-Sender` header (ADR-0017) signs a fixed set of fields. mash later needed more facts per message: which
running instance (process) of an endpoint sent it (a consumer needed to tell one launch of an endpoint from the next),
whether the sender marked it urgent, what kind of mail it is (ADR-0027), and which message it answers. Some of these
must be trusted, and a trusted mark must not be movable from one message to another.

## Decision Drivers

* Leave the SAP header untouched so other SAP verifiers still work (ADR-0003).
* A mark that changes behaviour (urgent wakes a session at once) must be bound to one message and one signer.
* Facts that only schedule delivery need no signature.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Separate mash headers, each signed under its own domain tag over the message's own signed fields.
* New signed fields inside SAP's `Agent-Sender` header.
* All marks unsigned.

## Decision Outcome

Chosen option: "separate, domain-separated signed headers for the marks that must be trusted", because the urgent mark
is "the sender's signature over the message's own header fields, under its own domain (`MASH-URGENT-V1`)", so a mark
replayed onto another message, or signed by another key, fails (PR #23). Kind is left unsigned because it is
"advisory and not signed ... A sender can claim blocker to wake" a session, "as all mail could before kinds existed"
(PR #75). Not changing the SAP header follows from ADR-0003 (reconstructed).

As built (mash layer, on top of SAP §13):

* `Mash-Sender-Instance: <instance>.<sig>`, signed under `MASH-SENDER-INSTANCE-V1` over account, user, subject,
  timestamp, nonce and the instance. Shown only when it verifies and the message is `verified` or `stale`.
* `Mash-Urgent: <sig>`, signed under `MASH-URGENT-V1` over account, user, subject, timestamp and nonce. Honoured only
  for a `verified` sender (ADR-0029).
* `Mash-Kind` and `Mash-In-Reply-To`: unsigned.

### Consequences

* Good, because SAP tools see a standard header and ignore mash's extras.
* Good, because a captured urgent mark or instance binding cannot be attached to a different message.
* Bad, because the instance binding proves only that the endpoint's key signed it: an endpoint can name an instance of
  its own choosing. This was accepted in review of PR #1.
* Bad, because kind and in-reply-to are the sender's unproven claims.

### Confirmation

* `internal/sap/sender_test.go`: `TestInstanceBindingRoundTrip`, `TestUrgentMarkRoundTrip`.
* `internal/endpoint/endpoint_test.go`: `TestSenderInstanceVerified`, `TestForgedSenderInstanceNotShown`.
* `internal/endpoint/hook_test.go`: `TestHookUrgentWakesAtOnce` (a claimed sender's urgent mark does not wake).
* `internal/endpoint/kind_test.go`: `TestSendWithSetsTheKindHeader`.

## Pros and Cons of the Options

### Separate signed headers

* Good, because each mark has its own domain tag, so one signature cannot pass as another.
* Bad, because each mark is a second signature to make and check.

### New fields in the SAP header

* Good, because one signature would cover everything.
* Bad, because it changes a header other SAP implementations parse and verify.

### All marks unsigned

* Good, because it is simplest.
* Bad, because any sender could claim urgency and any instance.

## More Information

* Sender instance: PR #1 (2026-10-25/26). Urgent: PR #23 (2026-10-28). Kind: PR #75 (2026-10-29).
* Spec `docs/specs/2026-10-21-mash-design.md` §7.2 (sender instance); `docs/operations.md`, "Hook delivery" (urgent, kind).
* Code: `internal/sap/sender.go` (`SignInstance`, `SignUrgent`, `VerifyInstance`, `VerifyUrgent`),
  `internal/endpoint/mail.go` (header names).
* Related: ADR-0017 (SAP signing), ADR-0019 (trust labels), ADR-0027 (kinds), ADR-0029 (urgent).
