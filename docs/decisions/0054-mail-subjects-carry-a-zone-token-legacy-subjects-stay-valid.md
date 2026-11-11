---
status: superseded by ADR-0091
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# Mail subjects carry a zone token, and the legacy subjects stay valid

Superseded by [0091](0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md).

## Context and Problem Statement

[ADR 0005](0005-subjects-follow-sap-hierarchy.md) fixed every mash subject as `agents.<verb>.cc.<owner>.<name>`. Mail
to an endpoint therefore goes to `agents.mail.cc.<owner>.<name>`, and one permission line,
`agents.mail.*.<owner>.*`, lets a user mail any endpoint of the owner. A subject with no room for anything between the
owner and the name gives mash nowhere to put a boundary inside an owner, such as a zone. Adding that token to the mail
subject changes a subject that stored inboxes, user permissions and running senders all depend on, and mash cannot
upgrade them all at once: users are minted by the operator's own tooling, and endpoints and senders are upgraded
machine by machine. How does a zone token get into the mail subject without breaking endpoints and users that do not
know about it?

## Decision Drivers

* Existing endpoints, their inboxes and the mail already stored must keep working, unchanged, with no migration step
  that can discard mail.
* A deployment is upgraded in stages, so for a while some senders will publish to the old subject of an endpoint that
  has already moved.
* Permissions are enforced by the NATS server from the user's JWT. A user minted before this change cannot be made to
  publish a new subject by upgrading mash.
* An endpoint with no zone must not be silently given one: "no zone" and a zone called `default` are different facts.
* The `who --json` form is a stable contract ([ADR 0049](0049-who-filters-and-its-json-form-are-a-stable-shared-contract.md)).
* Prompt, status and heartbeat are SAP's own verbs and are not part of this change.

## Considered Options

* Add the zone token to the mail subject only, keep the legacy subject valid next to it, and make both subjects land
  in a zoned endpoint's inbox.
* Replace the mail subject for every endpoint at once.
* Put the zone in a message header, with no subject change.
* Put the zone token on every verb (prompt, status, heartbeat and mail).

## Decision Outcome

Chosen option: add the zone token to the mail subject only, keep the legacy subject valid next to it, and make both
land in a zoned endpoint's inbox, because it is the only option that needs no flag day and loses no mail. This amends
ADR 0005 for the mail verb only.

* **The subject.** An endpoint in a zone has the mail subject `agents.mail.cc.<owner>.<zone>.<name>` (6 tokens). An
  endpoint with no zone keeps `agents.mail.cc.<owner>.<name>` (5 tokens).
* **The zone token** is one token: 1 to 32 lower-case letters, digits and hyphens (a tag is lower-cased by the JWT
  library). It never contains `.`, `*`, `>` or whitespace. The conventional name is `default` (`sap.DefaultZone`). A
  record with no `zone` field means the legacy subject; it does not mean `default`.
* **The record.** The directory record gains an optional `zone` field. A record that has one must have a valid zone and
  a `mail_subject` equal to the zoned subject; a record without one must have the legacy subject. Records written by
  older mash have no zone and load unchanged.
* **Dual stream subjects.** A legacy inbox stream has the subject `agents.mail.*.<owner>.<name>`. A zoned inbox has that
  and the exact filter of its own zone, `agents.mail.*.<owner>.<zone>.<name>`, never a wildcard zone. The exact zone is
  the point: with `agents.mail.*.<owner>.*.<name>` a sender whose permissions allow publishing only into zone A could
  address `agents.mail.cc.<owner>.A.<name>` and reach an endpoint that is really in zone B, and the receiver, which
  verifies the signature against the address it was sent to, would accept it, defeating zone-based publish gates. The
  legacy filter stays during migration, so a sender that holds the legacy wildcard publish line can still reach the
  endpoint on the legacy subject; users compiled from a policy, which lack that line, cannot. The strict check on an
  existing inbox of a zoned record accepts exactly the legacy filter and its own zone's filter, and refuses the
  any-zone wildcard or another zone's filter; a legacy record accepts the legacy filter alone or with one exact zone
  filter (a `set-zone` in progress). It stays strict for every other field.
* **Moving an endpoint** is `mash admin set-zone <name> <zone>`. It appends the exact filter of the target zone to the
  existing stream (which must be the legacy filter alone, or already carry that filter) with an update that changes nothing else and purges nothing, then rewrites the directory record's `zone` and
  `mail_subject` against the revision it read, keeping every other field. It is idempotent and can resume after a
  stop between its two steps. A new endpoint is provisioned with `--zone Z` and gets both subjects from the start. A
  zoned endpoint does not return to legacy, and does not change zone, in this version.
* **Dual publish permission.** The wildcard mail grant is two lines, `agents.mail.*.<owner>.*` and
  `agents.mail.*.<owner>.*.*`. The per-recipient form that the policy compiler uses keeps its one legacy line per
  recipient.
