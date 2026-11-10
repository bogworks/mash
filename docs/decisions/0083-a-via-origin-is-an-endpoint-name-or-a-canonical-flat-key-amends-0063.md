---
status: accepted
date: 2026-11-10
decision-makers: Randy Bias (mash maintainer)
---

# A via origin is a legacy endpoint name or a canonical flat key (amends 0063)

## Context and Problem Statement

[ADR 0063](0063-in-reply-to-is-signed-and-a-sender-can-declare-a-relay-with-a-signed-mash-via-both-are-shown-only-when-they-verify.md)
says the origin of a signed `Mash-Via` "is an endpoint name and is checked as one both when signing and when verifying",
and that name is the one syntax of a legacy endpoint (lower-case letters, digits and single hyphens, 1 to 48 characters).
Since [ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md) an endpoint may be three
SAP tokens, and the name its messages carry and its directory is keyed on is its flat form `agent_owner_name`. The endpoint
side already reads that name as an endpoint name when it builds a relay declaration (`internal/endpoint/mail.go`
`viaOrigin`, through `validDirectoryKey`), but `SignVia` and `VerifyVia` still apply the legacy syntax. A `send --via` that
relays mail from a three-token endpoint therefore fails at signing: the origin it must declare is refused by the signer.
Found by the Go SDK work and a Codex review of that change.

## Decision Drivers

* A relay of mail from a three-token endpoint must be declarable, or the relay mark is lost for the new form.
* The signed bytes of every existing declaration must not change, so legacy declarations still verify and legacy output is
  byte-identical.
* The origin is signed as a string and shown to a model: only canonical, unambiguous forms may be accepted, on both sides.
* Mash-Via is mash's own header (ADR 0018, 0063), not a SAP section 13 field.

## Considered Options

* Accept a legacy name or a canonical flat key as the origin, with one predicate for signing and verifying.
* Keep the legacy syntax and let the sender omit the declaration for a three-token origin.
* Add a new header for three-token origins.

## Decision Outcome

Chosen option: "accept a legacy name or a canonical flat key, with one predicate", because it keeps the signed input and
the header shape as they are and aligns the signer with the endpoint side and ADR 0074.

This amends ADR 0063 for the origin syntax only; the rest of that record, and its file, are unchanged.

* The origin of a `Mash-Via` is either a legacy endpoint name (exactly the previous rule) or a canonical flat key:
  `FromFlat(origin)` succeeds and `Flat()` of the result is the origin. `FromFlat` requires exactly three underscore-joined
  tokens, each valid under the new token rule, and refuses the legacy agent token and an over-long total. Anything else
  (wrong number of tokens, an empty token, a leading, trailing or doubled underscore, upper case, whitespace, a dot, a hash,
  a newline, a NUL, non-ASCII) is refused.
* `SignVia` and `VerifyVia` call the same predicate (`validViaOrigin`), so a value one side makes the other side checks
  by the same rule.
* The signed input (`MASH-VIA-V1` over the header fields, the origin and the sequence) is unchanged, and the origin is
  signed as the string given. For a three-token endpoint that string is the name its messages carry, its flat form.
  Declarations with a legacy origin are byte-identical to what the previous code produced.

### Consequences

* Good, because mail from a three-token endpoint can be relayed with a signed declaration, and the signer agrees with the
  endpoint side about what an endpoint name is.
* Good, because nothing signed before changes: legacy values and their signatures are untouched.
* Bad, because an older receiver rejects a flat-key origin as malformed and ignores the mark: it shows no
  `relayed from` line for such a message. The message itself, its trust label and its other marks are unaffected, and
  nothing errors. Until receivers are upgraded, a relay of three-token mail is shown only to upgraded receivers.
* Bad, because the origin is still only the name that the original message carried, verified or not (ADR 0063 said so and
  that does not change): a flat-key origin proves nothing more about the original's sender than a legacy name did.

### Confirmation

* `internal/sap` tests: a relay round trip from a three-token origin to a legacy signer and from a legacy origin to a
  three-token signer; forged origins refused (another flat key, a swapped legacy name, another sequence, a tampered or
  byte-flipped signature, another message's declaration); 22 malformed or non-canonical origins refused by both sides, with
  signatures valid for the malformed string so that only the syntax rule can refuse them; and a golden test that legacy
  declaration values are byte-identical to those of the previous code.

## Pros and Cons of the Options

### Accept a legacy name or a canonical flat key

* Good, because the header, the signed input and the verification path are unchanged.
* Bad, because the origin syntax is now two forms, and an older receiver ignores the newer one.

### Keep the legacy syntax and omit the declaration

* Good, because nothing changes.
* Bad, because a relay of three-token mail cannot be declared at all, which is the defect.

### A new header for three-token origins

* Good, because an older receiver would not see a value it rejects.
* Bad, because it adds a second header and a second signature domain for one concept.

## More Information

* amends: [0063](0063-in-reply-to-is-signed-and-a-sender-can-declare-a-relay-with-a-signed-mash-via-both-are-shown-only-when-they-verify.md)
  (the origin syntax only).
* Code: `internal/sap/sender.go` (`validViaOrigin`, `SignVia`, `VerifyVia`); `internal/endpoint/mail.go` (`viaOrigin`).
* Related: [ADR 0018](0018-mash-signed-marks-are-separate-headers.md),
  [ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md).
