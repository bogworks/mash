---
status: accepted
date: 2026-11-13
decision-makers: Randy Bias (mash maintainer); the planner's ruling that a sender sees only its own reach, and the mash coordinator's choices of subject and responder (REQ-010 as amended)
---

# A sender sees only its own reach, through a signed policy view request

## Context and Problem Statement

Contract mash-messaging-policy REQ-010 asks for `show` and `explain` in mash, runnable without admin rights or signing
keys. The full list set names every sender's exceptions, so showing it to every sender would let any sender read the
lists of all others. The planner ruled that a sender sees only its own reach, and that the full lists are an operator
view (`mashctl policy show|explain|diff`). A sender's credential grants nothing on the policy bucket, and should not. How
does `mash policy show|explain` get a sender's own view?

## Decision Drivers

* A sender learns its own reach and nothing about another sender's lists.
* The caller's address must come from proof, never from what the request says.
* No new read access to the policy bucket for senders.
* Accounts that run without list sets (owner line only) need no new service and no change to their credentials.
* No change pushed to a live account now: a template change disconnects every sender under the key (ADR 0097).

## Considered Options

* A signed request to a policy view responder on `agents.policy.<range>` (chosen).
* Read access to the status value for every sender.
* An answer on the endpoint's own SAP status subject.

## Decision Outcome

Chosen option: "A signed request to a policy view responder".

* Every list-set template carries one more fixed publish line, `agents.policy.<range>`, and `Check` requires it. The
  reply returns on the sender's own `_INBOX` prefix, which is already granted. User JWTs carry tags only, so the CONNECT
  line and its size limits do not change. No list set has been pushed to any account yet, so this changes what an
  adopted account receives on its first push, and disconnects no one today.
* The responder (`internal/policyview`, mounted by the long-running `mashctl policy serve`) serves that subject in a
  queue group. It answers only a request whose `Agent-Sender` header verifies (SAP §13, with replay protection), maps
  the verified user key to the sender's address through the directory, and returns only that sender's reach, open
  subjects and the state of the generation in force (`show`), or that sender's answer for one receiver (`explain`).
  An unsigned, claimed, forged or replayed request, or a key with no address in the range, is refused.
* `mash policy show|explain` signs the request with the endpoint's key. A credential that is not a list-set credential
  (an account with no list set, owner line only) gets a local answer, "open: owner line only, no list set in force",
  with no request.

### Consequences

* Good, because no sender can read another sender's lists through this path.
* Good, because the address is proven by signature and directory, not stated by the caller.
* Good, because accounts without list sets need nothing new.
* Bad, because a long-running responder must run for the view to answer; without it `mash policy show` reports the
  owner line only.
* Neutral, because the subject name awaits the seam (D211); the planner was told.

### Confirmation

`internal/policyview` tests: a verified caller sees only its own reach; explain for a receiver; unsigned, claimed,
forged and replayed requests refused; an unknown key refused; nothing in force. `internal/listset` tests: the grant is
in every template and `Check` refuses a template without it. `cmd/mash` tests run the command against a mounted
responder and against none.

## Pros and Cons of the Options

### A signed request to a policy view responder

* Good, because only proven callers get answers, and only about themselves.
* Bad, because it adds a responder and one fixed line to each template.

### Read access to the status value for every sender

* Good, because no responder is needed.
* Bad, because every sender could read every list.

### The endpoint's own SAP status subject

* Good, because no new subject.
* Bad, because it mixes the policy view into SAP status, which other agents and tools read.

## More Information

* Contract mash-messaging-policy REQ-010 (as amended), REQ-001, REQ-002.
* ADR 0097 (a template change disconnects the key's connections), ADR 0102 (templates and tags).
