---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# `mash doctor` checks every installed endpoint when none is chosen

Amends: ADR-0187

## Context and Problem Statement

[ADR 0187](0187-one-exit-rule-for-mash-and-mashctl-and-the-plain-url-and-encryption-override-refusals-exit-1.md) keeps
"no endpoint chosen and several installed" as a usage error: exit 2, `error: more than one endpoint is installed here:
a, b`, and a hint naming `--endpoint`. That is right for `send`, `inbox`, `read`, `who`, `prompt` and `status`: each acts
for one endpoint, and guessing which would send mail as the wrong one. `mash doctor` acts for none: it reads and reports
(#1785). A person with two endpoints who types `mash doctor` to see whether the install is healthy gets an error instead
of the answer. Should doctor stay a usage error here?

## Decision Drivers

* Doctor changes nothing, so checking every endpoint is safe where choosing one for `send` is not.
* The question "is my install healthy?" has an answer for the whole machine.
* One exit rule: 0 it worked, 1 a problem was found, 2 the command line was wrong.
* A script that reads `mash doctor --json` for one endpoint must not change.

## Considered Options

* Keep exit 2 for doctor, as ADR 0187 says.
* Check the first installed endpoint only.
* Check every installed endpoint and total the result (chosen).

## Decision Outcome

Chosen: for `mash doctor` only, several endpoints installed and none chosen (no `--endpoint`, no `MASH_ENDPOINT`) is no
longer a usage error. Doctor checks every installed endpoint, each exactly as `mash doctor --endpoint <address>` would,
and exits 0 when none has a problem, or 1 when any endpoint has a problem, or a warning with `--strict`. The commands
that act for one endpoint, `status` included, keep exit 2 and their hint.

* **Text.** `Endpoints installed here: N.`, then a table of endpoint, problem count, warning count and first issue
  (`<check>: <detail>` of the first failing row, else of the first warning), sorted by address and fitted to 79
  columns, then `No problems.` or the totals with `See it: mash doctor --endpoint <address>` for the first endpoint with
  a problem, else a warning. The hint is printed once, in that last line, and not again on stderr; the JSON `hint`
  field is null, because `--endpoint` shows a result and fixes nothing (ADR 0168).
* **JSON.** `{"endpoints":[...],"problems":N,"warnings":N,"hint":null}`: each endpoint's object as `--endpoint` prints it,
  and totals. With one endpoint installed, or one chosen, the object is unchanged.
* **The claude row** is machine-wide. It is run once and is in every endpoint's rows, so a problem in it counts once for
  each endpoint.
* **The MCP `doctor` tool** always has its endpoint and is unchanged.

This amends the "stays 2" case of ADR 0187 for doctor only. The rest of ADR 0187 stands.

### Consequences

* Good, because a bare `mash doctor` answers on a machine with several endpoints.
* Good, because the one hint leads from the overview to one endpoint's full rows.
* Bad, because a script that relied on exit 2 from a bare `mash doctor` to learn that several are installed now sees 0
  or 1.
* Neutral, because the totals count a machine-wide problem once for each endpoint.

### Confirmation

`cmd/mash/doctor_several_test.go` runs a bare `mash doctor` with two installed endpoints (clean, one problem, one
warning, `--strict`, `--json`), checks that each endpoint's object equals the `--endpoint` output, and that `status` still
exits 2. `spec/mash.json` records the output.

## Pros and Cons of the Options

### Keep exit 2 for doctor

* Good, because every command answers the same way.
* Bad, because the one command that cannot do harm refuses to answer.

### Check the first endpoint only

* Good, because the output is unchanged in shape.
* Bad, because it hides the other endpoints' problems and picks one silently.

### Check every installed endpoint

* Good, because the answer covers the install.
* Bad, because the JSON has a second shape when several are installed.

## More Information

* Amends [ADR 0187](0187-one-exit-rule-for-mash-and-mashctl-and-the-plain-url-and-encryption-override-refusals-exit-1.md).
* #1785.
