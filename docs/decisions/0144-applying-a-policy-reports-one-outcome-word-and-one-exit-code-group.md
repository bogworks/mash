---
status: proposed
date: 2026-11-16
decision-makers: Randy Bias (mash maintainer)
---

# Applying a policy reports one outcome word and one exit-code group

## Context and Problem Statement

The policy engine can be driven by a person at a terminal, by a script that reads JSON, or by a tool that wraps
`mashctl`. Today the same fault exits 1, 2 or 3 depending on the command, `policy diff` exits 1 for both a refused plan
and a failure, and an unchanged document is printed as a refusal ("refused: nothing would change"). A script cannot tell
"nothing to do" from "failed" without parsing text.

What words and exit codes does a command that applies or plans a policy use?

## Decision Drivers

* A script must decide what to do from the exit code alone, and from one `outcome` word in JSON.
* "Nothing to do" is a success, not a refusal.
* A new engine stage must force a decision about its outcome, not default to one.
* The words must be the same for every way of applying a policy.

## Considered Options

* One set of outcome words with fixed exit-code groups, a table that maps each engine stage, and a test that fails on an
  unmapped stage.
* Keep each command's own codes and document them.
* Exit 0 or 1 only.

## Decision Outcome

Chosen option: "One set of outcome words with fixed exit-code groups, a table that maps each engine stage, and a test
that fails on an unmapped stage", because a script can act on it without reading text and the table cannot silently
drift.

| Outcome | Exit | Meaning |
|---|---|---|
| applied | 0 | the gate passed on every server |
| planned | 0 | a dry run found nothing wrong; nothing changed |
| unchanged | 0 | the content is already in force; nothing changed |
| invalid | 1 | the document is malformed or breaks a rule |
| unsupported | 1 | the engine cannot do it in this release, such as narrowing an existing sender |
| stale | 1 | the base generation is not the one in force, or another generation is pending |
| blocked | 1 | a precondition fails: an address with no inbox, a size over a server's limit |
| partial | 5 | pushed, but the gate has not passed or the servers differ |
| error | 6 | a connection, a read or an internal failure; any unknown outcome |

* Usage errors exit 2 and a cancelled run exits 3, as for every `mashctl` command.
* Exit 4 is reserved for a signature that does not verify. A standalone `mashctl` submits no signature, so it never
  gives 4.
* An unchanged document is reported as "No changes needed", never as a refusal, and exits 0.
* Every outcome is also an `outcome` field of the JSON result, with the per-check results (document, base generation,
  size, servers) beside it.
* `internal/policydirect` holds the outcome words, the exit-code function and the single stage-to-outcome table; its
  test reads every stage constant of the engine and fails when one has no entry.

### Consequences

* Good: a script reads the exit code or the `outcome` word and never the text.
* Good: a new stage cannot ship without a decision.
* Bad: the codes differ from the ones some commands gave before; nothing is deployed, so nothing migrates.

### Confirmation

* `TestEveryStageMapsToOneOutcome` in `internal/policydirect`.
* The `internal/policydoc` golden tests for the plan, show and explain texts.
* `go run ./scripts/adrlint docs` passes.

## Pros and Cons of the Options

### One set of words and groups, with a table and a test

* Good: one contract, enforced by a test.
* Bad: a change to a group is a decision with an ADR.

### Keep each command's own codes

* Good: no change.
* Bad: the same fault keeps exiting with different codes.

### Exit 0 or 1 only

* Good: simple.
* Bad: a script cannot tell stale from a failed connection, or a partial apply from a refused one.

## More Information

* [ADR 0133](0133-the-issue-services-countersigner-is-a-requester-and-mash-carries-no-name-of-a-layer-above.md) and [ADR 0131](0131-mash-stands-alone-everything-works-without-homerange-or-any-upward-dependency-first.md) set the standalone model this contract serves; the table lives in `internal/policydirect/result.go`.
