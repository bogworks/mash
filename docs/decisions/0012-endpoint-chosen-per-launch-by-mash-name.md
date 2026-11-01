---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# An endpoint is chosen per launch by `MASH_NAME`; without it the server is inert

## Context and Problem Statement

mash ships as a Claude Code plugin whose MCP server (Model Context Protocol, how the agent host runs tool servers),
`mash serve`, starts with every session where the plugin is enabled. Only some sessions should be mail endpoints, and
each endpoint session must know which of the provisioned names it is. mash had to decide where a session's identity
comes from, and what `mash serve` does in a session that has none.

## Decision Drivers

* The plugin should be safe to enable for every session.
* The name must be chosen by whoever launches the session, before the session starts.
* A session without mail should still be able to tell its user why.

## Considered Options

* The name comes from the `MASH_NAME` environment variable, set per launch; unset means inert
* A default name derived from the user and directory (`<user>-<dir>`), planned but not built

## Decision Outcome

Chosen option: "The name comes from `MASH_NAME`, set per launch; unset means inert", because "the plugin can then be
enabled everywhere without effect on unnamed sessions" (spec §7.1). No reason was recorded for not building the
planned default name.

* With `MASH_NAME` unset, `mash serve` answers MCP, connects to nothing and says so in its instructions.
* Since 2026-10-30 (pull request #119), an inert server still offers two tools, `status` and `version`, because a
  session that "gets no tools at all" leaves the model unable to "tell the user what's wrong".
* The command-line tools (`mash send`, `mash inbox` and others) use the same `MASH_NAME` identity, and never take the
  lease (ADR-0013), so they work alongside the running session.
* A consumer's launcher sets the name per session.

### Consequences

* Good, because installing the plugin everywhere costs nothing in sessions that are not endpoints.
* Good, because the launcher, which knows the session's purpose, chooses its identity.
* Good, because `status` explains a missing name, config, credentials or connection in any session.
* Bad, because a session started without the variable silently has no mailbox until someone checks `status`.

### Confirmation

* `internal/config/config_test.go` `TestInertWithoutName` and `TestMissingURLIsErrorUnlessInert`.
* `cmd/mash/main_test.go` `TestServeInertWithoutName`.
* `cmd/mash/status_tool_test.go` `TestStatusToolInert` and `TestStatusToolInstructionsMentionIt`.

## Pros and Cons of the Options

### `MASH_NAME` per launch, inert without it

* Good, because no session becomes an endpoint by accident.
* Bad, because every endpoint launch must set the variable.

### Default name from user and directory

* Good, because a session would get a mailbox with no setup.
* Bad, because every default name would still have to be provisioned with its own NATS user, or the session fails.

## More Information

* Design spec §7.1 step 1 and §10 (configuration).
* `docs/install.md` (inert server with `status` and `version`; the CLI never takes the lease).
* Code: `internal/config/config.go` (`Inert`), `cmd/mash/serve.go`, `cmd/mash/status.go`.
* The planned default name: maintainers' working notes, 2026-10-30 (not public).
* Pull request #119 (`status` in every mode).
* Related: ADR-0013 (one live session per name), ADR-0036 (name syntax).
