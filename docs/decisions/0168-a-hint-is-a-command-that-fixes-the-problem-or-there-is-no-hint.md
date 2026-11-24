---
status: accepted
date: 2026-11-24
decision-makers: Randy Bias (mash maintainer)
---

# A hint is a command that fixes the problem, or there is no hint

## Context and Problem Statement

Until 0.5.8, a doctor that found a problem ended with one hint, and the hint could be a command or one plain sentence naming who or what fixes the problem. Two decisions rely on that sentence form. [ADR 0163](0163-config-json-records-the-configured-signing-keys-public-key-and-a-lost-signing-key-is-never-replaced-by-the-identity-key.md) says the refusal for a lost signing key names `mashctl init --config <file>` and the same `signing_key`. [ADR 0166](0166-doctor-health-covers-mashs-own-users-account-audit-is-explicit-audit-users.md) says `mashctl doctor --audit-users` ends with `hint: revoke it with the tool that issued it (mashctl revokes only its own)`.

The 0.5.9 Gate 1 ruling on REQ-006 (contract 0.5.9 revision 6; the planner's ruling on draft 4, 2026-11-24) says a hint is a command of ours that fixes the problem, or there is no hint and the output's own words say what is wrong. A person can run what a hint says; a sentence in the hint slot is advice in the wrong place. The ruling is about a doctor's own hint. The planner also accepted errors whose hint is a doctor (REQ-008's `hint: mash doctor`, and the signing refusals' `hint: mashctl doctor` in draft 5b): there the doctor shows what is wrong and gives the hint that fixes it.

## Decision Drivers

* A person can copy and run every hint line.
* One rule for both doctors, with no named exceptions.
* An error may send the person to the doctor, which says what is wrong.
* What is wrong is said once, in the error or the row, not again in the hint.

## Considered Options

* The ruled rule, with ADR 0163's and ADR 0166's sentences changed to fit it.
* The ruled rule, with ADR 0166's sentence kept as a named exception.

## Decision Outcome

Chosen option: the ruled rule, with no exceptions. It binds the hint a doctor prints (the specification's rule "Both doctors work the same way"). Any other command's error hint is a command of ours. That command fixes the problem, or it is the doctor, which says what is wrong. The planned cases in `x-output-next` and `spec/next.json` follow this.

* Amends ADR 0163: the signing refusals print `error: <what>` and `hint: mashctl init --config <file>`, for example `error: signing.nk and signing.pub are missing in <dir>`. The hint no longer repeats "the same signing_key": the setup file the command reads is the one that names it, and `<dir>` is the key folder as resolved. The long keys are not printed, neither in default output nor under `--detail` or `--json`.
* Amends ADR 0166: `mashctl doctor --audit-users` prints no hint. Its `keys` FAIL row already says what is wrong: `1 connected user was not issued by mashctl:`, then each key. mashctl has no command that revokes a user it did not issue, so there is no command to give.

### Confirmation

The planned cases in `spec/mashctl.json` (`x-output-next`, "--audit-users finds a user mashctl did not issue") and `spec/next.json` (the `signing-*` items) show the output. The conformance tests check them when the code lands and the cases move to `x-output`.

### Consequences

* Good: every doctor hint is a command that fixes the problem.
* Bad: an error that hints the doctor takes two steps to a fix, as the planner accepted for REQ-008 and the signing refusals.
* Good: one rule, with nothing to remember beside it.
* Bad: a person who meets a user issued elsewhere gets no pointer to the tool that issued it. The row says only that the user is not mashctl's.

## Pros and Cons of the Options

### The ruled rule, with no exceptions

* Good: every hint can be run.
* Bad: the audit-users row loses its pointer to the issuing tool.

### The ruled rule, with ADR 0166's sentence as a named exception

* Good: the audit-users pointer stays.
* Bad: the rule needs an exception that every later case has to check against.

## More Information

* Requirement: contract 0.5.9 revision 6, REQ-006; the planner's Gate 1 ruling on draft 4 and draft 5b (2026-11-24); the coordinator's ruling (#948).
* Pull request #1393; ctl #1397 (#1390) implements the signing form.
