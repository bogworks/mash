# Delivery proof: how `mashctl audit --strict` proves that no mail was lost

> **Drafting status.** This is a design, not a description of shipped behaviour. Nothing here is built except what is
> marked as existing. Each statement about code names a function and a file, which a reader can check on `main`, or
> carries a visible `Pending:` note when the code is on an open branch. Where behaviour was not checked it says
> `not verified`.
>
> | Item | Where in this file | Waits for | State of the text |
> |---|---|---|---|
> | The local presented ledger (`internal/ledger`, `internal/endpoint/ledger.go`, ADR 0109) | sections 2, 4.2, 4.3 | `pr/core/presented-ledger` | Written from that branch as it stands. The ledger is not on `main`. |
> | `MASH_SPOOL_REQUIRED` (ADR 0107) | sections 3.2, 4.8 | nothing, merged | On `main`. |
> | Everything else (the bucket, the delta keys, the audit, the deprovision gate) | sections 4 to 11 | the pull requests of section 11 | Design only. |
>
> The eight numbered decisions in section 3 are provisional. They were set by the maintainer's planner and the
> maintainer can overturn any of them at review. Section 3.9 lists six further decisions this design makes on its own,
> for the same review.

> **Review history.** A security review of the first version found four design flaws. This version changes:
>
> | # | Finding | Change | Where |
> |---|---|---|---|
> | H1 | The gate was specified in `deprovisionLocked` only. `DeprovisionIfUser` (which `mashctl retire`, `deprovision --user` and the rollback of `mashctl add` call) and `DeprovisionDrained` have their own removal paths and could remove an inbox with no proof check. | One shared step, `Admin.guardRemoval`, runs in all three before any destructive action and hands out a token that `deleteInbox` requires. A table test covers every exported removal entry point and a structural test fails when a removal path bypasses the step. | 3.9, 4.7, 6, 7, 11 |
> | H2 | The gate read `LastSeq` twice and left a window in which new mail arrived after the final check and was deleted with the stream. | The gate fences first (the existing suspend mechanism parks the inbox's subjects), then reads, evaluates and deletes only under the fence. A refused gate unfences. | 4.7, 6, 7 |
> | H3 | `proof rebase` moved the base past lost or channel-acked mail, so the strict findings no longer included that history and an operator could make `--strict` pass over a known loss. | A rebase never erases evidence: it writes an `excluded` record in the archive and `--strict` reports it, for as long as the generation exists. Only a new generation gives a clean strict. | 3.9, 4.4, 4.5, 4.8, 8, 10 |
> | H4 | Delta keys carried sequence bounds but no window token, so a subject filter could not select one numeric window, and the compaction purge could erase another window's facts that no snapshot covered. | The window is its own key token. Compaction writes the window's snapshot, verifies it covers exactly that window's facts, and only then purges by a filter on that window with a sequence bound. | 4.1, 4.6, 5, 7 |

## 1. Purpose and the claim

Today `mashctl audit --strict` cannot prove that no mail was lost. `store.Eviction` (`internal/store/eviction.go`)
counts mail the consumer never received, and `inboxProblems` (`cmd/mashctl/audit.go`) raises a problem only when that
count is above zero. A zero therefore says nothing about mail that was fetched and never shown, mail that was redelivered
(which hides a skip in the consumer's sequence numbers), or mail that was acked without being shown. The step 0 change
(ADR 0109) records on the endpoint's own machine what serve handed to the host, and `status` reports it. The audit cannot
read that file.

This design moves the record to the server, in a form the audit can read and the endpoint cannot rewrite, and builds on
it two checks: `audit --strict` and a gate on `deprovision`.

**The claim.** For an inbox whose delivery record covers its current stream generation, `mashctl audit --strict` exits 0
only when every message the stream took, from the record's base up to the stream's last sequence, is either still in the
stream, or was recorded by the endpoint as handed to the host's input (presented), refused by design, or a duplicate of
one that was presented. A message that left the stream with no such record is reported as lost, whatever removed it.

**The honest limit.** mash proves that it handed the text of a message to the host's input and that nothing it received
was dropped without a record. It does not prove, and cannot, that the model read the message, acted on it, that the host
did not truncate it, or that a later context compaction kept it. The record is the endpoint's own statement, written by
its own credential. It is not a signed receipt, and an endpoint that lies, or one whose credential is held by someone
else, can overclaim. The audit checks the record against server state (section 4.4) and catches an impossible claim, not
a false one that is possible.

What this design does not do: it does not change how mail is delivered by default, it does not make mash prove anything
about messages that left the stream before the record's base, and it does not give the operator a way to make `--strict`
pass over a loss.

## 2. Definitions

| Term | Meaning |
|---|---|
| **delivered** | The consumer handed the message to the endpoint. The consumer's sequences count this (`Delivered.Stream`, `Delivered.Consumer`). A redelivery counts again. |
| **presented** | The full text was handed to the host's input: a hook wrote it to stdout and the write succeeded, or the `read` tool returned it. The evidence is the tombstone a hook leaves in the spool (`spool.Spool.Drained`, `internal/spool/spool.go`). A digest line is *listed*, not presented (decision 7, ADR 0046). |
| **channel-acked** | The endpoint wrote the message to the host's channel and acked it (`deliverBatch`, `internal/endpoint/mail.go`). A channel notification is a hint, so this is delivered, not proven shown. It is never counted as presented. |
| **refused** | Mail dropped by design: a forged or malformed sender header (`ReadRef`, `internal/endpoint/mail.go`). |
| **duplicate-of(t)** | Acked without being shown again, because the same signed message was shown under sequence t (`dedupe`, `internal/endpoint/mail.go`) or because a hook had already shown this one (the `IsDrained` branch of `spoolBatch`, `internal/endpoint/hook.go`). It counts only if t is presented. A duplicate that names no original, or whose original is not presented, explains nothing. |
| **generation** | The identity of one stream's life: the stream's creation time. Sequences restart at 1 when a stream is deprovisioned and provisioned again, so every record is keyed by generation. ADR 0109 writes it as `name@created` (`streamIdent`, `internal/endpoint/ledger.go`); in a key it is the creation time in Unix nanoseconds, digits only. |
| **base** (B) | The first sequence the record covers for a generation. Below B nothing is claimed. It is written explicitly (section 4.1), never inferred from the lowest recorded sequence, so that the first messages of an inbox cannot fall below the base unnoticed. |
| **window** | Sequences are grouped by `floor(seq / 4096)`; that number is the window, and it is its own token in every key (section 4.1). |
| **delta key** | One KV key that holds one fact about one range of sequences of one window, written once and never changed: `e.<flat>.<gen>.<window>.p.<lo>.<hi>` says sequences lo to hi of that generation were presented. |
| **excluded** | A set of lost, unproven or channel-acked sequences that an operator moved the base past (`proof rebase`). They stay findings of class `excluded` for as long as the generation exists (section 4.8). |
| **accounted** | A sequence that is presented, refused, or a duplicate of a presented one. (ADR 0109's `Gen.Accounted` also counts channel-acked sequences. The proof does not: section 4.4.) |
| **lost** | A sequence in [B, FirstSeq-1] that is not accounted. The stream no longer holds it, so it can never be delivered or recorded again, except by a late push of a record already written locally (section 4.3). |
| **outstanding** | A sequence in [max(B, FirstSeq), LastSeq] that is not accounted: still in the stream, waiting or not yet recorded. Normal while mail is in flight. It becomes lost if it falls below FirstSeq unrecorded. |
| **unproven** | A sequence that would be lost, but the generation shows a takeover by a different session (decision 6), or that was channel-acked only. |
| **no record** | The delivery bucket holds no key for this inbox's current generation. |
| **no proof** | A record exists, but it does not cover everything: its base is above 1, or it holds unproven sequences. |

## 3. The decisions

### 3.1 A self-attested record is enough (decision 1)

The record is written by the endpoint's own credential and is not signed by a key the audit can tie to a presentation. A
lying endpoint, or a stolen endpoint credential, is outside the threat model for now. Signed receipts are recorded as a
**deferred option**.

Rejected: a signed receipt per message to an audit endpoint (ADR 0109's fourth option). It costs one extra mail per
message, the audit inbox is bound by the same 16 MiB and 30 day limits whose losses it would prove (so it needs its own
proof of delivery), and every endpoint needs a new publish grant to it. It is the only option that helps against a lying
endpoint. If the threat model changes, the delta keys of section 4.1 can carry a signature over each fact without a change
to the key layout, which is why the value schema has a version field.

### 3.2 Hold-until-shown is a setting, not mash's default (decision 2)

Holding mail until a hook or the `read` tool shows it is `MASH_SPOOL_REQUIRED` (`spool_required`, ADR 0107, on `main`:
`config.Config.SpoolRequired`, set up in `cmd/mash/serve.go`). The layer above mash, which owns named profiles, decides
which endpoints set it. It stays off by default. This design adds no new default and no new delivery setting. What it
adds is how the audit treats an inbox whose record contains channel-acked mail (section 4.8).

Rejected: making it the default. Hosts with no hooks (hookless Claude sessions, Pi) would then hold every message until
`read`, which changes behaviour for hosts that work today (ADR 0041 and ADR 0032).

### 3.3 Unread mail that aged out at 30 days counts as a loss (decision 3)

A message that leaves the inbox stream with no record that it was presented is lost, whether the byte cap (16 MiB,
`store.InboxMaxBytes`) or the age limit (30 days, `store.InboxMaxAge`) removed it. Once it is gone nothing can tell which
limit did it, and to an operator an unread message that aged out is an unread message that was lost. There is no
"aged out by policy" exemption.

Rejected: an exemption for age. It needs the arrival time of each message in the record and a rule about how long an
endpoint may be away, and it is a policy decision mash does not own.

This changes what an operator sees (section 8): an endpoint that was down for a month now makes `--strict` fail.

### 3.4 The endpoint writes only, with delta keys (decision 4)

The endpoint gets a publish-only grant on its own delta keys. It has no read grant on the bucket (no `DIRECT.GET`, no
`STREAM.INFO`, no consumer), so it cannot read the record back, cannot see another endpoint's, and cannot rewrite or
shrink what it wrote: a delta key can be written once (section 4.1), and a new key can only add a fact.

Rejected:

- One record per endpoint, rewritten by compare-and-set (ADR 0101's pattern with the endpoint as writer). The writer could
  put a smaller record, and the 64 retained revisions would detect a shrink only within the last 64 writes.
- One key per message. It adds a publish and about 200 bytes of storage per message without bound.
- A rollup-based design. The bucket refuses rollup so that nobody, the endpoint included, can replace a subject's history.

### 3.5 Deprovision is refused until audited (decision 5)

`deprovision` (and `retire`, which calls it) removes an inbox only if the inbox is accounted for in full and its record is
archived in an admin-side bucket. Every removal path reaches one shared step that fences the inbox first (section 4.7).
There is no bypass flag. When something is not accounted for, the operator runs an
explicit acknowledgement command that records the loss in the archive; the gate then accepts exactly that loss. Section
4.7 defines the condition, the archive and the flow.

Rejected: archiving the record and deprovisioning anyway, with a warning. That deletes the unread mail with the stream and
leaves only a log line. Also rejected: a `--force` flag. `retire` already has a `--force` that means something else
(remove an endpoint the policy file does not declare), and overloading it would let a habit bypass the gate.

### 3.6 A takeover that hides a shown message is reported as unproven, not lost (decision 6)

A message shown by one session, after which a different session of the same endpoint name took over, whose old spool is
never read again, and which was evicted before redelivery, is reported as `unproven`. The conservative direction is kept
(a false loss, never a false proof), but the wording does not say a person did not see it.

Rejected: reporting it as lost. A report that says "lost" about mail a human saw would be wrong in a way an operator would
learn to ignore. The cost of the distinction is that the audit cannot tell this case from a real loss in a generation
that also saw a takeover, so the **gate treats unproven exactly like lost** (section 4.7). The two differ in the words
the report uses, not in what is blocked.

### 3.7 A digest listing never counts as presented (decision 7)

A digest line, and a line of the `inbox` tool, tell the session a message exists and show a preview of its first line.
The spool marks the message listed (`spool.Spool.Listed`) and serve does not ack it (ADR 0046). It is not presented, and
nothing in the record is written for it.

Rejected: letting a fully conveyed preview (an `fyi` message) count. ADR 0046 settled that an ack means the message was
seen in full, and a second class of "seen enough" would make the proof depend on message kind.

### 3.8 Roll out per owner group (decision 8)

The new template lines are granted per owner group's scoped signing key, one group at a time, with a canary first
(section 4.9). Each group's endpoints see one disconnect and reconnect (ADR 0097).

Rejected: granting all groups in one push. It is one reconnect storm for every endpoint at once and gives no place to
find a mistake in the lines before they are everywhere.

### 3.9 Further decisions this design makes

These are not among the eight. They follow from them, and the maintainer should review them with the eight.

1. **The ack is never held for the remote record.** Serve writes the local ledger, then tries the remote write for a
   bounded time, then acks whether or not the remote write worked (section 4.2).
2. **Delta keys are write-once.** The bucket stream uses discard-new per subject with one message per subject, so a key
   cannot be overwritten, and a KV delete marker, which is only a publish to the same subject, is refused too.
3. **The admin compacts, and may purge by subject filter; nobody else can.** The bucket allows purge on the stream, and
   no endpoint template grants the purge subject. The bucket's `DenyDelete` stays on. A purge always names one window
   and a sequence bound, and happens only after a verified snapshot of that window (section 4.6).
4. **The base is written explicitly** by the endpoint at its first bind of a generation and may be raised by the admin
   (`proof rebase`), never lowered. A raise never erases evidence: it leaves an `excluded` record that `--strict`
   keeps reporting (section 4.8).
5. **One shared removal step, under a fence.** Every path that removes an inbox calls `Admin.guardRemoval` before any
   destructive action. It parks the inbox's subjects, reads the final state, evaluates the gate, and holds the park through
   deletion (section 4.7). A removal cannot delete mail that arrived after the check, because none can arrive.
6. **Key layout carries the window as a token.** `e.<flat>.<gen>.<window>.<kind>.<lo>.<hi>`, so one subject filter selects
   one window of one generation and nothing else (section 4.1).

## 4. Design

### 4.1 The data

#### The bucket

A new KV bucket, `mash-presented` (stream `KV_mash-presented`, subjects `$KV.mash-presented.>`), named beside
`DirectoryBucket`, `LeaseBucket`, `RetiredBucket` and `IssuedBucket` (`internal/store/names.go`). It is created hardened
from the first moment, the way `store.CreateIssuedBucket` and `store.CreateLeaseBucket` create theirs, by a new
`store.CreatePresentedBucket` and checked by `store.VerifyPresentedBucket` (`internal/store`). `mash admin init`
(`Admin.Init`, `internal/admin/admin.go`) creates it, and `mashctl init` does through the same call. The configuration:

| Setting | Value | Why |
|---|---|---|
| `AllowRollup` | false | Nobody can replace a subject's history. The KV default allows rollup and `KeyValueConfig` cannot say otherwise, so the stream is made directly, as `CreateIssuedBucket` does. |
| `DenyDelete` | true | No per-message delete, by anyone. |
| `DenyPurge` | **false** | The admin compacts by purging with a subject filter (section 4.6). The issue record sets this true because nothing ever needs to be purged there. Here no endpoint template grants the purge subject, and a test asserts it (section 7). |
| `Discard` | new | A full bucket refuses new writes and never evicts an old record (the same fail-closed choice as `IssuedConfig`). |
| `DiscardNewPerSubject` with `MaxMsgsPerSubject` 1 | on | Makes each key write-once. The server requires discard new and a per-subject maximum above zero for this flag; verified in nats-server 2.15.2 (`server/stream.go`, stream configuration check) and the flag exists in `jetstream.StreamConfig` in nats.go 1.55.0. The error a second write gets is `maximum messages per subject exceeded` (`ErrMaxMsgsPerSubject`, `server/store.go`). Its API error code over the wire, and that the flag behaves as intended against a live server, are `not verified`: the step 1 spike must check both. |
| `MaxAge` | 0 | Nothing expires. A TTL would destroy the evidence for exactly the long windows the audit cares about. |
| `Storage` | file | A restart must not erase the record. |
| `MaxBytes` | 128 MiB (`PresentedMaxBytes`) | Hosted NATS accounts reject a stream with no byte cap (the comment on the bucket size caps in `internal/store/store.go`). The size is derived in section 5. |
| `Replicas`, `AllowDirect` | as `CreateIssuedBucket` | The admin reads by direct get and by consumer. |

`VerifyPresentedBucket` returns an error wrapping `ErrPresentedBucket` unless the stream refuses rollup and delete,
discards new per subject with one message per subject, has no TTL, is on file storage, and has a byte cap. It never
changes the bucket. An existing bucket that is not as mash makes it is refused for writing (fail closed) and reported by
`init` and by the audit.

#### The archive bucket

A second bucket, `mash-presented-archive`, written only by the admin connection, for the final fold of an inbox's record
at deprovision and for acknowledgements (section 4.7). It follows the retired-marker bucket (`store.RetiredConfig`,
`store.VerifyRetiredStatus` in `internal/store/store.go`): no TTL, file storage, discard new, a byte cap, never expires,
and the issue record's rollup, delete and purge refusal (`DenyDelete` and `DenyPurge` true). History is 64 revisions per
key, the most a KV bucket keeps, so an acknowledgement or an `excluded` record added after the archive is kept beside it.
The revision limit must refuse the 65th write and never evict the oldest, so that evidence cannot roll off (the same
discard-new-per-subject setting as `mash-presented`; that this behaves so on the archive's config is `not verified`, and the
step 1 spike checks it). It is created by
`mash admin init` and, on a deployment from before it, on first use over the admin connection, as the issue bucket is.

#### The key layout

Keys are dot-separated tokens, so they can be matched by subject wildcards in a permission. A key has these tokens, in the
bucket's subject space `$KV.mash-presented.<key>`:

| Writer | Key | Fact |
|---|---|---|
| endpoint | `e.<flat>.<gen>.<window>.p.<lo>.<hi>` | sequences lo..hi were presented |
| endpoint | `e.<flat>.<gen>.<window>.r.<lo>.<hi>` | sequences lo..hi were refused by design |
| endpoint | `e.<flat>.<gen>.<window>.c.<lo>.<hi>` | sequences lo..hi were channel-acked (delivered, not proven shown) |
| endpoint | `e.<flat>.<gen>.<window>.d.<seq>.<of>` | sequence seq was acked without being shown again, as a duplicate of `<of>`; `<of>` is `x` when the original is unknown; `<window>` is the window of `<seq>` |
| endpoint | `e.<flat>.<gen>.b.<B>` | the endpoint's base for this generation (no window token: it is about the generation) |
| admin | `a.<flat>.<gen>.b.<B>` | a base raised by the operator (`proof rebase`, section 4.8) |
| admin | `s.<flat>.<gen>.<window>.<rev>` | a snapshot of one window, unioned with the deltas of that window (section 4.6) |

`<window>` is `floor(seq / 4096)` in decimal, so window w holds sequences `w*4096 .. w*4096+4095` (window 0 holds 1 to
4095). A range key's `lo` and `hi` are in the same window, and its window token is that window. Every key of one window of
one generation therefore matches the subject filter `e.<flat>.<gen>.<window>.>`, and no key of another window or
generation does, because a filter matches whole tokens (window 1 does not match window 11).

`<flat>` is the flat key of the *stream* being accounted: the bare name for a legacy endpoint, `agent_owner_name` for a
new-form one (`fixedPubKeyed`, `internal/admin/permissions.go`). Names contain no dot (the name rule: 1 to 48 lower-case
letters, digits and single hyphens, `docs/name-rule.md` and the check in `internal/ctl/ctl.go`; the new-form flat key joins its three tokens with
underscores, `sap.EndpointIdentity.Flat`), so a flat key is one token. `<gen>` is the stream's creation time as Unix
nanoseconds, in decimal. `<window>`, `<lo>`, `<hi>` and `<seq>` are decimal and `lo <= hi`.

**How ranges are named and bounded.** A delta range never crosses a window boundary, and serve splits a run that does
into two keys, each with its own window token. The bound makes a key's span at most 4096, which gives the reader a cheap
validity check (a range wider than 4096, a range across a boundary, or a window token that is not `floor(lo / 4096)` is
refused as malformed), lets compaction work window by window with a subject filter, and caps the cost of expanding a
range. Within a window, one key covers one contiguous run of one kind from one drain pass. Two passes that record
adjacent runs write two keys; they are never merged by the writer, because a key cannot be rewritten.

**Value schema (version 1).** The fact is in the key. The value is informational and is not needed to fold:

```json
{"v":1,"t":"2026-11-12T10:00:00Z","n":10,"s":"a1b2c3d4e5f60718","req":1}
```

`v` is the schema version: a reader that does not know a version counts the key as unreadable (a finding), never as
absent evidence of presentation. `t` is the write time. `n` is the number of sequences the key covers. `s` is a random
session id that serve generates once per process, and is how the reader sees that more than one session wrote a
generation (section 4.4). `req` is 1 when `MASH_SPOOL_REQUIRED` was on for that serve, so the audit can say whether the
setting was on when channel-acked mail was recorded. The `b` marker and the admin keys carry the same fields where they
apply. An admin snapshot value is described in section 4.6 and the archive value in section 4.7.

An example key, for the new-form endpoint `agent_owner_name` after 1,000,009 messages:
`$KV.mash-presented.e.agent_owner_name.1731412345123456789.244.p.1000000.1000009` (1,000,000 is in window 244). That is 79
bytes of subject. The longest a key can be, with a 146 byte flat key (three 48 character names), a 7 digit window and two 10
digit sequences, is about 225 bytes; that is far under the server's control line limit, and the step 1 spike confirms no
lower subject limit applies (open question 4).

**The base.** At its first bind to a generation, serve reads the stream and its consumer and writes
`e.<flat>.<gen>.b.<B>` with B = `Delivered.Stream + 1` of the consumer, or 1 when the consumer does not exist or has
delivered nothing. Every message above `Delivered.Stream` has not been delivered to anyone yet, so every such message
passes through the code that records it; a message at or below it may have been shown before the record existed, and is
outside the proof. For an inbox provisioned with the feature B is 1. The write is write-once, so the first bind's base
stands; a later bind cannot lower it. The effective base of a generation is the highest base among the `e` and `a`
markers. The reader reports an endpoint base above the lowest sequence the endpoint recorded as an inconsistency. This
amends ADR 0109, whose ledger takes the base to be the lowest recorded sequence: step 1 adds a local `base` record to the
ledger so that `status` and the audit agree (section 9).

**Permission template lines.** One publish line per endpoint, in `fixedPubKeyed` style (`internal/admin/permissions.go`):

```text
$KV.mash-presented.e.<flat>.>
```

with `<flat>` being `{{name()}}` in a zone key's template (`ZonePermissions`), the literal flat key in a user's own
permissions (`Permissions`, `NewFormPermissions`). The line is unchanged by the window token: `>` matches one or more
trailing tokens, so it covers `e.<flat>.<gen>.<window>.<kind>.<lo>.<hi>` and the shorter `e.<flat>.<gen>.b.<B>` alike, and
the wildcard does not depend on the key's length. Nothing else: no `$JS.API.DIRECT.GET.KV_mash-presented...`, no
`$JS.API.STREAM.INFO.KV_mash-presented`, no consumer subject, no subscribe line. A publish into a stream's subject needs
no API grant, and the acknowledgement comes back on the connection's own reply prefix, which the endpoint already uses
for its lease writes. The `e.` prefix keeps the admin's `s.` and `a.` keys outside the grant, so an endpoint cannot write
a snapshot or an admin base. A new-form endpoint with a legacy alias reads the alias's inbox too (`WindowBridge`,
`internal/admin/permissions_newform.go`), so it needs the same line for the alias's flat key, added next to
`legacyLeaseGrants` as a `legacyPresentedGrants`.

A test asserts, for every permission list mash makes, that these lines are the only ones that name the bucket, and runs
it against a live endpoint connection (section 7), as ADR 0101 does for the issue bucket.

### 4.2 The write path

Serve folds the local ledger into delta keys in `settleHeld` (`internal/endpoint/hook.go`), where it already calls
`ledgerNote` (the ledger branch) for every message a hook has drained in the pass. The order of one pass is:

1. **Tombstone.** A hook wrote the message to stdout and `spool.Spool.Drained` wrote `drained/<seq>` and then removed
   `<seq>.json` (`cmd/mash/hook.go`). This is the evidence.
2. **Ledger line.** `ledgerNote(ledger.Presented, refs...)` appends one line and syncs it (ADR 0109). From here the
   endpoint can always say, from its own disk, that these sequences were presented.
3. **Remote write.** New: `proofPush` turns the pass's records into delta keys (a run per kind per stream per window),
   publishes them as one asynchronous batch (`PublishAsync`) and waits for the acknowledgements for at most
   `proofWait` (1 second). A key that comes back as already written (`maximum messages per subject exceeded`) counts as
   written. Each key that is acknowledged gets a `pushed` line in the local ledger. A `pushed` line is not synced: if it
   is lost, the key is published again, is refused as already written, and counts as written.
4. **Ack.** `ackDrained` (`internal/endpoint/hook.go`) acks, as today, and `ack` waits for the server's confirmation under
   its 2 second `ackTimeout`.
5. **Tombstone removal.** `spool.Spool.Acked` removes the tombstone.

**The ack is not held for the remote write.** If step 3 fails or times out, step 4 goes ahead. The unwritten facts stay in
the ledger as unpushed, and a background retry (every pass while there are unpushed keys, with a backoff from 2 seconds to
60 seconds, at most 64 keys per attempt) writes them. At start serve folds the ledger, subtracts the pushed ranges and
pushes what is left, which is also how the ledger of a step 0 endpoint reaches the bucket the first time.

Why this order, now that the ledger exists. The earlier design note held the ack until the remote write succeeded, as
the only durable record. With the local ledger synced before step 3 there is a durable record already, and holding the ack
adds nothing against a crash: a crash anywhere leaves the tombstone and the ledger line, and the pass repeats. What
holding would add is protection against losing the state directory between the ack and the retry, at the price that any
condition that stops remote writes also stops acks: a template not yet granted (the whole rollout), a full bucket, an
admin who has not run `init`, a network partition between the endpoint and the account. Unacked mail is redelivered, runs
into `MaxAckPending` (1000) and then stops arriving, which is the same back pressure the byte cap exerts (ADR 0069).
Failing open on the ack costs only completeness in an outage: an acked message whose fact was never pushed and whose state
directory is then lost reads as outstanding, and, once it falls below FirstSeq, as lost. That is a false loss, never a
false proof, which is the direction ADR 0109 chose too. The attempt before the ack (bounded to 1 second) makes the remote
record exist before the ack in every healthy case.

Rejected: holding the ack (a hard dependency of mail delivery on a bucket and a permission). An opt-in setting that does
hold it, for a deployment that wants the stronger order, is a deferred option; it would sit beside `MASH_SPOOL_REQUIRED`
and is not part of this design.

**What happens on write failure.** The first failure of a spell is logged once and shown in the `status` tool, as ADR 0109
does for the ledger. Three classes are told apart:

| Failure | What serve sees | What serve does |
|---|---|---|
| Not granted: the publish is denied by the server | A permissions violation reported through the connection's error handler, and no acknowledgement. `not verified`: the exact behaviour of a denied JetStream publish in nats.go 1.55.0, and how fast it fails; the step 1 spike must measure it. | Stop pushing until the connection reconnects or 5 minutes pass, whichever is first (a template change takes effect at reconnect, ADR 0097), so a denied publish does not cost 1 second per pass. Status: `proof: not granted` |
| Bucket missing: no responders | An immediate no-responders error | Retry with the backoff. Status: `proof: bucket missing (run mash admin init)` |
| Bucket full, or any other server error, or a timeout | A server error or no acknowledgement within `proofWait` | Retry with the backoff. Status: `proof: push failing: <error>` |

In every case the status line also says how many keys are unpushed, and that mail is still acked.

The hook is not involved: it is a separate short process with no NATS connection and stays that way.

```mermaid
sequenceDiagram
    autonumber
    participant H as hook (mash hook)
    participant S as spool (state dir)
    participant E as serve (settleHeld)
    participant L as local ledger
    participant B as mash-presented
    participant J as inbox stream

    H->>S: MarkShown, write to host stdout
    H->>S: Drained: write tombstone, remove entry
    E->>S: IsDrained(seq)
    E->>L: Note(presented, seqs), fsync
    E->>B: PublishAsync delta keys (e.flat.gen.window.p.lo.hi), wait at most 1 s
    alt acknowledged, or already written
        B-->>E: PubAck
        E->>L: pushed line (no fsync)
    else denied, timeout, full, missing
        B--xE: error or nothing
        Note over E,L: the facts stay unpushed in the ledger,<br/>retried with backoff; status says why
    end
    E->>J: DoubleAck(seq), confirmed within 2 s
    E->>S: Acked: remove tombstone
```

### 4.3 The same facts for refused, duplicate and channel

`refused`, `duplicate` and `channel` take the same path through the ledger (`ledgerNote`, `ledgerDuplicate`) and the same
push, with kinds `r`, `d` and `c`. A duplicate is one key per sequence, because each names its own original in the key.
Only the `p` kind comes from a tombstone. Nothing writes `p` for a sequence from any other source, which is what keeps a
loss from looking like a presentation.

Pushes may be late: a delta written after its window was compacted is a new key in that window, and the reader unions it
with the window's snapshot (section 4.6). A fact only ever adds to what is accounted, so a late push can turn a sequence
the snapshot lists as lost into an accounted one, and nothing turns an accounted one back.

### 4.4 The read path

The admin side (`mashctl audit`, `mashctl proof check`, the deprovision gate) folds the bucket per inbox. It reads over
the admin connection, which the bucket's permissions do not restrict. It does not use KV `Get` or `Keys`, because a KV
read reports the latest value of a key and a delete marker as a deletion; the fold reads the stream's messages, by an
ordered consumer on `$KV.mash-presented.>` from the start, and treats every message as a fact. (The consumer's use against
this stream config is `not verified`; the step 1 spike covers it.) A KV delete or purge marker, which the stream would
refuse anyway, is counted as an anomaly finding and not as a removal.

For each (flat, generation) pair the fold builds a `ledger.Gen` (`internal/ledger`, the ledger branch): the sets of
presented, refused and channel-acked sequences, and duplicates with their named originals. It reuses `Ranges`, `Accounted`
and `Lost` from that package, so the audit and `status` fold by one rule. It checks each key before it counts it:

- the key parses, its range is at most 4096 wide and inside one window, its window token equals `floor(lo / 4096)`, and
  `lo >= 1`;
- the version is known;
- for the current generation, `hi <= LastSeq` of the stream, and the highest presented sequence is at most
  `Delivered.Stream`. A key above these is an **overclaim**: it is ignored, counted and reported as a finding;
- the facts of a window are the union of the highest-`<rev>` snapshot of that window and every delta key of that window
  still present. A snapshot never hides a delta: facts only add, so a duplicate (a delta that the snapshot also holds)
  changes nothing.

The generation of an inbox is the stream's creation time, which `auditInbox.Created` already carries. Keys of any other
generation of the same flat key are an old generation: the stream was deprovisioned (and archived) or recreated.

**The invariant.** With B the effective base, and F and L the stream's first and last sequence:

    lost        = [B .. F-1]  minus accounted
    outstanding = [max(B,F) .. L]  minus accounted
    accounted   = presented, refused, and duplicate-of(t) with t presented

and, when B > 1, `before = [1 .. B-1]` is outside the proof: the report says how many of those are evicted and how many
are still stored. A sequence a hook presented and the endpoint recorded is accounted for ever, even after the stream
evicts it; that is why the record is a set of facts and not a re-read of the stream.

**Classification.** The sequences of `lost` split three ways, in this order:

1. *unproven, channel*: sequences recorded channel-acked (`c` keys) and not presented. They were delivered and handed to the
   host's channel, and are not proven shown. (Duplicates of a channel-acked sequence inherit this.)
2. *unproven, takeover*: sequences at or below `Delivered.Stream` that are unrecorded, in a generation whose keys carry two
   or more distinct session ids `s` (decision 6). They were delivered, so a session held them; a different session wrote
   later; the first one's spool may never be read again.
3. *lost*: the rest, including everything above `Delivered.Stream` (never delivered at all) and every unrecorded delivered
   sequence in a generation with a single writing session.

The classes are exclusive and add up to `lost` before classification. All three make a finding. `unproven` and `lost`
differ in their words and in what the acknowledgement records, not in what they block.

**Excluded sets.** The fold also reads every `excluded` record under the generation's archive key (section 4.8). Each is a
finding of class `excluded`, reported with its date, the admin key id that ran it, its reason and its counts and ranges by
class (lost, unproven, channel-acked). It is not part of `lost`, `unproven` or `outstanding` above the raised base: the
sequences are below the base now, so those sums do not change, and the record is how the history stays visible. An
`excluded` record cannot be cleared: the archive refuses delete and purge, and nothing in mash removes a revision.

**States of an inbox.**

| State | Meaning |
|---|---|
| `proven` | A record exists, `lost` and `unproven` are empty, nothing is overclaimed. "No loss proven from #B." `outstanding` may be non-empty. When the generation has an `excluded` record the state is still `proven` from the base, and the report adds the `excluded` findings beside it; `--strict` fails (section 4.5). |
| `lost` | `lost > 0`. |
| `unproven` | `lost == 0` and `unproven > 0`. |
| `no proof` | A record exists but B > 1 with evicted sequences below it, or the generation's record is partly unreadable. Reported beside whichever of the above applies. |
| `no record` | The bucket holds no key for the current generation, and the stream holds or has held mail (`LastSeq > 0`). |
| `empty` | `LastSeq == 0`: nothing was ever stored, so there is nothing to prove. Not a finding. |

### 4.5 The audit and status changes

**Exact wording (`mashctl audit`, replacing the text of `inboxProblems`, `cmd/mashctl/audit.go`).** Sequences are given as
ranges, at most 5 ranges and then a count.

- State `lost`: `N message(s) lost: #a-#b were evicted from this inbox with no record that they were presented to the host (it was full or its mail aged out); the endpoint should read mail sooner, or a sender is flooding it (see docs/operations.md)`.
- State `unproven`, takeover: `N message(s) unproven: #a-#b were delivered and then evicted with no record, after a different session took this endpoint's name over; the earlier session may have shown them`.
- State `unproven`, channel: `N message(s) were handed to the host by the channel only (not proven shown): #a-#b; set MASH_SPOOL_REQUIRED=1 on this endpoint to hold mail until a hook or read shows it; mashctl proof rebase NAME starts the proof after them but records them as excluded, which stays a --strict finding`. When the keys say `req` was already 1 at the time, the second clause is left out.
- Class `excluded`, one line per record: `N message(s) excluded from the proof on <date> by <admin key id>: lost L (#a-#b), unproven U (#c-#d), channel-acked C (#e-#f); reason: <reason>`. It stays for as long as the generation exists.
- State `no record`: `no delivery record for this inbox (mash-presented has no entry for its current generation): M message(s) stored, E evicted; there is no proof either way`, followed by one of `; the endpoint is not granted the record lines` (the endpoint's directory record or creds say so, when the audit can tell) or `; the endpoint has not written one (an older mash, or its template does not grant it yet)`.
- Beside any state, `no proof`: `the delivery record starts at #B: #1-#B-1 (E evicted, S still stored) are outside the proof`.
- Overclaim: `the delivery record claims #a-#b, above this inbox's last sequence #L`.

The text `at least N unread message(s) lost` goes away when a record covers the generation. It stays, worded as before, for
an inbox with no record (the lower bound from consumer sequences, plus the line above), so no inbox reads cleaner than it did.

**JSON, additive** (`version` stays 1: it changes only when a field is removed or changes meaning). `auditInbox` gains:

```json
"delivery": {
  "state": "proven|lost|unproven|no_record|empty",
  "generation": "1731412345123456789",
  "base": 1, "base_source": "endpoint|admin",
  "presented": 0, "refused": 0, "duplicate": 0, "channel": 0,
  "lost": 0, "unproven_takeover": 0, "unproven_channel": 0, "outstanding": 0, "before_base": 0,
  "lost_ranges": [[a,b]], "overclaim": 0, "unreadable_keys": 0, "sessions": 1,
  "spool_required": true,
  "excluded": [{"at":"2026-11-12T10:00:00Z","by":"<admin key id>","reason":"...",
                "lost":[[a,b]],"unproven":[[c,d]],"channel":[[e,f]]}]
}
```

`evicted_unread` stays with its present meaning, documented as the consumer-sequence lower bound. `summary` gains
`delivery_lost`, `delivery_unproven`, `delivery_no_record`, `delivery_overclaim`, `delivery_excluded` (inbox counts) and `delivery_bucket`
(`ok`, `missing`, `unhardened`, `near_full`, `full`, with the bytes against the cap, as `retired` does).

**Exit codes** are unchanged: 0, 1 for `--strict` findings, 2 for usage, 3 for a bad `--compare` file. Without `--strict`
the audit reports and exits 0 as today. With `--strict`, `findings()` also counts: every inbox in state `lost` or
`unproven`, every inbox whose current generation has an `excluded` record (whatever its state), every inbox in state `no record` that has mail, every overclaim, every unreadable key, a presented bucket that
is missing, unhardened or at least 90 percent full (a full bucket makes the proof impossible, unlike a full inbox, which is
operational state), and a delivery bucket that could not be read. `outstanding` is not a finding: mail in flight is normal.
An acknowledgement is **never** honoured by `--strict`: an acknowledged loss still fails it, and the report says
`(acknowledged)` beside it. Neither is an exclusion: a `proof rebase` does not clear it, and the generation fails `--strict`
for as long as it exists. Strict means no loss, not no unacknowledged loss.

**`mashctl proof check NAME`** (new, read only) prints the same evaluation for one endpoint, with the full range lists, and
exits 0 when the state is `proven` or `empty`, 1 otherwise, 2 on usage or connection failure. It is the evaluation the
deprovision gate runs.

**`status` and `mash status`** (`LedgerLine`, `internal/endpoint/ledger.go`; `ledger.Summary.Text`). The delivery line gains
the remote part after the loss clause: `; remote record: all pushed` or `; remote record: K unpushed (<reason>)`, with
`<reason>` one of `not granted`, `bucket missing`, `push failing: <error>`. From a shell it says `remote record: not
checked`, because the command connects to nothing.

### 4.6 Compaction

Deltas accumulate: one key per drain pass at best (section 5). The admin compacts by window. A window is compacted when
every one of its sequences is decided: all are accounted, or all lie below FirstSeq. `mashctl proof compact [NAME]`
(new; for all endpoints without NAME) does, per such window:

1. Read the window's deltas (the subject filter `$KV.mash-presented.e.<flat>.<gen>.<window>.>`) and its current snapshot,
   if any (`s.<flat>.<gen>.<window>.>`), and note `R`, the stream sequence of the last message read.
2. Write `s.<flat>.<gen>.<window>.<rev+1>` with the folded window: counts per kind (presented, refused, channel,
   resolved duplicates), the unaccounted sequences of the window as ranges (`lost`), the duplicates still unresolved with
   their named originals, and the sessions seen. A window whose sequences are all accounted is a few hundred bytes.
3. **Verify.** Read the snapshot back and check that it covers exactly the facts of that window: the count per kind equals
   the count of the facts read in step 1, and the union of its ranges equals the union of the facts read in step 1 (the
   previous snapshot's facts included). A snapshot that fails either check is left in place (a duplicate, harmless), is
   reported, and nothing is purged.
4. **Purge, only after step 3 passed.** Purge by exactly two filters, each with a sequence bound: the window's deltas,
   `$KV.mash-presented.e.<flat>.<gen>.<window>.>`, bounded to messages below `R + 1`, so any delta written after the read
   in step 1 (a higher stream sequence) is kept; and the window's older snapshots, `s.<flat>.<gen>.<window>.>`, bounded to
   messages below the new snapshot's own stream sequence, so the new snapshot stays. No purge ever uses a broader filter
   (not the generation's `e.<flat>.<gen>.>`, not a flat key, not a kind), so no other window's facts can be erased, and a
   fact that no verified snapshot of its own window holds is never purged.
   `not verified`: that a purge request with a filter and a sequence bound does exactly this against nats-server 2.15.2
   (the sequence field of the purge request purges messages below it), and that the window filter matches only the
   window's own keys; the spike checks both.

A late delta for a compacted window is a new key, and the next compaction folds it into the next revision. A snapshot's
`lost` ranges are not final: a late push clears them (section 4.3). Compaction is idempotent and crash safe: the new
snapshot is written and verified before anything is purged, the reader unions the highest revision with the deltas still
present, and a crash between steps 2 and 4 leaves the snapshot and the deltas it covers side by side. That state is
duplicates of facts, which are harmless because the fold is a union, and the next run folds the window again and purges.
A failure of the purge itself leaves the same state.

The endpoint cannot compact. It has no purge grant, no read grant, and no permission on the `s.` and `a.` keys, and the
bucket refuses rollup and delete. The audit reports the bucket's bytes against its cap, and `near_full` from 90 percent,
with the advice to run `mashctl proof compact`. `mashctl proof compact` is meant to run from the operator's scheduler;
nothing in mash runs it unattended (open question 3).

**What happens at the bucket's limits.** At the byte cap, discard-new refuses the next write: endpoints see
`proof: push failing: ...` and keep acking (section 4.2), their facts stay in their ledgers, and the audit reports
`delivery_bucket: full` as a finding. Running `proof compact` frees space, and the endpoints' retry pushes the backlog.
There is no per-key history limit to reach, because a key is written once. The stream's message count is unbounded
(`MaxMsgs` -1), so the byte cap is the only limit; the account's JetStream storage limit (`docs/operations.md`, "Account
limits") must have room for it.

### 4.7 The deprovision gate

**The structure of removal on `main`** (checked in `internal/admin/admin.go`, `internal/ctl/ctl.go`,
`cmd/mashctl/retire.go`, `cmd/mashctl/add.go`). There are three exported removal methods on `*admin.Admin`, and each has
its own body, not a call to a shared one:

| Entry point | Reached by | What it does, in order |
|---|---|---|
| `Admin.Deprovision` (calls `deprovisionLocked`) | `mashctl deprovision NAME` and `mash admin deprovision NAME` (the verb is one function in `internal/ctl/ctl.go`, shared by both) | lock the name (`lockStream`), alias guard, read the record, `beginRetire` (pending marker), `revokeLocked`, lease `fence`, `purgeDirectory`, `finalizeRetire`, `deleteInbox` |
| `Admin.DeprovisionIfUser` | `deprovision NAME --user KEY` (both tools); `mashctl retire` (through the test seam `deprovisionIfUser`, `cmd/mashctl/retire.go`); the rollback of a failed `mashctl add` (`cmd/mashctl/add.go`) | lock, alias guard, read the record and check its user, `beginRetire`, `revokeLocked`, guarded `purgeDirectory`, `finalizeRetire`, then `fenceAndDeleteInbox` (lease `fence`, `deleteInbox`). A name with no record but a retired marker goes through `finishAfterPurge`, which ends in `fenceAndDeleteInbox` too |
| `Admin.DeprovisionDrained` | `deprovision NAME --drained-to SEQ --suspension ID` (both tools) | lock, requires the inbox suspended with the given id and `LastSeq == SEQ`, read the record, alias guard, `beginRetire`, `revokeLocked`, guarded `purgeDirectory`, `finalizeRetire`, `fenceAndDeleteInbox`; or `finishAfterPurge` when the record is gone |

All paths delete the stream in one place: `Admin.deleteInbox` calls `DeleteStream`, and `deleteInbox` is the only caller of
`DeleteStream` in the code. The destructive actions that come before it differ by path, and the revocation, the retired
marker and the directory purge each happen well before the stream is deleted. So a gate placed in `deprovisionLocked`
(the first version of this design) is reached only by plain `Deprovision`: the other two would remove an inbox, and
`retire` calls `DeprovisionIfUser`.

**The one shared removal step: `Admin.guardRemoval`** (new, in a new file `internal/admin/removal.go`). Every removal path
calls it, once, with the name's lock held (`lk`) and after its own non-destructive checks (alias guard, the record read
and the ownership or suspension-id checks, which refuse without changing anything), and **before the first destructive
action**: before `beginRetire`, before `revokeLocked`, before any `purgeDirectory`, before the lease `fence`, and before
`deleteInbox`. It runs the sequence below and returns either a refusal (`*refusedError`, which `admin.IsRefusal` and the
existing refusal text of `retire` and `deprovision` already handle: nothing was changed) or a `*removalFence`, a token
that proves the inbox is parked, was evaluated and archived. The entry points are made to use it as follows:

- `deprovisionLocked`: call it after the record read and before `beginRetire`.
- `DeprovisionIfUser`: call it once, after the first ownership check passes (a record that belongs to another user refuses
  earlier, with no fence), before `beginRetire`. `finishAfterPurge` calls it before `fenceAndDeleteInbox`, since a removal
  that stopped after the purge still has an inbox to delete.
- `DeprovisionDrained`: call it after its existing checks and before `beginRetire`. The inbox is already suspended by the
  operator (the fence is held already, see below).
- `fenceAndDeleteInbox` and `deleteInbox` take the `*removalFence` as a required parameter, and `deleteInbox` refuses a nil
  one. A new removal path cannot reach the deletion without having called `guardRemoval`, and a missing call is a compile
  error at the call site, not a review finding.
- `mashctl retire`, `mashctl deprovision` and `mash admin deprovision` are made to use it by being unchanged: they reach
  the three methods (the table above), so they get the gate with no code of their own. The only CLI change is the
  `--keep-suspended` flag (below), which weakens nothing.

The token also records whether this call parked the inbox. A `defer tok.release(ctx)` in each entry point resumes the
inbox on every return that comes before the first destructive action (a refusal by the gate, a later non-destructive
refusal such as a record that changed). The first call to `beginRetire`, `revokeLocked` or `purgeDirectory` marks the token
`removing`: from then on the park is held, never resumed by this call, through to the deletion, because the removal is
under way and a re-run finishes it.

**The sequence (the fence).**

1. *Fence.* Park the inbox's subjects with the existing suspend mechanism (`Admin.Suspend`, ADR 0057, which `mashctl
   suspend NAME` runs: `internal/admin/suspend.go`). It saves the stream's subjects in the stream metadata and replaces them
   with the one subject `mash.suspended.<name>` (`store.SuspendedSubject`), which sits outside `agents.>`, so no sender's
   publish finds the stream and no new message can be stored. `Suspend` takes the name's lock itself, so `guardRemoval`
   uses a new `suspendLocked` (the body of `Suspend`, taking the held `lk`) and `Suspend` becomes a wrapper around it; the
   same for `resumeLocked`. An inbox that is already suspended is used as it is (`Suspend` is idempotent). A stream that
   is absent has nothing to fence, and the gate passes (below). The fence adds one metadata key,
   `mash_suspended_by=removal-gate`, to a park this call makes, so a later run can tell the gate's park from the operator's.
2. *Settle and read the final state.* `settledState` reads `LastSeq` until it is unchanged across two reads 50 ms apart
   (it exists because a publish the stream accepted before the update can still be queued for ingest and stored after the
   update returns; it stops after 2 seconds and never fails for want of a quiet stream). The gate requires that its last two
   reads were equal; a stream that did not settle in time refuses with a retry message and unfences. Then read, under the
   fence and once: `FirstSeq`, `LastSeq`, the consumer's `Delivered.Stream`, the directory record, and the delivery record
   of the current generation from the bucket (section 4.4). Mail that arrived before the fence is in `LastSeq` and is
   counted. Mail arriving after the fence is refused by the stream's subject, so `LastSeq` is final.
3. *Evaluate* the condition below on exactly those values.
4. *Decide.* If the condition fails, release the fence (`resumeLocked`) unless the token's park was not the gate's (the
   operator had already suspended the inbox, or `DeprovisionDrained` is the caller) or the operator passed `--keep-suspended`,
   and return the refusal. If it holds, write the archive entry (below) and read it back with the same fold.
5. *Delete, only under the fence.* Before the first destructive action, re-read `LastSeq` and compare it with step 2; a
   difference refuses (a safety check on the fence, not a substitute for it: under the fence it cannot differ) and
   unfences. Then the path's own destructive steps run (marker, revocation, record purge, lease tombstone) and
   `deleteInbox(tok)` deletes the stream. The fence is held through all of it; it is never released between the check and
   the deletion.

**What a crash leaves.** After step 1 and before step 5, the stream is parked with `mash_suspended_by=removal-gate` and
nothing else changed. `mashctl audit` shows the inbox as `suspended` (the existing field), and `mashctl resume NAME`
restores it; no mail is lost, only refused for the time it is parked (senders see no stream, as with any suspend). Running
`deprovision` again finds a park carrying the gate's tag, treats it as its own and starts again at step 2. After step 5
begins, the failure rows of the existing removal apply unchanged (`PARTIAL`, a pending retired marker, a re-run finishes
it) and the inbox stays parked, so no mail is stored in between.

`--keep-suspended` (a new flag on `deprovision` and `retire`, accepted by all three forms) leaves the inbox suspended when
the gate refuses, for an operator who wants to drain it without new mail; the refusal text then says so and gives
`mashctl resume NAME`. It does not skip the gate.

**The condition.** The gate passes when the inbox stream is absent, or the stream, read under the fence, has never stored
a message (`LastSeq == 0`; the rollback calls that provisioning makes freely, which now fence an empty inbox for a moment
before deleting it, so a message cannot slip in between), or all of these hold, evaluated against the values of step 2:

1. the evaluation of section 4.4 finds a record for the current generation (state is not `no record`);
2. `lost + unproven + outstanding == 0` over `[B .. LastSeq]`: every message the stream stored since the base was presented,
   refused, or a duplicate of one that was. Mail still waiting in the stream blocks, because deleting the stream would
   destroy it unread;
3. no overclaim and no unreadable key;
4. the archive entry (below) was written and read back, with the same fold.

`unproven` blocks as lost does (section 3.6). Sequences below B (`before`) do not block: they are outside the proof, and
the archive records how many there were. When some of 2 or 1 fails, the gate also passes if the archive holds an
**acknowledgement** for this generation whose digest equals the digest of the current unaccounted set (below).

There is one read of `LastSeq` for the evaluation (step 2), taken under the fence, and one equality check before the first
destructive action (step 5). The gate runs in all three forms of the verb, because it is inside the methods all three
reach. `deprovision --drained-to SEQ --suspension ID` keeps its own refusal of a stream that moved past SEQ, and its park
is the operator's, so the gate neither resumes it on a refusal nor needs to park it again.

**The archive.** Key `<flat>.<gen>` in `mash-presented-archive`, written over the admin connection. The value is JSON with
a version field:

```json
{"v":1,"kind":"final","flat":"agent_owner_name","name":"agent.owner.name","generation":"1731412345123456789",
 "stream_created":"2026-11-10T08:00:00.123456789Z","archived_at":"2026-11-12T10:00:00Z","by":"<admin user public key>",
 "mash":"v0.5.0","first_seq":1200,"last_seq":4100,"base":1,"base_source":"endpoint",
 "counts":{"presented":4000,"refused":3,"duplicate":97,"channel":0},
 "lost":[],"unproven":[],"outstanding":[],"excluded":[],"before_base":0,"sessions":1,"spool_required":true,
 "fold_digest":"sha256:...","ranges_truncated":false}
```

`fold_digest` is the SHA-256 of the canonical encoding of the generation, base, last sequence and the three range lists. A
list longer than 10,000 ranges is cut, `ranges_truncated` is set, and the counts stay exact. The archive never expires
and nothing in mash deletes from it. After the stream is removed, the admin purges the generation's delta, snapshot and
admin-base subjects from `mash-presented` (three subject filters, `e.<flat>.<gen>.>`, `s.<flat>.<gen>.>` and
`a.<flat>.<gen>.>`, each naming one generation), so the live bucket holds only generations that still exist. The stream is
gone and fenced by then, so no later fact for that generation can be needed. A failure of that purge is reported and leaves harmless keys; the audit lists them as
an old generation with an archive entry.

**The flow, with no force flag.**

```text
mashctl deprovision NAME            # gate runs; refuses with the sequences if the inbox is not accounted for
mashctl proof check NAME            # the same evaluation, read only, with the full lists
mashctl proof acknowledge NAME --reason TEXT
mashctl deprovision NAME            # gate passes if the acknowledgement matches
```

**The refusal text:** `mash: refusing to deprovision NAME: N message(s) of its inbox are not accounted for (lost L, unproven U, waiting W: #a-#b ...); nothing was changed. Run mashctl proof check NAME for the list. If the loss is accepted, run mashctl proof acknowledge NAME --reason TEXT and then this again`. With no record: `mash: refusing to deprovision NAME: its inbox holds M message(s) (#a-#b) and no delivery record exists for them, so nothing shows they were presented; nothing was changed. ...`.

**`proof acknowledge NAME --reason TEXT`** (new; the reason is required and is stored as given, bounded to 1 KiB) writes a
revision `{"kind":"acknowledgement","by":...,"at":...,"reason":...,"digest":...,"lost":N,"unproven":N,"outstanding":N,
"ranges":[...]}` under the same key. The digest covers the generation, `LastSeq` and the unaccounted set. The gate accepts
an acknowledgement only if its digest equals the current one, so mail that arrives or is lost afterwards blocks again. The
command prints what it records, and does nothing to the inbox. It does not make `--strict` pass.

**Interplay with `retire`.** `retire` calls `DeprovisionIfUser`, which revokes the user, removes the record and the inbox,
and then `retire` edits the policy file (`cmd/mashctl/retire.go`). The gate (through `guardRemoval`, inside
`DeprovisionIfUser`) refuses before the revocation closure runs, so a refused `retire` has revoked nothing, has resumed the
inbox, and has left the policy file as it was (the refusal goes down the existing path, `fail("%v: nothing was changed")`);
the operator acknowledges or drains, then runs `retire` again. `retire --force` does not bypass the gate: `--force` only
decides what `retire` does when the server has no record. The rollback of a failed `mashctl add` also calls
`DeprovisionIfUser`: its inbox is new and empty, so the gate passes after fencing it. A gate refusal there (an inbox that
holds mail) is reported as `PARTIAL` by `add` and finished by the operator. A `PARTIAL` result from a retire that failed midway is finished with `mashctl
deprovision NAME`, which runs the gate; if the stream is already gone the gate passes (absent stream), the archive is
written from what the bucket holds with `"stream":"absent"`, and the removal completes.

```mermaid
sequenceDiagram
    autonumber
    actor O as operator
    participant C as mashctl (deprovision / retire)
    participant A as removal entry point<br/>(Deprovision, DeprovisionIfUser, DeprovisionDrained)
    participant G as Admin.guardRemoval
    participant J as inbox stream
    participant B as mash-presented
    participant R as mash-presented-archive

    O->>C: deprovision NAME (or retire NAME)
    C->>A: lock NAME (lockStream), alias guard, record checks
    A->>G: guardRemoval (before any destructive action)
    G->>J: FENCE: park the subjects (suspendLocked); new mail is refused
    G->>J: settle, then read FirstSeq, LastSeq, consumer (final)
    G->>B: read every key of NAME's generation
    G->>G: fold, classify (lost, unproven, outstanding)
    alt LastSeq is 0 or stream absent
        G->>G: gate passes
    else accounted in full and a record exists
        G->>R: write final archive entry, read it back
    else an acknowledgement with the current digest exists
        G->>R: write final archive entry citing it, read it back
    else
        G->>J: UNFENCE: resume (unless kept suspended)
        G-->>C: refused: N not accounted for (nothing changed)
        C-->>O: refusal text, exit 1
        O->>C: proof acknowledge NAME --reason TEXT
        C->>R: write acknowledgement (digest of the unaccounted set)
        O->>C: deprovision NAME (again)
    end
    G->>J: re-read LastSeq (equal to the evaluated one)
    G-->>A: removalFence token (inbox stays parked)
    A->>A: beginRetire (pending marker), revoke, remove record, lease tombstone
    A->>J: deleteInbox(token): delete the stream (fence held to here)
    A->>B: purge the generation's keys
    A-->>C: done
```

### 4.8 What step 2 still has to build

`MASH_SPOOL_REQUIRED` (ADR 0107) is already the hold-until-shown setting: with it on, serve sets no-channel for every host
and, once the spool is bound, spools from the first message, so mail the spool cannot take is held unacked and never
acked through the channel, and a spool failure is reported (`cmd/mash/serve.go`, `endpoint.Endpoint.NoChannel`,
`SpoolFromStart`). The channel-ack gap is therefore closed for an endpoint that sets it. That leaves, for step 2:

1. **How the audit treats an inbox that ever used the channel path.** Mail acked on a channel write is delivered and not
   proven. With the setting off by default (decision 2), such an inbox exists in every default deployment. The audit
   reports it as `unproven, channel` (section 4.4) and `--strict` fails on it. This is the truthful answer and the main
   work of step 2: the fold, the classes, the wording and the JSON of sections 4.4 and 4.5, which step 1 builds without
   the `c` kind's special case.
2. **`req` in the record.** Serve writes `req:1` into each delta key when `MASH_SPOOL_REQUIRED` is on, so the audit can
   say whether the setting was on when mail was acked by the channel and what to change. `config.Config.SpoolRequired`
   is the input; the value schema already has the field.
3. **`mashctl proof rebase NAME --reason TEXT`.** Raises the base to the current `LastSeq + 1`, by writing
   `a.<flat>.<gen>.b.<B>` and, **first**, an `excluded` record: a revision under the generation's key `<flat>.<gen>` in
   `mash-presented-archive` (written only over the admin connection; no endpoint grant reaches that bucket). The record
   lists exactly what the rebase moves past: the counts and the ranges, by class (lost, unproven, channel-acked), as the
   fold stood when it ran; who ran it (the admin key id); when; and the reason (required, bounded to 1 KiB). It is written
   and read back before the base key is written, so a base is never raised without its record; a crash between the two
   leaves a record and no raise, and the next run writes the raise.

   A rebase never erases evidence. The base moves, so the sequences are no longer in `lost` or `unproven` for the live
   fold, but `--strict` reports every `excluded` record of the generation as a finding of class `excluded` (section 4.4:
   date, admin key id, reason, counts and ranges) and fails for as long as the generation exists. A known loss stays a
   finding. An acknowledgement does not clear it, a second rebase does not, and nothing in mash removes an archive
   revision. The only way back to a clean `--strict` for that endpoint is a new generation: remove the inbox through the
   deprovision gate and provision it again. The excluded sequences are below the base, so they do not block the gate; the
   final archive entry lists the generation's `excluded` records (section 4.7), so the history stays in the archive. The new generation starts at base 1 with an empty record.

   What `rebase` is for: it serves non-strict operation, where `audit` and `status` should report new losses from now on
   and stop restating old ones in the live counts; and the transition after `MASH_SPOOL_REQUIRED` is turned on, where the
   channel-acked history is a fact of the past and the operator wants every message from now on to be judged against the
   setting. After a rebase the audit prints `base_source: admin` and still lists the exclusion. It never lowers a base.
4. **`status` and the `status` tool** report `hold-until-shown: on` or `off`, and, when off and the ledger holds channel-acked
   mail, say that those messages are not proven shown.
5. **Docs** (section 8): the `MASH_SPOOL_REQUIRED` paragraph in `docs/operations.md` names the proof as the reason to set
   it.

What step 2 does not build: a new default, a new delivery setting, or any change to `deliverBatch`. Under
`MASH_SPOOL_REQUIRED` serve never records `c`, so the `c` kind exists for endpoints that run with it off, and for the
mail acked before they turned it on.

Not verified: that a hookless host under `MASH_SPOOL_REQUIRED` always reaches `read` for held mail (ADR 0041's hookless
state). Mail it never reads stays held, which the audit reports as `outstanding`, and, if it ages out, as lost.

### 4.9 Permission template changes and their rollout

The one new publish line of section 4.1 goes into three places in `internal/admin/permissions.go` and
`permissions_newform.go`: `fixedPubKeyed` (so a user's own permissions, a zone key's template via `fixedPub`, and
`NewFormPermissions` all carry it), a `legacyPresentedGrants` beside `legacyLeaseGrants` for a window bridge, and the
list-set compile (`internal/listset`, ADR 0102), whose check expands the template as the server does and must accept the
line. `mashctl policy apply` and the drift check (`docs/operations.md`, "Checking and repairing drift") learn the line: a
group that is meant to have it and does not is a drift finding.

The template is per owner group's scoped key (ADR 0102: one scoped key per owner group, the fixed lines name the sender
through `{{name()}}`). Users that were minted one by one and carry their own permissions, not a scoped key, get the line
through `mashctl reissue NAME`, which is a widening and keeps the key (ADR 0101).

**The sequence.**

0. *Release.* Upgrade every admin tool and every endpoint binary to the release. An endpoint without the grant runs the
   step 0 behaviour: it keeps the ledger, tries the push, is refused, and says `proof: not granted`. Nothing changes for
   delivery.
1. *Buckets.* Run `mash admin init`. It creates and verifies `mash-presented` and `mash-presented-archive`. `mashctl audit`
   now reports `no record` for every inbox that has mail, and `delivery_bucket: ok`.
2. *Canary.* Switch the first group on. The way a group opts in is a per-group switch that makes the compile emit the line
   (name and file placement are open question 1). Run `mashctl policy apply`. The server closes every open connection
   under that group's key as it takes the account update, and each endpoint reconnects with the same credentials and the new
   template (ADR 0097: measured on nats-server 2.15.1 at 13 to 123 ms with a 10 ms reconnect wait; a mash endpoint waits
   2 seconds, so expect about 2 seconds plus the reconnect, which is inferred from the wait, not measured). What an
   endpoint sees: one disconnect and reconnect, the connection's pending acks fail and the unacked mail is redelivered, which
   the spool turns into duplicates the `IsDrained` branch acks without showing again. It does not lose mail: ADR 0097's
   loss is of core publishes in flight at the instant of the close, and mail is JetStream.
3. *Verify the group.* `mashctl policy drift` shows the group `ok`. After one drain pass on each endpoint,
   `mashctl audit --strict --delivery-zone GROUP` shows the group's inboxes as `proven`, `unproven` (if channel-acked mail
   is in them) or `no proof` with a base above 1 (every inbox with history, by design: sequences at or below the consumer's
   delivered sequence at first bind are outside the proof).
4. *Soak, then the next group.* Repeat 2 and 3 per group, in an order the operator chooses.

**Mixed state, audited.** Each inbox is in one state (section 4.4). A group that is not yet granted shows `no record`: the
endpoint never wrote. A group that is granted shows `proven` or `no proof`. `--delivery-zone Z` (repeatable) limits the
delivery findings of `--strict` to the named groups, and the report lists the other groups' states as notes marked `not
rolled out`, so a rollout can be audited one group at a time. Without the flag, `--strict` fails on any `no record`: the
claim is about the whole account.

**Rollback.** Remove the group's switch and run `mashctl policy apply`. The template narrows, the connections close and
reconnect (ADR 0097), and the endpoints go back to `proof: not granted` with their local ledger intact. The keys already in
the bucket stay (they cannot be removed, and are harmless). From then on new mail is not recorded remotely, so the audit
shows `lost` for it once it leaves the stream, and `--strict` fails for that group. That is the honest result: with the
proof off, `--strict` cannot say no loss. An operator who rolls back to stop auditing a group uses `--delivery-zone` to
exclude it.

## 5. Cost per message

Numbers derive from the key layout and the write path. A *drain pass* is one run of `settleHeld`; a hook drains at most
about 10 messages or 16 KiB per run (ADR 0026), and serve coalesces the drains it sees in one 250 ms pass
(`SpoolCheck`). Measured on this design: none. Every figure below is arithmetic from the layout, marked as such.

**Bytes per delta.** The subject is about 68 to 85 bytes with the window token (section 4.1's example is 79: the window
token adds 2 to 8 bytes, 4 for the example), the value about 75 bytes (`{"v":1,"t":...,"n":10,"s":...,"req":1}`), and a
file-store message adds about 30 bytes of record header and hash. That is 184 bytes for the example, and the longest
realistic key adds about 10, so a delta is still about **190 bytes stored** (the earlier estimate stands, its subject
figure having been rounded up), and about 160 bytes on the wire (`PUB` line, subject, payload). The window token costs
nothing in the permission template (the one line is unchanged) and about 4 bytes per delta. A snapshot of a window
with nothing lost is about 220 bytes stored, and each lost run in it adds about 14.

**Deltas per message.** One delta per (kind, contiguous run, window) per pass. A pass that drains k contiguous messages
writes one delta, so between 1/10 and 1 delta per message:

| Messages | Deltas, best (10 per pass) | Deltas, worst (1 per pass) | Stored, best | Stored, worst |
|---|---|---|---|---|
| 1,000 per day | 100 | 1,000 | 19 KB | 190 KB |
| 10,000 per day | 1,000 | 10,000 | 190 KB | 1.9 MB |
| 1,000,000 in all | 100,000 | 1,000,000 | 19 MB | 190 MB |

**After compaction.** Windows of 4096 sequences fold to one snapshot each. 1,000,000 messages are 245 windows, about
54 KB, plus the deltas of the windows not yet decided. The uncompacted tail is bounded by what the stream holds (16 MiB of
mail), so an endpoint's deltas in the bucket are at most a few thousand keys between compactions, hundreds of kilobytes
to about 1 MB in the worst case. The 128 MiB cap holds that for more than a hundred endpoints at the worst case, and
indefinitely when compaction runs; after compaction the record grows by about 54 bytes per thousand messages.

**Requests.** One JetStream publish with its acknowledgement per delta, in addition to the one `DoubleAck` per message that
exists today and the ledger's one synced append per pass. So between 0.1 and 1.0 extra requests per message: with batches of
10 the load rises by about one request per ten messages; with one message per hook run it doubles the request count. The
batch is published asynchronously, so a pass costs one round trip, not one per key.

**Latency.** None on the display path (the hook has shown the message before serve writes anything). The ack is delayed by
at most one round trip in the healthy case (loopback about 0.2 ms, a remote server one RTT) and by `proofWait` (1 second)
in the worst case, inside the 30 second `AckWait` that `SpoolKeepAlive` keeps pushing back.

**NATS load and reads.** Writes are small appends to one stream. The audit reads the bucket once per run, by one consumer
over the whole stream: at the cap that is about 128 MiB, or 700,000 messages at 190 bytes. The time that takes is not
measured. Compaction reads one window at a time through a subject filter, so it reads about 4096 / 10 to 4096 deltas
(80 KB to 800 KB) per window and writes one snapshot of a few hundred bytes. A refused gate costs two stream configuration
updates (park, resume) and a brief spell in which senders find no stream; a passing gate costs the park, which the deletion
then supersedes.

## 6. Failure modes and crash points

| Situation | What survives | What the audit reports |
|---|---|---|
| Crash after the hook's tombstone, before the ledger line | the tombstone | Nothing is lost. The message is redelivered (unacked), `IsDrained` is true, serve records `presented` (ADR 0109) and the pass repeats. `proven`. |
| Crash after the ledger line, before the remote write | tombstone and ledger line | The message is redelivered, serve records it again (a set), pushes, acks. Until the push the sequence is `outstanding`. `proven` after. If the machine and its state are lost here, the redelivered message is shown again (a duplicate display) and recorded. |
| Crash after the remote write, before the ack | tombstone, ledger line and remote key | The message is redelivered, the push is refused as already written and counts as written, the ack goes. `proven`. |
| Crash after the ack, before the tombstone is removed | the stale tombstone | Nothing changes. `spool.Spool.Acked` removes it later. |
| The state directory (spool and ledger) is lost after the ack, with the remote key written | the remote key | `proven`: the record does not depend on the endpoint's disk. |
| The state directory is lost after the ack, before the remote write | nothing local | The sequence is `outstanding`; once it falls below FirstSeq it is `lost` (or `unproven` if two sessions have written the generation). A false loss, never a false proof. |
| The stream is deprovisioned and provisioned again (new generation) | the old generation's keys, or the archive | The new generation starts at base 1 with its own keys. The old generation is evaluated by the deprovision gate and archived; a generation with keys and no stream and no archive entry is reported as `a delivery record for generation G has no stream and no archive`, a finding (the stream was removed by hand). |
| A different session takes the name over (ADR 0092) | the new session's keys; the old session's spool may be gone | Sequences the old session showed and never recorded, evicted before redelivery: `unproven, takeover`. A duplicate presentation across the takeover is a set union and changes nothing. A late push by the old holder is true and harmless. |
| The bucket is full | each endpoint's ledger | Endpoints say `proof: push failing`, keep acking; `delivery_bucket: full` is a finding; sequences presented meanwhile are `outstanding` until compaction frees space and the retry pushes them. |
| The template is not granted yet | the local ledger | `no record` for an endpoint that never wrote; `outstanding` then `lost` for sequences after a rollback. The endpoint says `proof: not granted`. |
| The bucket does not exist (`init` not run) | the local ledger | Endpoints say `proof: bucket missing`; the audit reports `delivery_bucket: missing` and every inbox with mail as `no record`. |
| An overclaiming or buggy endpoint writes a range above the last sequence | the key (it cannot be removed) | `overclaim`, a finding; the key is ignored for the fold. |
| A reader meets an unknown value version | the key | `unreadable_keys`, a finding; the key does not count as presentation. |
| The consumer was recreated, resetting `Delivered.Stream` | the keys | Unchanged: the record does not use consumer state except in the classification of `unproven, takeover` and the overclaim bound. The base was written at first bind and stays. |
| The admin credentials are used to add a key | the key | Indistinguishable from the endpoint's, as the issue record's admin writes are. The record guards against an endpoint's mistakes, not against the admin credentials. |
| Mail arrives before the removal gate's fence | the stream | It is in `LastSeq` when the gate reads under the fence, so it is counted: `outstanding` until a hook shows it, and the gate refuses. |
| Mail is sent after the fence, during the gate | nothing stored | The stream no longer takes the subject, so the sender gets no stream (as with any suspend, ADR 0057). `LastSeq` cannot move, so the evaluated state is the state that is deleted. |
| The gate refuses (not accounted for) | the stream, unchanged | The gate resumes the inbox (unless it was already suspended by the operator, or `--keep-suspended`); nothing was marked, revoked or removed. The refusal says what is missing. |
| Crash after the fence, before any destructive action | the parked stream, tagged `mash_suspended_by=removal-gate` | `audit` shows the inbox as suspended; `mashctl resume NAME` restores it, or running `deprovision` again takes the park as its own and re-evaluates. No mail is lost. |
| Crash or error after the first destructive action (marker, revocation, purge) | the parked stream, pending marker | The removal's existing partial states apply (`PARTIAL`, a pending retired marker); the inbox stays parked, no mail is stored meanwhile, and a re-run runs the gate again and finishes. |
| A new removal path is added that does not call the shared step | nothing | Does not compile when it deletes the stream (`deleteInbox` needs the token), and the structural test of section 7 fails when it takes any other destructive action without calling `guardRemoval` first. |
| `proof rebase` moves the base past lost, unproven or channel-acked mail | the `excluded` record in the archive, then the raised base | `--strict` reports each excluded set as a finding and fails for the life of the generation; the live counts above the new base are clean. A crash between the record and the base leaves a record and no raise. |
| Compaction crashes after the snapshot, before the purge, or the purge fails | the snapshot and the deltas it covers | Duplicate facts, harmless: the fold is a union. The next compaction folds the window again and purges. Nothing is lost, and no other window is touched, because the purge filter names one window. |
| Compaction's verify step finds the snapshot does not cover the window's facts | the snapshot, and all deltas | Nothing is purged; the snapshot is reported as a finding of the compaction and left (a duplicate). The deltas are still the evidence. |
| A delta arrives for a window while it is being compacted | the delta (a higher stream sequence than the read) | The purge is bounded to sequences below the read, so the delta is kept and the next compaction folds it. |

## 7. Test plan

**Unit tests** (no server):

- Key encode and decode: every kind, the window token (`floor(seq / 4096)`; sequences 4095, 4096 and 4097 fall in windows
  0, 1 and 1), the window split of a run that crosses a boundary, the bounds (a 4097-wide range, a range across a boundary,
  a window token that is not `floor(lo / 4096)`, `lo` 0, a wrong version), that the filter
  `e.<flat>.<gen>.<window>.>` matches the keys of that window and no key of window 1 for window 11 or of another
  generation, and that decode rejects what encode never writes.
- The fold: the invariant `lost = [B..F-1] minus accounted` on given stream states, with presented, refused, channel and
  duplicate keys; a duplicate with no named original, and one whose original is not presented, explaining nothing; the
  classes of section 4.4 and that they add up; the base rule (explicit, highest wins, inconsistent when above the lowest
  recorded sequence); generations kept apart; the overclaim bounds.
- Compaction: a fold of deltas and the fold of the snapshot plus a late delta agree; compaction twice is the same as once; a
  crash between the snapshot write and the purge leaves a state that folds the same (duplicates, union); a snapshot that
  fails the verify step (a count or a range missing) leads to no purge; a compaction of window 1 never purges a key of
  window 11, of window 0 or of another generation; a delta written after the read of step 1 survives the purge.
- The removal gate: the sequence of section 4.7 on fakes: fence, then read, then evaluate, then delete, in that order, and
  no read of `LastSeq` before the fence; a refusal resumes only a park the gate made (not the operator's, not under
  `--keep-suspended`); a stream that does not settle refuses; a message published between the evaluation and the deletion is
  refused by the stream and `LastSeq` is equal at the pre-deletion check.
- **Every removal entry point is gated (table test).** A table lists every exported removal entry point of `*admin.Admin`:
  `Deprovision`, `DeprovisionIfUser`, `DeprovisionDrained`, and the paths through them (`finishAfterPurge`, the rollback
  call of `mashctl add`, `mashctl retire`, `mashctl deprovision`, `mash admin deprovision`). For each, the test provisions
  an inbox with unaccounted mail (one lost, one unproven, one outstanding, and one with no record) and asserts that the
  entry point refuses, that the stream, the directory record, the lease, the retired marker and the user's revocation are
  all unchanged, that the inbox is not left suspended (unless it was suspended before or `--keep-suspended` was given), and
  that the same inbox, accounted in full, is removed. `retire --force` is in the table.
- **Structural test (a lint that fails when a removal path bypasses the shared step).** A test in `internal/admin` parses
  the package with `go/parser` and builds the call graph of its functions. It fails when (a) any function other than
  `deleteInbox` calls `DeleteStream`; (b) `deleteInbox` or `fenceAndDeleteInbox` is called with anything but a
  `*removalFence` obtained from `guardRemoval`; (c) any function that calls `beginRetire`, `revokeLocked`, `purgeDirectory`
  or `fence` is reachable from an exported method that does not call `guardRemoval` earlier in source order (the
  guard before the first destructive call); (d) an exported `*Admin` method that reaches `deleteInbox` is not named in the
  table test above, so adding a removal entry point without adding its table row fails. The same test runs over
  `cmd/mashctl` and `internal/ctl` and fails on any direct call to `DeleteStream` there.
- The `excluded` record: a rebase writes the record before the base; the record holds the counts and ranges by class, the
  admin key id, the time and the reason; it is written once per rebase and the archive refuses a revision past 64; the
  audit reports every excluded set as a finding of class `excluded` with its date and reason; `--strict` exits 1 for
  the generation after a rebase, after a second rebase, and after `proof acknowledge`; a new generation (deprovision then
  provision again) is clean.
- The ledger additions: `base` and `pushed` records fold as sets, a lost `pushed` line only causes a re-push.
- `VerifyPresentedBucket` for each of the settings in section 4.1 gone wrong, as `VerifyIssuedBucket` is tested.
- The permission lists: the line is in `fixedPubKeyed`, `NewFormPermissions`, `ZonePermissions` and the bridge, nothing else
  names `KV_mash-presented`, and the list-set check accepts the template.
- The audit wording, the JSON fields and the exit codes for each state, including that an acknowledgement does not change
  `--strict`.

**Crash-point tests.** These use the failpoints that exist and two new ones. Existing: the hook's `before-print`,
`after-print` and `after-first-drained` (`failpoint`, `cmd/mash/hook.go`, set by `MASH_HOOK_FAILPOINT`) and serve's
`before-ack` (`failpointBeforeAck`, `internal/endpoint/hook.go`, set by `MASH_SERVE_FAILPOINT`). New, in the same style:
`after-ledger` (after the ledger line, before the remote write) and `after-push` (after the acknowledged write, before the
ack). For the removal gate, three more: `after-fence` (the inbox is parked, nothing else changed), `after-archive` (the
archive entry is written, the removal has not begun) and `after-marker` (the first destructive action is done). Each of
the crash rows of section 6 is a test: kill the process at the point, restart, and assert the audit's state
and that nothing is shown twice except where the table says so.

**AT-style tests** (the numbers are to be assigned by the contract's owner; which are free is not verified):

- An inbox with a presented, refused and duplicate history, evicted by the byte cap, is `proven`; the same history with one
  sequence removed from the record is `lost` and `--strict` exits 1.
- A 30-day-old unread message is reported lost (decision 3), by running the age limit at a small value as the inbox limits
  tests do.
- A takeover test: session A shows and does not ack, session B takes over, A's state is removed, the message is evicted:
  `unproven`, and the gate refuses it.
- A digest line never produces a `p` key (decision 7).
- The deprovision gate: refused with each of lost, unproven, outstanding and no record, through each of the three entry
  points; passes when accounted in full; passes with a matching acknowledgement and refuses once new mail arrives;
  `retire` refuses with the policy file untouched and the user not revoked; `retire --force` does not bypass; a stream
  that is absent passes; the archive entry is read back. With a sender publishing continuously while the gate runs,
  every message the stream stored is in the evaluated `LastSeq` (no message is stored after the fence) and the refused
  inbox resumes and takes mail again. After a crash at `after-fence`, `audit` shows the inbox suspended and `mashctl
  resume` restores it.
- Rebase: after `proof rebase` the live state is clean above the base and `--strict` still exits 1 with an `excluded`
  finding naming the date, the admin key id and the reason; only a new generation exits 0.
- Mixed state: one group granted, one not; `--delivery-zone` scopes `--strict`; without it `--strict` fails on `no record`.

**Live test with a real operator-mode server and a write-only endpoint credential.** The harness is the one AT-011 uses
(`TestAT011TemplateChangeReachesOpenConnections`, `internal/policy`: an in-process nats-server in operator mode with a full
account resolver). It creates the bucket as `mash admin init` does, mints an endpoint user under a scoped key whose template
carries the one line, and connects as that user. It asserts, on the real server:

- the endpoint can publish its own delta key and receives the acknowledgement;
- it is refused (a permissions violation or no response, not verified which) for: another endpoint's key, a key outside the
  `e.<flat>.` prefix, an `s.` and an `a.` key, `$KV.mash-presented.>`, `DIRECT.GET` of any key, `STREAM.INFO` on the
  bucket, a consumer create on it, `$JS.API.STREAM.PURGE.KV_mash-presented`, and `$JS.API.STREAM.MSG.DELETE.KV_mash-presented`;
- it cannot shrink or rewrite: a second publish to the same key (same value, then another value) is refused with
  `maximum messages per subject exceeded`, a KV delete marker and a purge marker (headers `KV-Operation` DEL and PURGE) are
  refused, and a rollup header is refused because the stream does not allow rollup;
- a reader with the admin creds sees exactly the facts written, folds them, and compaction (snapshot, read back, purge by
  filter with a sequence bound, one window at a time) leaves the fold unchanged, a concurrent write intact, and every
  other window's keys intact;
- a full bucket (a small `MaxBytes` for the test) refuses the next write, and serve acks regardless and reports the failure.

A separate live test runs a built `mash serve` and `mash hook` against the server, shows a message, and checks that the
delta key exists before the consumer's ack floor moves.

## 8. Rollout, migration, and the release notes

**Migration.** There is no data to migrate: an endpoint's step 0 ledger is pushed the first time it can write (section
4.2), and sequences at or below the consumer's delivered sequence at first bind are outside the proof, as `base`. Existing
inboxes therefore report `no record` until their group is granted, then `proven` or `no proof` with a base above 1. The
buckets are created by `mash admin init`, and an admin tool that cannot create them says so rather than writing without
them (as with the issue bucket).

**Docs to change.** `docs/operations.md`: "Hook delivery" gets the definition of presented and the limit that the model's
reading is not proven; "Inbox limits" gets the invariant and what a strict failure means; "Removing an endpoint" gets the
gate, the acknowledgement and the archive; the `MASH_SPOOL_REQUIRED` text names the proof as a reason to set it.
`docs/audit.md` (the report, the new fields, the exit rule), `docs/mashctl.md` (`proof check`, `proof acknowledge`,
`proof compact`, `proof rebase`, the `--delivery-zone` flag), `docs/security` (the record is self-attested; the
threat-model entry for a lying endpoint, with signed receipts as the deferred option).

**What the Upgrading section of the release notes must say.** The release notes' Upgrading section (ADR 0105) must carry
these items, in this order, in words an operator can act on. The text below is the text to use.

1. **Run `mash admin init` once after upgrading.** It creates the delivery record buckets (`KV_mash-presented`, and
   `KV_mash-presented-archive` for archived records). Until it has run, `mashctl audit --strict` reports every inbox that
   has mail as having no delivery record, and fails.
2. **`mashctl audit --strict` now counts mail that aged out unread as lost.** Before, it counted only mail an endpoint never
   received, as a lower bound, and a clean result proved nothing. Now, for an inbox whose delivery record covers its
   generation, any message that left the inbox (the 16 MiB byte cap or the 30 day age limit) with no record that it was
   handed to the host is reported as lost, and `--strict` exits 1. An endpoint that was off for a month makes `--strict` fail
   for it. Mail already gone before the record started is reported as outside the proof, not as lost.
3. **`mashctl deprovision` and `mashctl retire` are refused until the endpoint's inbox is audited.** They refuse when any
   message in the inbox is not accounted for (lost, unproven, or still waiting) or when the inbox holds mail and has no
   delivery record. There is no force flag. While the check runs the inbox is parked (new mail is refused, as with `mashctl
   suspend`) and it is resumed if the check refuses; `--keep-suspended` leaves it parked. Run `mashctl proof check NAME` to see what is missing. To accept the loss, run
   `mashctl proof acknowledge NAME --reason "..."` and then the same command again. The acknowledgement and the final record
   are kept in `KV_mash-presented-archive`, which never expires. `mash admin deprovision` has the same check.
4. **Grant the record lines one owner group at a time.** The endpoints of a group disconnect and reconnect once when
   its template changes. `mashctl audit --strict --delivery-zone GROUP` audits a group on its own while the others are not
   granted. Endpoints of groups not yet granted keep delivering mail as before and report `proof: not granted`.
5. **Run `mashctl proof compact` regularly** (daily is enough for most deployments); `mashctl audit` warns when the bucket
   passes 90 percent of its 128 MiB cap, and `--strict` fails when it is full.
6. **What `--strict` proves.** It proves mash handed each message to the host's input, and that none that it received was
   dropped without a record. It does not prove the model read it, and the record is the endpoint's own statement.
7. For a deployment that wants mail held until it is shown: set `MASH_SPOOL_REQUIRED=1` (ADR 0107) on those endpoints, and
   run `mashctl proof rebase NAME --reason "..."` once after setting it to judge mail from then on against the setting.
   The rebase does not hide the earlier mail: it is kept as an `excluded` record, and `mashctl audit --strict` keeps
   failing for that inbox for as long as its generation exists. To get a clean `--strict` for the endpoint, remove it
   through the deprovision check and provision it again (a new generation).

Items 2 and 3 change what an operator sees for the first time, so they are the first things to say in the release summary
too.

## 9. ADRs this will need

Numbers are assigned when each is written (ADR 0106: a number must be unused and not higher than the base). Titles are
working titles. Each pull request that makes a decision carries its ADR and a `Decision: ADR-NNNN` line; a pull request that
makes none says `Decision: none`.

1. *The delivery record is a bucket of write-once delta keys that the endpoint publishes and cannot read* (decisions 1, 4,
   and the write-once, purge and base choices of 3.9; amends ADR 0109 to add the explicit base and the pushed marks to the
   local ledger).
2. *The ack is never held for the remote record* (the choice of section 4.2; supersedes the fail-closed choice in the design
   note this design started from, which was not an ADR).
3. *Unread mail that ages out counts as lost, and `--strict` never honours an acknowledgement* (decisions 3 and 6, and the
   classes of section 4.4).
4. *The permission templates grant the delivery record line, per owner group* (decision 8, the template line, the switch, the
   rollback).
5. *Deprovision and retire are refused until the inbox is audited, and the loss is acknowledged explicitly* (decision 5, the
   archive bucket, the digest). Also: *Every removal path goes through one shared step that fences the inbox first* (the
   `guardRemoval` token, the suspend-based fence, the structural test; relates to ADR 0057).
6. *`audit --strict` treats channel-acked mail as unproven, and a rebase never erases evidence* (decision 2 as it applies
   to the audit, step 2; relates to ADR 0107; the `excluded` record and the rule that a known loss stays a finding for the
   life of the generation).

## 10. Open questions

The eight decisions are settled. These remain:

1. **The switch that makes a group's template carry the line.** Where it lives (the policy file, a per-zone key; the
   policy file's format for it is `not verified`), what it is called, and in which release its default flips from off to
   on. The first release should default it off.
2. **The template size.** The line adds about 60 bytes to every key's template and every new-form user's JWT. Whether that
   stays inside the JWT size budget (REQ-008, `mashctl policy show --budget`) for the largest group is not measured.
3. **Who runs compaction.** `mashctl proof compact` is a command; nothing in mash runs it on a schedule. Whether `mash admin
   init`, `audit`, or a documented cron entry should, and at what bucket fill an `audit` should do it itself.
4. **The behaviour of the pieces marked `not verified`:** a denied JetStream publish in nats.go 1.55.0 (how it fails and how
   fast), the API error code of a second write to a write-once key, an ordered consumer over a stream with discard-new per
   subject, a purge with a subject filter and a sequence bound, the longest key the server accepts (about 225 bytes here)
   and the archive's refusal of a 65th revision. One spike before the first pull request of section 11 settles all of
   them, and may change the details of sections 4.2 and 4.6, not the design.
5. **A clustered server.** Everything here was reasoned against a single server (`Replicas` 1, as the issue bucket). That
   discard-new per subject and the purge behave the same with replicas is not verified.
6. **May an operator-acknowledged exclusion ever clear `--strict`?** This is the one remaining policy question about the
   proof. Today a rebase leaves an `excluded` finding that `--strict` reports for the life of the generation, and only a
   new generation is clean (section 4.8). A deployment that has accepted an old loss in writing might want the audit to
   stop failing on it. **Recommendation: no.** A strict result that an operator can clear is a strict result that says
   "no loss we chose to remember", which is the claim this design refuses to make; the cost of the recommendation is a
   re-provision to get a clean generation.

## 11. Estimate and the order of pull requests

S is up to a day, M a few days, L a week or more.

| # | Pull request | Size | Depends on |
|---|---|---|---|
| 0 | Spike: the unverified behaviours of open question 4 (a denied publish, the write-once error code, the ordered consumer, the filtered and bounded purge, the longest key, the archive's 65th revision), against nats-server 2.15.2, with a test that fails if the server changes them (as AT-011 does). | S | |
| 1 | The buckets: `store.CreatePresentedBucket`, `VerifyPresentedBucket`, the archive bucket, `mash admin init`, the audit's bucket check, the permission line in `fixedPubKeyed`, the bridge and the list-set compile, the live write-only test. No behaviour changes: nothing writes yet. ADR 1 (the bucket part) and ADR 4. | M | 0, the ledger branch |
| 2 | The write path: the delta keys, `proofPush`, the `base`, `pushed` and `req` ledger additions, the status line, the two failpoints, the crash-point tests. | M | 1 |
| 3 | The read path: the fold, the classes, the audit wording and JSON, `proof check`, `--delivery-zone`, the exit rule. ADR 3. | M | 2 |
| 4 | Compaction: `proof compact`, the windowed snapshot with its verify step, the purge by window filter and sequence bound, the near-full warning. | M | 3 |
| 5a | The shared removal step: `Admin.guardRemoval` and the `removalFence` token in `internal/admin/removal.go`, `suspendLocked` and `resumeLocked`, the call in `deprovisionLocked`, `DeprovisionIfUser`, `DeprovisionDrained` and `finishAfterPurge`, `deleteInbox` taking the token, `--keep-suspended`, the table test over every entry point and the structural test. The evaluation is a pass, so the only behaviour change is the brief fence. Part of ADR 5. | M | 0 |
| 5b | The deprovision gate proper: the evaluation inside `guardRemoval`, the archive entry, `proof acknowledge`, the refusal text, the `retire` and `add` rollback cases, the failpoints and crash tests. ADR 5. | M | 3, 5a |
| 6 | Step 2: channel-acked mail as unproven, `proof rebase` with its `excluded` record, the `excluded` class in the audit and the strict rule, the `status` wording, `req`. ADR 6. | M | 3, 1 |
| 7 | Docs and the release notes' Upgrading text, the threat-model entry. | S | 3 to 6 |

The whole is L (the two changes of the review history add one M: the gate is two pull requests, and rebase is M rather than S).
Pull requests 4, 5b and 6 are independent of one another once 3 has landed, and can be reviewed in any order; 5a needs only
the spike and can land before the read path.
PR 1 can merge before any endpoint writes, so the first rollout step (the buckets) is separate from the first behaviour
change. No pull request changes delivery.
