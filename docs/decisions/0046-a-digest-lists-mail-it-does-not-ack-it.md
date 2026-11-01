---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (mash maintainer)
---

# A digest lists mail and does not ack it; the read tool or a full showing does

## Context and Problem Statement

When a hook run's batch is too large (past 10 messages or 16 KiB, or past the 9,500-unit fit under Claude Code's
10,000-character hook limit), the hook shows a digest: one line per message with a preview of its first line (ADR-0026).
It then marked every listed message drained, and serve acked it (ADR-0024, ADR-0025). A message whose instruction sits
past the preview was acked as delivered although the model may never have called `read` for its body. "Acked" then meant
"listed", not "seen", against ADR-0024's rule that a message is acked once the model has been shown it, and against the
inbox's unread count, which operators read as "not yet seen" (issue #44). The first answer to that issue was a docs line
saying a digest line counts as delivery. This ADR takes the other one.

## Decision Drivers

* An ack must mean the session has seen the message (ADR-0024).
* A message listed and never read must not make serve wake the session for ever (ADR-0028, ADR-0041).
* Nothing that was listed may be lost: it must survive a restart and be shown again.
* `inbox` never acks, and the hookless `read` ack (ADR-0041) stays as it is.

## Considered Options

* Document that a digest line counts as delivery, and tell senders to put the action in the first line.
* Mark listed messages in the spool as `listed`, not drained; ack on `read` or on a later full showing; the idle wake
  ignores listed mail.
* Make a digest list every message again on each hook run until it is read.
* Never digest: show every batch in full, whatever its size.

## Decision Outcome

Chosen option: "mark listed, ack on read or a full showing, no wake", because it makes an ack mean "seen" without
turning an unread listed message into a wake loop or a repeat on every tool call. The documentation-only option leaves
the unread count lying and puts the burden on every sender. Listing again on each run would add the same digest to the
context after every tool call. Never digesting breaks the bound on context ADR-0026 exists for.

This amends ADR-0026 (a digest line no longer counts as delivery: "Only what was printed is marked drained" holds for
what was shown in full) and ADR-0024 (the ack-when-seen rule now covers digests). Their text is not rewritten
(ADR-0002).

As built:

* **State.** The spool has one more state beside waiting, shown and drained: `listed/<seq>`, an empty 0600 file written
  atomically (`Spool.Listed`). A listed message keeps its waiting file and is not drained, so serve does not ack it.
  `Spool.Waiting` flags such entries `Listed`; the flag is never persisted. A drain (`Spool.Drained`) and an ack
  (`Spool.Acked`) remove the mark.
* **Digest.** The host adapter's `Present` marks the entries it rendered as digest lines `Listed` (and the others not),
  and `mash hook` calls `Spool.Listed` for those instead of `Spool.Drained`. The shown mark is removed, so a listed
  message is not later flagged re-delivered. The hook's digest text now says: "Read each with the read tool (#seq);
  they stay unread until you do." (`render.HookBatch`; the channel's digest and the idle wake's summary line are
  unchanged). The 9,500-unit fit is measured on the final text, so the longer wording is inside it.
* **Drained by `read`.** The `read` tool's after-write step calls `Endpoint.MarkRead`, which now drains a listed message
  in every mode, under the spool lock, once the result has been written to the client; serve then acks it. The hookless
  rule of ADR-0041 (a waiting message in a proven-hookless session) is unchanged and is the only other `read` ack.
  `inbox` never acks.
* **Drained by a full showing.** A hook run shows only mail no digest has listed, so a run with nothing new shows
  nothing, and a listed message is never repeated in a digest. When the new mail fits in full, the hook also tries the
  listed mail with it, and takes that batch only if it too is shown in full, with no digest line; every message shown in
  full is drained. A listed message too big to fit in full, alone or with the rest, is therefore acked only by `read`.
