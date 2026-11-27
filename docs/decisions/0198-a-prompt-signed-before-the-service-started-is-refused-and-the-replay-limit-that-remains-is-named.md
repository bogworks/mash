---
status: accepted
date: 2026-11-27
decision-makers: Randy Bias (mash maintainer)
---

# A prompt signed before the service started is refused, and the replay limit that remains is named

## Context and Problem Statement

[ADR 0030](0030-sessions-are-full-sap-agents.md) has an endpoint accept a live prompt from a listed sender when its signature verifies, its timestamp is within 30 seconds of the receiver's clock, and its nonce is new. The record of seen nonces is in memory. A restart empties it, so a copy of a prompt that the earlier process had answered was accepted again for up to 30 seconds (#1829).

## Decision Drivers

* No change to the SAP protocol and none to the SAP Go SDK.
* A session start restarts the endpoint, so a restarted endpoint must keep answering prompts at once.
* The fix must be small and need nothing on disk, because the state folder can be missing (#1893).

## Considered Options

* Refuse a prompt whose signed timestamp is before the service started (chosen).
* Refuse every prompt whose timestamp is within 30 seconds after the start.
* Keep the seen nonces on disk across restarts.

## Decision Outcome

Chosen option: "refuse a prompt whose signed timestamp is before the service started". The timestamp is signed, so a replay carries its original one. The service records its start time. After the SDK admits a prompt, mash refuses it with status 401 and the text `this endpoint restarted after the prompt was signed: send the prompt again` when its timestamp is before that time. The start time is rounded down to the second, because the timestamp has one-second resolution and a prompt signed in the second the service started must not be refused.

This amends the acceptance rule of ADR 0030 by one refusal; nothing else about prompts changes.

Two limits remain and are one known issue (#1829). First, the rounding: a prompt signed in the same second the service started carries a timestamp that is not before the start, so a copy of a prompt answered earlier in that second is admitted again. Refusing that second too would cost a legitimate prompt sent in it one resend. Second, SAP allows a sender's clock to run ahead by up to 30 seconds, so a prompt from such a sender, answered before a restart, carries a timestamp that is not before the start and can still be replayed within about 30 seconds. Closing both takes either a refusal of every prompt for 30 seconds after a start or the nonces on disk.

### Consequences

* Good, because every replay of a prompt from a sender whose clock is not ahead is refused after a restart, with no protocol change and nothing kept on disk.
* Good, because a prompt sent after the restart is answered as before.
* Bad, because a prompt signed just before a restart that arrives after it is refused once, and the sender sends it again.
* Bad, because the replay of a prompt signed in the second the service started, or from a sender whose clock runs ahead, is not closed.

### Confirmation

A test signs a prompt before the service starts and one after: the first is refused with that text and never reaches the session, the second is answered. The full test suite passes in a container.

## Pros and Cons of the Options

### Refuse a prompt signed before the start

* Good, because it is a few lines, needs no disk and costs a real sender at most one resend.
* Bad, because it leaves the start second and the fast-clock case open.

### Refuse every prompt for 30 seconds after the start

* Good, because it closes the replay for every sender.
* Bad, because a session start restarts the endpoint, so every start would refuse every prompt for 30 seconds, which a person would meet all the time.

### Keep the seen nonces on disk

* Good, because it closes the replay for every sender.
* Bad, because it needs a working state folder, a write per prompt and cleanup, and is the durable replay memory that belongs to the policy work (#1060).

## More Information

* Issue #1829; related: ADR 0030.
