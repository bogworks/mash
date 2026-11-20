---
status: accepted
date: 2026-11-20
decision-makers: Randy Bias (mash maintainer)
---

# Further departures of the 0.5.6 specification, by the maintainer's word (ADR 0147)

## Context and Problem Statement

ADR 0147 records that 0.5.6 is built from the specification files in `spec/`, and lists what that supersedes. Reviews of the
specification keep finding further places where it departs from an earlier accepted record. Editing ADR 0147 for each one
would send its pull request back through the merge queue, and an accepted record cannot be changed afterwards. Where are
the later departures recorded?

## Decision Drivers

* An accepted ADR's body cannot change; each departure needs a record.
* The specification (`spec/rules.json` and the command files) is the single source and is built as written.

## Considered Options

* Edit ADR 0147 for each departure.
* Collect further departures in one new record, added to as they are found until it lands (chosen).

## Decision Outcome

Chosen: this record. By the maintainer's word, the specification reverses the following, and the code is built to the
specification:

* **ADR 0034:** `mash who` prints a headed table (columns, a header row, two spaces between columns), not tab-separated
  lines.
* **ADR 0049:** `--json` always prints an object, so `mash who --json` is `{"endpoints":[...]}`, not an array, and the empty
  result is `{"endpoints":[]}`.
* **Decision 264:** `mash serve` and `mash hook` never auto-pick an endpoint: they act only for the endpoint named by
  `--endpoint`, `MASH_ENDPOINT` or the `endpoint` key. With none named, `serve` offers only `status`, `doctor` and `version`,
  which answer "no endpoint is chosen for this session", and `hook` prints nothing. Choosing the only installed endpoint
  stays for the commands a person types. This narrows decision 264's auto-pick (the planner's call, flagged to the
  maintainer).

Departures found later are added here until this record is merged.

### Consequences

* Good: a script that parses `mash who` or `mash who --json` is told by the release notes, which list both as breaking.
* Bad: scripts written against the tab-separated or array output stop working in 0.5.6.

### Confirmation

The conformance test (planner's acceptance test C1) compares each command's output with the specification.

## Pros and Cons of the Options

### Edit ADR 0147 for each departure

* Bad: each edit requeues its pull request, and an accepted record cannot be edited after it lands.

### One new record that collects them

* Good: one place, added to until it lands.
* Bad: the record's list is open until it merges.

## More Information

ADR 0147 and the specification files in `spec/`.
