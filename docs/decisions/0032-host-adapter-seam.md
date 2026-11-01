---
status: accepted
date: 2026-10-28
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# A host adapter seam isolates the agent host; Claude Code is the only host

## Context and Problem Statement

Hook delivery (ADR-0024) depends on details of the agent host, the program that runs the model session: how a hook is
invoked and what it reads on stdin, which process owns a session's spool, how mail is presented as context, and how
an idle session is woken. In the first build these details were Claude Code's, spread through `mash serve` and
`mash hook`. Other hosts were wanted later (issue #43 names Hermes and Pi), and the original spec kept other agents a
non-goal that "the design doesn't preclude" (spec §1.2).

## Decision Drivers

* Another host must be addable without touching delivery, ack or spool logic.
* No change in behaviour for Claude Code sessions.
* An unknown host must fail loudly, never fall back silently to Claude Code's behaviour.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Keep Claude Code specifics inline until a second host exists
* Extract a host adapter interface now, with Claude Code as the only implementation

## Decision Outcome

Chosen option: "extract a host adapter interface now", because it was "a pure refactor, so issue #43 (Hermes, Pi) can
plug in later" (PR #45).

As built (mash layer), `internal/host`:

* `host.Adapter` has `Name`, `Trigger` (read a host hook invocation), `OwnerToken` and `Owns` (who may drain a
  spool), `Present` (render spooled mail for the host), `SpoolsFromStart` and `Waker` (shape the wake event).
* `SpoolsFromStart` (PR #79, 2026-10-29) marks a host that takes no channel delivery at all: `serve` then spools from
  the first message and holds mail unacknowledged rather than deliver it by a channel the host never sees.
* `MASH_HOST` selects the adapter; empty or `claude` is Claude Code, the only one built. An unknown name is an error
  listing the known hosts, "never a fallback" (PR #45).

### Consequences

* Good, because a second host is a new adapter, not a change to the delivery engine.
* Good, because the refactor was made with no change in behaviour (PR #45's title says "behaviour unchanged").
* Bad, because the interface was shaped by one host, so a second host may still need it changed.
* Neutral, because issue #43's durable mode (explicit receive and ack MCP tools, a mode declared at startup) is
  deliberately not built; the package comment says the interface will grow for it.

### Confirmation

* `internal/host/claude_test.go`: `TestSelect`, `TestClaudeTrigger`, `TestOwnsStopsAtTheOwner`,
  `TestClaudeSpoolsFromStart`.
* `internal/endpoint/spool_from_start_test.go`: `TestSpoolFromStartHoldsUntilDrained`,
  `TestSpoolFromStartReWakesWithoutDrain`, `TestSpoolFromStartWithoutSpoolHoldsMail` (the no-channel path).
* No test exercises a second adapter, since none is built.

## Pros and Cons of the Options

### Inline until a second host exists

* Good, because no abstraction is built before its second user.
* Bad, because host details would stay mixed with delivery and ack logic, where a change risks losing mail.

### Adapter interface now

* Good, because the host boundary is explicit and testable on its own.
* Bad, because it is an abstraction with one implementation.

## More Information

* PR #45 (2026-10-28), PR #79 (2026-10-29); issue #43 (open: portable durable delivery for other hosts, which records
  the receive/ack tool set and the startup-declared durable mode as options).
* Design spec §1.2 (non-goals).
* A consumer needed sessions on more than one agent host.
* Related: ADR-0024 (hook delivery), ADR-0025 (the spool), ADR-0033 (the Claude Code plugin).
