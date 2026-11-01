---
status: accepted
date: 2026-10-29
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Text output and the plugin's commands are a compatibility contract

## Context and Problem Statement

mash's interface is mostly text: the rendered mail a session sees, the lines `mash who` prints, and the command lines
the Claude Code plugin runs. Other programs depend on that text. A downstream parser "broke on the `kind:` line"
when mash 0.2.5 added it to rendered mail (PR #99), and a plugin hook that ran a command an older binary lacked
blocked every prompt on the machine (PR #66). mash had to decide which text is a contract and how a change to it is
caught before release.

## Decision Drivers

* A change to parsed output must never ship unnoticed.
* An installed plugin at version N runs against mash binaries N and N-1, because several sessions share one Claude
  Code configuration directory (`cmd/mash/plugin_hook_test.go`).
* Changes must stay possible; the contract makes them deliberate, not forbidden.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* No contract: document the formats and let consumers adapt
* Machine-readable output (for example JSON) for programs, free-form text for people
* Pin the existing text and command lines in golden fixtures and gate releases on them

## Decision Outcome

Chosen option: "pin the existing text and commands in fixtures, gated at release", because of the two breakages above
(PR #99, PR #66). Why golden fixtures rather than a structured format was not recorded.

As built (mash layer):

* Render contract (PR #99, 2026-10-29): `internal/render/testdata/contract.txt` pins, byte for byte, what
  `render.One` (the full message) and `render.Line` (the one-line form) print for a fixed set of messages. Release
  check refuses a release whose fixture differs from the previous release's unless a commit since then has a subject
  starting with `render-contract:`; that subject lands in the release notes (`docs/release.md`).
* Plugin commands (PR #66, 2026-10-29): a golden list of the plugin's hook commands and MCP server command, with
  `pluginMinMash` (`0.2.0`), the oldest mash that has every command they run. A new command may ship only once the
  previous release has it. Release check also runs the plugin against the candidate and previous binaries.
* `mash who` (PRs #104, #110): its default lines (`<name>\t<online|offline>...`) stay unchanged as metadata is added;
  new fields go to `who --long` and the MCP `who` tool, "because [a consumer] parses `who`" (PR #104).
* Changes do happen through the gate: PR #170 (2026-10-31) changed how senders are shown (ADR-0036) and called it
  out with a `render-contract:` commit, although the fixture's own cases print the same text as before.

### Consequences

* Good, because a change to parsed text is visible in review and in release notes.
* Good, because an upgrade or rollback of either the plugin or the binary keeps sessions working.
* Bad, because every output change costs a fixture update and a callout, and a parser still breaks if its owner
  ignores the notes.
* Neutral, because only the listed fixture cases are pinned; text outside them can change without the gate.

### Confirmation

* `internal/render/contract_test.go`: `TestRenderContract`.
* `scripts/releasecheck/main_test.go`: `TestContractVerdict`, `TestRenderContractCallouts`,
  `TestCheck_renderContractStep`.
* `cmd/mash/plugin_hook_test.go`: `TestPluginCommandsAreFrozen`.
* `cmd/mash/meta_test.go`: `TestAdminProvisionAndSetMetaCLI`, `TestWhoLongAndMCPWhoShowMetaAndLease` (default `who`
  lines unchanged); `cmd/mash/roundtrips_test.go`: `TestCLIWhoDefaultFormat`.
* `internal/release/plugin_test.go`: `TestHookCommands`, `TestCheckHookMatchers`, `TestMCPCommand`.

## Pros and Cons of the Options

### No contract

* Good, because output can change freely.
* Bad, because parsers break silently, as one did with the `kind:` line.

### Machine-readable output

* Good, because programs get a stable structure that can grow.
* Bad, because it is a second output format to build and keep, and a session reads the text form anyway.

### Golden fixtures with a release gate

* Good, because it pins what is already parsed, with little new code.
* Bad, because the gate checks only the cases in the fixture.

## More Information

* PRs #66, #99 (2026-10-29), #104 (2026-10-30), #110 (2026-10-30), #170 (2026-10-31).
* `docs/release.md` (the render contract and plugin steps).
* A consumer parsed the rendered mail and the `who` lines.
* Related: ADR-0027 (the `kind:` line), ADR-0033 (the plugin), ADR-0036 (how senders are shown).
