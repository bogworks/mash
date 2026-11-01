---
status: accepted; the ownership clause is superseded by ADR-0040
date: 2026-10-28
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# A local spool hands mail from serve to the hook; ack only once shown; at-least-once

## Context and Problem Statement

With hook delivery (ADR-0024), mail reaches the model through `mash hook`, a short-lived command Claude Code runs after
every tool call and with every prompt. The mail itself arrives at `mash serve`, the long-lived endpoint manager
(ADR-0022), from a NATS JetStream pull consumer, which redelivers any message not acknowledged ("acked") in time.
mash needed a way to pass mail from serve to the hook, and a rule for when serve acks. The first design acked when a
channel event was written, and mail written before Claude Code took channel input was acked and lost (issue #16).

## Decision Drivers

* "The ack is 'seen'": a message is acked only after a hook has shown it (design approval condition, PR #21).
* The hook runs on every tool call, so it must be cheap.
* A message must never be lost; showing one twice is the lesser harm.
* Another session under the same name, or a subagent, must not take this session's mail.

## Considered Options

Recorded in the design discussion of 2026-10-27/28 (not public) and in PRs #21 and #84:

* A hook that reads NATS itself.
* Acks sent by the model once it has read the mail.
* A local spool written by serve and drained by the hook, at first used only after the first hook run, with the
  channel delivering until then (PR #21).
* The same spool used from the session's first message (PR #84).

## Decision Outcome

Chosen option: "a local spool, from the first message under the plugin", because a hook reading NATS "runs on every
tool call", too costly for each call; acks by the model were rejected because "if it made it to a durable queue it's
effectively there" (maintainer, 2026-10-27, not public); and spooling from the start is needed because "`claude -p`
stream-json drops channel events that arrive before the first user message, so that mail was acked and lost" (PR #84).

As built (mash layer):

* serve writes each message to `$XDG_STATE_HOME/mash/spool/<owner>/<name>/<seq>.json` (directories 0700, files 0600)
  and holds it unacked, renewing its 30-second ack deadline.
* The hook makes no NATS connection; an empty spool costs one directory read. It records `shown/<seq>`, prints the
  batch, then marks `drained/<seq>`. serve acks a drained message, and drops the mark once the server confirms.
* At-least-once: a hook killed between print and mark shows the batch again, each message flagged "re-delivered
  after an interrupted hook". A redelivered message already marked drained is acked, not shown again.
* The spool is bound to the serve that holds the lease: the hook checks that this serve's Claude Code process is
  among its own parent processes, and ignores a subagent's `PostToolUse` (its input carries `agent_id`).
* Bounds: the consumer's MaxAckPending (1,000 messages) and `SpoolMaxBytes` (32 MiB). Past either, mail stays unread
  in NATS. The value 32 MiB was chosen without a recorded reason.
* serve computes the trust label (ADR-0019) and stores it in the spool file, so the local user account is the trust
  boundary of the hand-off.

### Consequences

* Good, because an ack now means a hook printed the mail, and the inbox's unread count shows the rest.
* Good, because the hook costs no network call on any tool call.
* Bad, because a message can be shown twice; the model is told when that may have happened.
* Bad, because anyone who can write the spool directory as that user can plant mail with any label.

### Confirmation

* `internal/spool/spool_test.go`: `TestPermissions`, `TestDrainedThenAcked`, `TestShownMarksRedelivery`, `TestOwn`.
* `internal/endpoint/hook_test.go`: `TestHookRedeliveryOfADrainedMessageIsAcked`,
  `TestHookFullSpoolLeavesMailInTheInbox`, `TestHookReprovisionDoesNotAckNewMailByAnOldTombstone`.
* `internal/endpoint/hook_resilience_test.go`: `TestAckKeepsTheTombstoneUntilConfirmed`,
  `TestHookUnwritableSpoolFallsBackToTheChannel`, `TestHookSpoolFileSelfHeals`.
* `internal/endpoint/spool_from_start_claude_test.go`: `TestSpoolFromStartClaudeHoldsMailSentBeforeAnyHook`.
* `cmd/mash/hook_crash_test.go`: `TestHookCrashMatrix`; `cmd/mash/hook_chain_test.go`:
  `TestHookOwnershipThroughARealProcessChain`, `TestHookShownOnceAcrossAServeKilledBeforeAck`;
  `cmd/mash/hook_test.go`: `TestHookIgnoresASubagent`.

## Pros and Cons of the Options

* **Hook reads NATS:** no local state; but a connection on every tool call.
* **Model acks:** proves the model read it; but costs a tool call per batch and makes the model part of the protocol.
* **Spool after the first hook:** works without hooks; but mail before the first hook rode the channel and could be lost.
* **Spool from the start:** nothing acked unshown; but a session without hooks gets mail only by wake and prompt.

## More Information

* PR #21 (spool, drain, fallback), #47 (lost ack, lost wake, failing or damaged spool), #49 (at-least-once made
  visible, ownership by process chain), #84 (spool from start, closes issue #16). All 2026-10-28 except #84 (10-29).
* Docs: `docs/operations.md` "Hook delivery". Code: `internal/spool/spool.go`, `internal/endpoint/hook.go`,
  `cmd/mash/hook.go`, `internal/host/claude.go`, `plugin/.mcp.json`.
* Related: ADR-0009 (the inbox consumer), ADR-0019 (trust labels), ADR-0024 (hook delivery), ADR-0028 (wake timing).
