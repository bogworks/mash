---
status: proposed
date: 2026-11-15
decision-makers: Randy Bias (mash maintainer), who ruled to report peer state over the SAP status endpoint now; the mash coordinator (mash-lead), for the asking side and where it is shown
---

# mash asks a peer's reported state over SAP status, and shows it only where `who` already adds fields

## Context and Problem Statement

A sender's mash knows whether a peer's mash answers (`who`, presence, [ADR 0066](0066-mash-serve-asks-who-is-online-when-someone-asks-instead-of-listening-to-every-heartbeat.md)).
It does not know whether the peer can take a turn. A session that holds a dialog open (a permission prompt, or a
question to its user) answers presence probes, but it cannot read mail. Every sender then knows only "I sent a message".

SAP 0.3 gives every agent a `status` endpoint, which answers a request with a heartbeat-shaped reply (SAP §8.7).
SAP 0.3.1, as merged (synadia-agent-sdk-docs PR #25), lets an agent add fields of its own to its heartbeat and its
status reply:

> An agent MAY add fields of its own to the payload. An added field MUST NOT reuse the name of a field in the table
> above. ... An agent that adds fields to its heartbeat SHOULD add the same ones to its status reply (§8.7).
> (§8.3)

> Callers MUST tolerate additional unknown fields. (§8.3)

The status reply is specified so that those fields are in it:

> Reply body: A §8.3 heartbeat-shaped JSON payload, freshly built per request, with the agent's added fields (§8.3).
> (§8.7)

> Agents SHOULD source the reply payload from the same builder used to construct periodic heartbeats so the two emit
> identical values for fields they share, the agent's added fields (§8.3) included. (§8.7.1)

mash uses that room for one field of its own, `program_status`: an object with a `state` (`idle`, `working`, `done`,
`blocked`, `error`) and, with `blocked`, a `kind` (`permission`, `question`, `auth`), with the meanings of OSC 7501 (the
terminal program-status protocol). Its reservation of top-level `state` and `kind` keys (PR #24) is not relied on: the
maintainer declined it as written. Randy ruled that mash reports peer state over SAP status now. This record covers
both sides: asking, and serving (what mash puts in its own heartbeat and status reply, and where the state comes
from).

## Decision Drivers

* No change to SAP, and no divergence from the SAP Go SDK (D229). Asking is an ordinary SAP status request, and
  `program_status` is an added field SAP §8.3 already allows.
* A reported state is the peer's unsigned claim (SAP §13.11: a reply is never signed), so mash never grants anything on it.
* Silence is never "free". An absent state is not `idle`.
* `who`'s default line is a compatibility contract that launchers parse
  ([ADR 0034](0034-text-output-and-plugin-commands-are-a-compatibility-contract.md)).
* Asking many peers must be bounded in time and load.
* The heartbeat is public and signed, so what it carries is five words and three kinds. Anything that could
  identify work stays out of it.

## Considered Options

* Ask each peer's SAP status on demand, and show the answer in `who --long`, `who --json` and the `who` tool
  (chosen).
* Listen for state in heartbeats.
* Add the state to `who`'s default line.

## Decision Outcome

Chosen option: "Ask each peer's SAP status on demand, and show it where `who` already adds fields".

**Asking.** `Endpoint.PeerActivity(name)` sends one status request to the peer's status subject. The subject is
resolved as a send resolves a recipient, legacy form included.
* The request has an empty body and a signed `Agent-Sender`, as SAP §12 asks of a caller that implements §13. It is
  unsigned only when signing is impossible.
* Each request waits at most 1 s. At most 8 requests are in flight per endpoint. Each answer is cached for 5 s.
* It never fails and never returns an error. The outcome is one of four:
  * the peer reported a state;
  * it answered with no state it recognises (not reported);
  * no answer (a timeout, or no responder);
  * refused (a permissions violation on the status subject).

**Reading the reply.**
* Only `program_status.state`, `program_status.kind` and `instance_id` are read. Top-level `state` and `kind` are not
  read: they were never released.
* A `program_status` that is not an object, or a `state` outside the five values, means not reported. A `kind`
  outside the three values, or without `blocked`, is dropped. Unknown members and keys are ignored.
* `program_detail` and every other key are never read or shown. Liveness never depends on `program_status`.

**Showing it.** `who` adds a `state` column for online endpoints only.
* The column appears in the MCP `who` tool, `who --long` and `who --json`.
* The default `who` line and `who --names` are unchanged, and send no status request.
* All online peers are asked in parallel, under one overall 2 s budget.
* Cell values:
  * `-` means not reported, or offline. It never means idle.
  * `?` means no answer in time, or refused.
  * A reported state prints as, for example, `working` or `blocked(question)`.
* JSON output carries `activity` (`state`, `kind`) and `"activity_trust": "claimed"`.
* The tool's description says that `online` means the peer's mash answers, not that the peer can take a turn.

**Serving.** One builder produces the heartbeat and the status reply, and mash's own code copies the SAP Go SDK's
extras contract (`host.WithHeartbeatExtras`, SDK v0.1.0, `host/options.go` and `host/heartbeat.go`). mash does not
import the SDK: adopting its `host` package is its own item (#667, #669, #672), and this is no divergence from it
(D229).
* `Endpoint.SetHeartbeatExtras(func() map[string]any)` is read once per build, and its fields go after the §8.3
  fields. A provider that panics, returns a §8.3 field name or returns a value that does not marshal costs that build
  all its extras. The beat is still built, and the failure is logged at error level without its text.
* `Endpoint.SetStatusExtras` gives the fields only the reply carries, under the same rules. They may not reuse a
  §8.3 name or a key the heartbeat carries.
* `heartbeat()` is the §8.3 fields plus the heartbeat extras. `statusPayload()` is that same call plus the status
  extras, so every heartbeat field is in the reply, and `program_status` is byte-identical in both.
* `mash serve` registers one heartbeat provider, `programStatus()`, and it is the only place that maps activity to the
  wire. The heartbeat and the reply carry:

  ```json
  "program_status": {"state": "idle|working|blocked|done|error", "kind": "permission|question|auth"}
  ```

  `kind` appears only with `blocked`, and only when known. When no state is known the key is absent: never `{}`, and
  never `idle` as a default. There are no other members: no since, no source, no id, no clear, no base64.
* The reply alone carries `program_detail`, `{"title": "...", "msg": "..."}`, from the status provider. `title` is the
  endpoint display name and " (Claude Code)", cut at a rune boundary to 120 bytes. `msg` is a short state sentence
  ("working", "idle", "waiting for permission", "waiting for an answer to a question"), cut to 300 bytes. Each
  member is omitted when empty and the key when both are. Both are generated by mash, never taken from the session or
  the model, name no tool and quote no text. The heartbeat never carries `program_detail`.
* The reply alone also carries `delivery`, mash's own field for the health of mail delivery to the session:

  ```json
  "delivery": {"health": "ok|degraded|holding", "reason": "spool_failing|channel_hidden|held_unacked"}
  ```

  * `reason` appears only when `health` is not `ok`. When serve does not know, the key is absent.
  * serve sets it from its spool and channel state:
    * `holding` means mail is held unacked and is not reaching the session;
    * `degraded` means mail still reaches the session, by the channel, while the spool fails.
  * It is a word and a class from fixed sets and never carries content: no subject, name, count or error text. It is
    not part of `program_status` and is none of OSC 7501's keys.
  * It is reply-only, from the same status provider as `program_detail`, so the heartbeat and the lease never carry it.
  * `PeerActivity` reads `delivery.health` and `delivery.reason`, whether or not a state is reported. An unknown
    health is not reported, and an unknown reason is dropped. `who --long`, `who --json` and the `who` tool show it.
    A peer that did not answer shows `?`, and one that answered without it shows `-`. Neither is ever shown as `ok`.
    The default `who` line is unchanged.

**Off-cycle beat.** mash's heartbeat interval stays 30 s and `interval_s` stays 30. SAP §8.2 allows an extra beat:

> Each instance chooses its own interval ... (§8.2)

> Values below 1 second SHOULD NOT be used on shared infrastructure. (§8.2)

When `program_status` changes, mash publishes one extra heartbeat at once, at most one per 2 s. A change inside the
window is sent when the window ends, and the latest value wins. A change is noticed by reading the provider every
250 ms and comparing the marshalled result, so a watcher that reads heartbeats is not 30 s stale. mash's own
readers still ask by status request (ADR 0066).

**Sources.** The state comes from Claude Code hooks and, where present, a terminal runtime, through
`internal/activity`, which is not changed here. The ruling, quoted:

> planner ruling #1232 (2026-11-16, via mash-lead #599): mash may report working/idle from its own Claude Code hooks; herdr adds blocked; without herdr mash never says blocked; Claude Code hooks only <!-- adrlint:allow vocabulary -->

| Source and activity | `program_status` |
|---|---|
| none (absent) | no key |
| hooks: UserPromptSubmit, PreToolUse, PostToolUse, PreCompact | `working` |
| hooks: Stop | `idle` |
| hooks: PostCompact | `idle` (a hooks-only done is idle whichever hook produced it; the next turn event turns it back to `working`) |
| hooks only: PermissionRequest (blocked) | `working` (the turn is in progress, and no hook fires when the dialog ends) |
| terminal runtime: blocked | `blocked`, with `kind` where known |
| terminal runtime: idle, working, done | kept as they are |
| runtime and hooks agree | the runtime state |

The bound is Claude Code hooks only: no other harness and no wrapper. `error` and `auth` have no source yet; the
values are defined so a reader accepts them. All of this works with no homerange: without a terminal runtime the hooks still give
working and idle.

### Consequences

* Good, because a sender can see that a peer is blocked on its user before it waits, and nothing about the protocol
  changes.
* Good, because the default `who` line and its cost are unchanged for launchers.
* Bad, because the answer is a claim: anyone in the peer's queue group could answer. It decides only what is shown,
  never trust.
* Good, because a watcher of the public heartbeat sees the five states within about 2 s of a change, and every
  other detail is reply-only.
* Good, because the heartbeat and reply cannot drift: one builder, and a test compares them.
* Bad, because without a terminal runtime a session at a permission dialog reads `working`, never `blocked`.
* Neutral, because peers that do not report yet show `-`, which is the correct reading of absence.

### Confirmation

* `internal/endpoint/peeractivity_test.go` covers:
  * a reported state, a reply with no state, an unrecognised state and an unrecognised kind;
  * a `program_status` that is not an object, a kind without `blocked`, and top-level `state` and `kind` ignored;
  * that `program_detail` is never exposed;
  * `delivery.health` and `delivery.reason`: known words kept, an unknown health not reported, an unknown reason
    dropped, a non-object `delivery` and top-level `delivery` and `delivery_reason` ignored;
  * no responder, and a responder that never answers;
  * a refusal, the concurrency limit of 8, and the cache;
  * that the request carries an `Agent-Sender` that verifies.
* `internal/endpoint/status_payload_test.go` covers the serving side:
  * the extras contract: a panic, a §8.3 name collision and a value that does not marshal each drop that build's
    extras, the beat is still built, and the log has no provider text;
  * the leakage guard: heartbeat keys are §8.3 plus `program_status`, reply keys are those plus `program_detail` and
    `delivery`, and `program_status` is byte-identical in both; the heartbeat never carries `delivery`;
  * the off-cycle beat: a change publishes at once, a burst yields one beat per window with the latest value, no
    change yields none, and every such heartbeat still verifies as signed.
* `cmd/mash/programstatus_test.go` covers the mapping table above and `program_detail`.
* `cmd/mash/delivery_test.go` covers the mapping from spool and channel state to `delivery` and its wire shape.
* `cmd/mash/whoactivity_test.go` covers:
  * the column and its values, the 2 s budget, and that offline peers are not asked;
  * the delivery column (`?`, `-`, never `ok` unless reported) and the JSON fields;
  * that the default line and `--names` are unchanged and send no request.

## Pros and Cons of the Options

### Ask each peer's SAP status on demand (chosen)

* Good, because it costs one small request per peer, only when someone looks, and it is bounded at 8 requests in
  flight and 2 s.
* Good, because a status reply goes only to the asker. Program status is not broadcast.
* Bad, because the answer can be up to 5 s old (the cache).

### Listen for state in heartbeats

* Good, because there is no request at all.
* Bad, because mash stopped listening to heartbeats (ADR 0066), and a reader would hold a subscription to every
  endpoint's beats for a value it needs only when someone looks. mash's readers ask by status request. The heartbeat
  also carries `program_status`, for other watchers.

### Add the state to `who`'s default line

* Good, because everyone would see it.
* Bad, because launchers parse that line (ADR 0034), and every plain `who` would then cost up to 2 s.

## More Information

* The SAP text is §8.2 (interval), §8.3 (added fields), §8.7 and §8.7.1 of 0.3.1 as merged. No SAP change is needed,
  and none is made (D229): an added field is 0.3.1-additive, `protocol_version` stays "0.3", and there is no SDK
  divergence. The SDKs already work this way (Go `host.WithHeartbeatExtras`, TypeScript `heartbeatExtras`, Python
  `heartbeat_extras`). PR #25 adds that no names are reserved and that grouping related fields under one key of the
  agent's own keeps them clear of fields a later revision may define, which is why mash uses `program_status` and
  `program_detail`.
* Later: mash's policy will keep the heartbeat public and answer status only to authorized callers. Everything beyond
  the five states and three kinds is reply-only for that reason.
