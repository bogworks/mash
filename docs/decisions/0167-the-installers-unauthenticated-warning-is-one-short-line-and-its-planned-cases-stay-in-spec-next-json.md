---
status: accepted
date: 2026-11-24
decision-makers: Randy Bias (mash maintainer)
---

# The installer's unauthenticated warning is one short line, and its planned cases stay in spec/next.json

## Context and Problem Statement

In 0.5.9, `install.sh` checks the release attestation only with `gh` 2.102.0 or newer (#1165, #1153). With no `gh`, a `gh` that does not run, one whose version cannot be read, or one older than 2.102.0, it installs on the release's checksums alone and warns. [ADR 0150](0150-releases-carry-install-and-local-nats-setup-scripts-as-attested-assets.md) says that warning states that the checksums come from the same download and so trust github.com's TLS. The 0.5.9 Gate 1 ruling, recorded as REQ-014 in contract 0.5.9 revision 6, says the warning is one short line on stderr, and that how to authenticate lives in `docs/install.md`, not in the line.

[ADR 0164](0164-planned-output-is-written-in-x-output-next-and-spec-next-json-and-rendered-as-the-gate-1-demo.md) says a planned case leaves `spec/next.json` when its code lands. The installer has no `x-output` home in the specification, so removing its cases would drop their text, stream and exit code from the specification, and the Gate 1 demo could no longer show them.

## Decision Drivers

* REQ-014 (contract 0.5.9 revision 6): one short warning line on stderr that says the download was not authenticated and was checked against the release's checksums only.
* A person sees one line, not a paragraph. How to verify by hand is in one place, `docs/install.md`.
* The specification keeps every output case, with its stream and exit code, until the release closes.

## Considered Options

* Keep ADR 0150's long warning, which explains the same-download caveat.
* One short warning line on stderr, with the explanation in `docs/install.md`.

## Decision Outcome

Chosen option: one short line. This amends ADR 0150 and ADR 0164 as follows.

* Amends ADR 0150: every fallback prints one line on stderr, `warning: <reason>: checked checksums only, not verified`, then installs. The reasons are `gh not found`, `gh did not run`, `gh version unreadable` and `gh is below 2.102.0`. The same-download caveat and the steps to verify by hand are in `docs/install.md`. A pass prints `Verified the release attestation for <repo> <tag>` on stdout. A failure prints `error: the release failed verification; nothing was installed` on stderr and exits 1.
* Amends ADR 0164: the installer's cases stay in `spec/next.json`, with the text the code prints, until the specification is closed for 0.5.9, because the installer has no `x-output` home. `docs/spec-next.md` says so.

### Confirmation

`scripts/install_sh_test.go` asserts each line exactly, on its stream, with its exit code. `go test ./scripts/gate1demo` requires the installer cases in the real `spec/next.json`.

### Consequences

* Good: the warning reads in one line, and says what the person needs to know: checksums only, not verified.
* Good: the specification and the Gate 1 demo still show the installer's output.
* Bad: the line no longer says why checksums alone are weaker. A person has to open `docs/install.md` to learn that.

## Pros and Cons of the Options

### Keep ADR 0150's long warning

* Good: the warning itself explains why checksums from the same download are weaker.
* Bad: several lines of warning on every install without a new enough `gh`, against REQ-014.

### One short warning line on stderr

* Good: one line a person reads, on the stream warnings belong on, as REQ-014 asks.
* Bad: the reason checksums alone are weaker is one link away, in `docs/install.md`.

## More Information

* Requirement: contract 0.5.9 revision 6, REQ-014 (decision 291, Randy, option b, 2026-11-23, applied by the planner's Gate 1 ruling, option A, 2026-11-24).
* Issues #1165, #1153, #1152 and #1136; pull request #1274.
