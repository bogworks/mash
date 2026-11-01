---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# One long-lived endpoint manager per session holds the connection; the session acts through its tools

## Context and Problem Statement

A Claude Code session needs to send, read and answer mail, and to receive it, all with low latency and without spending
model turns on plumbing. Something has to hold the NATS connection, the lease (ADR-0013), the SAP service registration
(ADR-0030) and the inbox consumer (ADR-0009) for the session's lifetime. mash had to decide where that state lives and
how the session reaches it.

## Decision Drivers

* "Fast. One long-lived process per session and one connection. A send is one tool call and one network round trip,
  with no subprocess." (spec §1.1, goal 1)
* Receiving mail needs a process that is already connected when mail arrives.
* Shells and scripts still need a way in without a session.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* One long-lived endpoint manager per session, reached through MCP tools.
* A command-line subprocess per action, connecting each time.
* One shared daemon per machine serving every session on it.

## Decision Outcome

Chosen option: "one long-lived endpoint manager per session", because of goal 1 quoted above (spec §1.1).

As built (mash layer):

* `mash serve` is an MCP server over stdio (Model Context Protocol, the standard way a tool server talks to an AI
  host; ADR-0023). Claude Code starts it from the mash plugin, one per session, and it exits when stdin closes.
* It holds one NATS connection and reconnects forever, waiting 2 seconds plus up to 5 seconds of random jitter,
  because "the server rate-limits new connections per source" (spec §7.1).
* The session acts through its tools: `send`, `read`, `inbox`, `who`, `reply`, `version` and `status`.
* The `mash` command line remains the path for shells and scripts (spec §11.1). Its NATS round trips per command were
  later cut from tens, growing with the number of endpoints, to under six, flat (PR #116), because a consumer needed
  command-line calls to stay fast over a high-latency link.

### Consequences

* Good, because a send is one tool call and one round trip on an open connection.
* Good, because the same process that receives mail can push a wake or a notice to the session at once.
* Bad, because each session runs its own process and connection, so connections grow with sessions, not machines.
* Neutral, because the process's life is the session's: when Claude Code exits, the lease is released and unacked mail
  is redelivered to the next session.

### Confirmation

* `cmd/mash/serve_roundtrips_test.go`: `TestServeStartupRoundTrips`.
* `cmd/mash/roundtrips_test.go`: `TestCLIRoundTrips`.
* `internal/mcp/server_test.go`: `TestToolsListAndCall`, `TestRunEndsWhenInputCloses`.
* `cmd/mash/standalone_test.go`: `TestStandaloneOnlyNATS`.

## Pros and Cons of the Options

### Long-lived manager per session

* Good, because it is connected before mail arrives and stays connected.
* Bad, because a crashed manager takes the session's mail path down until the session restarts.

### Subprocess per action

* Good, because there is no long-lived state to manage.
* Bad, because every action pays a connect, and nothing is listening when mail arrives.

### Shared daemon per machine

* Good, because one connection serves many sessions.
* Bad, because it must keep identities apart in one process, and the one-user-per-endpoint model (ADR-0007) then
  shares a process across endpoints.

## More Information

* Spec `docs/specs/2026-10-21-mash-design.md` §1.1 (goal 1), §7 and §7.1, §11.1.
* PR #116 (command-line round trips).
* Code: `cmd/mash/serve.go`, `internal/endpoint/endpoint.go` (`connect` options).
* Related: ADR-0007 (one NATS user per endpoint), ADR-0009 (inbox stream and consumer), ADR-0012 (`MASH_NAME`),
  ADR-0013 (the lease), ADR-0023 (mash's MCP server), ADR-0030 (SAP agents).
