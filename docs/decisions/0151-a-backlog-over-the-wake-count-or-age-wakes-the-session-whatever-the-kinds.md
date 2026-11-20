---
status: accepted
date: 2026-11-20
decision-makers: Randy Bias (mash maintainer)
---

# A backlog over the wake count or the wake age wakes the session, whatever the kinds, and listed mail counts as waiting

## Context and Problem Statement

Mail waits unseen in an idle session when it is of a kind that does not wake (result, fyi) or when it was announced by a digest
and never opened: [ADR 0046](0046-a-digest-lists-mail-it-does-not-ack-it.md) says listed mail
never wakes, and the idle wake of ADR 0028 follows only the kinds that wake. A person can come back to an agent that has held
mail for hours. The maintainer ruled the wake (REQ-015, 2026-11-19): a backlog wakes the session, and the limits are settings:
`--wake-count` (default 15) and `--wake-age` (default 1 hour), each `0` for off. The count is any whole number, 0 or more; the age
is `0` or any positive length, written as a whole number with `m`, `h` or `d` or as a Go duration such as `90s`. There is no upper
or lower range, and `mash serve` refuses a value that is neither (exit 2), the same rule as `MASH_WAKE_COUNT`, `MASH_WAKE_AGE` and the
config file.

## Decision Drivers

* Mail that is waiting must not wait for ever because of its kind or because a digest listed it.
* The wake must not become a storm: a bounded rate, and quiet while a session starts.
* One meaning of "waiting": not yet acknowledged by the session.
* A busy session is never woken (ADR 0028), and neither is one in which no hook runs, however many messages wait.

## Considered Options

* Keep ADR 0046 and the kinds rule: result and fyi mail and listed mail never wake.
* Wake on a backlog by count or age, whatever the kinds (chosen).

## Decision Outcome

Chosen: wake on a backlog. `mash serve` wakes the idle session when more than `--wake-count` messages are waiting, or the oldest
waiting message is older than `--wake-age`, whatever the kinds of the messages. Waiting means held and not yet acknowledged by the
session: mail the hook showed in full, or the agent read, is acknowledged and does not count; **mail listed by a digest counts**,
because a digest does not acknowledge it. The specification says only that a waiting message "has not yet reached the session";
counting listed mail is the coordinator's ruling, made while building, on what that phrase covers (the maintainer ruled the wake, not
this reading): a digest names a message but the session has not read it. A backlog wake happens at most once every 10 minutes, measured from the last wake of any kind, and not in the first 10 minutes after
the server starts; a restart keeps no cooldown. Idle, urgent and backlog wakes share one last-wake time, and each sets it. An idle
wake reads it too: it is not sent until the idle backoff (ADR 0028, doubled for each wake since a hook drained) has passed since the
last wake of any kind, and a backlog wake counts as a wake in that backoff. Urgent mail is exempt from the reading, so it still wakes
an idle session at once as ADR 0028 says, but it sets the time. A backlog wake is not sent while a hook has run in the last 10
minutes, whether the hook drained mail or only listed it in a digest, because the session is then busy and ADR 0028 says a busy
session is never woken; nor when the session is suspected or proven to run no hook, as the idle wake stops then too. The wake
carries the summary of all waiting mail, with the number already listed. The existing wakes (idle, urgent, near capacity) are
unchanged.

This amends ADR 0046 only where it says listed mail never wakes: listed mail still does not wake by itself, but counts toward the
backlog. It adds to ADR 0028's wake timing and leaves its rules as they are.

### Consequences

* Good: a held message is always eventually offered to the person or agent.
* Good: two settings, each with `0` for off, as the spec says.
* Bad: a session with a long backlog is woken once every 10 minutes until the mail is read or acknowledged, unless a hook runs in
  that time (then the hook is already showing it).

### Confirmation

Clock-driven tests of the endpoint's backlog wake: over the count, over the age, each off, the cooldown, the start hold, listed mail
counted, result and fyi mail counted, acknowledged mail not counted, refused mail not counted, no wake in a channel-only session, no
cooldown across a restart; through the settle pass: no wake after an idle wake within the cooldown, none while a hook has run within
it (drain or digest), none in a session suspected or proven hookless, an idle wake spaced from a backlog wake, urgent mail not.

## Pros and Cons of the Options

### Keep ADR 0046 and the kinds rule

* Good: no new wake.
* Bad: result and fyi mail, and mail a digest announced, can wait for ever.

### Wake on a backlog by count or age

* Good: nothing waits for ever.
* Bad: one more wake in the timing rules.

## More Information

[ADR 0147](0147-the-0-5-6-command-line-and-mcp-surface-is-built-from-a-specification-that-supersedes-contract-revision-26.md);
the serve command and its notes in `spec/mash.json`.
