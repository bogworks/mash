---
status: proposed
date: 2026-11-24
decision-makers: Randy Bias (mash maintainer; contract 0.6.0 REQ-005)
---

# mash adopts the SAP SDK's strict Agent-Sender parser outright, and ten shapes it read before are now malformed

## Context and Problem Statement

This touches requirement REQ-005 of the 0.6.0 contract, "sender identity through the SDK, with the strict parser" (stage S4). mash reads the `Agent-Sender` header with its own parser in `internal/sap`: a lenient JSON decode that takes any RFC 3339 `ts`, takes the last of a repeated member, has no size cap and accepts any `name`. The SAP Go SDK's `identity` package reads the same header strictly. Moving signing, `VerifyStored`, `VerifyLive` and the nonce set onto the SDK raises one question: does mash keep reading stored and live headers its own way, or the SDK's way? Earlier drafts of this decision (pull request #672) are rewritten here against the contract.

Ten header shapes read differently under the two parsers. Shapes 1 to 7 and 10 are refused even when the signature is valid; shapes 8 and 9 cannot be validly signed.

1. the value is over 2,048 bytes (header too long);
2. `ts` has an offset, not UTC `Z` (time not in UTC);
3. `name` has a control character (control character in name);
4. `name` is over 64 UTF-16 units (name too long);
5. `name` has a lone surrogate escape (broken character in name);
6. a JSON member repeats; mash took the last (a field appears twice);
7. `sub` has a space (space in subject);
8. `sig` is not base64url of 64 bytes; mash called it forged (signature unreadable);
9. the header value is empty; mash called it absent (empty sender header);
10. two `Agent-Sender` values; mash read the first (two sender headers).

## Decision Drivers

* One reading of a header across the SAP family. A repeated member or a second header value that one reader takes and another refuses is a parser differential.
* A refused message must not reach the session; the refusal exists to keep hostile header text from being shown.
* The maintainer ruled that mash reads SAP headers the way the SAP family does, on the live and the stored path, with no divergence to keep.
* mash's own headers must be unaffected.
* A refusal says a class and never the sender's content (REQ-017).

## Considered Options

* Keep the lenient parser behind a setting.
* Adopt the SDK's strict parser outright, on the live and the stored path, with no setting (chosen).
* Stay on mash's own verifier.

## Decision Outcome

Chosen: adopt the strict parser outright. The SDK `identity` package parses `Agent-Sender` on the live path and on the stored path. There is no setting, flag or environment variable that keeps the lenient parser.

* The ten shapes above are malformed. The class names in brackets are the reasons the views show. REQ-017 fixes the classes: these ten, one class for each other malformed case mash refuses today, and `unreadable sender header` for any SDK parse error outside the list.
* Every `Agent-Sender` value is passed to the verifier, on the stored read path and on the live path, not the first one only. Passing one `Header.Get` value would lose shape 10 at that boundary.
* A message with no `Agent-Sender` header at all stays `absent`, as today. Only an empty value (shape 9) is malformed. Two values are malformed (shape 10).
* mash's old parser and verifier stay in the tree as a test oracle only. No production path calls them.
* A live prompt with a malformed header is refused with `400` and today's text, `malformed Agent-Sender header`. A live prompt past the nonce caps is refused with `401` and its own reason text, `too many recent prompts: try again shortly`; the status code does not change, and the reply no longer says a signed header is required to a sender that was correctly signed.
* The doctor refused-mail row changes its wording from "no unverified mail was refused" to "no mail was refused", and its state is `ok` whatever it counts. This is a spec change under [ADR 0164](0164-planned-output-is-written-in-x-output-next-and-spec-next-json-and-rendered-as-the-gate-1-demo.md): the new wording is written in `x-output-next` and `spec/next.json` first, and the old strings are replaced only when the switch-on release's code lands (stage W).

An eleventh shape is a contract amendment, not an ADR choice. A shape outside the ten is added by amending REQ-005, and then this record is superseded or amended; it is not added by editing a list in a pull request.

### Consequences

* Good, because mash and the other SAP implementations read every header the same way, and the repeated-member and second-header differentials are closed.
* Good, because there is one code path on each side and no setting to test or explain.
* Bad, because stored mail in these shapes that read verified, forged or absent before the switch-on release reads malformed after it. Mail in shapes 1 to 7 and 10 was shown with its label; shape 8 was dropped and logged; shape 9 was shown as `absent`. A validly signed message from a foreign or hand-made header is no longer delivered. A header mash wrote is not affected.
* Neutral, because malformed mail is hidden from sessions, as mail mash already reads as malformed is hidden today. The stream keeps it for its retention window.
* Neutral, because the switch-on release counts and lists these messages (stage W, REQ-006): in `inbox`, `status --detail`, `doctor` and `mashctl endpoint inbox`, each with its class and no body or header text.
* Neutral, because the 0.5.x binaries do not change. Nothing here takes effect before the switch-on release, and the release notes say first that the ten shapes are now refused.
* Neutral, because the live nonce caps follow the SDK's, and a prompt past them is refused with `401` and the reason text above.

### Confirmation

* S2's compat corpus gate (stored-header corpus, live skews and nonce replay run through mash's verifier and the SDK's side by side) holds each shape's old and new verdict.
* S4's oracle test keeps two columns for every fixture. The old column is the lenient parser's verdict, asserted on the pre-switch production binary only; the strict column is the ten shapes as malformed, each with its class, and the missing-header control as `absent` in both, asserted on S4's adapter and on W's binary. No test asserts the old column against the strict adapter.
* Acceptance test AT-005 has two parts: the adapter and the oracle driven with every fixture before S4 merges, and W's binary repeating the receive-path, exact-output and refusal checks, because the production receive paths first switch over in W.

## Pros and Cons of the Options

### Keep the lenient parser behind a setting

* Good, because nothing mash accepted yesterday is refused today for an operator who sets it.
* Bad, because the same header reads two ways in one product, and the setting keeps the parser differential reachable.
* Bad, because every output, test and release note has to describe both readings, and a setting that restores hostile-header display is one an operator can turn on without seeing why it is off.

### Adopt the strict parser outright

* Good, because there is one reading, the family's, and the oracle makes each change a listed, tested verdict.
* Bad, because mail that was shown before is refused after the switch-on release, with no way back for the operator; the stream still holds it.

### Stay on mash's own verifier

* Good, because no mail changes class and no code moves.
* Bad, because mash stays the one reader in the family that takes the last repeated member and the first of two headers, and the 0.6.0 move to the SDK's signing and caller code is not finished.

## More Information

Contract 0.6.0, REQ-005, REQ-006, REQ-017 and the change list at the switch-on (CHG-1 to CHG-9, CHG-14). Pull request #672 carried the earlier draft. Related: [ADR 0063](0063-in-reply-to-is-signed-and-a-sender-can-declare-a-relay-with-a-signed-mash-via-both-are-shown-only-when-they-verify.md) (the signed marks mash keeps), [ADR 0164](0164-planned-output-is-written-in-x-output-next-and-spec-next-json-and-rendered-as-the-gate-1-demo.md). The number is unused on `main` when written; the lint refuses it if another record takes it first.
