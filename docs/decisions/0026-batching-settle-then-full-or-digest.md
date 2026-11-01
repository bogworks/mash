---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Batching: one batch per 100 ms of quiet; full up to 10 messages and 16 KiB, else a digest

## Context and Problem Statement

Mail often arrives in bursts. Shown one message at a time, a burst would cost one wake or one block of context per
message, and a large burst shown in full would flood the model's context. Spec goal 2 asks that "several messages
arrive as one turn". mash needed a rule for grouping messages and for how much of a group to show in full.

## Decision Drivers

* A burst should reach the session together, in one turn or one hook run.
* The model's context must stay bounded however much mail arrives.
* Nothing may be dropped: what is not shown in full must stay readable.

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* Deliver each message on its own.
* Gather until a short quiet period, then show the batch in full if small, else as a digest of one line per message.
* Deliver on a fixed timer.

## Decision Outcome

Chosen option: "gather until a short quiet period; full if small, else a digest", because goal 2 asks that "several
messages arrive as one turn" (spec §1.1) (reconstructed). A design-era spike on 2026-10-21 used the same 10-message
and 16 KiB thresholds with a 1.5-second quiet period (maintainer design discussion, 2026-10-21, not public). The
values 100 ms, 10 messages and 16 KiB were chosen without a recorded reason, as was the change from 1.5 s to 100 ms.

As built (mash layer):

* serve gathers messages until 100 ms pass with no new one (`MASH_SETTLE_MS` or `settle_ms`), or until 200 are
  pending, then treats them as one batch.
* A batch of at most 10 messages (`MASH_BATCH_MSGS`) whose bodies total at most 16,384 bytes (`MASH_BATCH_BYTES`) is
  shown in full. Otherwise it is a digest: one line per message (seq, sender, trust, size, first line, kind), and the
  model reads the ones it needs with the `read` tool.
* The first line of every batch summarises the count, the senders and the kinds; it is also the one-line idle
  wake (ADR-0024).
* Under hook delivery, one hook run shows at most `MASH_BATCH_MSGS` messages, and the rest wait for the next run. Claude
  Code truncates hook context over 10,000 characters, so the hook fits its output to 9,500 (counted in UTF-16 units,
  as Claude Code counts): full form, then digest, then fewer messages. Only what was printed is marked drained.

### Consequences

* Good, because a burst costs one turn, or one hook run, instead of one per message (live, 2026-10-22: of a burst of
  12 sends, 11 arrived as one digest turn and the slowest sender's message in a second turn).
* Good, because context per delivery is bounded, and digest entries stay readable by `#seq`.
* Bad, because a single message over 16 KiB always arrives as a digest line and needs a `read` call.
* Neutral, because a 100 ms quiet period adds at most that much latency to a single message.

### Confirmation

* `internal/render/render_test.go`: `TestBatchFull`, `TestBatchDigestByCount`, `TestBatchDigestByBytes`,
  `TestRenderOversizedSingle`, `TestBatchFirstLine`, `TestSummaryNamesTheKinds`.
* `internal/config/config_test.go`: `TestLoadDefaults` (100 ms, 10, 16,384).
* `cmd/mash/hook_test.go`: `TestHookDrainIsCapped`, `TestHookDrainWithZeroBatchMsgs`, `TestHookContextFitsClaudeCodeLimit`,
  `TestHookContextCountsUTF16Units`.
* No automated test checks the 100 ms quiet period itself.

## Pros and Cons of the Options

### Each message on its own

* Good, because there is no added latency.
* Bad, because a burst costs one wake or context block per message.

### Quiet period, then full or digest

* Good, because bursts group naturally and context stays bounded.
* Bad, because the thresholds are fixed numbers with no recorded basis.

### Fixed timer

* Good, because delivery cost is predictable.
* Bad, because every message waits up to a full interval.

## More Information

* Spec `docs/specs/2026-10-21-mash-design.md` §1.1 (goal 2) and §7.2; live evidence `docs/evidence/2026-10-22-live-e2e.md`.
* PR #17 (summary first line), PR #21 (hook drain capped per run; 10,000-character fit).
* Code: `internal/endpoint/mail.go` (`deliverLoop`, `maxPending`), `internal/render/render.go` (`Batch`),
  `internal/host/claude.go` (`Present`, `hookContextMax`), `internal/config/config.go` (defaults).
* Related: ADR-0021 (quoted bodies), ADR-0024 (hook delivery), ADR-0025 (the spool), ADR-0034 (text contract).
