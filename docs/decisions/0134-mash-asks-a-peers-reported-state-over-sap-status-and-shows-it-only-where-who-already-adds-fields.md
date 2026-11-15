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

SAP 0.3 gives every agent a `status` endpoint, which answers a request with a heartbeat-shaped reply (SAP §8.7). A
draft change to SAP, 0.3.1, reserves the keys of OSC 7501 (the terminal program-status protocol, revision 0.4) in that
reply: a top-level `state` (`idle`, `working`, `done`, `blocked`, `error`), and `kind` with `blocked` (`permission`,
`question`, `auth`), with OSC 7501's meanings. Randy ruled that mash does this now, and conforms to the text if it is
added. The serving side, a state source and the reply, is a separate change. This record is the asking side.

## Decision Drivers

* No change to SAP beyond the draft, and no divergence from the SAP Go SDK. Asking is an ordinary SAP status request.
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
* Only `state`, `kind` and `instance_id` are read.
* An unrecognised `state` means not reported, and an unrecognised `kind` is dropped (OSC 7501's rules). `kind` counts
  only with `blocked`.
* `msg`, `title` and every other key are never read or shown.

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
  * that `msg` and `title` are never exposed;
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
* Bad, because every heartbeat subscriber would receive every endpoint's state, and the SAP draft keeps the
  program-status keys in the status reply. mash also stopped listening to heartbeats (ADR 0066).

### Add the state to `who`'s default line

* Good, because everyone would see it.
* Bad, because launchers parse that line (ADR 0034), and every plain `who` would then cost up to 2 s.

## More Information

* The serving side, the state source and the status reply, is the internal/activity change and the status payload
  that follows it.
* The draft SAP text is §8.7.2, "Reserved program-status keys" (0.3.1). It is not filed upstream.
