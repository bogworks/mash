---
status: proposed
date: 2026-11-16
decision-makers: Randy Bias (mash maintainer)
---

# Pi is a supported host, chosen with MASH_HOST=pi and served by a shipped extension

## Context and Problem Statement

[ADR 0137](0137-the-host-adapter-declares-its-session-id-sources-wake-and-words-claude-code-is-still-the-only-host.md)
moved what differs between hosts behind `host.Adapter`, with Claude Code still the only host. The Pi coding agent can
use mash's MCP tools today, but mail never reaches its model: Pi has no hooks and ignores the channel notification, so
serve's channel delivery is acknowledged and lost, and an idle Pi session is never woken. A trial with a prototype
adapter and a Pi extension showed that a small adapter plus one extension closes the gap, and that the lease and the
ownership record already work under Pi.

## Decision Drivers

* Pi must be an adapter, not a change spread through the callers.
* Claude Code's behavior and every string it prints stay exactly as they are.
* Mail must be delivered and an idle session woken without a channel.
* The host is named by the user for now; detecting it needs a decision of its own.

## Considered Options

* Have the Pi extension fake Claude's hook input and parse Claude's JSON output.
* A `pi` adapter that reads the extension's own input and prints plain text (chosen).

## Decision Outcome

Chosen: add a `Pi` adapter, selected explicitly with `MASH_HOST=pi`.

1. **The adapter.** `Pi` reads `{"event":"tool_result"|"before_agent_start","session":"<id>"}` on the hook's stdin and
   maps the two events to the ones the hook loop already knows (`PostToolUse`, `UserPromptSubmit`); anything else is
   not a delivery point. Its session-id sources are no environment variable, the hook field `session`, and the process
   ancestry. Its wake is none, so mail is spooled from the first message and acked only after a hook drained it.
   Its words are "Pi", "Pi process" and "Pi (mash): ", and it has none of Claude Code's settings checks. Ownership
   follows Claude's rules (launch token, process ancestry); the session-id rule never fires because Pi gives serve no
   session id. `host.SessionID` returns "" for a host with no variable and does not read the environment.
2. **Output.** Pi's `Present` renders the text Claude puts in `additionalContext`, with the same trust-label frame and
   the same size cap. Both adapters call one shared function for the text, so the two cannot drift. Pi's hook prints it
   as JSON, `{"text": "...", "ack": "<token>"}` (see 6); its notices print `{"text": "..."}`.
3. **Notice moves onto the adapter.** The other-launch and lease-wait notices were written as Claude's JSON by
   `host.Notice`. `Adapter.Notice(event, text)` now renders them: Claude's is byte for byte what `host.Notice` returned
   (a test pins it), Pi's is the text and a newline.
4. **Delivery and wake by an extension.** `hosts/pi/mash.ts` runs `mash hook` after each tool result and before each
   agent start, and injects what it prints into the model's next request. It watches the directory that the new
   `mash spool-dir` prints and wakes an idle session with `sendMessage({triggerTurn:true})`, holding a wake while the
   model is mid-answer until the turn settles. It is copied by hand to `~/.pi/agent/extensions/`.
5. **`mash spool-dir`** prints the endpoint's spool directory and exits 0, or says why not on stderr and exits
   non-zero, so the extension does not rebuild the path.

6. **Two-phase delivery.** Draining before the extension has the text loses mail when Pi changes session while the
   hook runs: the extension drops the text and the mail is already marked delivered. So an adapter may declare
   `TwoPhase` (an optional interface, `host.TwoPhaser`; true for Pi only). Its hook offers the batch and leaves it
   spooled, recording who it was offered to and when. `mash hook --ack <token>` (stdin as for the hook) marks exactly
   the offered entries delivered, under the spool lock after the same owner check; entries already delivered or gone
   are skipped, and a refused owner marks nothing. The token is base64url of a JSON list of the shown refs and whether
   each was a full message or a digest line. The extension acks only after Pi accepted the message (right before the
   tool result or `before_agent_start` message is returned; after `sendMessage` returns for a wake), and never when the
   session changed. Delivery is at-least-once: a batch is not offered again to the same session for 60 seconds, and
   is offered again after that or to another session, flagged as a repeat. Claude Code and headless are unchanged.

Not decided here, and later work:

* Detection of the host from the MCP client, and the rule for an unrecognised one. `Select("")` still returns Claude
  Code and nothing reads Pi's environment.
* An installer command for Pi's configuration and extension.
* Live prompts (`mash prompt`) to a Pi endpoint, which need the request id the model replies with to reach it.
* An intake of Pi's activity (working, idle, blocked) into mash.
* Getting mash's server instructions to a Pi model. Until then each delivered batch carries the trust-label frame.

This amends [ADR 0032](0032-host-adapter-seam.md): the adapter's surface gains `Notice`, and a second host is
registered. It is not edited.

### Consequences

* Good, because a Pi session receives labelled mail and an idle one is woken, with no change to Claude Code.
* Good, because a host's hook output is no longer assumed to be Claude's JSON.
* Bad, because the install is manual until the installer exists, and live prompts to Pi do not work yet.
* Bad, because the extension and the adapter must agree on two event names; a test pins the adapter's side.

### Confirmation

Every existing test passes with no expected string changed, except Pi's own: the hook output is now JSON, and Pi's
`Notice` is `{"text": ...}`. New tests cover two-phase delivery (the hook offers and does not drain, the ack drains
exactly the offered entries, a second ack is a no-op, an ack after an owner change is refused, a recent batch is not
re-offered to the same session and is to a new one, Claude has no ack phase), Pi's `Trigger` (both events, foreign input
refused), `Present` equal to Claude's `additionalContext`, `Notice` for both hosts, `Select` and `Known`, `SessionID`
with no variable, the words, wake and `SpoolsFromStart`, the hook path with `MASH_HOST=pi`, and `mash spool-dir`.

## Pros and Cons of the Options

### Fake Claude's hook input

* Good, because no Go change.
* Bad, because the extension carries Claude's JSON shapes and breaks when they change, and the refusal notice stays
  Claude-shaped.

### A `pi` adapter (chosen)

* Good, because the extension speaks a small input and reads plain text.
* Bad, because there is a second adapter to keep in step with the first.

## More Information

Amends [ADR 0032](0032-host-adapter-seam.md). Builds on
[ADR 0137](0137-the-host-adapter-declares-its-session-id-sources-wake-and-words-claude-code-is-still-the-only-host.md).
Install page: [docs/hosts/pi.md](../hosts/pi.md).
