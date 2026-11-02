---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# `log:` is rejected until implemented, `policy check` runs the budget, case-variant labels warn, usage errors exit 2

## Context and Problem Statement

A negative sweep of `mashctl` (issue 185) found places where the tool says less than the truth or accepts what it
ignores: a rule's `log: true` is accepted and does nothing; `policy check` prints OK for a file whose account JWT is over
the 1 MiB cap that `show --budget`, `add` and `apply` refuse; label values that differ only in case are silently
distinct; and some verbs return exit 1 for a usage error where the rest return 2, and ignore unknown flags. Each one lets
an operator believe something is true that is not.

## Decision Drivers

* A setting that has no effect must not look like a control (`log:` suggests refusals are recorded).
* The validator must agree with the commands that refuse, or "check passes" means little.
* A typo in a label value is the likeliest way a rule silently covers nobody.
* A script must tell "you called it wrong" (2) from "it failed" (1).

## Considered Options

* Leave each as it is and document it.
* Reject or warn on each, keeping the schema and the CLI surface unchanged.

## Decision Outcome

Chosen option: reject or warn on each, with no new verb or flag.

* A rule with `log: true` is an error from `Check`: `rule NAME (line N): log is not implemented yet; remove it`. The
  field stays in the schema, so the message is clear and not an unknown-field error. The error goes when the feature
  exists.
* `mashctl policy check` also runs the size budget (`policy.Budget`) and fails with the same error text as
  `show --budget` when the account JWT or a user JWT is over its limit. When it passes it prints the same OK line. The
  owner it compiles with is the configured one, else a placeholder; the size differs by a few bytes per subject only.
* `Check` warns, without failing, when a label key has values that differ only in case across endpoints
  (`Finance`, `finance`): the warning names the key, both spellings and their lines. Selectors still compare exactly.
* Every `mashctl` verb exits 2 for a usage error (wrong arguments, an unknown flag, a stray argument, an argument that
  fails validation) and 1 for an operational failure. `mash VERB --help` prints the verb's usage line and exits 0;
  `mash status` and `mash version` refuse extra arguments with exit 2.
* An argument that fails validation is named, never printed back: a seed pasted into `--user` must not reach a terminal
  or a log. A well-formed name that is merely unknown may still be shown.
* `mashctl deprovision NAME` for a name with no record and no inbox prints `no endpoint named NAME` and exits 1. The
  admin operation stays idempotent, because rollbacks call it; only the verb looks first.
* `mashctl permissions NAME` prints a note on stderr, so the JSON and the `--nsc` flags on stdout stay parseable, that
  its lines are for hand-made legacy users and that endpoints added under a policy file get narrower permissions.

### Consequences

* Good: what `check` accepts is what `apply` accepts; no silent no-op field; case typos surface.
* Good: scripts can branch on 2 versus 1; a pasted secret is not echoed.
* Bad: a policy file that carries `log: true` stops passing `check` until the line is removed. It never had any effect.
* Bad: `check` is slower for a large file (it compiles and measures the JWTs).
* Bad: a script that treated exit 1 from a mistyped `suspend` as the only failure code now sees 2.

### Confirmation

* Tests: `log: true` is refused with the exact message and `log: false` is accepted; a file of 200 zones fails
  `policy check` with the budget text; `Finance` and `finance` warn and exit 0; each verb's usage error exits 2 and an
  unknown flag is refused; a value shaped like a seed never appears in output; `deprovision` of a missing name exits 1.

## Pros and Cons of the Options

### Leave each as it is and document it

* Good, because nothing changes for existing files and scripts.
* Bad, because `log:` keeps looking like a control, and a passing `check` keeps meaning less than `apply`.
* Bad, because a pasted seed is still echoed.

### Reject or warn on each

* Good, because each false belief is replaced by a clear message at the earliest command.
* Good, because it adds no flag or verb, within the CLI freeze.
* Bad, because a file with `log:` must be edited, and a script that keyed on exit 1 must learn 2.

## More Information

* Issue 185. ADR 0053 (the policy file and its Check); the budget is the one `show --budget`, `add` and `apply` already run.
