---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# One exit rule for mash and mashctl, and the plain-URL and encryption-override refusals exit 1

Amends: ADR-0146

## Context and Problem Statement

[ADR 0146](0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md)
says a refused plain URL makes "`mash serve` exits 2 at session start; the hook ... exits 0; the CLI commands exit
2". Other refusals that are not about the command line also exit 2: no endpoint installed here, a join file that needs
the person's seed, and `mashctl endpoint delete` with no terminal to type the address in. Exit 2 also means "the command
line was wrong", so a script or an agent cannot tell a typo from a refusal of a well-formed command.

The CLI review (#1100) planned one exit rule for both tools in `spec/rules.json`, "One exit rule, the same in both
tools", and the rulings on #1700 (2026-11-25) named the cases that move from 2 to 1. Which code does each of
these refusals give?

## Decision Drivers

* A caller reads the exit code before the text: 2 must mean only "fix the command line".
* mash and mashctl give the same code for the same kind of outcome.
* `mash hook` must never fail the session that runs it
  ([ADR 0033](0033-claude-code-plugin-is-the-distribution-unit.md)).
* Each exception is named; none is left implicit.

## Considered Options

* Keep exit 2 for refusals that are not command-line errors, as ADR 0146 has it.
* One exit rule: 0 it worked, 1 it failed, 2 the command line was wrong and nothing was attempted (chosen).

## Decision Outcome

Chosen: one exit rule, the same in both tools.

* **The rule.** 0 it worked, 1 it failed, 2 the command line was wrong and nothing was attempted. No command exits with
  a code above 2. `status` and `doctor` exit 1 when they found a problem. A command that did part of its work exits 1.
* **Cases that move from 2 to 1:**
  * a refused plain URL (`nats://`, `ws://` or no scheme), in the CLI commands and in `mash serve` at session start;
  * a URL whose scheme is not supported, with or without the override;
  * a refused encryption override: the old names `MASH_INSECURE_PLAINTEXT` and `insecure_plaintext`, refused with an
    error that names `MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1`;
  * no endpoint installed here;
  * a join file that needs the person's seed;
  * `mashctl endpoint delete` with no terminal to type the address in, and no `--yes`.
* **Cases that stay 2:** they are command-line errors.
  * No endpoint chosen and several installed; the fix is `--endpoint`.
  * `mash serve` run by hand in a terminal.
  * A setting value that is not valid
    ([ADR 0151](0151-a-backlog-over-the-wake-count-or-age-wakes-the-session-whatever-the-kinds.md)).
* **The one exception.** `mash hook` exits 0 whatever happens, an unknown host included. This agrees with ADR 0146 and
  ADR 0033.

This amends the refusal paragraph of ADR 0146: the CLI commands and `mash serve` exit 1 for a refused plain URL, not 2.
The rest of ADR 0146 stands. `docs/install.md` and `docs/nats-server.md` say exit code 1.

### Consequences

* Good: exit 2 always means "fix the command line"; a refusal of a well-formed command is 1 in both tools.
* Good: a script needs one rule for mash and mashctl.
* Bad: a script that tested for 2 on these refusals must test for 1. The changelog lists the cases.
* Neutral: the text of every refusal is unchanged; only its code moves.

### Confirmation

* `cmd/mash/plaintext_refusal_test.go`:
  * `TestServeRefusesPlainURLAndExitsOne` (serve exits 1);
  * `TestCLIRefusesPlainURLExitOne` (the CLI commands exit 1);
  * `TestHookRefusesPlainURLSaysSoAndExitsZero` (hook exits 0);
  * `TestUnsupportedSchemeRefusedEvenWithTheOverride` (exit 1).
* The spec cases are checked by the conformance tests:
  * `spec/mash.json`: "No endpoint installed" and "The join file needs your seed", exit 1;
  * `spec/mashctl.json`: "No terminal and no --yes", exit 1;
  * "No endpoint chosen and several installed" stays exit 2.
* `spec/rules.json`, "One exit rule, the same in both tools".

## Pros and Cons of the Options

### Keep exit 2 for these refusals

* Good: no script changes.
* Bad: 2 means both "the command line was wrong" and "refused", so a caller cannot tell which without parsing text.

### One exit rule

* Good: the code alone says what kind of outcome it was, the same way in both tools.
* Bad: a breaking change in the exit code of six refusals.

## More Information

* Issue #1100 (CLI review); the spec PR #1700 and the rulings on it.
* Amends [ADR 0146](0146-every-mash-and-mashctl-connection-requires-tls-loopback-included-and-one-override-insecure-plaintext-allows-plain-text.md).
* Related: [ADR 0033](0033-claude-code-plugin-is-the-distribution-unit.md) (the hook exits 0),
  [ADR 0062](0062-log-is-rejected-until-implemented-check-runs-the-budget-case-variant-labels-warn-usage-errors-exit-2.md)
  (usage errors exit 2).
