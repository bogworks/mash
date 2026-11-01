---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Sessions are full SAP agents: heartbeat presence, status and live prompts; only the CLI starts a prompt

## Context and Problem Statement

SAP (the Synadia Agent Protocol) defines an agent as a NATS micro service (NATS's request/reply service framework)
named `agents`, with a `prompt` endpoint for live requests, a `status` endpoint, and a signed heartbeat that other
tools use for presence. mash adds durable mail (ADR-0003), but a mash session could also be an ordinary SAP agent
that any SAP tool can find, check and prompt. mash had to decide how much of SAP's agent surface a session carries,
and who may start a live prompt.

## Decision Drivers

* Design goal 5: "Any SAP tool sees mash sessions as ordinary SAP agents" (spec §1.1).
* Presence (who is online) should come from SAP's own heartbeat, not a mash invention.
* A live prompt makes the session do work now, so it must come from a proven sender.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Mail only: register no SAP service
* Register a SAP service for discovery and heartbeat only
* Register the full SAP agent: heartbeat, `status` and `prompt`

## Decision Outcome

Chosen option: "register the full SAP agent", because of design goal 5 (spec §1.1) (reconstructed from the goal; no
comparison was recorded). Why the session has a tool to answer a prompt but none to start one is not recorded.

As built (SAP layer inherited, mash choices noted), in `internal/endpoint/service.go`:

* Each running session registers the SAP service `agents` with its `prompt` and `status` endpoints, and publishes a
  heartbeat every 30 s, signed per SAP §13.11. The heartbeat declares both `prompt` and `mail` (ADR-0004).
* Presence: an endpoint is online if a verified heartbeat was seen within 90 s, three heartbeat intervals (SAP §8.2;
  `internal/endpoint/directory.go`).
* `prompt` metadata: `max_payload 1MB`, `attachments_ok false`, `min_sender_trust signed`. An unverified sender gets
  `401`, a malformed header `400`, and (a mash addition) a verified sender that is not a mash endpoint in the
  directory gets `403`.
* While the session has not answered, `serve` sends a `working` status every 30 s, so "the caller's 60 s inactivity
  window (§6.6)" stays open (spec §7.4). After 10 minutes with no answer it ends the prompt with error `500`. The
  value 10 minutes was chosen without a recorded reason.
* A prompt reaches the session only as a channel event naming its `request_id`; the session answers with its MCP
  `reply` tool, which can stream partial answers.
* Starting a prompt is the CLI's alone: `mash prompt <to> <text>`. The session's MCP tools are `send`, `read`,
  `inbox`, `who`, `reply`, `status` and `version`.

### Consequences

* Good, because SAP tooling can discover, check and prompt mash sessions without knowing mash.
* Good, because presence needs no mash-specific record; it rides on SAP's heartbeat.
* Bad, because the `prompt` path is not durable: it depends on the channel event reaching the session, and a lost
  event leaves the caller waiting 10 minutes for a `500`.
* Bad, because a SAP tool that is not a provisioned mash endpoint cannot prompt a session (`403`), which narrows
  "ordinary SAP agent".
* Neutral, because live prompts and mail are separate paths: a prompt is answered now or not at all, mail waits.

### Confirmation

`internal/endpoint/service_test.go`: `TestRegistrationMetadata`, `TestHeartbeatSigned`, `TestWhoShowsOnline`,
`TestStatusReply`, `TestDiscoverAndPromptReply`, `TestPromptEnvelopeJSON`, `TestPromptUnsigned401`,
`TestPromptBadEnvelope400`, `TestPromptWorkingKeepaliveAndTimeout`, `TestReplyUnknownRequest`,
`TestPromptNothingAfterTerminator`.

## Pros and Cons of the Options

### Mail only

* Good, because there is less to build and no live path that bypasses the durable inbox.
* Bad, because SAP tools could not see sessions or their presence.

### Discovery and heartbeat only

* Good, because SAP tools see presence without a live request path.
* Bad, because SAP's main verb, `prompt`, would be missing.

### Full SAP agent

* Good, because it meets goal 5 as written.
* Bad, because it carries a second, non-durable delivery path alongside mail.

## More Information

* Design spec §1.1 (goal 5) and §7.4 (SAP registration); SAP 0.3 §6, §8 and §13.11.
* Code: `internal/endpoint/service.go`, `internal/endpoint/directory.go`, `cmd/mash/serve.go`, `cmd/mash/cli.go`.
* Related: ADR-0003, ADR-0004, ADR-0016 (the lease as a health record), ADR-0024 (what the channel still carries).
* **Not yet true:** besides a verified heartbeat seen within 90 s, the code also marks an endpoint seen when its
  `$SRV.INFO` registration answers at startup with a valid `id_sig` and the directory's key (`internal/endpoint/directory.go`,
  `bootstrapPresence`). The code is a superset of this decision.
