---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer)
---

# Serve keeps a local ledger of what it presented, written before the ack and reported in status

## Context and Problem Statement

The inbox line of `status` and the audit count mail lost unread from the consumer's sequence numbers. Those count
deliveries, not what the host was shown: a message fetched and never shown, a redelivery that hides a skip, and a message
acked without being shown all read as delivered. A message held unacked in the spool can also be evicted from the stream
(the byte cap discards unacked mail too) and then lost with its spool copy, and nothing records that it was never shown.
A zero in the inbox line therefore proves nothing. What can mash record, with no new permission, bucket or connection,
so that it can say what it handed to the host and what is missing?

## Decision Drivers

* A claim must be one mash can back: handing text to the host's input, not that the model read it.
* The record must be written before the ack, so a crash leaves the evidence and never an ack without it.
* A failure to record must not hold or lose mail: this step is informational.
* No change to account templates, permissions, buckets or the reconnect, and no change to delivery otherwise.
* A re-provisioned stream restarts its sequences at 1, so records must name the stream generation.

## Considered Options

* A local append-only ledger next to the spool, written by serve before the ack, reported in `status` (chosen).
* A per-message marker in a key-value bucket.
* A record per inbox generation in a hardened bucket, written before the ack, failing the ack closed.
* A signed receipt message per message to an audit inbox.
* Rely on the ack alone, or on a spool-required switch alone.

## Decision Outcome

Serve appends what it has handed to the host to `presented.log` in the endpoint's spool directory (same custody as the
spool: file 0600, directory 0700), and `status` reports it.

**What "presented" means.** A message is presented when mash handed its full text to the host's input: the hook wrote it
to stdout and the write succeeded, or the `read` tool returned it. The evidence is the tombstone a hook leaves in the
spool. A digest line is listed, not presented. Mash does not and cannot claim the model read it, that the host did not
truncate it, or that a later context compaction kept it. A channel hand-off is recorded as `channel`: delivered, not
proven, and never counted as presented. Mail dropped by design (a forged or malformed sender header) is recorded
`refused`. A message acked without being shown (the same signed message under another sequence, or a redelivery of one
already shown) is recorded `duplicate`, with `dup_of` when the original is known.

**Order and failure.** The tombstone exists, then serve appends the line and syncs it, then it acks, then it removes the
tombstone. One line covers every message drained in a pass. A crash anywhere leaves the tombstone, so the pass repeats;
writing a sequence twice is harmless because the ledger is folded as a set. A failed write is logged once, reported in the
`status` tool as `LEDGER WRITE FAILED`, and the ack goes ahead: this step never holds mail for the ledger. A redelivery of
a message that has a tombstone is recorded `presented` too (the tombstone is the evidence), so a crash between the tombstone
and the first write does not read as a loss.

**Format.** One JSON line per record: the generation (the stream identity the spool is bound to), a time, a kind and
sequence ranges with contiguous runs collapsed. Reading folds the file in memory into sets of ranges per generation. A
line that does not parse (a torn last write) is skipped and counted. Past 1 MiB the file is rewritten as one snapshot
line per generation through a temporary file and a rename, keeping the counts. Lines are appended with `O_APPEND` and kept
under 4096 bytes where they can be. Every append and every compaction (read, temporary file, sync, rename, directory
sync) holds an exclusive `flock` on a sidecar lock file, `presented.log.lock` (0600, opened without following a symlink),
taken before the log is opened, so two serves of the same session never interleave inside a line and a compaction never
discards a line another process appended after its read: an append lands either before the compaction's read (and is
folded into the snapshot) or after its rename (in the new file). A wait for the lock is bounded (3 seconds); past it the
write fails like any other ledger write failure (logged once, shown in status, the ack goes ahead). Readers take no lock
and tolerate a torn last line. Where there is no `flock` (not unix) the lock is a no-op.

**Report.** `mash status` and the `status` tool print
`delivery: N presented, M refused, K duplicate, J channel-acked (not proven) since <first record time>; loss: ...`, with
a sentence saying that presented means handed to the host's input, not that the model read it. From a shell the loss
part says it is not computed, because the command connects to nothing. In a session it is computed against the inbox
stream: the sequences from the lowest one the ledger covers for the generation up to the stream's first sequence that
the ledger does not account for are `at least L unproven`, and the evicted sequences below the ledger's first record
are `at least X more may be lost before the ledger started`. A sequence is accounted for by a presented, refused or
channel-acked record, or by a duplicate whose named original is recorded. A duplicate with no named original explains
nothing, so a loss cannot hide as a duplicate. The existing inbox eviction line is unchanged and is shown beside it.

**Staged plan.** This is step 0. Later steps are not part of this change: a hardened bucket holding one record per
endpoint and generation, written by serve before the ack and failing the ack closed, which the audit could read and
check against server state; and a strict switch that holds mail instead of acking a channel hand-off. Both need new
permission lines in the account templates (and the reconnect that a template change brings), so each needs its own
decision.

### Consequences

* Good, because mash can say what it handed to the host and list sequences it cannot account for, with no change to
  permissions, buckets, connections or delivery.
* Good, because the order of writes the later steps need is exercised first.
* Bad, because the ledger is local: it is lost with the state directory, it is the endpoint's own statement and not a
  signed receipt, and `mashctl audit` cannot read it.
* Bad, because a message the ledger never saw (before its first record) is reported only as a possible loss.
* Bad, because a message shown by a session whose state directory is not read again, then evicted, reads as unproven
  although a person saw it: the direction that errs toward claiming less.

### Confirmation

Tests cover: a hook display then serve's ack writing one line before the ack, with the ack held and the ledger already
written; a repeated write leaving the fold unchanged; an unwritable ledger still acking and the status line saying so;
the `refused`, `duplicate` and `channel` records, with channel not counted as presented and a duplicate of an unrecorded
original not accounted; the fold (runs collapsed, generations apart, a torn last line skipped and counted and not
swallowing the next write, a compaction round trip); the exact status wording in the tool and in `mash status`; the loss
computed against given stream states; and the spool's scans (waiting, listed, drained, clearing, rebinding) ignoring the
ledger file.

## Pros and Cons of the Options

### Local append-only ledger (chosen)

* Good, because it needs no permission, bucket or connection and costs one synced append per drain pass.
* Good, because a failure to write cannot lose or hold mail.
* Bad, because it is local and self-attested, and the audit cannot read it.

### Per-message marker in a key-value bucket

* Good, because it is simple to read back.
* Bad, because it adds a publish and its acknowledgement per message and stores about 200 bytes per message without
  bound, and the endpoint needs a new permission.

### One record per generation in a hardened bucket

* Good, because the audit could read it and check it against server state.
* Bad, because it needs new permission lines in the account templates, the drift check and the reconnect that follows a
  template change; it is the next step, not this one.

### Signed receipt message per message

* Good, because it is tamper-evident.
* Bad, because it costs a message per message, the audit inbox is bound by the same limits whose losses it would prove,
  and every endpoint needs a new publish grant.

### The ack alone, or a spool-required switch alone

* Good, because they cost nothing per message.
* Bad, because the server keeps an ack floor and counts, not the set of acked sequences, so neither says what was lost
  or tells a presentation from a duplicate or refused ack; the spool-required switch is necessary for a proof and does
  not provide one.

## More Information

* Related: [ADR 0107](0107-mail-delivery-fails-closed-under-one-concrete-setting-mash-spool-required.md),
  [ADR 0046](0046-a-digest-lists-mail-it-does-not-ack-it.md).
