---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (mash maintainer)
---

# Sessions whose hooks never run stop waking and deliver through the read tool

## Context and Problem Statement

The plugin starts `mash serve` with `MASH_SPOOL_FROM_START=1`: every message waits in the spool for a hook to show it, and
is acked only once a hook has (ADR-0024, ADR-0025). If mash's hooks never run in the session, nothing ever drains. Serve
rings the session at once, then re-wakes it at 2x, 4x and so on the idle wake, up to every 30 minutes (ADR-0028), for as
long as the session lives. Each wake costs a model turn that shows a one-line summary and no mail, so the user sees mash
"pinging" with nothing to read. Hooks do not run when `disableAllHooks` is set in any settings file; when managed
`allowManagedHooksOnly` blocks plugin hooks that are not force-enabled in managed `enabledPlugins`; when the plugin's hooks
are not loaded (plugin disabled in the project, an MCP-only install, a host that runs MCP servers but not plugin hooks);
or when the hooks run but are refused as another session's (ADR-0040) or `mash` is not on the hook's PATH. The `read` tool
never acks, so even a model that reads every message by `#seq` does not stop the loop.

## Decision Drivers

* A wake that cannot get the mail shown must not repeat for ever.
* Nothing is acked without evidence the model saw it (ADR-0024).
* The evidence must not flag a slow hook, a session busy in a subagent, or another session's refused hook as working.
* A session that makes no tool calls must not be woken for nothing either.

## Considered Options

* Leave the wake loop as it is.
* Switch back to full channel delivery when hooks look dead.
* Two signals, computed in serve: stop re-waking when hooks are suspected dead, and deliver through the `read` tool when
  they are proven dead.

## Decision Outcome

Chosen option: the two signals, because they stop the loop without acking anything the model has not read. Switching back
to channel delivery was rejected: it brings back the ack-before-seen loss before the first prompt (issue #16) and the
untrusted-channel framing that ADR-0024 moved away from.

Both signals hold only while no main-session hook has run since this serve bound the spool, and both end when one does
(`.hooked` newer than the bind), when normal delivery and the backoff resume.

* **Suspected hookless:** `HooklessWakes` (3) wakes have been sent since the bind with no hook run. Re-waking stops: no
  wake is due by the idle wake or backoff, and each newly spooled waking message still rings once, as urgent mail does.
  Nothing is acked.
* **Proven hookless:** two mash tool calls, both after the bind and ended at least `HookGrace` (12 s, above the plugin's
  10 s hook timeout) apart, with no hook of any kind run since the bind. A PostToolUse hook (matcher `*`) runs after every
  successful tool call, so the first call's hook would have run by the second. The call is timed when it ends, so a slow
  call cannot make a pending hook look absent. As suspected, plus: one wake at entry, and every wake line after it, ends
  "mash's hooks are not running in this session; read each message with the read tool (#seq).", and the `read` tool
  marks a held message drained once its result has been written to the client (`Endpoint.MarkRead`, under the spool lock, only for a message waiting in the spool), so
  serve acks it. The model has seen the body, so ADR-0024's "acked once seen" still holds. The `inbox` tool never acks.
  The one notice in the proven state is sent for held mail of any kind, result and fyi included.
  `status` reports `delivery: DEGRADED` with the likely cause and the fix; suspected is `delivery: SUSPECTED`.

What counts as "a hook ran": a main-session hook (`.hooked`) or a hook of this session's own subagent, recorded in a new
spool marker `.alive`. A subagent's hook now takes the spool lock and passes the same ownership check as any hook (ADR-0040)
before it touches `.alive`, so another Claude's subagent cannot mark this spool alive; it never touches `.hooked` and
never drains. A hook refused as another session's counts as no hook: for delivery, that session is hookless.

This amends ADR-0024 (ack on `read` when proven hookless: one more way a message becomes seen) and ADR-0028 (the re-wake
backoff applies only after a hook has run; before that it stops at the third wake). Their status lines say so; their
text is not rewritten (ADR-0002).

### Consequences

* Good, because a session whose hooks never run is woken at most three times and then only for new mail, not for ever.
* Good, because in a proven session the model can read and ack its mail through `read`, with nothing acked unseen.
* Good, because `status` names the likely cause and the fix instead of "hooks have not run yet".
* Bad, because a session that makes no tool calls is never proven: it stays suspected, and reads mail only if the model
  uses `read`. No turn is wasted.
* Bad, because mash tool calls made by a subagent count as session tool calls. `.alive` keeps that from flagging a healthy
  session, but a session whose only hooks are a subagent's is never proven. Accepted.
* Bad, because a backlog that would have woken the session to drain (500 held messages, ADR-0028) does not once suspected:
  no drain can follow.
* Neutral, because mash has no reader of Claude Code's settings files, so the cause in `status` comes from the refusal
  record, else lists the causes that leave no trace. A settings reader could name the exact cause later.

### Confirmation

* `internal/endpoint/hookless_test.go`: `TestNoHookStopsReWakingAfterThreeWakes`, `TestNewMailRingsOnceWhileSuspected`,
  `TestToolCallsWithoutHooksProveHookless`, `TestToolCallsBeforeTheBindOrWithinTheGraceDoNotProve`,
  `TestReadAcksOnlyWhenProvenHookless` (the no-op cases), `TestAHookRunEndsHooklessMode`, `TestSubagentHookIsNotHookless`,
  `TestRefusedHooksStillProveHookless`.
* `internal/endpoint/spool_from_start_test.go`: `TestSpoolFromStartReWakesWithoutDrain`.
* `cmd/mash/hook_test.go`: `TestHookSubagentTouchesAlive`. `cmd/mash/hookless_status_test.go`: `TestStatusToolSuspected`,
  `TestStatusToolDegraded`, `TestStatusToolDegradedNamesARefusal`, `TestReadToolAcksOnlyWhenProvenHookless`.
* `internal/spool/spool_test.go`: `TestAliveIsNotHooked`. `internal/mcp/server_test.go`: `TestOnToolCallSeesEachEndedCall`.

## Pros and Cons of the Options

### Leave the wake loop

* Good, because nothing changes.
* Bad, because a session with disabled hooks is woken for ever with nothing to read.

### Switch back to full channel delivery

* Good, because the mail body arrives without a hook.
* Bad, because Claude drops channel events before its first prompt while the channel acks (issue #16), and channel events
  carry the untrusted-channel framing ADR-0024 moved away from.

### Suspect, then prove, then deliver through `read`

* Good, because each step needs more evidence, and an ack needs the model to have read the body.
* Bad, because proof needs two tool calls at least 12 s apart.

## More Information

* Amends ADR-0024 and ADR-0028. Related: ADR-0025, ADR-0040 (the `Trigger` now also reports a subagent).
* Docs: `docs/operations.md` "Hook delivery", `docs/install.md` section 2. Code: `internal/endpoint/hookless.go`,
  `internal/endpoint/hook.go`, `internal/spool/spool.go`, `cmd/mash/hook.go`, `cmd/mash/status.go`.
