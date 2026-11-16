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
maintainer declined it as written. Randy ruled that mash reports peer state over SAP status now. The serving side, a
state source and the reply, is a separate change. This record is the asking side.

## Decision Drivers

* No change to SAP, and no divergence from the SAP Go SDK (D229). Asking is an ordinary SAP status request, and
  `program_status` is an added field SAP §8.3 already allows.
* A reported state is the peer's unsigned claim (SAP §13.11: a reply is never signed), so mash never grants anything on it.
* Silence is never "free". An absent state is not `idle`.
* `who`'s default line is a compatibility contract that launchers parse
  ([ADR 0034](0034-text-output-and-plugin-commands-are-a-compatibility-contract.md)).
* Asking many peers must be bounded in time and load.

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

### Consequences

* Good, because a sender can see that a peer is blocked on its user before it waits, and nothing about the protocol
  changes.
* Good, because the default `who` line and its cost are unchanged for launchers.
* Bad, because the answer is a claim: anyone in the peer's queue group could answer. It decides only what is shown,
  never trust.
* Neutral, because peers that do not report yet show `-`, which is the correct reading of absence.

### Confirmation

* `internal/endpoint/peeractivity_test.go` covers:
  * a reported state, a reply with no state, an unrecognised state and an unrecognised kind;
  * a `program_status` that is not an object, a kind without `blocked`, and top-level `state` and `kind` ignored;
  * that `program_detail` is never exposed;
  * no responder, and a responder that never answers;
  * a refusal, the concurrency limit of 8, and the cache;
  * that the request carries an `Agent-Sender` that verifies.
* `cmd/mash/whoactivity_test.go` covers:
  * the column and its values, the 2 s budget, and that offline peers are not asked;
  * the JSON fields;
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
  may carry `program_status` for other watchers; that is the serving side's change.

### Add the state to `who`'s default line

* Good, because everyone would see it.
* Bad, because launchers parse that line (ADR 0034), and every plain `who` would then cost up to 2 s.

## More Information

* The serving side, the state source and the status reply, is the internal/activity change and the status payload
  that follows it.
* The SAP text is §8.3 (added fields), §8.7 and §8.7.1 of 0.3.1 as merged. No SAP change is needed, and none is made
  (D229).
* Later: mash's policy will keep the heartbeat public and answer status only to authorized callers, so everything
  beyond the five states and three kinds stays reply-only.
