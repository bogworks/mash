---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer); the choices were delegated to the implementers by contract mash-messaging-policy draft-2 (D206), REQ-017
---

# A list set has a generation and two SHA-256 digests, and is refused unless its base is the generation in force

## Context and Problem Statement

Contract mash-messaging-policy draft-2 REQ-017 has each accepted list set carry a generation number and the digest of
the list set as received and of what was signed, and refuses a list set whose stated base generation is not the one in
force (AT-016). It does not say how the number and digests are formed. The list set's file format (DEC-004) and the
storage that holds what is in force are not decided, so the code, in `internal/listset`, has to keep these rules without
either.

## Decision Drivers

* A stale list set must change nothing and sign nothing, and be refused before any other work (the contract's rule that
  an error at any step leaves the previous generation in force).
* The same content must give the same digest however it was ordered, since lists are sets.
* The received digest will move to the received bytes once a file format exists; that must be a change in one place.

## Considered Options

* A counter in the list set's own content: `Base` is the generation it was written against, the new generation is
  `Base+1`, and `Accept` is a pure function of the record in force and the list set (chosen).
* A counter held by storage, with the list set carrying only a digest of its predecessor.
* A timestamp as the version.

## Decision Outcome

Chosen option: the first.

* **Generation.** The first accepted list set is generation 1; a zero `Record` means nothing is in force. Each accepted
  list set is the in-force generation plus one. `ListSet.Base` states the generation the submitter worked from, 0 when
  nothing was in force. `Accept` refuses, before it validates or compiles, any list set whose `Base` differs from the
  generation in force, in either direction, with `*StaleBaseError` (matching `ErrStaleBase`), and refuses to overflow the
  number. On any error it returns the zero `Record` and no output, so the caller keeps what is in force.
* **Digests.** SHA-256, written `sha256:<hex>`. The received digest covers the list set (`DigestListSet`) in a canonical
  encoding with switches, senders and each allow and deny list sorted, and includes the range and the base, so reordering
  does not change it and any changed entry does. The signed digest covers what Compile produced (`DigestCompiled`):
  the range, each template's lines (sorted within each list) and each user's sender, name and sorted tags. Each encoding
  starts with a version string and uses length-prefixed fields, so one cannot be read as another. When a file format
  exists, `DigestListSet` is the one function that moves to the received bytes.
* **Scope.** The package keeps no state. Storing the record, comparing it across restarts and the apply path belong to the
  code that applies a list set.

### Consequences

* Good, because two submitters working from the same generation cannot both be accepted, and a stale submission is
  refused before anything is computed.
* Good, because the check and the record are pure and testable without storage.
* Bad, because until a file format exists the received digest covers a canonical re-encoding, not the bytes the operator
  sent, so two files that decode to the same list set share a digest.

### Confirmation

`internal/listset` tests: AT-016 (base N-1 and N+1 refused with nothing returned), a chain of acceptances, digest
stability under reordering and change under any edit, and a validation failure returning no record.

## Pros and Cons of the Options

### Base in the list set, generation `Base+1` (chosen)

* Good, because it needs no storage to be decided and states the submitter's intent in the content that is digested.
* Bad, because a submitter has to know the generation in force.

### Storage-held counter with a predecessor digest

* Good, because a digest chain detects any gap.
* Bad, because it needs storage and a file format that do not exist yet, and AT-016 speaks of generation numbers.

### Timestamp as version

* Good, because it needs no number.
* Bad, because it depends on clocks and cannot say which list set was meant as the base.

## More Information

Contract mash-messaging-policy draft-2 (D206), REQ-017, AT-016. Builds on [ADR 0094](0094-list-set-permissions-are-one-template-per-owner-group-with-allow-and-deny-tags-and-a-deny-sentinel.md).
Code: `internal/listset/generation.go`.