* **Senders follow the record.** A sender publishes to the `mail_subject` in the recipient's directory record, so it
  reaches a legacy and a zoned endpoint alike without knowing which it is. An endpoint advertises its own record's
  mail subject.
* **Rollout order.** (1) Upgrade the mash binaries, which read both subjects. (2) Re-issue user permissions to include
  the zoned publish line: a user minted before this change can only publish the 5-token mail subject, so it cannot mail
  a zoned endpoint. (3) Run `set-zone` or `provision --zone` per endpoint, in any order.
* **Prompt, status and heartbeat** keep their 5-token subjects. Whether they should carry a zone too is undecided and
  is the maintainer's call; this decision does not prejudge it.
* **`who --json`** gains an optional `zone` key, present only when the endpoint has a zone. Adding an optional key is
  allowed by ADR 0049; no existing key changes, and consumers must already ignore keys they do not know. `who --long`
  and `admin list` show the zone.

It relates to the mashctl and policy records (0052, 0053) only through the permission lines: the policy compiler
emits per-recipient lines, which stay the legacy form until it is changed to emit the zoned line too.

### Consequences

* Good, because nothing existing changes: an endpoint without a zone has the same subject, stream and permissions it
  always had.
* Good, because the move is an append to the stream's subjects and a rewrite of one record, so stored mail and the
  stream's sequence are untouched, and a rollout never has a moment when mail to the old subject is dropped.
* Good, because senders need no change in how they find a recipient: the directory already holds the subject.
* Bad, because an old user cannot mail a zoned endpoint until its permissions are re-issued, and mash cannot re-issue
  them: it never mints users.
* Bad, because a user holding the 5-token wildcard and one holding both lines are both valid, so a deployment mid-roll
  has two kinds of users until the old ones are replaced.
* Bad, because the legacy subject stays in every zoned stream, so mail to it lands in a zoned inbox with no zone
  check; the signature, not the subject, proves the sender, as before.
* Bad, because a zoned endpoint cannot be moved back or to another zone in this version.

### Confirmation

Tests assert each part. `internal/sap`: `ForZone`, `MailFilterZone`, `ZoneOfMailFilter`, `SplitMail` and a table for `ValidZone`.
`internal/store`: a legacy record loads, a zoned record round trips, a zone and subject that disagree are refused, and a
zoned stream config differs from a legacy one only in its subjects. `internal/admin`: a zoned provision creates both
subjects, the inbox check accepts both shapes and still rejects other drift, `SetZone` appends the subject, keeps the
stored mail and sequence and every other record field, is idempotent and refuses a bad zone and a stream carrying another zone's filter or the wildcard, and a message addressed to the wrong zone is not stored in a zoned endpoint's inbox while the right zone and the legacy subject are. The permissions test lists
both mail lines. `internal/endpoint`: legacy and zoned endpoints mail each other, and mail published to the old subject
of a moved endpoint arrives verified. `cmd/mash`: `provision --zone`, `set-zone`, `list` and `who` show the zone, and
`who --json` has the key only when set.

## Pros and Cons of the Options

### Zone on the mail subject only, legacy subject kept valid

* Good, because it needs no flag day and loses no mail.
* Good, because the permission model stays subject-based, so the NATS server enforces it.
* Bad, because every zoned stream and every wildcard user carries a second line for as long as legacy senders exist.

### Replace the mail subject for every endpoint at once

* Good, because there would be one subject shape and one permission line.
* Bad, because every user would need new permissions at the same moment as every endpoint's stream was changed, and a
  sender that was not yet upgraded would publish to a subject no stream takes: that mail is lost.

### Zone in a message header

* Good, because the subject and the permissions would not change.
* Bad, because the server cannot enforce a header, so a user could mail any zone, and a zone boundary that only the
  receiver checks is not a boundary.

### Zone token on every verb

* Good, because all four subjects would follow one shape.
* Bad, because prompt, status and heartbeat are SAP's own subjects ([ADR 0005](0005-subjects-follow-sap-hierarchy.md)),
  discovery and the signed `id_sig` depend on their shape, and changing them is a larger decision than this one needs.

## More Information

* Code: `internal/sap/subjects.go` (`ForZone`, `ValidZone`, `MailFilterZone`, `ZoneOfMailFilter`, `SplitMail`), `internal/store/store.go`
  (`Record.Zone`, `InboxConfigZoned`), `internal/admin/admin.go` (`Meta.Zone`, `SetZone`, `checkInboxStream`),
  `internal/admin/permissions.go`, `cmd/mash/admin.go`.
* Guide: "Zones" in `docs/operations.md`.
* ADR 0005 is not edited: accepted ADRs are immutable, and this record amends it.
