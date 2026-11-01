---
status: accepted; amended by ADR-0041 (the re-wake backoff stops when no hook has run)
date: 2026-10-28
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Wake timing: a 45 s idle wake with backoff, at once before the first hook, and a backlog wake

## Context and Problem Statement

Under hook delivery (ADR-0024), `mash serve` holds mail in a local spool (ADR-0025) until the session's next hook run
shows it. A busy session runs hooks after every tool call; an idle one runs none, so `serve` must wake it with a
channel event, which costs a model turn. mash had to decide how long held mail waits before that wake, what happens
when a wake starts no turn, what a fresh session does before its first hook, and what stops mail that never wakes
(ADR-0027) from piling up until the inbox loses messages.

## Decision Drivers

* Never add a turn to a session that is busy but slow between tool calls.
* An idle recipient should see waking mail about as fast as the earlier channel push delivered it.
* A wake that lands no turn must be retried, but not at a fixed fast rate.
* Mail held unacknowledged must never reach the limits at which NATS refuses or discards mail.

## Considered Options

* A 5-minute idle wake (built first, PR #22)
* A 45-second idle wake, configurable (PR #69)

## Decision Outcome

Chosen option: "a 45-second idle wake, configurable", because under the 5-minute wake mail to an idle session "waited 5
minutes" while the earlier channel push had been immediate (PR #69). The 5-minute value was the operator's default and
"a conservative guess ... so as never to add a turn to a session that is busy but slow between tool calls. For a
truly idle session it buys nothing" (maintainer design discussion, 2026-10-28, not public). The value 45 s was chosen
without a recorded reason.

As built (mash layer), in `internal/endpoint/hook.go`:

* Idle wake: once waking mail has been held 45 s with no hook drain, `serve` sends one channel event carrying the
  summary line alone. Set by `MASH_IDLE_WAKE` or `idle_wake` in `config.json`, 5 s to 30 min.
* Re-wake backoff (PR #47): while waking mail stays held and no hook drains, the next wake comes at twice the idle wake,
  then doubling, capped at 30 minutes. Any drain resets it. The 30-minute cap was chosen without a recorded reason.
* Before the first hook (PR #93): in a session spooled from the start, waking mail rings at once, each message once,
  and that ring does not start the backoff, so the idle wake still follows if it starts no turn.
* Backlog wake (PR #75): when held mail reaches 500 messages (half the inbox consumer's `MaxAckPending` of 1000) or a
  quarter of the inbox stream's 16 MiB byte limit (4 MiB, headers included), or the spool's byte cap has turned mail
  away, `serve` wakes at once to drain, even if all of it is `result` or `fyi`. The reason is recorded: so "waking mail
  behind it can still arrive", and 4 MiB is "well below the inbox stream's 16 MiB limit ... at which NATS discards
  old messages" (PR #75). The fractions one half and one quarter were chosen without a recorded reason.

### Consequences

* Good, because a busy session is never woken: its hooks drain the spool first.
* Good, because a missed wake is retried, and a session left idle for hours is woken at most every 30 minutes.
* Good, because a flood of `fyi` mail cannot push waking mail out of reach or past the stream's discard limit.
* Bad, because an idle recipient's mail is still up to 45 s late, against a near-immediate channel push.
* Neutral, because urgent mail (ADR-0029) skips the idle wait entirely.

### Confirmation

* `internal/endpoint/hook_test.go`: `TestHookIdleWake`.
* `internal/endpoint/hook_resilience_test.go`: `TestHookReWakesWithBackoff`, `TestWakeBackoffDoublesToTheCap`,
  `TestBusySessionGetsNoWake`, `TestIdleSessionWakesOnceAfterTheConfiguredWake`.
* `internal/endpoint/prehook_wake_test.go`: `TestSpoolFromStartWakesAtOnceBeforeTheFirstHook`,
  `TestSpoolFromStartPreHookRingDoesNotStartTheBackoff`, `TestSpoolFromStartWaitsForTheIdleWakeAfterAHook`.
* `internal/endpoint/kind_test.go`: `TestFyiBacklogAtTheCountWakes`, `TestFyiBacklogAtTheWakeBytesWakes`,
  `TestSpoolFullWakes`, `TestSpoolWakeBytesIsWellBelowTheInboxLimit`.
* `internal/config/config_test.go`: `TestIdleWakeSettings` (the 5 s to 30 min range).
* The release gate (`docs/release.md`) measures fresh-session mail latency against the previous release.

## Pros and Cons of the Options

### 5-minute idle wake

* Good, because it almost never wakes a session that is only slow between tool calls.
* Bad, because every message to a truly idle session waited 5 minutes.

### 45-second idle wake, configurable

* Good, because idle recipients see mail within about a minute, and an operator can tune it per deployment.
* Bad, because a session that pauses longer than 45 s between tool calls may get an extra turn.

## More Information

* PRs #22 (5-minute idle wake, 2026-10-28), #47 (backoff), #69 (45 s, 2026-10-28), #75 (backlog thresholds,
  2026-10-29), #93 (ring before the first hook, 2026-10-29).
* `docs/operations.md`, "Hook delivery".
* Related: ADR-0010 (inbox limits), ADR-0024, ADR-0025, ADR-0027, ADR-0029.
