# Numbering sites: every place that keys on the bare stream sequence

Naming plan step 1.4a. During the transition an endpoint reads two streams (the legacy inbox stream and a new one) whose
sequence numbers collide, so a structure keyed on the bare sequence would merge two different messages. Each site below
is keyed on `(stream, seq)` after 1.4a (`spool.Ref{Stream, Seq}`; `endpoint.MsgRef` is the same type). The endpoint's own
(primary) stream is `Stream == ""`, so a single-stream endpoint produces the same keys, file names and output as before.
Line numbers are those before the change.

Classes: (a) a key or lookup: becomes `Ref`. (b) display or wire only (what the model or a peer sees, a log line): stays
a bare number in 1.4a; the qualified form `#L5` is step 1.4b. (c) the stream itself, not a message number.

## internal/spool/spool.go

| Line | What | Class | Change |
|---|---|---|---|
| 28 `Entry.Seq` | the number a message carries on disk and to render | a/b | stays, plus `Entry.Stream` (`json:"stream,omitempty"`, absent for the primary stream, so a single-stream entry is byte-identical); `Entry.Ref()` |
| 63, 67, 71 `listedFile`, `shownFile`, `file` | file names `<seq>` and `<seq>.json` | a | take a `Ref`; primary `<seq>`, other stream `<stream>@<seq>` (old files named `<seq>` read as stream "", written the same) |
| 96 `ClearWaiting`, `seqOf` | recognises waiting files by `<seq>.json` | a | `parseRefName`: a name is a ref; `ClearWaiting` still clears every stream (serve restart) |
| 133 `Bind(stream)` | drops every tombstone, waiting file and mark when the stream's identity changed; one `.stream` marker | a/c | per stream: `BindStream(name, ident)` clears only that stream's files and marks, marker `.stream` for the primary, `.stream.<name>` for another; `Bind(ident)` is `BindStream("", ident)` |
| 189 `Waiting` | lists entries, checks `e.Seq == seq`, marks `Redelivered`/`Listed` by name in `shown/` and `listed/`, sorts by `Seq` | a | compares `Ref`, marks by `Ref` file name, sorts by (stream, seq) (primary first) |
| 230 `MarkShown(seqs...)` | `shown/<seq>` | a | takes `...Ref` |
| 246 `Listed(seq)`, 277 `IsListed(seq)` | `listed/<seq>` | a | take `Ref` |
| 288 `Drained(seq)`, 309 `IsDrained(seq)` | `drained/<seq>` tombstone | a | take `Ref` |
| 316 `Acked(seq)` | removes tombstone, file, marks | a | takes `Ref` |
| 487 `Has(seq)` | waiting file parses | a | takes `Ref` |

## internal/endpoint/mail.go

| Line | What | Class | Change |
|---|---|---|---|
| 119-139 `seenSet.seqs`, 168 `dup`, 212 `mark`, 232 `pruneSeqs` | shown stream sequences, so a redelivery is dropped | a | `map[Ref]struct{}`; `pruneSeqs` prunes only primary-stream refs against the ack floor (the floor belongs to the primary consumer; a second stream gets its own floor in 1.4b) |
| 153 `seenEntry.seq`, 175, 211 | the sequence of the delivery a `(user, nonce)` was shown at | a | `ref Ref` |
| 320 `waking.received(meta.Sequence.Stream)` | startup-backlog accounting | a | `received(Ref)` |
| 333 `render.Mail.Seq` | message number | a/b | `Mail.Seq` stays (what render prints); `Mail.Stream` added (zero for primary); `refOf(Mail)` is the key |
| 337 `relayInbound(p.mail.Seq,...)`, relay.go:96, 106 `relayRecv.seq` | the last inbound mails, for a log line | b | unchanged (log text `#n`); not a key |
| 338 `waking.add(p.mail.Seq, ...)` | unread set | a | `add(Ref, wakes)` |
| 367 `held{msgs, at, rung, fails}` | the batch map, per-message spool times, rings, failures | a | all `map[Ref]...` |
| 402 `spool.IsDrained(p.mail.Seq)` | tombstone check before showing | a | `IsDrained(refOf(p.mail))` |
| 428-435 `dedupe`: `h.msgs[p.mail.Seq]`, `waking.done` | already-held redelivery | a | by `Ref` |
| 876-895 `deliverBatch`: `strconv.FormatUint(p.mail.Seq)` for meta `seqs`, `waking.done` | channel event `seqs` meta and ack | a/b | the meta string stays the bare number (display); `done(Ref)` |
| 466 `SendResult.Seq`, 549, 678 | the recipient's inbox sequence, the sender's receipt | b | unchanged: the number on another endpoint's primary stream |
| 684 `viaOrigin(seq)`, 689 `GetMsg(seq)` | read the own inbox message to declare a relay | a | primary stream (`GetMsg` on the primary); `Via` is signed and wire-format: unchanged |
| 759, 762 `render.Mail{Seq: raw.Sequence}`; 767 `Read(seq)`, 772 `GetMsg`; 794-809 `Inbox`, `InboxAfter`, `collect`, `seqsDown`, `seqsUp` with `FirstSeq`/`LastSeq` | read and inbox tools | a | read by `GetMsg` on the primary stream, `Stream` left "" (single stream). A second stream's reader is 1.4b. `ResolveRef` is the one place a typed `#n` / `#Ln` becomes a `MsgRef` |
| 36-87 `InReplyToHeader`, `ViaHeader` | signed `<seq>` the sender says this answers | b | unchanged (wire format, signed) |

