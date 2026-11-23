---
status: accepted
date: 2026-11-23
decision-makers: mash-lead (0.5.7 gate rulings, under the maintainer's release delegation); Randy Bias for dev-process rules 20-21
---

# The 0.5.7 gate rulings on scoped signing keys and on which Markdown is documentation

Amends: ADR-0124, ADR-0157, ADR-0158

## Context and Problem Statement

The 0.5.7 final gate asked for two rulings to be recorded. ADR 0158 says foreign scoped signing keys are still refused
without saying how that sits with ADR 0124, and ADR 0157 lists `SKILL.md` and the agent-instruction paths as code for the
docs-only classification, which the maintainer's later standard (`dev-process` rules 20 and 21, bogworks-dev-standards 0.8.0
and 0.8.1) overrules. Accepted ADRs are immutable, so the rulings are recorded here.

## Decision Drivers

* A reader of ADR 0158 must not read it as changing what ADR 0124 decided.
* The classifier of docs-only changes must follow one standard.

## Considered Options

* Edit ADRs 0157 and 0158 (refused by `adrlint`: an accepted ADR is immutable).
* Record both rulings in one new ADR that amends them.

## Decision Outcome

Chosen option: the second.

* **ADR 0124 stands, and 0.5.7 never carries a scoped key it did not make.** ADR 0124's drop, in an adopted account's
  first apply, of a foreign scoped signing key stands unchanged. ADR 0158 carries only plain foreign signing keys; a
  scoped one is refused or dropped as ADRs 0059 and 0124 say.
* **Every Markdown file is documentation.** `dev-process` rules 20 and 21 make every Markdown file documentation, so
  `SKILL.md` and the agent-instruction paths ADR 0157 lists are not code for the docs-only classification, and
  `scripts/ci-changes` follows the standard (every `*.md`, and every path under `docs/` except its data files, is documentation).

### Consequences

* Good: the gate's two open points have a record; no accepted ADR is edited.
* Bad: a change to an agent-instruction Markdown file now takes the docs queue and runs only the docs lint.

### Confirmation

* `scripts/ci-changes` and `internal/release/mergify_test.go` agree on the documentation definition; `adrlint` passes.

## Pros and Cons of the Options

### Edit ADRs 0157 and 0158

* Bad, because accepted ADRs are immutable; `adrlint` refuses the change.

### Record the rulings in one new ADR

* Good, because the record is checked and nothing accepted is rewritten.
* Bad, because a reader of ADR 0157 or 0158 must follow the link to see the amendment.

## More Information

Amends [ADR 0124](0124-the-reconciles-yes-acts-only-on-the-plan-the-person-read-and-an-adopted-accounts-first-apply-drops-the-signing-keys-mash-did-not-make.md),
[ADR 0157](0157-a-docs-only-pull-request-has-its-own-queue-and-needs-only-the-docs-lint.md) and
[ADR 0158](0158-mashctl-carries-the-accounts-plain-signing-keys-and-still-refuses-scoped-ones.md). Issue #1261; 0.5.7 final gate rulings of 2026-11-23.
