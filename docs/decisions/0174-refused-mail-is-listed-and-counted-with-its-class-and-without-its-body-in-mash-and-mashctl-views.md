---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer; contract 0.6.0 SDK adoption r8a3, REQ-006, CHG-14)
---

# Refused mail is listed and counted, with its class and without its body, in mash and mashctl views

## Context and Problem Statement

[ADR 0147](0147-the-0-5-6-command-line-and-mcp-surface-is-built-from-a-specification-that-supersedes-contract-revision-26.md) amends [ADR 0019](0019-five-trust-labels-forged-never-shown-stale-after-five-minutes.md): unverified mail is refused on receipt by default, and a refused message stays out of the inbox listing and its count. Today only `mash doctor` says anything about it, in a `warn` row that counts unverified mail. A person whose mail was refused (a malformed sender header, or a sender who is not verified) sees an inbox with nothing wrong in it. Contract 0.6.0 SDK adoption r8a3 (REQ-006, CHG-14) asks for every view to account for refused mail. Which views show it, and what do they show of it?

## Decision Drivers

* A refusal nobody can see is a silent loss; the person needs to know that mail arrived and was refused, and why.
* The body of a refused message is the sender's untrusted data and is never shown ([ADR 0021](0021-message-bodies-are-shown-quoted.md) and ADR 0019's rule that forged mail is never shown).
* `mashctl` is not assumed to sit beside `mash`, so the count has to be readable from the endpoint's own views.
* ADR 0147's default (refuse unverified mail) is not what is in question.

## Considered Options

* Keep refused mail out of every listing and count (ADR 0147 as written).
* List and count refused mail with its class and no body, in the `mash` and `mashctl` views (chosen).

## Decision Outcome

Chosen: list and count it. This amends ADR 0147 on this one point; ADR 0147's default of refusing unverified mail stands, and so does ADR 0019's rule that a refused message's body is never shown.

* `mash inbox` and MCP `inbox` list every refused entry still in the stream as a row with the state `refused`, sender `?` and its class as the reason, no body and no preview, and count it in the header (target output TO-1, TO-2).
* `mash read <number>` and MCP `read` on a refused message answer with the refusal and its class, never the body (TO-3).
* `mash status --detail`, `mash doctor` and their JSON give the total and a count per class (TO-4, TO-5). The `doctor` refused row is `ok` whatever it counts, so it never changes doctor's verdict or exit status; today's `warn` for unverified mail becomes `ok`.
* `mashctl endpoint inbox` lists every message; a message refused for a malformed header has the trust `refused` and its class as the reason, and unverified mail keeps its trust label and gets its class as the reason (TO-6).
* `mash status` (the one line) is unchanged.

### Consequences

* Good: a refusal is visible where the person looks, with the reason, without reading the sender's data.
* Good: the doctor row stops warning about something that is working as designed.
* Bad: the inbox listing and its count are no longer only mail the person can act on; a refused row is a different kind of row (TO-1 draws it beside the others).
* Bad: this changes the planned output in `spec/mash.json`, `spec/mashctl.json` and `spec/next.json` (planner-owned; rendered as Gate 1, [ADR 0164](0164-planned-output-is-written-in-x-output-next-and-spec-next-json-and-rendered-as-the-gate-1-demo.md)) and the stored refusal records (REQ-006).

### Confirmation

The conformance test over `spec/*.json` and the acceptance tests of contract r8a3 (AT-007) check each view's wording and counts when the cases are promoted from `spec/next.json`.

## Pros and Cons of the Options

### Keep refused mail out of every listing and count

* Good: the inbox stays only what the person can act on.
* Bad: a refusal is invisible except in one `warn` row that covers unverified mail alone; malformed-header refusals are not shown at all.

### List and count refused mail with its class and no body

* Good: nothing is refused silently, and no untrusted body is shown.
* Bad: a new row kind in four views and a new stored record.

## More Information

* Contract 0.6.0 SDK adoption r8a3 (commit 65d598be): REQ-006, CHG-14, target output TO-1 to TO-6.
* [ADR 0019](0019-five-trust-labels-forged-never-shown-stale-after-five-minutes.md) and [ADR 0147](0147-the-0-5-6-command-line-and-mcp-surface-is-built-from-a-specification-that-supersedes-contract-revision-26.md): the refusal default this amends on visibility only.
