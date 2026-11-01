---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (mash maintainer)
---

# Product code carries no personal convention; a ratchet lint enforces it

## Context and Problem Statement

mash is installed by strangers. Code that names a maintainer, a machine or a private project, assumes a home or temp
directory layout, points at a foreign GitHub org, or hard-codes a long timeout only works on the machine it was written
on. Reviewers do not catch these reliably, and a one-off cleanup decays. The tree today has a handful of such places and
no check that stops more.

## Decision Drivers

* Every path, name, host, org and long timeout in product code is a setting, flag or environment variable with a neutral default.
* The check must run in the existing `go test ./...`, with no new tool or CI step.
* Existing violations must not block unrelated work, but the count must never rise.

## Considered Options

* A review checklist only.
* A lint with a zero-tolerance rule, fixing every current hit first.
* A lint with a ratchet baseline: counts per rule and file that can only fall.

## Decision Outcome

Chosen option: the ratchet lint, as the plain `go test` package `internal/portable`, because it enforces the rule from
today without a clean-up first and shrinks to zero as hits are fixed.

* **Scan.** `*.go` under `cmd/`, `internal/` and `scripts/`, excluding `_test.go`, `testdata/`, `internal/testenv/` and
  `internal/portable/`, parsed with `go/parser` so comments never count; shell files under `scripts/` and
  `.github/workflows/*.yml`, by line.
* **Rules.** R1 a string literal naming a person, machine or private project (the pattern is `personalRE`; `bogworks`
  is the public org and is not on it). R2 a literal starting `~/`, `/tmp/`, `/home/` or `/Users/`, and calls to
  `os.UserHomeDir` or `os.Getenv("HOME")` outside `internal/config` and `internal/claudesettings`. R3 a literal naming
  a GitHub org other than `bogworks`. R4 an inline `<integer> * time.Minute` or `* time.Hour`, or a
  `time.ParseDuration` of a literal over 30 s, that is not the initialiser of a package-level const or var. R5 the R1
  pattern on non-comment lines of shell scripts and workflows.
* **Ratchet.** `internal/portable/baseline.txt` holds `<rule> <path> <count>` rows, sorted, each under a non-empty
  `# reason` comment. The test fails when a count exceeds its row (a new violation) and when a count is below its row or
  the row has no hits (stale: lower or delete it). `MASH_PORTABLE_UPDATE=1` lowers rows and refuses to raise any.
* The initial population carries the reason "baseline 2026-11-01". A new row needs a reason a stranger installing mash
  could not hit.

### Consequences

* Good, because new personal convention fails CI at the line.
* Good, because the baseline is a to-do list that only shrinks.
* Bad, because some R4 hits are unit arithmetic or validation bounds, not timeouts; they sit in the baseline until
  rewritten as named constants.
* Bad, because the rules are patterns: a name outside `personalRE` passes.

### Confirmation

`go test ./internal/portable/`: `TestPortableCode` runs the scan against the baseline; `TestRegexes`, `TestGoRules`,
`TestShellRule`, `TestBaselineParse` and `TestCompare` pin the rules on positive and negative inputs. CI runs it through
`go test ./...`.

## Pros and Cons of the Options

### A review checklist only

* Good, because it costs nothing to add.
* Bad, because it depends on every reviewer remembering it.

### A lint with a zero-tolerance rule

* Good, because the tree is clean at once.
* Bad, because it blocks the lint on a clean-up of unrelated code.

### A lint with a ratchet baseline

* Good, because it is enforced from the first day and cannot regress.
* Bad, because the baseline is one more file to keep honest.

## More Information

* Code: `internal/portable/`. Guide: the "Portable code" section of `CONTRIBUTING.md`.
