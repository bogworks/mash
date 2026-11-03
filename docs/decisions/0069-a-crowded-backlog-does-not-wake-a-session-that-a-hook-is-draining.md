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
* Record when a hook last drained and let the crowded rule apply only when no hook drained within the idle wake; measure
  the idle spell from the later of the oldest held message and the last drain.
* Drop the crowded rule.

## Decision Outcome

Chosen option: the second. The held state records `hookAt`, the time of the last drain. The crowded "wake at once" rule
applies only when no hook drained within the idle wake, and the idle spell runs from the later of the oldest held mail and
`hookAt`. A backlog nobody drains still wakes at once; one that is being drained waits for the idle spell to pass after the
last drain.

### Consequences

* Good, because a draining session is not woken on every settle.
* Good, because a stalled drain (no hook for the idle wake) wakes the session even if the backlog is crowded.
* Bad, because the first wake after a drain stops waits one idle wake (the default) even for a crowded backlog.

### Confirmation

`TestBoundaryBurstWakesOnce` drains with a small delay per message, which models a hook run taking time; before this change
it failed with extra wakes, after it the wake count stays at two.

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

* Amends the backlog rule of ADR-0028. Code: `internal/endpoint/hook.go` (`held.hookAt`, `wakeIfIdle`).
