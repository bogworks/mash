---
status: proposed
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# An admin can suspend an endpoint's inbound mail by parking its inbox stream's subjects, and read whether it is suspended

## Context and Problem Statement

An operator who retires an endpoint wants its mail before `deprovision` deletes the inbox stream
([ADR 0015](0015-deprovision-fences-the-name-with-a-lease-tombstone.md)). The tombstone fences the name, not inbound
mail; revoking the endpoint's credentials stops what it sends, not what others send to it; and deletion is denied on the
stream ([ADR 0010](0010-inbox-limits-and-append-only-history.md)), so a stream cannot be drained to a known end. Mail
that lands between an archiver's last read and the deprovision is deleted unarchived (issue 130, from an agent-management tool's
retirement step). Two things are needed without a gap: stop inbound delivery to one endpoint from every sender, and read whether
it has stopped, with the sequence number it stopped at.

## Decision Drivers

* Enforced by the server for every sender; nothing of ours in the message path ([ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md)).
* Reversible (`resume`), because an operator can suspend the wrong endpoint.
* Admin only: an endpoint holds no stream-update permission ([ADR 0007](0007-one-nats-user-per-endpoint-least-privilege.md)), so it cannot suspend or resume itself or a peer.
* The owner's reads and the endpoint's own session are unaffected.
* A script can require the state, and the final sequence, before it drains.

## Considered Options

* Seal the stream (JetStream `sealed`).
* A policy-file deny for the recipient, compiled into every sender's permissions.
* Pause the endpoint's consumer.
* Park the stream's subjects: replace them with one subject no endpoint may publish to, remembering the originals.

## Decision Outcome

Chosen option: park the subjects. `Admin.Suspend(name)` takes the name's stream lock ([ADR 0048](0048-limit-changes-take-a-per-stream-kv-lock-with-expiry.md)),
saves the stream's subjects in the stream's own `metadata` (`mash_suspended_subjects`, with `mash_suspended_at`) and updates the
stream's subjects to the single subject `mash.suspended.<name>`, which sits outside `agents.>`, so no endpoint's publish
allow-list reaches it. Every other field is untouched and nothing is purged. A sender's publish to the endpoint then
finds no stream and fails at once with no acknowledgement; the sender sees the mail was not delivered. Mail stored
before the update stays, readable through the existing consumer and by direct get. `Admin.Resume(name)` restores the
saved subjects and clears the metadata. Both are idempotent.

The state is read from the stream, not from a file: suspended means the metadata is present and the stream's subjects are the parked
one. `Admin.Suspended(name)` returns that, when, and the stream's message count and last sequence. A message is acknowledged to
its sender only after it is stored and before the update returns, so the last sequence read after a suspension is final
for as long as the state holds. `mashctl suspend NAME` prints it (`last_seq`), `mashctl resume NAME` undoes it, and
`mashctl suspended NAME` exits 0 when suspended, 3 when not and 1 on any error, so `suspended && drain && deprovision`
cannot proceed on a failed read. The same verbs exist under the deprecated `mash admin`. Deprovision is unchanged and
works on a suspended endpoint.

Around it: `EnsureInbox` accepts a suspended stream as the endpoint's own (the session starts and runs as usual) and still
rejects any other difference; `SetZone` refuses on a suspended endpoint, because it edits the same subjects; `limits`
keeps working, and keeps the suspension, because it updates only the caps.

Sealing is rejected because it cannot be undone and a sealed stream accepts no configuration update, which would also
stop `limits`. A deny rule is rejected because a per-recipient deny means rewriting every sender's permissions, and the
state would live in the policy file, not on the inbox it guards. A paused consumer is rejected because it stops reads,
not arrivals.

### Consequences

* Good: the guarantee is the server's; a sender cannot bypass it, and no new permission, subject or component exists.
* Good: the drain has a known end, `last_seq`, for the first time.
* Bad: mail sent while suspended is refused, not queued; the sender must retry after `resume`. A sender cannot tell
  "suspended" from "no such inbox" from the error alone.
* Bad: the state is two edits of one stream (subjects and metadata) in one update; a crashed admin cannot leave half of
  it, but a stream edited by hand to the parked subject without the metadata is reported as a configuration difference, not as suspended.
* Bad: it relies on the server keeping a stream's `metadata` through updates (nats-server 2.10 and later).

### Confirmation

* Tests against an in-process server: after `Suspend` a publish with acknowledgement fails and a stored message is still
  read through the consumer; `Resume` restores delivery and the original subjects, zoned and legacy; both are idempotent;
  `Suspended` reports the last sequence after a burst of concurrent publishes; `EnsureInbox`, `limits` and `Deprovision`
  work on a suspended endpoint and `SetZone` refuses.
* An endpoint's own credentials cannot update its stream.
* A `mashctl` test confirms the exit statuses 0, 3 and 1.

## Pros and Cons of the Options

### Seal the stream

* Good, because it is one flag and the server rejects publishes.
* Bad, because it is permanent and blocks every later configuration update.

### A policy-file deny

* Good, because the policy file is the one place for who may mail whom.
* Bad, because it needs every sender's JWT rewritten and cannot say what the inbox's last sequence is.

### Pause the consumer

* Good, because it is trivial.
* Bad, because mail keeps arriving.

### Park the subjects

* Good, because it is reversible, server enforced, and touches one stream.
* Bad, because senders see a missing inbox, not a named refusal.

## More Information

* Issue 130, whose author's retirement design depends on it.
* Not yet true in code: all of it. It lands in the change that follows this record.
