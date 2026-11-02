---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# In-reply-to is signed, and a sender can declare a relay with a signed Mash-Via; both are shown only when they verify (amends 0018)

## Context and Problem Statement

[ADR 0018](0018-mash-signed-marks-are-separate-headers.md) left `Mash-In-Reply-To` unsigned, and mash never read it: the
header was written and nothing showed it. Two things changed. A recipient can usefully see which of its own earlier messages
a message answers, and which message of the sender's own inbox a message passes on. Showing a pointer that anyone with
publish rights to the inbox subject could set, or move from one message to another, would be a new way to put a false
causal story in front of a model. And a session that relays or quotes mail it received has no way to say so: the recipient
cannot tell an honest relay from an original (roadmap N15). This ADR amends 0018 for in-reply-to only; kind stays as 0018
left it, and 0018's file is unchanged.

## Decision Drivers

* A mark a recipient will show must be bound to one message and one signer, as the urgent mark and the instance already are.
* A mark must not change a trust label or wake anything: it is information about a message, not a grant.
* Declaring a relay must be cheap and honest-by-default, without mash judging content.
* Mail from older senders must keep working and must not show anything unverified.

## Considered Options

* Sign `Mash-In-Reply-To` under its own domain, and add a signed `Mash-Via`; show each only when it verifies.
* Show the unsigned in-reply-to as sent.
* Add the relay declaration as a field of the SAP `Agent-Sender` header.
* Detect relays by comparing a message with the sender's inbox (content matching).

## Decision Outcome

Chosen option: "sign in-reply-to under its own domain, add a signed `Mash-Via`, show each only when it verifies",
because it is the pattern 0018 set for marks that matter (a separate header, its own domain tag, signed over the message's
own fields), it leaves the SAP header untouched ([ADR 0003](0003-sap-plus-mail-verb-and-jetstream-durability.md)), and it
needs no judgement about content.

* `Mash-In-Reply-To: <seq>.<sig>`, signed under `MASH-IN-REPLY-TO-V1` over the message's account, user, subject, timestamp
  and nonce (as the instance mark is), and the decimal sequence. A sender always sends the signed form.
* `Mash-Via: <origin>#<seq>.<sig>`, signed under `MASH-VIA-V1` over the same fields, the origin and the sequence, so the
  declaration is bound to this message and to that origin and sequence. The origin is an endpoint name and is checked as
  one both when signing and when verifying.
* The send API takes `Via`, a sequence in the sender's own inbox (the MCP `send` tool's optional `via`). The sender reads
  that message from its own inbox stream, takes the sender's name from its `Agent-Sender` header, and refuses to send when
  there is no such message (`mash: no message #N in your inbox to relay`) or when its sender name is not a valid endpoint
  name. The original need not be verified: relaying unverified mail is still worth declaring. At most one `via` per message.
* A receiver accepts either mark only when it verifies for this message AND the message's trust is `verified` or `stale`,
  as for the instance. Anything else is ignored, never shown and never an error: the old bare decimal from an older
  sender, a forged or moved signature, a malformed value, a claimed or unsigned sender's marks.
* Shown as `answers #N` and `relayed from <origin> #N` between the header line and the instance line (and as tab fields of
  the one-line and digest forms), only when set. Bodies stay quoted data. Output with neither mark is byte-identical.
* Neither mark changes a trust label, the kind, the urgent handling or any wake.
* `Mash-Kind` stays unsigned, for the reasons in 0018 (it only schedules delivery).

### Consequences

* Good, because a causal pointer can no longer be set by a party that does not hold the sender's key, nor moved to another
  message, and the receiver can show it.
* Good, because an honest relay is visible: the recipient sees which of the relayer's own inbox messages, from which
  endpoint, the message derives from.
* Bad, because an older sender's bare-decimal in-reply-to is no longer shown (it never was, before this change). A reply
  from a pre-change mash is shown without `answers`.
* Bad, because `Mash-Via` proves little: the sender's key declares that this message derives from #N of its own inbox,
  whose sender was the origin. It does not prove that the content relates to #N, that the sender did not also relay
  without declaring, or that #N's sender is who it said (the origin is the name that message carried, verified or not).
  Absence of `Mash-Via` means nothing. A sequence is in the sender's inbox, so the recipient cannot read #N itself.
* Neutral, because a recipient that sees `relayed from` must still weigh the relayer: it is the relayer's own signed claim,
  and the trust label is the relayer's, never the origin's.

### Confirmation

* `internal/sap/sender_test.go`: `TestInReplyToRoundTripAndForgery`, `TestViaRoundTripAndForgery` (round trips; a mark moved
  to another nonce, timestamp or subject; another key; another sequence or origin; empty, no dot, non-numeric or
  non-canonical sequence, bad origin characters, a tampered signature; the bare decimal).
* `internal/endpoint/marks_test.go`: `TestReplyToIsSignedAndShown`, `TestReplyToUnsignedOrForgedIsIgnored`,
  `TestViaIsDeclaredAndShown`, `TestViaToAMissingMessageErrorsWithoutSending`, `TestViaOfUnverifiedMail`,
  `TestMarksOfAClaimedSenderAreNotShown`, `TestMarksChangeNeitherTrustKindNorWake`, `TestViaOriginSyntaxMatchesConfig`.
* `internal/render/render_test.go`: `TestAnswersAndRelayedFromLines` (and the existing golden tests, unchanged).
* `cmd/mash/kind_test.go`: `TestSendViaFromTheMCPTool` (the schema and the tool errors).

## Pros and Cons of the Options

### Sign in-reply-to, add a signed Mash-Via

* Good, because it reuses a pattern already reviewed and tested.
* Bad, because each mark is another signature to make and check.

### Show the unsigned in-reply-to

* Good, because it costs nothing.
* Bad, because anyone who can publish to the subject can attach a pointer to a verified message.

### A field of the SAP header

* Good, because one signature would cover it.
* Bad, because it changes a header other SAP verifiers parse (ADR 0003).

### Detect relays by content

* Good, because it needs no cooperation from the sender.
* Bad, because it is a content judgement mash does not make, and it is easy to evade.

## More Information

* Spec `docs/specs/2026-10-21-mash-design.md` §7.2 (in-reply-to and via); `docs/operations.md`, "Hook delivery".
* Code: `internal/sap/sender.go` (`SignInReplyTo`, `VerifyInReplyTo`, `SignVia`, `VerifyVia`), `internal/endpoint/mail.go`
  (`senderMarks`, `viaOrigin`), `internal/render/render.go`.
* Related: [ADR 0017](0017-every-message-is-signed-per-sap-13.md), [ADR 0018](0018-mash-signed-marks-are-separate-headers.md),
  [ADR 0019](0019-five-trust-labels-forged-never-shown-stale-after-five-minutes.md),
  [ADR 0020](0020-verified-mail-is-a-delegated-request.md), [ADR 0021](0021-message-bodies-are-shown-quoted.md).