## internal/endpoint/waking.go

| Line | What | Class | Change |
|---|---|---|---|
| 62 `unread map[uint64]bool` | received and not yet acked, by sequence | a | `map[Ref]bool` |
| 65 `last uint64`, 66 `haveLast` | the primary stream's last sequence at consumer start | a/c | `last map[string]uint64` by stream; `start(backlog, last, haveLast, probe)` still describes the primary stream |
| 67 `seen map[uint64]bool` | distinct startup sequences | a | `map[Ref]bool` |
| 94 `received`, 112 `add`, 133 `listed`, 149 `done` | by sequence | a | by `Ref` |
| 168 `reconcile`: `seq < first` | drop sequences the stream evicted | a | `first` is the primary stream's: only primary refs are dropped |

## internal/endpoint/hook.go

| Line | What | Class | Change |
|---|---|---|---|
| 137-150 `held` fields | `msgs`, `at`, `listed`, `rung`, `fails` | a | `map[Ref]...` |
| 163-194 `spoolBatch` | `IsDrained`, `Put`, `h.msgs`, `h.fails`, `h.at` by `p.mail.Seq` | a | `Ref` |
| 221-251 `settleHeld` | `IsListed`, `waking.listed`, `IsDrained`, deletes from the maps | a | `Ref` |
| 280-376 `wakeIfIdle` | `ring []uint64`, `seqs` sorted, `ids` (the wake meta `seqs`) | a/b | `ring []Ref`; the sort is by (stream, seq); `ids` stay the bare number for the primary stream |
| 425, 434-439 `failpointBeforeAck(seq)` | test-only stderr text `#%d` | b | unchanged |
| 449-460 `ack` | `waking.done`, `spool.Acked` | a | `Ref` |
| 465-488 `healHeld` | `Has`, `IsDrained`, `Put` | a | `Ref` |
| 492, 498 `EntryOf`, `MailOf` | `Mail <-> Entry` | a | carry `Stream` |
| 89 `s.Bind(name@created)` | binds the spool to the inbox stream's identity | a/c | `BindStream("", ...)` (the primary stream); 1.4b binds the legacy stream separately |

## internal/endpoint/hookless.go

| Line | What | Class | Change |
|---|---|---|---|
| 149 `MarkRead(seq)` | `IsListed`, `Has`, `IsDrained`, `Drained` | a | `MarkRead(MsgRef)` |

## cmd/mash

| Line | What | Class | Change |
|---|---|---|---|
| hook.go:124, 131-148 | sorts entries by `Seq`, `MarkShown`, `Listed`, `Drained` | a | by `e.Ref()` (sort by stream, then seq); log text stays `#%d` |
| serve.go:538-554 | `read` tool: `{seq}` -> `ep.MarkRead(a.Seq)`, `ep.Read(ctx, a.Seq)` | a | the argument is a number: `MarkRead(MsgRef{Seq: a.Seq})` (primary); `ResolveRef` is the parser to switch to when the argument becomes `#n` or `#Ln` (1.4b) |

## Not keyed on a message number

`internal/admin/admin.go:1200`, `internal/admin/suspend.go`, `internal/endpoint/dirpoll.go` use a stream's `LastSeq` for
drain and suspend checks and for directory polling: class (c), the stream itself, not message identity; unchanged.
`internal/endpoint/endpoint.go` (`JSErrCodeStreamWrongLastSequence`) is a publish precondition: unchanged.
`internal/render/render.go` prints `#%d` from `Mail.Seq`: display only; unchanged (1.4b).

## Obligations for 1.4b (second stream)

From the Codex review of 1.4a. None of these bites while only the primary stream has a consumer; each must land before
a second consumer is attached.

1. `seenSet` history for a named stream needs its own ack-floor pruning: `pruneSeqs` prunes only `Stream == ""`
   entries, so a named stream's entries would grow without bound.
2. The waking tracker's reconcile and startup accounting (the probe, the first sequence, the last sequence and the
   `received` readiness) are primary-consumer only and need per-stream equivalents.
3. Rendering, the channel and wake `seqs` metadata and the `read` tool must accept and print the qualified form
   (`#L5`) before a second consumer is attached; today they print and parse bare numbers.
