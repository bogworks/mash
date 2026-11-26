---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# Staticcheck findings fail the lint job in CI

Amends: ADR-0080

## Context and Problem Statement

[ADR 0080](0080-review-is-a-gate-in-the-implementers-loop-adopted-from-a-sibling-project.md) records staticcheck as
"advisory, as in mash's CI": the lint workflow ran it with `continue-on-error`, so a finding was listed and the job
still passed. The tree has since been brought to zero staticcheck findings at v0.8.2. An advisory check on a clean tree
lets new findings pile up again unseen. Should a staticcheck finding fail CI?

## Decision Drivers

* The tree is clean now, so a blocking check costs nothing today and keeps it clean.
* Code-quality checks that only warn are ignored; the project's best-practice criteria ask for warnings that fail.
* The local gate (`scripts/gate --pre-pr`) is a fast author aid, not the place to enforce.

## Considered Options

* Keep staticcheck advisory in CI.
* Make staticcheck fail the lint job in CI; leave `--pre-pr` advisory (chosen).
* Make staticcheck fail both CI and `--pre-pr`.

## Decision Outcome

Chosen: a staticcheck finding fails the lint job in CI. The staticcheck step in `.github/workflows/lint.yml` has no
`continue-on-error`. `scripts/gate --pre-pr` still runs staticcheck as advisory, as ADR 0080 says. A finding that must
stay, such as deliberate test input, carries a `//lint:ignore <check> <reason>` line.

This amends the "advisory, as in mash's CI" words of ADR 0080 for CI only. The rest of ADR 0080 stands.

### Consequences

* Good, because a new staticcheck finding is seen on the pull request that adds it.
* Bad, because a staticcheck upgrade that adds checks can fail lint on unchanged code until the findings are fixed or
  ignored with a reason.
* Neutral, because the lint workflow is not yet a required queue check (#1782), so a failing lint job does not by itself
  stop a merge until it is.

### Confirmation

`.github/workflows/lint.yml`: the staticcheck step has no `continue-on-error`. `go run
honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...` exits 0 on the tree.

## Pros and Cons of the Options

### Keep staticcheck advisory in CI

* Good, because nothing can fail on a staticcheck upgrade.
* Bad, because findings pile up unseen, as they did (63 by v0.8.2).

### Fail CI, `--pre-pr` advisory

* Good, because CI enforces and the author's fast loop stays fast and forgiving.
* Bad, because an author can push a finding that `--pre-pr` only warned about.

### Fail both

* Good, because the author sees the failure before pushing.
* Bad, because `--pre-pr` then blocks on findings in code the author did not touch after a staticcheck upgrade.

## More Information

* Amends [ADR 0080](0080-review-is-a-gate-in-the-implementers-loop-adopted-from-a-sibling-project.md).
* #1781 fixed the 63 findings and removed `continue-on-error`; #1782 tracks making lint a required queue check.
