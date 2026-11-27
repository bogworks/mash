---
status: accepted
date: 2026-11-27
decision-makers: the planner (a planner ruling of 2026-11-27, which Randy Bias, the mash maintainer, may overturn)
---

# A lane changes spec/ only in a PR the lead reviews, and the planner sees changes to what a person sees

Amends: ADR-0147

## Context and Problem Statement

[ADR 0147](0147-the-0-5-6-command-line-and-mcp-surface-is-built-from-a-specification-that-supersedes-contract-revision-26.md)
says the files in `spec/` "are kept in the repository unchanged from the planner's copy; the planner is their only
editor and nothing else goes in `spec/`". That clause was the planner's own arrangement to protect the specification the
maintainer ratified on 2026-11-20. As far as the planner can find, it is not the maintainer's words. It became a
bottleneck, and practice had already left it behind: #1851, #1930 and #1944 were lane edits to `spec/` that the lead
reviewed.

How is `spec/` edited so that the review is real, and what a person sees still matches the ratified specification?

## Decision Drivers

* What a person sees (a command, a flag, an output line, an exit code, an error text) must not change unchecked.
* The planner should not be the only editor of files that follow the program.
* The rule should match how `spec/` is already changed.

## Considered Options

* Keep the planner as the only editor of `spec/`.
* Let any lane change `spec/` in a pull request the lead reviews, and send the planner the diff when behaviour changes.
* Let any lane change `spec/` with no further rule.

## Decision Outcome

Chosen option: "Let any lane change `spec/` in a pull request the lead reviews, and send the planner the diff when
behaviour changes", because it keeps one reviewer on every change and one more on the changes that matter, and it
describes what already happens.

1. A lane changes `spec/` only in a pull request that the lead reviews and labels `decision-confirmed`.
2. The planner gets the `spec/` diff before merge for any change of command behaviour: a command, flag, output line,
   exit code or error text that a person sees.
3. A change that only records what the program already does, or a version string, needs no diff to the planner.
4. Nobody changes what a person sees without checking it against the specification the maintainer ratified on
   2026-11-20.

This amends only the editor clause of ADR 0147. The rest of ADR 0147 stands, and ADR 0147 is not edited. If the
maintainer ruled that clause himself, this amendment goes to him.

### Consequences

* Good, because the review of `spec/` no longer waits on one person.
* Good, because a change to what a person sees still reaches the planner before it merges.
* Bad, because the line between "behaviour" and "records what the program does" is a judgement the lead makes.

### Confirmation

The `decision-confirmed` label on a pull request that touches `spec/` shows rule 1. Rule 2 shows in the pull request's
record that the planner got the diff.
