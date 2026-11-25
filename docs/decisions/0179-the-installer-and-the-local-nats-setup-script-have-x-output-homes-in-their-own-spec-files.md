---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer)
---

# The installer and the local NATS setup script have x-output homes in their own spec files

## Context and Problem Statement

[ADR 0167](0167-the-installers-unauthenticated-warning-is-one-short-line-and-its-planned-cases-stay-in-spec-next-json.md) kept the installer's output cases in `spec/next.json`, because the installer had no `x-output` home in the specification. The setup script's cases (#1151, #1287) were kept there for the same reason. `spec/next.json` is meant for planned changes ([ADR 0164](0164-planned-output-is-written-in-x-output-next-and-spec-next-json-and-rendered-as-the-gate-1-demo.md)), so released output sat among planned output, and nothing held its text to the scripts. Issue #1577 asks for a home for these cases.

## Decision Drivers

* Every output case lives where the specification describes released behavior, with its text, stream and exit code.
* `spec/next.json` carries only what the specification has no other place for.
* A test fails when a script's text and its specified case drift apart.

## Considered Options

* Keep the cases in `spec/next.json` (ADR 0167).
* Give each script its own spec file, in the OpenCLI shape of `spec/mash.json`, with an `x-output` list.

## Decision Outcome

Chosen option: a spec file per script. `spec/install.json` describes `install.sh` and its `MASH_*` variables; `spec/mash-local-nats-setup.json` describes `mash-local-nats-setup.sh` and its options. Each carries its released cases in `x-output` and may carry planned ones in `x-output-next`, which the Gate 1 demo renders. This amends ADR 0167: the installer's cases leave `spec/next.json` for `spec/install.json`. ADR 0164's rule holds for the scripts as for the commands: a planned case moves into `x-output` when its code lands.

### Confirmation

`scripts/spec_scripts_test.go` requires every line of every case in the two files to be in the script's source, on its stream, with each varying value (repository and tag, port, pid, folder) named per case; `TestSetupSpecExitCodes` checks the setup script's exit codes. `scripts/install_sh_test.go` still runs the installer and asserts each line exactly. `go test ./scripts/gate1demo` reads the two files for `x-output-next`.

### Consequences

* Good: the scripts' output is specified as released behavior, and a change to a message fails a test until the spec moves with it.
* Good: `spec/next.json` holds planned changes only.
* Bad: two more spec files to keep at the specification's version. `scripts/refgen` does not render them, so the command reference does not show the scripts.

## Pros and Cons of the Options

### Keep the cases in spec/next.json

* Good: no new files.
* Bad: released and planned output are mixed, and the cases must be kept there by exception at every release.

### A spec file per script

* Good: one place per program, in the shape the other spec files use.
* Bad: the test checks the source text, not a run; varying values need a table per case.

## More Information

* Issue #1577; ADR 0164; ADR 0167; issues #1151, #1152, #1274, #1287, #1352.
