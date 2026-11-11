---
status: accepted
date: 2026-11-11
decision-makers: Randy Bias (mash maintainer)
---

# There are no zones: mail keeps the five-token subject and the address is the fence

## Context and Problem Statement

[ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md) put one label, the
zone, into the mail subject (`agents.mail.cc.<owner>.<zone>.<name>`) so that a label-defined group inside an owner had a
token to stand on, and [ADR 0054](0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md) kept the legacy
five-token subject valid beside it. The analysis in `docs/specs/2026-11-11-fencing-after-zones.md` finds that the zone
makes a policy attribute part of an address, that the six-token mail subject is outside the five-token shape SAP 0.3
gives every agent instance, and that the three-token naming contract
([ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md)) leaves no room for a
fourth identity token. No deployment uses zones (0 zoned records of 68, no policy file deployed).

The maintainer ruled on 2026-11-11 (D188): "There's no zones. I see no reason for them. The natural boundaries in
homerange are the range, herd, and lane." <!-- adrlint:allow vocabulary -->

## Decision Drivers

* The address must say who an endpoint is, not which policy group it is in.
* Mail keeps SAP's five tokens, `agents.mail.<agent>.<owner>.<name>`.
* A fence must still be enforced by the server, not by a sender's good manners.
* Removing an unused feature should cost no migration.

## Considered Options

* Remove the zone; the agent and owner tokens already in the address are the fence (alternative A of the analysis).
* Keep the zone and fix its seams (the compiler, the legacy subject).
* Label-compiled exact allow lists with no subject token (B), or a hybrid (F).

## Decision Outcome

Chosen option: remove the zone, because the maintainer ruled there are no zones and the address already carries the
boundary the server can enforce.

* **No zone token.** A mail subject is `agents.mail.<agent>.<owner>.<name>`. No command, record field, key or stream
  carries a zone. `mashctl set-zone`, `provision --zone` and `subject_label` go; so do the six-token permission lines,
  the zone-wildcard stage of the policy compiler, the per-zone scoped signing keys, the dual-subject (zoned and legacy)
  inbox stream, and the zone check of `mashctl drift`.
* **The address is the fence.** An endpoint's publish permission is its own agent and owner tokens, as
  `NewFormPermissions` already gives it; reaching another owner is an explicit extra grant.
* **Existing records.** A directory record that still carries a zone field is read, and the field is ignored (none
  exists live); nothing writes one.
* **What this does not decide.** The label policy model (what replaces `docs/policy-file.md`, whether a bounded deny or a
  cross-owner grant is offered, and how) is the planner's to write and comes later as contract input. The analysis's
  F1 and F2 stay as input to it, not as decisions. Prompt and status stay outside the fence, as
  [ADR 0055](0055-until-the-maintainer-rules-only-mail-is-policed-prompt-and-status-stay-open.md) says.
* **Supersession.** This record supersedes ADR 0054 and ADR 0056 in full and amends ADR 0053 (its zone token and zone
  keys; its policy-file compiler stays until the label model is ruled) and ADR 0055 (its framing refers to zone keys).

The removal lands as pull requests by package; each names this record.

### Confirmation

* After the removal pull requests merge, `grep -ri zone` over the Go sources and the shipped documentation finds only
  history (records, the changelog, evidence).
* Tests: a mail subject built for any endpoint has five tokens; `mashctl` has no `set-zone` and `provision` has no
  `--zone`; an inbox stream takes one mail subject.

### Consequences

* Good, because mail keeps SAP's five tokens and an address does not change with a policy attribute.
* Good, because the zone code, its keys and its tests go, and the compiler can serve three-token endpoints.
* Good, because nothing live uses a zone, so there is no migration.
* Bad, because a within-owner fence has no mechanism until the label model is ruled; inside an owner every endpoint may
  mail every other, as it already may for the live deployments.
* Bad, because ADR 0053's policy file loses its zone-wildcard groups and is smaller until it is rewritten.

## Pros and Cons of the Options

### Remove the zone (chosen)

* Good, because mail keeps SAP's five tokens, and the server-enforced fence already exists in `NewFormPermissions`.
* Bad, because there is no within-owner fence until the label model is ruled.

### Keep the zone and fix its seams

* Good, because within-owner groups keep a mechanism.
* Bad, because the address still carries a policy attribute and breaks the three-token contract.

### Exact allow lists (B) or a hybrid (F)

* Good, because a within-owner fence survives without a subject token.
* Bad, because B's lists grow quadratically and F needs a policy model nobody has ruled on; it is the planner's to design.

## More Information

Analysis and specification: `docs/specs/2026-11-11-fencing-after-zones.md`. Ruling: D188, planner record
`decisions/2026-11-11-messaging-policy-labels-model-rulings.md`.
