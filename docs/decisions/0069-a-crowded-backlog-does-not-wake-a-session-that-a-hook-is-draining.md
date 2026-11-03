---
status: accepted
date: 2026-11-03
decision-makers: Randy Bias (mash maintainer)
---

# A crowded backlog does not wake a session that a hook is draining

## Context and Problem Statement

ADR-0028 wakes a session at once when the held mail is crowded (500 messages or half the byte limit), because waiting
could cost mail behind it, and otherwise after the idle wake. Any hook drain zeroed the wake clock, and the next settle
then saw a still-crowded backlog and woke at once. A hook drains a bounded batch (about ten messages) per run, so the held
count stays above 500 for many runs, and the session was woken on nearly every settle while it was actively draining. That
contradicts ADR-0028's own rule that a busy session is never woken. It showed as a flaky `TestBoundaryBurstWakesOnce`:
a third wake mid-drain on slow runners (the held count in the wake text was 570, then 520).

## Decision Drivers

* A session whose hook is draining the spool is not idle and must not be woken.
* A crowded backlog must still be woken when nobody drains it.
* The inbox stream's limit is far above the wake threshold, so waiting one idle period costs no mail.

## Considered Options

* Keep waking at once on a crowded backlog, even mid-drain, and loosen the test.
* Record when a hook last drained, and the held count and size right after it; suppress the crowded rule only while the
  drain is making headroom; measure the idle spell from the later of the oldest held message and the last drain.
* Drop the crowded rule.

## Decision Outcome

Chosen option: the second. The held state records `hookAt`, the time of the last drain, and `hookHeld` and `hookStored`,
the held count and stored size right after it (refreshed on every drain). The crowded "wake at once" rule is suppressed
only while the drain is making headroom: a hook drained within the idle wake, the stream is not full, the backlog has not
grown since the drain (count and stored size), and the held size is under half the inbox byte limit (twice
`SpoolWakeBytes`, a hard line past which a crowded backlog always wakes at once). Otherwise it wakes at once, as before.
The idle spell runs from the later of the oldest held mail and `hookAt`. A backlog nobody drains, or one that keeps
growing, still wakes at once; one that is shrinking waits for the idle spell to pass after the last drain.

### Consequences

* Good, because a draining session is not woken on every settle.
* Good, because a stalled drain (no hook for the idle wake) wakes the session even if the backlog is crowded.
* Good, because incoming mail that grows the backlog, a full stream, or held bytes past half the inbox limit wake at
  once, so one drain cannot hide a backlog from the session while the inbox could discard unacked older mail.
* Bad, because the first wake after a drain stops waits one idle wake (the default) for a crowded backlog that is below
  half the byte limit and not growing.
* Bad, because the shift of the idle spell to the later of the oldest held mail and the last drain also delays the non-crowded idle wake, by up to one idle wake after an unrelated drain.

### Confirmation

`TestBoundaryBurstWakesOnce` drains with a small delay per message, which models a hook run taking time; before this change
it failed with extra wakes, after it the wake count stays at two.

`internal/endpoint/wake_bound_test.go` pins the bound: held mail does not wake before the idle wake has run from the last
drain (`TestIdleSpellRunsFromTheLastDrain`), a crowded backlog nobody drains wakes at once
(`TestCrowdedBacklogWithoutDrainWakesAtOnce`), and a shrinking crowded backlog behind a drain waits for the idle wake after it
(`TestCrowdedBacklogAfterADrainWaitsForTheIdleSpell`), a backlog that grows after a drain wakes at once
(`TestCrowdedBacklogGrowingAfterADrainWakesAtOnce`), and held bytes past twice `SpoolWakeBytes` wake at once after a drain
(`TestCrowdedBytesPastTheHardLineWakeAfterADrain`).

## Pros and Cons of the Options

### Keep waking at once and loosen the test

* Good, because nothing changes in the product.
* Bad, because a session is woken while it is draining, against ADR-0028.

### Record the last drain

* Good, because it keeps the crowded wake for an undrained backlog and removes it for a drained one.
* Bad, because one more field of state and a wait of one idle wake after a drain stops.

### Drop the crowded rule

* Good, because it is the simplest rule.
* Bad, because an undrained crowded backlog would wait for the idle wake with mail piling up behind it.

## More Information

* Amends the backlog rule of ADR-0028. Code: `internal/endpoint/hook.go` (`held.hookAt`, `held.hookHeld`, `wakeIfIdle`).
