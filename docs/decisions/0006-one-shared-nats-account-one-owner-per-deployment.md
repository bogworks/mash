---
status: accepted
date: 2026-10-21
decision-makers: Randy Bias (mash maintainer)
recorded: 2026-10-31 (backfilled)
---

# All endpoints share one NATS account; one owner per deployment; names are unique in the account

## Context and Problem Statement

NATS separates tenants with accounts: subjects, streams and KV buckets in one account are invisible to another
unless explicitly exported. SAP separates operators with the owner token, the fourth subject token (ADR-0005). mash
had to decide where its endpoints live in NATS, what the owner token means, and how widely an endpoint name must be
unique.

## Decision Drivers

* Mail must reach any endpoint of the deployment without NATS account exports or imports.
* Each endpoint's streams and KV keys need names the admin and the endpoint can compute.
* Cross-account delivery was a non-goal for v0.1 (spec §1.2).

## Considered Options (reconstructed 2026-10-31; not recorded at the time)

* One NATS account for all of mash, with the owner as a subject token set per deployment
* One NATS account per owner, with the owner token equal to the account
* Owner-qualified resource names inside one account (for example `inbox-<owner>-<name>`)

## Decision Outcome

Chosen option: "One NATS account for all of mash, with the owner as a subject token set per deployment". No reason
was recorded.

As it stands at main `9839c96`:

* mash needs "one account for mash" (`docs/operations.md`); every endpoint user is issued in that account.
* The owner is set once in the config (`owner` or `MASH_OWNER`); it is required, and an endpoint's permissions confine
  its mail, prompts and status probes to subjects of its own owner (ADR-0007).
* The inbox stream `inbox-<name>`, the directory key and the lease key carry only the name, so a name is unique in the
  account, not only within an owner.

### Consequences

* Good, because one account means no exports, imports or cross-account trust to configure.
* Good, because resource names are short and derived from the endpoint name alone.
* Bad, because two owners in one account cannot both use the same endpoint name.
* Bad, because the account is a shared resource: account-wide limits (such as a hosted service's stream count) are
  shared by every endpoint.
* Neutral, because the directory may hold records with different owners; the sender line shows another owner's
  sender as `owner/name`.

### Confirmation

* `internal/admin/admin_test.go` `TestProvisionRejectsBadNameAndDuplicate`: a second provision of the same name is
  refused.
* `internal/admin/permissions_test.go` `TestPermissionsExact`: the publish and subscribe lists name only the
  endpoint's own owner.
* `internal/render/render_test.go` `TestSenderRules`: another owner's sender is shown as `owner/name`.
* No automated test confirms that one account is used; it follows from the setup in `docs/operations.md`.

## Pros and Cons of the Options

### One account, owner as a subject token

* Good, because it is the simplest NATS setup that lets every endpoint mail every other.
* Bad, because names collide across owners.

### One account per owner

* Good, because NATS itself would isolate owners, including their streams and limits.
* Bad, because mail between owners would need account exports and imports, which v0.1 did not build.

### Owner-qualified resource names in one account

* Good, because names would be unique per owner, as spec §2 states.
* Bad, because every stream, key and permission would carry two tokens.

## More Information

* Design spec §1.2 (non-goal: cross-account delivery), §2 (owner: "One per deployment, set in the config"), §5.
* Code: `internal/sap/subjects.go` (`InboxStream`), `internal/store/store.go` (directory and lease keys),
  `internal/config/config.go` (owner required), `internal/render/render.go` (`Sender`).
* A consumer runs all of its endpoints in one shared account under one owner.
* Related: ADR-0005 (subjects), ADR-0007 (permissions), ADR-0011 (directory).
* **Not yet true:** spec §2 says a name is "Unique within an owner"; the stream and KV keys make it unique within the
  account.
