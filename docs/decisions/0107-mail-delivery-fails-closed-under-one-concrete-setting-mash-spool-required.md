---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# Mail delivery fails closed under one concrete setting, MASH_SPOOL_REQUIRED

## Context and Problem Statement

Hook delivery spools mail and acks it only once a hook has shown it. When the spool cannot be made or written, mash
falls back to the channel, which acks what the host may never show (Claude Code drops channel events before its first
prompt), and the failure is only a log line. An operator who prefers mail to wait over mail that may be lost has no way
to say so, and a broken spool is not visible to the session. How does mail delivery fail closed, and where is that
choice made?

## Decision Drivers

* A message must never be acked on a channel write when the operator asked for fail-closed delivery.
* A failing spool must be visible to the session, not routed around in silence.
* mash exposes concrete settings; the layer above mash owns named profiles.
* Nothing may change for a deployment that does not set it.

## Considered Options

* One setting, `MASH_SPOOL_REQUIRED` (`spool_required`), default off (chosen).
* Always fail closed.
* A named profile that mash reads and expands into settings.

## Decision Outcome

`MASH_SPOOL_REQUIRED` is parsed like `MASH_SPOOL_FROM_START` (`1`/`true`, `0`/`false`, anything else an error; the
config key is `spool_required`; default off). When on, serve sets no-channel for every host and, once the spool is
bound, spooling from the first message. Mail the spool cannot take is held unacked in the inbox and redelivered, never
delivered by the channel. A spool failure is reported once per spell on stderr, in a channel notice and in a `spool:
FAILING` line of the session's `status` tool, and the line clears after a later successful bind or write. A spool that
cannot be made at start is retried every 5 seconds in the background, and bound by the deliver loop, the only reader of
the spool, when it works. When off, behaviour is as before.

### Consequences

* Good, because no message is acked without being shown by a hook or read by the model.
* Good, because a failing spool is reported to the operator and the session.
* Bad, because a spool that stays broken leaves mail waiting unseen until it recovers or the model reads it.

### Confirmation

Tests cover a spool failing at start (mail held unacked, no channel body, stderr and status line, retry binds it, held
mail spooled and acked once after a drain), a spool failing mid-session (held past the spool tries, reported, cleared
and acked after recovery), the unchanged channel fallback with the setting off and no status line, the config parse for
env and file including a rejected value, and the status line text.

## Pros and Cons of the Options

### One setting, default off (chosen)

* Good, because it is concrete and independent of other settings, and changes nothing by default.
* Bad, because an operator who wants it must set it.

### Always fail closed

* Good, because there is one behaviour.
* Bad, because hosts without hooks, or with a broken spool, would stop receiving mail they receive today.

### A named profile read by mash

* Good, because one name could set several settings.
* Bad, because the layer above mash owns profiles; mash would duplicate them and have to keep them in step.

## More Information

* Related: [ADR 0093](0093-the-lease-bucket-does-not-allow-rollup-writes.md) for the format.
