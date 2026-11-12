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
* Two submissions built on the same generation must not both be accepted, so the check and the replacement of the record
  in force must be one atomic step.

## Considered Options

* A counter in the list set's own content: `Base` is the generation it was written against, the new generation is
  `Base+1`, and `Accept` is a pure function of the record in force and the list set, and a `Ledger.Apply` makes the check, the
  caller's apply step and the replacement atomic (chosen).
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
* **Serialization.** `Accept` is pure and does not by itself serialize submissions: two built on the same record both pass
  it. `Ledger` holds the record in force behind a mutex. `Ledger.Apply(ls, received, apply)` runs the pure `Accept` against
  the current record, then calls `apply` (the caller's sign-and-push step) with the new `Record` and the compiled output
  while still holding the lock, and replaces the record only when `apply` returns nil. A nil `apply` is refused. On any
  error, including one from `apply` (returned wrapped, so `errors.Is` works), it returns the zero `Record` and the ledger
  is unchanged, so the previous generation stays in force and nothing new is committed. Of several submissions on the
  same base exactly one succeeds and the rest get `ErrStaleBase`. The lock is held across `apply`, so `apply` must not
  call back into the `Ledger`.
* **Scope.** A `Ledger` lives in process memory. Persisting the record, comparing it across restarts and the
  cross-process apply path are not built yet; that path must give the same guarantee: an atomic base check against the stored record at the moment of commit, after a
  successful apply.

### Consequences

* Good, because two submitters working from the same generation cannot both be accepted, and a stale submission is
  refused before anything is computed.
* Good, because the check and the record are pure and testable without storage.
* Good, because `Received` identifies the submission as sent while `Content` still equates reordered or re-encoded forms.
* Bad, because the caller must pass the received bytes, and mash cannot yet check that they decode to the list set.
* Bad, because exclusivity holds only within one process until the apply path is built.

### Confirmation

`internal/listset` tests: AT-016 (base N-1 and N+1 refused with nothing returned), a chain of acceptances, digest
stability under reordering and change under any edit, received bytes giving `Received` (same bytes the same, different bytes different, nil and empty refused) with the same `Content`,
a validation failure returning no record, many concurrent `Apply` calls on one base where exactly one succeeds, the rest
get `ErrStaleBase`, and the ledger ends at the next generation (run under `-race`), a failing `apply` leaving the ledger
unchanged so the same base then succeeds, and `apply` receiving the record and output about to be committed.

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
