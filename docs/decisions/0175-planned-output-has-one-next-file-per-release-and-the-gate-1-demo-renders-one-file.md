---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer)
---

# Planned output has one next file per release, and the Gate 1 demo renders one file

## Context and Problem Statement

[ADR 0164](0164-planned-output-is-written-in-x-output-next-and-spec-next-json-and-rendered-as-the-gate-1-demo.md) keeps planned cases that have no `x-output` home in one file, `spec/next.json`, and renders them as the Gate 1 demo. The SDK adoption contract (revision 8) plans 17 cases for 0.6.0 while the 0.5.9 installer and setup cases are still planned. In one file the two releases are mixed, and the demo cannot say which release it shows.

## Decision Drivers

* A Gate 1 demo shows one release, labelled with it.
* `spec/next.json` should keep holding the release being closed.
* The planner's rule B: the 0.6.0 cases leave `next.json`.

## Considered Options

* Keep one `spec/next.json` with a release field on every case.
* One planned-cases file per release; the demo renders one file.

## Decision Outcome

Chosen option: one file per release. This amends ADR 0164 as follows.

* `spec/next.json` holds the planned cases of the release being closed (0.5.9). The cases of a later release are in `spec/next-<release>.json` (`spec/next-0.6.0.json`). Each file carries its release as `info.version`.
* `go run ./scripts/gate1demo -next <file>` renders one file and labels it with that release. A bare name is looked up in `-spec`; a value with a folder is used as given.
* The `x-output-next` cases inside `spec/mash.json` and `spec/mashctl.json` have no release label of their own and are rendered with every file. A per-case release field is not added.

### Confirmation

`go test ./scripts/gate1demo` covers `-next` selecting a file, and requires the cases of the real `spec/next.json`.

### Consequences

* Good: each demo is one release with one label.
* Good: closing 0.5.9 empties `next.json` without touching the 0.6.0 cases.
* Bad: command cases in `mash.json` and `mashctl.json` show in every file's demo until their code lands.

## Pros and Cons of the Options

### One file with a release field on every case

* Good: one place to look.
* Bad: a new field on every case, in a specification the planner owns, and a filter in the demo.

### One file per release

* Good: no new field; the file is the label.
* Bad: a second file to know about.

## More Information

* ADR 0164 and ADR 0167. Contract: SDK adoption Gate 1, revision 8. Pull request #1560; issue #1577.
