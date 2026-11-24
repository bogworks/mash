---
status: accepted
date: 2026-11-24
decision-makers: mash maintainers (issue #1536)
---

# The prompt endpoint advertises the max_payload it enforces

## Context and Problem Statement

[ADR 0030](0030-sessions-are-full-sap-agents.md) gives the `prompt` endpoint the metadata `max_payload 1MB`. A later fix (commit f23b44a6) capped a live prompt at 32768 bytes of text (`endpoint.MaxPromptBytes`), so that the session's channel notice, which uses the same number, never has to cut a prompt. The endpoint refuses a longer prompt with `413 prompt_too_large`. `docs/install.md` and `docs/operations.md` document that cap. The advertised value was never changed. A SAP caller that trusts `max_payload`, such as an SDK-hosted agent, sends 33 KiB to 1 MB and is refused (#1536). Which number is right, and how are the two kept together?

## Decision Drivers

* A caller must be able to trust what an endpoint advertises (SAP §2.1).
* The 32 KiB cap has a reason: the channel notice shows the whole prompt.
* SAP's `max_payload` counts the whole request on the wire, the signed `Agent-Sender` header included, in units of 1024 (the SAP SDKs' reading; the spec is silent on the base).
* The two numbers should not be able to drift apart again.

## Considered Options

* Raise the enforced cap to the advertised 1 MB.
* Advertise the enforced cap, derived from `MaxPromptBytes` (chosen).

## Decision Outcome

Chosen: advertise the enforced cap. The `prompt` endpoint's `max_payload` is `MaxPromptBytes` plus a fixed headroom of 1 KiB for the request's headers and envelope (`PromptEnvelopeHeadroom`), written in SAP's grammar: `33KB`, which is 33792 bytes. The 32768-byte cap on the text is unchanged. The rest of ADR 0030's `prompt` metadata stands.

### Confirmation

`TestPromptAdvertisedMaxPayloadMatchesTheLimit` (internal/endpoint) reads the registered `max_payload` and checks that it equals `MaxPromptBytes` plus the headroom. It builds a signed request carrying `MaxPromptBytes` of text, the way `mash prompt` does, and checks that its wire size is within the advertised value. It also checks that one byte more is still refused with 413. Changing either number alone fails it.

### Consequences

* Good, because a caller that trusts `max_payload` is no longer refused for a prompt the endpoint said it accepts.
* Good, because the channel notice still never cuts a prompt.
* Bad, because a caller whose envelope inflates the text, such as heavily escaped JSON, can go over 33KB on the wire with less than 32 KiB of text; such a caller refuses locally, which is the safe direction.

## Pros and Cons of the Options

### Raise the enforced cap to 1 MB

* Good, because callers that already send large prompts keep working.
* Bad, because the channel notice would have to cut long prompts, which f23b44a6 removed.
* Bad, because long text already has a place: mail, up to 1 MiB.

### Advertise the enforced cap

* Good, because the advertised and enforced numbers come from one constant.
* Bad, because the advertised value includes a headroom estimate, so it is not exactly the text limit.

## More Information

Issue #1536, found while mapping mash onto the SAP Go SDK, whose host enforces `max_payload`. This amends ADR 0030's `max_payload 1MB`.
