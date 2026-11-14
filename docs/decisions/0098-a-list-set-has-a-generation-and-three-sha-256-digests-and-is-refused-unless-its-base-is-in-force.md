---
status: accepted
date: 2026-11-12
decision-makers: Randy Bias (mash maintainer); the choices were delegated to the implementers by contract mash-messaging-policy draft-2 (D206), REQ-017
---

# A list set has a generation and three SHA-256 digests, and is refused unless its base is the generation in force

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
* The digest of what was received must identify the submission as received, and the digest of content must still treat two
  encodings of the same content as equal, so both are kept.
* Two submissions built on the same generation must not both be accepted; that needs an atomic check at commit, which
  needs the apply path and so belongs to the apply work.

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
* **Digests.** SHA-256, written `sha256:<hex>`. A `Record` holds three. `Received` is the SHA-256 of exactly the bytes the
  list set was received as, passed to `Accept`. Nil or empty bytes are refused with `ErrNoReceivedBytes` (after the
  stale-base check, before compiling), so `Received` is never the zero digest on an accepted record. No file format
  exists (DEC-004), so mash cannot yet check that the bytes decode to the list set; the decoder, once it exists, must
  hand over the bytes it decoded. `Content` is the digest of the list set (`DigestListSet`) in a canonical
  encoding with switches, senders and each allow and deny list sorted, and includes the range and the base, so reordering
  does not change it and any changed entry does; two encodings of the same content share a `Content` and differ in
  `Received`. `Signed` covers what Compile produced (`DigestCompiled`):
  the range, each template's lines (sorted within each list) and each user's sender, name and sorted tags. Each encoding
  starts with a version string and uses length-prefixed fields, so one cannot be read as another.
* **Serialization and commit belong to the apply work.** `Accept` is pure and decides nothing on its own: it neither
  serializes submissions nor commits anything, so two built on the same record both pass it. The caller that signs and
  pushes must make the base check and the replacement of the record in force one atomic step, at commit time and after a
  successful apply, so that of several submissions on the same base exactly one wins. It must use exactly the
  `Compiled` that `Accept` returned, unmodified, whose digest is `Record.Signed`. A failed or partial apply must not
  leave a state that lets the same base be accepted again while partial changes are visible. That path belongs to the
  apply work, under REQ-011's applied-policy gate (contract mash-messaging-policy), and is not built here. No in-process
  ledger is added: it would be built before the real sign-and-push call site exists.

### Consequences

* Good, because two submitters working from the same generation cannot both be accepted, and a stale submission is
  refused before anything is computed.
* Good, because the check and the record are pure and testable without storage.
* Good, because `Received` identifies the submission as sent while `Content` still equates reordered or re-encoded forms.
* Bad, because the caller must pass the received bytes, and mash cannot yet check that they decode to the list set.
* Bad, because nothing here yet gives one winner per base: serialization, commit-after-apply, partial-apply handling
  and the use of the unmodified `Compiled` belong to the apply work under REQ-011's gate.

### Confirmation

`internal/listset` tests: AT-016 (base N-1 and N+1 refused with nothing returned), a chain of acceptances, digest
stability under reordering and change under any edit, received bytes giving `Received` (same bytes the same, different bytes different, nil and empty refused) with the same `Content`,
a validation failure returning no record.

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

Contract mash-messaging-policy draft-2 (D206), REQ-017, AT-016. Builds on [ADR 0102](0102-list-set-permissions-are-one-template-per-owner-group-with-allow-and-deny-tags-and-a-deny-sentinel.md).
Code: `internal/listset/generation.go`.
