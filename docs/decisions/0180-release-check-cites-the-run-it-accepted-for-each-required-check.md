---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer; issue #1627)
---

# release-check cites the run it accepted for each required check

## Context and Problem Statement

`scripts/release-check` accepts a candidate when the merge queue's test run on it passed, or, for a docs-only candidate, when the nearest tested commit below it on `main`'s first parent passed and every commit between is docs-only ([ADR 0160](0160-macos-runs-on-tagged-releases-only-and-the-ephemeral-pilot-runs-nightly.md) sets what runs when). It prints `ok    CI check runs succeeded` and nothing about which run that was. The 0.5.9 release record needed a run to cite, found none in the gate's output, and cited a hand-started nightly. The nightly gates nothing, so the record pointed at evidence the gate never read. How does the gate give the record the run it actually accepted?

## Decision Drivers

* A release record must cite the evidence the gate used, not a look-alike.
* The gate's verdict must not change: this is about what it says, not what it accepts.
* The citation must come from the same code that decides, so the two cannot drift.
* The output must stay readable and the report's step and problem counts stay as they are.

## Considered Options

* A. Print the accepted run for each required check, under the CI step.
* B. Change the gate to read the nightly runs.
* C. Document in `docs/release.md` which run to cite, and change nothing in the gate.

## Decision Outcome

Chosen option: "A". Under `ok    CI check runs succeeded`, `release-check` prints one line per required check, in the order of `release.RequiredChecks`:

```text
      <name>: check run <id> <html_url> on <first 12 characters of the commit>
```

When the deciding commit is not the candidate (a docs-only candidate over a tested ancestor) the line ends with ` (tested ancestor of the docs-only candidate, <n> docs-only commit(s) above it)`. With no `html_url` the line omits the URL: `<name>: check run <id> on <commit>`. The accepted run is the highest-id completed, successful, not-skipped run of the GitHub Actions app on the deciding commit (`release.CitedRuns`); `release.CheckCIWalkCited` returns it from the same walk that decides, and `CheckCIWalk` is a wrapper that drops it. The lines are not steps and add no problem. `docs/release.md` says a release record cites these lines and that a queue run is the record, never a nightly or a hand-started run.

### Consequences

* Good, because the record cites the run the gate read.
* Good, because the verdict is unchanged: the walk is the same code.
* Bad, because the output gains a few lines per release check.
* Risk accepted: nothing stops a person citing another run; the printed lines are the reference, and a reviewer compares them.
* Reversible: remove the printing and the two functions; no state is kept.

### Confirmation

* `TestCitedRuns` and `TestCheckCIWalkCited` in `internal/release/docsonly_test.go`: the latest tested run wins over a later skipped one, a queue success followed by a skipped push run is cited, another app's run and a failed run are never cited, a docs-only candidate returns its tested ancestor and the count above it, and a direct pass returns zero above.
* `TestCheck_printsTheAcceptedRunForEachRequiredCheck` in `scripts/releasecheck/main_test.go`: the output holds the citation line for each required check.

## Pros and Cons of the Options

### A. Print the accepted runs

* Good, because the evidence comes from the decision itself.
* Bad, because it adds output.

### B. Read the nightlies

* Good, because a nightly run would then have a reason to be cited.
* Bad, because it changes what the gate accepts, and a nightly does not test the commit the queue fast-forwards to.

### C. Documentation only

* Good, because it needs no code.
* Bad, because the gate still names no run, and the next record can repeat the mistake.

## More Information

Issue #1627. [ADR 0160](0160-macos-runs-on-tagged-releases-only-and-the-ephemeral-pilot-runs-nightly.md). `internal/release/docsonly.go`, `scripts/releasecheck/main.go`, `docs/release.md`.
