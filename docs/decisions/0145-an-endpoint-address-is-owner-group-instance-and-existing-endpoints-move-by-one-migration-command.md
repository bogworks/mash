---
status: proposed
date: 2026-11-17
decision-makers: Randy Bias (mash maintainer)
---

# An endpoint address is owner, group, instance, and existing endpoints move by one migration command

## Context and Problem Statement

mash has two address forms. The original one puts the constant `cc` in the first SAP slot and the deployment's owner and
the endpoint's name in the other two (`cc.<owner>.<name>`, [ADR 0005](0005-subjects-follow-sap-hierarchy.md)). A second,
three-token form, off by default, lets the first slot carry a configurable token
([ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md)). Trust has been attached to neither: the
policy work had to add a tag and a template outside the address to say who belongs with whom.

The maintainer's rulings of 2026-11-16 (decision 240) settle the shape: the address is a hierarchy of **owner, then
group, then instance**; mash does not follow SAP's SHOULD for the first slot and says so; there are no legacy agents
("they should never have existed; we make no considerations for them other than how we get them transitioned"); mash
ships wide open; this release has no policy. This record fixes the model and the transition. The design is in
[the plan](../plans/2026-11-17-endpoint-model-owner-group-instance.md).

## Decision Drivers

* Trust follows the path, and NATS wildcards match whole tokens only: each level that needs a rule must be its own token.
* One model. A solo developer should never need to know groups exist.
* No change to the SAP protocol and no divergence in the SAP Go SDK: the subject keeps its shape and its token count.
* The harness stays truthful in the service metadata.
* Existing endpoints have credentials in running sessions: the transition must not cut them off before they are moved.
* Mash stands alone: everything here works for a solo developer with `mashctl init --local`.

## Considered Options

* Keep `cc` in slot 1 and put group and instance behind it (`cc.<owner>.<group>-<instance>`).
* Keep both forms, with the three-token form as the new default.
* One form, owner, group, instance in the three slots, and one migration command.

## Decision Outcome

Chosen: one form, with one migration command.

* **The three slots carry owner, group and instance.** `agents.<verb>.<owner>.<group>.<instance>`. Each token is
  `[a-z][a-z0-9-]*`, 1 to 32 characters, no `--`, no trailing dash; the whole address is at most 64 characters. The flat
  key is `<owner>_<group>_<instance>`.
* **mash departs from SAP's SHOULD for the first slot, and documents it.** Finding agents by a wildcard on the harness
  slot does not work with mash; discovery by the service name works. `metadata.agent` stays `claude-code`; the metadata
  adds `owner`, `group` and `instance`. This is a documented departure, not a protocol change: subject shape and token
  count are SAP's.
* **Records and streams follow the address.** The directory record is keyed by the flat key and has `owner`, `group` and
  `instance` (the harness stays in `agent`; `agent_token` and `name` go). The inbox stream is `inbox-<flat>` with the
  filter `agents.mail.<owner>.<group>.<instance>`; the lease key, the issue record's address and the retired ledger use the
  flat key; the creds file is `<owner>.<group>.<instance>.creds`.
* **A credential's reach is a prefix of the path.** It publishes `agents.{mail,prompt,status}.<owner>.*.*` (open by
  default: ships wide open), its own heartbeat, and the fixed lines it has today; it subscribes to its own prompt and
  status and to `agents.hb.<owner>.*.*`. The owner is a literal in every peer line, so no credential reaches another
  owner. Narrowing to a group is a user's later choice, not in this release.
* **There is one form and no legacy.** `mashctl add`, legacy `reissue`, `agent_token`, the `cc` form and its parsers and
  record code are removed (the CLI face is `mashctl endpoint create|reissue|retire`, designed separately).
* **One migration command moves existing endpoints.** `mashctl endpoint migrate` plans (changes nothing), `--apply` makes
  the new identity of one endpoint (record, inbox stream, new key and creds) and leaves the legacy record, inbox and
  credentials untouched, and `--finish` revokes the old credentials, drains and deletes the legacy inbox and removes the
  legacy record. A legacy name becomes owner = its owner, group = `--group` or `main`, instance = its name; a name over
  32 characters is refused until `--name` gives one.
* **The window is bridged in both directions with the alias machinery of
  [ADR 0076](0076-a-three-token-record-may-carry-the-legacy-identity-it-answers-to-as-an-alias.md).** Old to new:
  old senders still publish to the retained legacy inbox, which the moved endpoint reads. New to old: the moved
  endpoint's credential carries bridge grants for the owner's legacy subjects until `--finish`. This is the only
  legacy-aware code left in 0.5.5 and it is deleted in 0.6.0.

This amends ADR 0005 (subjects), 0006 (one account; owners are slot 1), 0011 (the record), 0074 and 0076 (the alias
window becomes the migration window), 0077 (the rule file's keys) and 0102 (the group is slot 2, not the owner).

### Consequences

* Good: one model; trust in the subject; no tag or template needed to say who belongs with whom; a solo developer sees
  only an owner and a default group.
* Good: running sessions are never cut off; mail is carried over by not moving it; the move is reversible until
  `--finish`.
* Bad: a breaking change for every deployment: every identity changes, every creds file is replaced, every peer that
  addresses by the old form must move. The window and the bridge make it gradual, not free.
* Bad: SAP clients that find agents by a wildcard on the harness slot find nothing in mash. This is documented.
* Bad: the first slot's meaning differs from SAP's SHOULD. The mitigation is documentation and truthful metadata.
* Neutral: the policy release must re-key the held policy stack by the new address.

### Confirmation

* The live proof in the plan (section 2.4) on one local server: two legacy endpoints migrate and move one at a time, mail
  crosses in both directions during the window and is carried over, the old credentials are refused after `--finish`,
  `audit` is clean, and an interrupted `--apply` is finished by running it again.
* Unit tests: the token rule and the 64-character bound, the flat key's unambiguity, `ValidateGrant` refusing a wildcard
  owner, the permission set for an `O.G.I` (no peer line reaches another owner), the refusals (long name, taken address,
  retired flat key, zoned record).
* A test fails the build if a subject, record field or error text still carries the removed legacy form or `agent_token`.

## Pros and Cons of the Options

### Keep `cc` in slot 1 and put group and instance behind it

* Good: no change to the first slot, so SAP's SHOULD is not departed from.
* Bad: the group and instance share one token, joined by a dash, and a wildcard cannot say "this group": trust does not
  follow the path.
* Bad: keeps the legacy form.

### Keep both forms, with the three-token form as the new default

* Good: no forced migration.
* Bad: two models forever, which the maintainer ruled out ("no legacy agents"); every command and every test carries
  both.

### One form, owner, group, instance, and one migration command

* Good: trust follows the path; one model; a solo developer sees only an owner and a default group.
* Bad: a breaking change for every deployment, and a documented departure from SAP's SHOULD.

## More Information

[The plan](../plans/2026-11-17-endpoint-model-owner-group-instance.md) has the tables, the order the operator follows,
what breaks while endpoints move one at a time, the triage of the held policy stack and the size. Decision 240 and the
no-legacy ruling are the maintainer's, 2026-11-16.
