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
* One model, with no default group: every endpoint names its group.
* No change to the SAP protocol and no divergence in the SAP Go SDK: the subject keeps its shape and its token count.
* The harness stays truthful in the service metadata.
* Existing endpoints have credentials in running sessions: the transition must not cut them off until the operator has moved them and chosen to finish; until then it can be rolled back.
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
* **There is no default group.** `endpoint create` needs a group, `migrate` needs `--group`, a session needs `MASH_GROUP`;
  a missing group is a readable refusal, code `group_missing`.
* **There is one form and no legacy.** `mashctl add`, legacy `reissue`, `agent_token`, the `cc` form and its parsers and
  record code are removed (the CLI face is `mashctl endpoint create|reissue|retire`, designed separately).
* **One migration command moves existing endpoints.** `mashctl endpoint migrate --all --dry-run` plans (changes nothing),
  `--apply` makes the new identities (record, inbox stream, user, copy of unread mail) and leaves the legacy record, inbox
  and credentials untouched, `--rollback` removes the new identities, and `--finish` revokes the old credentials and
  removes the legacy inbox and record. A legacy name becomes owner = its owner, group = `--group` (required), instance =
  its name; a name over 32 characters is refused until `--name` gives one.
* **The move is one sitting with a clean rollback.** No bridge, alias read or legacy lease is built. `--apply` makes every
  new identity and copies unread mail; the operator restarts every session; `--finish` revokes the old credentials and
  removes the legacy inboxes and records. Before `--finish` the old binaries and credentials work unchanged and
  `--rollback` removes the new identities (no flat key burned); after it nothing is reversible. While sessions move, old
  and new endpoints cannot reach each other: the restart pass is the whole window.
* **The endpoint makes its own key in 0.5.5.** `endpoint create` and `migrate --apply` accept the endpoint's public key (a
  folder of `<name>.pub`), signed by the account signing key; no private key crosses to the central host. This is the
  credential primitive of ADR 0136 lifted out of the list-set path.

This amends ADR 0005 (subjects), 0006 (one account; owners are slot 1), 0011 (the record), 0074 and 0076 (their alias machinery is removed), 0077 (the rule file's keys) and 0102 (the group is slot 2, not the owner).

### Consequences

* Good: one model; trust in the subject; no tag or template needed to say who belongs with whom; a solo developer names one group once.
* Good: running sessions are never cut off; mail is carried over by not moving it; the move is reversible until
  `--finish`.
* Bad: a breaking change for every deployment: every identity changes, every creds file is replaced, every peer that
  addresses by the old form must move, in one sitting.
* Bad: SAP clients that find agents by a wildcard on the harness slot find nothing in mash. This is documented.
* Bad: the first slot's meaning differs from SAP's SHOULD. The mitigation is documentation and truthful metadata.
* Neutral: the policy release must re-key the held policy stack by the new address.

### Confirmation

* The live proof in the plan (section 2.4) on one local server: the sitting, a rollback (old binaries and credentials work,
  all mail is there, no flat key burned), the sitting again, `--finish` (old credentials refused, `audit` clean), the
  revocation dropping a live connection, and an interrupted `--apply` finished by running it again.
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

* Good: trust follows the path; one model; a solo developer names one group once.
* Bad: a breaking change for every deployment, and a documented departure from SAP's SHOULD.

## More Information

[The plan](../plans/2026-11-17-endpoint-model-owner-group-instance.md) has the tables, the order the operator follows,
what breaks while endpoints move one at a time, the triage of the held policy stack and the size. Decision 240 and the
no-legacy ruling are the maintainer's, 2026-11-16.
