---
status: accepted
date: 2026-11-23
decision-makers: Randy Bias (mash maintainer)
---

# Planned output is written in x-output-next and spec/next.json, and rendered as the Gate 1 demo

## Context and Problem Statement

The 0.5.8 contract (REQ-018, REQ-019) requires every output the release changes to be written in the specification before the code, and a demo rendered by a script from the specification's own cases (Gate 1). The conformance tests and `scripts/refgen` hold the code to the specification's `x-output` cases, so a case written there before its code lands fails the tests.

## Decision Drivers

* The specification comes before the code; the tests stay green until the code lands.
* The demo is rendered from the specification, never written by hand.
* No change to what 0.5.6 and 0.5.7 promise.

## Considered Options

* Put planned cases in `x-output` with a release marker the tests honour (a change to every conformance test and to `refgen`).
* Keep the planned text in a document outside the specification.
* Put planned cases beside the current ones: `x-output-next` on a command, and `spec/next.json` for changes not tied to one command.

## Decision Outcome

Chosen option: the third. A command carries planned cases in an `x-output-next` metadata entry (same shape as `x-output`, with `issue`, an optional `replaces` naming a current case, and a `note`); changes with no command (the installer, the setup script, the policy document, rules) are in `spec/next.json`, with `rules_next` for amended rules. The tests and `refgen` ignore both. `scripts/gate1demo` renders them as Markdown, with the current text beside the planned text, and refuses to write if a line passes 80 columns or a text looks like a secret. When the code lands, the case moves into `x-output` with the release marker in the same change and leaves the next-places. The format is in `docs/spec-next.md`.

### Consequences

* Good: the specification and the demo agree by construction, and the tests stay green until each code change.
* Good: no existing case, release marker or version moves.
* Bad: a planned case is not checked against code until it moves into `x-output`.
