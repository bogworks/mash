---
status: accepted
date: 2026-10-29
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# Mail kinds decide wake or wait; the default kind is `task`; the kind is the sender's unsigned claim

## Context and Problem Statement

With hook delivery (ADR-0024), mail that reaches a busy session rides along with its next tool call at no extra cost,
but mail to an idle session needs a wake: a channel event that starts a new model turn. Idle wakes are the expensive
turns, and a wake that comes after the model host's prompt cache has expired re-reads the whole context at full price.
Much mail (a notice, a finished result) does not need an answer now. mash needed a way for a sender to say whether
its mail is worth waking an idle session for.

## Decision Drivers

* Cut model turns spent on mail that can wait for the recipient's next turn.
* Mail from older senders, which carry no kind, must never be stranded in an idle session.
* A kind only schedules delivery; it must never change how far mail is trusted (ADR-0019, ADR-0020).

## Considered Options

* A message with no kind is `fyi` (waits); waking is opt-in
* A message with no kind is `task` (wakes); waiting is opt-in

## Decision Outcome

Chosen option: "a message with no kind is `task`", because with `fyi` as the default "unkinded mail ... never woke"
idle sessions (PR #85): senders that did not set a kind silently stopped waking their recipients. The `fyi` default
was built first (PR #75, 2026-10-29) and reversed the same day (PR #85). Mail from an older mash with no kind header,
or with a kind this mash does not know, is also `task`, "so no older sender's mail can stall" a session (PR #75).

As built (mash layer):

* Five kinds, in the `Mash-Kind` message header: `blocker`, `question` and `task` wake an idle session; `result` and
  `fyi` wait for the session's next hook run. The three waking kinds behave identically today; no reason for having
  three was recorded.
* The kind header is not signed. It is the sender's claim and decides only wake or wait, never trust.
* Auto-tag: when the session's MCP `send` tool sends a reply (`in_reply_to` set) of kind `question` whose body has no
  `?` and is at most 280 characters, it goes as `fyi`, and the tool says so. A `task` or `blocker` is never
  downgraded. The CLI `mash send` does not auto-tag. The value 280 was chosen without a recorded reason.
* Urgent mail (ADR-0029) and a large backlog (ADR-0028) wake regardless of kind.

### Consequences

* Good, because notices and results cost no idle turn; they are read on the recipient's next turn.
* Good, because a sender that never sets a kind gets the same behaviour as before kinds existed: its mail wakes.
* Bad, because a sender can mark anything as waking; nothing stops an unhelpful sender from waking a session.
* Bad, because a careless `fyi` on mail that needed action leaves it unread until the session next runs.
* Neutral, because every rendered message shows its `kind:` line, which became part of the text contract (ADR-0034).

### Confirmation

* `internal/kind/kind_test.go`: `TestParse`, `TestWakes`, `TestTag`, `TestTagLeavesAnUnkindedReplyAlone`.
* `internal/endpoint/kind_test.go`: `TestKindDecidesTheIdleWake`, `TestOldSenderWithoutAKindWakes`,
  `TestUnkindedSendWakesButFyiDoesNot`, `TestWakingKindArrivingAfterAFyiWakesWithBoth`, `TestSendWithSetsTheKindHeader`.
* `cmd/mash/kind_test.go`: `TestUnkindedCLISendIsTask`, `TestUnkindedMCPSendIsTask`, `TestSendKindFromTheCLI`,
  `TestSendKindFromTheMCPTool`.
* The release gate (`docs/release.md`) measures live that `--kind fyi` mail does not wake a session whose hook has run.

## Pros and Cons of the Options

### `fyi` by default

* Good, because the cheapest behaviour is what a sender gets without thinking.
* Bad, because every existing sender that set no kind stopped waking idle recipients, which failed a consumer's tests the
  day it shipped (PR #85).

### `task` by default

* Good, because it keeps the pre-kinds behaviour for every sender that does not opt in.
* Bad, because savings depend on senders choosing `fyi` or `result`.

## More Information

* PR #75 ("mail kinds; only blocker, question, task and urgent wake"), PR #85 ("unkinded sends default to task").
* The cost argument: maintainer design discussion, 2026-10-29 (not public). PR #75 calls kinds "the biggest turn-cost
  lever".
* Code: `internal/kind/kind.go`; `cmd/mash/serve.go` (auto-tag in the `send` tool); `docs/operations.md`.
* Live evidence: `docs/evidence/2026-10-29-default-kind-live-v0.2.6/`.
* Related: ADR-0018 (which marks are signed), ADR-0024 (hook delivery), ADR-0028 (wake timing), ADR-0029 (urgent).
* **Not yet true:** a code comment in `internal/render/render.go` (function `kinds`) still calls `fyi` "the default".
