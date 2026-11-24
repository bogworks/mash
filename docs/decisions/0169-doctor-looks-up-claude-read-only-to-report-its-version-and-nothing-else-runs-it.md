---
status: accepted
date: 2026-11-24
decision-makers: Randy Bias (mash maintainer)
---

# Doctor looks up claude, read-only, to report its version; nothing else runs it

## Context and Problem Statement

`mash doctor` gains a `claude` row (#1130, REQ-017, release rule 26): it fails below the oldest Claude Code mash is tested with, warns above the newest series, skips when there is no `claude` command, and warns when the version cannot be read. To say any of that, doctor has to find `claude` and ask it for its version. `TestNoForeignToolInSource` (`cmd/mash/standalone_test.go`, from [ADR 0131](0131-mash-stands-alone-everything-works-without-homerange-or-any-upward-dependency-first.md)) forbids every non-test Go file under `cmd/` and `internal/` from running, looking up or importing a foreign tool or `claude`.

## Decision Drivers

* The specification's five cases for the row need the version of the `claude` on the path; the MCP `initialize` handshake gives it only inside a session, and the shell has no session.
* ADR 0131 is about mash not depending on anything above it. A health check that reads a version is not a dependency: nothing breaks, and nothing is started, without `claude`.
* The guard should stay as tight as it was for everything else.

## Considered Options

* Allow-list one file in the guard, with the reason written down.
* Read the version only from the MCP client information, and drop the shell cases.
* Do not check at all, and stop promising a minimum.

## Decision Outcome

Chosen option: allow-list one file. This amends the guard that ADR 0131 introduced.

* `cmd/mash/claudecheck.go` may look up `claude` on the path and run `claude --version`, with a short timeout, to fill the doctor row. It reads the version and nothing else.
* No other non-test file may run or look up `claude` or any other foreign tool. `TestNoForeignToolInSource` allows exactly that one path.
* The programs never refuse to run because of the result: status and serve are unchanged, and doctor only reports.
* The oldest and the newest versions it compares against live in `internal/host/claude.go` (`MinClaudeCode`, `TestedClaudeSeries`), and a test checks that `docs/compatibility.md` states the same two values.

### Confirmation

`TestNoForeignToolInSource` fails for any other file that looks up or runs `claude`. The doctor tests run the five cases against fakes and never against a real `claude`.

### Consequences

* Good: the promise of a minimum Claude Code is checked where a person looks for it, and the bounds are in one place.
* Good: the guard keeps its meaning; the exception is one named file and one read-only call.
* Bad: doctor now touches a foreign command, once, with a timeout; a hung `claude` costs up to that timeout.

## Pros and Cons of the Options

### Allow-list one file

* Good: all five cases work at a shell and in a session.
* Bad: a named exception to a guard.

### MCP client information only

* Good: no lookup at all.
* Bad: the shell cannot tell "no claude" from "no session", so the specified cases cannot be shown there.

### No check

* Good: nothing to maintain.
* Bad: the minimum stays a promise nothing checks (#1130).

## More Information

* Requirement: contract 0.5.9 revision 6, REQ-017 (planner's Gate 1 ruling, 2026-11-24).
* Issue #1130; amends ADR 0131.
