---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# One NATS user per endpoint, and a fixed least-privilege permission set is the whole security model

## Context and Problem Statement

mash endpoints share one NATS account (ADR-0006), so nothing at the account level stops one endpoint from reading,
deleting or impersonating another. NATS users carry publish and subscribe permissions in their credentials (a signed
JWT, JSON Web Token). mash needed an identity per endpoint that a receiver can verify, and a way to confine each
endpoint to its own inbox, lease and subjects.

## Decision Drivers

* "Unique, verifiable identities" (spec §1.1 goal 3).
* One endpoint must not be able to read, delete, purge or take over another endpoint's inbox or lease.
* The rules must be checkable: a short, fixed list, the same for every endpoint.

## Considered Options

* Reply rights through NATS's `allow_responses` (at most one response per request), as in the first spec
* Reply rights through a publish allowance on `_INBOX.>` only, with a private `_INBOX.<name>` reply prefix per endpoint

## Decision Outcome

Chosen option: one NATS user per endpoint, with a fixed permission set, and replies through `_INBOX.>` publish only.
The spec states: "These permissions … are the whole security model" (spec §8). The response permission was dropped
the night it was written because, with it, an endpoint "could send itself a request whose reply subject is
`$JS.API.STREAM.DELETE.<victim>` and answer it" (spec §8). Directory consumers were removed the same night (ADR-0011).

The set (spec §8; `internal/admin/permissions.go`), in short:

* Publish: mail, prompts and status probes to any endpoint of the same owner; its own heartbeat; `_INBOX.>` and
  `$SRV.>` replies; info, fetch, ack and direct reads on its own inbox stream; read-only access to the directory
  (stream info and direct get); its own lease key.
* Subscribe: its own reply prefix `_INBOX.<name>.>`, its own prompt and status subjects, `$SRV.>`, and heartbeats of
  its owner.
* Everything else is denied, including another endpoint's inbox, history and lease, deleting or purging streams,
  creating consumers on the directory, and the JetStream admin API.

### Consequences

* Good, because each message's sender is a distinct NATS user whose key signs it (ADR-0003).
* Good, because the permission set is the same template for every endpoint and is printed by
  `mash admin permissions <name>`, as JSON or as `nsc` flags.
* Bad, because any endpoint of the same owner may send mail to any other, so it can fill another inbox (see
  `SECURITY.md`, Known limits, and ADR-0010).
* Neutral, because callers must use `_INBOX.` reply subjects, which is every NATS client's default.

### Confirmation

* `internal/admin/permissions_test.go` `TestPermissionsExact`: the exact publish and subscribe lists.
* `internal/admin/permissions_test.go` `TestPermissionsNeverGrantOthers`: no over-broad grant (`>`, `$JS.API.>`, stream
  delete or purge, message delete) and no grant on another endpoint's inbox or reply prefix.
* `internal/admin/admin_test.go` `TestEndpointPermissionDenials`: against a real NATS server, reads of another inbox,
  another lease, writes to the directory, message delete, purge, stream delete and create, directory consumers, and
  subscriptions to other reply prefixes are all denied.

## Pros and Cons of the Options

### `allow_responses`

* Good, because it is NATS's standard way to let a service reply to requests.
* Bad, because it lets an endpoint answer any reply subject it receives, including one it chose itself, such as a
  JetStream API subject that deletes another inbox.

### `_INBOX.>` publish and a private reply prefix

* Good, because a reply can only ever land in an inbox subject.
* Good, because the private prefix keeps one endpoint's replies (JetStream deliveries, KV reads, prompt answers)
  unreadable by another.
* Bad, because it relies on callers using `_INBOX.` reply subjects.

## More Information

* Design spec §4 and §8. The first version of §8 (commit `5ab1e4b`) used `allow_responses`; it was replaced in commit
  `7ab5872` (2026-10-21), and directory consumers were removed in commit `9a82418`.
* Code: `internal/admin/permissions.go` (`Permissions`, `NSCFlags`).
* `SECURITY.md` (trust model and known limits).
* Related: ADR-0006 (one account), ADR-0008 (who mints users), ADR-0009 (inbox per endpoint), ADR-0011 (directory).
