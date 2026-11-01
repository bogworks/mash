---
status: accepted
date: 2026-10-28
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# The lease doubles as a zero-turn health record, and claims nothing about delivery

## Context and Problem Statement

A supervisor, meaning any process that watches a fleet of sessions, needs to know whether a session's mash is alive
and which build it runs. The only way to ask before this decision was to mail the session and wait for an answer,
and every such probe cost the session a model turn. Each session already holds a lease, an entry in a NATS KV bucket
(NATS's key-value store) that `mash serve` refreshes every 30 seconds (ADR-0013). A consumer needed "a zero-turn
reading of delivery up to the session" (PR #14).

## Decision Drivers

* Watching a session must cost it no model turn.
* What the record says must be true: it may not claim more than mash can observe.
* Additive: older readers ignore fields they do not know.

## Considered Options

Recorded in PR #14 and its review:

* Mail probes that the session's model answers (the practice being replaced).
* Lease fields that record what serve can observe, including a `channels` field saying channel delivery is on.
* Lease fields that record what serve can observe, with no field that claims delivery.

## Decision Outcome

Chosen option: "lease fields with no delivery claim", because a probe costs a turn, and because "no client message
can prove channels are enabled": a stub MCP server saw identical messages from Claude Code 2.1.312 with and without
`--channels` (PR #14, review round 2). The `channels` field was built and then removed in review.

As built (mash layer), the lease value holds `instance_id`, `host`, `pid`, `since`, `start` (process start time),
`machine`, `launch_token` (from `MASH_LAUNCH_TOKEN`, added 2026-10-24), `version`, `commit` and `mcp_seen`. serve
sends its MCP client a JSON-RPC `ping` request at start and every 30 seconds, waits up to 10 seconds, and records an
answered ping as `mcp_seen`; the next lease refresh carries it. The code calls `mcp_seen` "not a delivery signal".
The values 30 seconds and 10 seconds were chosen without a recorded reason.

### Consequences

* Good, because a supervisor reads liveness, build and MCP-client health from one KV read, with no mail and no turn.
* Good, because the fields are optional (`omitempty`), so an older serve or reader is unaffected.
* Bad, because the strongest signal is that the MCP client answers serve: it does not prove the session took a channel
  event in, that the model can take a turn, or that it acted on mail.
* Neutral, because `mcp_seen` lags by up to 30 seconds and is absent in a session's first moments; readers treat an
  absent value as unknown.

### Confirmation

* `cmd/mash/retry_test.go`: `TestServeRecordsMCPSeenAndChannelsInTheLease` (no `mcp_seen` before an answered ping, a
  fresh one after, and no `channels` field).
* `internal/mcp/server_test.go`: `TestPingRoundTrip`, `TestPingUnansweredTimesOut`.
* `internal/endpoint/endpoint_test.go`: `TestLeaseCarriesVersionAndCommit`, `TestLeaseCarriesTheLaunchToken`.
* `cmd/mash/live_claude_test.go`: `TestLiveClaudeAnswersServePings` (opt-in, against a real Claude Code).

## Pros and Cons of the Options

### Mail probes answered by the model

* Good, because an answer proves the whole path, model included.
* Bad, because every probe spends a model turn in every watched session.

### Lease fields including `channels`

* Good, because it would have told a supervisor whether channel delivery was on.
* Bad, because serve cannot know it: the field was set on a signal that a client without channels also passes.

### Lease fields with no delivery claim

* Good, because every field is something serve observed.
* Bad, because it leaves the last hop, the model, unobserved.

## More Information

* PR #14 (`mcp_seen`, review round 2 removed `channels`); commit `b5e1f9a` (`launch_token`, 2026-10-24).
* Code: `internal/endpoint/endpoint.go` (`leaseValue`), `cmd/mash/serve.go` (`pingClient`), `internal/mcp/server.go`
  (`Ping`).
* With hook delivery (ADR-0024, ADR-0025) the inbox consumer's unread count counts mail until a hook has shown it, which
  covers more of the path than `mcp_seen` does.
* Related: ADR-0013 (the lease), ADR-0014 (waiting and takeover), ADR-0023 (mash's MCP server).