* **No wake.** `wakeIfIdle` and the ring skip listed messages, the wake's summary line does not name them, and the
  waking-pending count the lease carries stops counting them (they were announced, and the model was told to read them).
  Serve notices a listed message at its next 250 ms check; that also restarts the idle spell, as a drain does. They
  stay unacked, so the inbox consumer's unread count still includes them.
* **Restart.** A restarted serve clears the spool's waiting files and the `listed/` marks (`ClearWaiting`, and `Bind` for
  a recreated stream), and NATS redelivers what was never acked. A redelivered listed message is spooled as new mail
  and is listed again once, in one digest with the rest of its batch; it rings once like any new waking message and
  then, once listed, never wakes again. A listed message acked by `read` before the restart is not redelivered.

### Consequences

* Good, because "acked" means "seen" for digests too, and the unread count is honest about listed mail.
* Good, because a message the model never reads stays in the inbox for the next session instead of being lost.
* Good, because an unread listed message costs no wake and no repeat, so there is no loop.
* Bad, because a message the model chooses never to read stays unacked for ever (until the inbox's age limit), and
  keeps counting in the unread count that a launcher's health check reads. That is the intent, and it is visible.
* Bad, because the idle wake no longer reminds the model of unread listed mail; only the digest text and a
  restart do. A model that ignores the digest line is not nudged.
* Bad, because a message listed in a digest is shown again as a digest after every restart of serve until it is read.
* Neutral, because a listed message's body stays in the spool (counted in `SpoolMaxBytes`) until it is read.

### Confirmation

* `internal/endpoint/listed_test.go`: `TestListedStaysUnackedUntilRead` (unread count unchanged until `read`, then
  acked), `TestReadOfAnUnlistedMessageStillDoesNotAckInTheNormalState`, `TestListedNeverWakes` (no wake and no
  waking-pending after the first ring), `TestListedMailDoesNotHideOrRepeatInANewWake`,
  `TestListedIsAckedWhenALaterHookShowsItInFull`, `TestListedMailIsListedOnceAgainAfterARebind` (no wake loop after a
  rebind).
* `cmd/mash/hook_test.go`: `TestHookDigestListsAndDoesNotDrain`, `TestHookALaterFullShowingDrainsListedMail`,
  `TestHookDrainWithZeroBatchMsgs`, `TestHookContextFitsClaudeCodeLimit`, `TestHookContextCountsUTF16Units`.
* `internal/spool/spool_test.go`: `TestListedMarkLifecycle`. `internal/render/render_test.go`:
  `TestHookBatchDigestSaysListedMailStaysUnread`. `internal/host/claude_test.go`: `TestPresentMarksDigestEntriesListed`.

## Pros and Cons of the Options

### Document that a digest counts as delivery

* Good, because nothing in the code changes.
* Bad, because the unread count then says "listed", an instruction past the preview is acked unread, and every sender
  has to know the rule.

### Listed state, ack on read or full showing, no wake

* Good, because the ack keeps its meaning and nothing loops.
* Bad, because a spool state and a path through the hook and serve are added, and an unread message stays unacked.

### List again on every run

* Good, because the model is reminded.
* Bad, because the same lines enter the context after every tool call.

### Never digest

* Good, because every ack follows a full showing.
* Bad, because a burst can flood the context, which ADR-0026 bounds.

## More Information

* Amends ADR-0026 and ADR-0024; builds on ADR-0025 (the spool), ADR-0028 and ADR-0041 (wakes, and the hookless `read`
  ack). Issue #44 (option B).
* Docs: `docs/operations.md` "Hook delivery", `docs/faq.md`, `docs/troubleshooting.md`, `docs/architecture.md`. Code:
  `internal/spool/spool.go`, `internal/host/claude.go`, `internal/render/render.go`, `cmd/mash/hook.go`,
  `internal/endpoint/hook.go`, `internal/endpoint/hookless.go`, `internal/endpoint/waking.go`, `cmd/mash/serve.go`.
