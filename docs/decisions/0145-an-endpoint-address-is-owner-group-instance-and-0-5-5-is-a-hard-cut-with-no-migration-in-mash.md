---
status: proposed
date: 2026-11-17
decision-makers: Randy Bias (mash maintainer)
---

# An endpoint address is owner, group, instance, and 0.5.5 is a hard cut with no migration in mash

## Context and Problem Statement

mash has two address forms. The original one puts the constant `cc` in the first SAP slot and the deployment's owner and
the endpoint's name in the other two (`cc.<owner>.<name>`, [ADR 0005](0005-subjects-follow-sap-hierarchy.md)). A second,
three-token form, off by default, lets the first slot carry a configurable token
([ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md)). Trust has been attached to neither: the
policy work had to add a tag and a template outside the address to say who belongs with whom.

The maintainer's rulings of 2026-11-16 and 2026-11-17 settle the shape. The address is a hierarchy of **owner, then
group, then instance** (decision 240); mash does not follow SAP's SHOULD for the first slot and says so; there is no
default group; there are no legacy agents; mash ships wide open; this release has no policy; and the old endpoints "are
literally disappearing and will exist nowhere": no one uses mash yet, so there is no soft cut and no command for one-time
use. This record fixes the model. The design is in [the plan](../plans/2026-11-17-endpoint-model-owner-group-instance.md).

## Decision Drivers

* Trust follows the path, and NATS wildcards match whole tokens only: each level that needs a rule must be its own token.
* One model, one form.
* No change to the SAP protocol and no divergence in the SAP Go SDK: the subject keeps its shape and its token count.
* The harness stays truthful in the service metadata.
* A command that exists for one use does not belong in mash.
* Mash stands alone: everything here works for a solo developer with `mashctl init --local`.

## Considered Options

* Keep `cc` in slot 1 and put group and instance behind it (`cc.<owner>.<group>-<instance>`).
* Keep both forms, with the three-token form as the new default.
* One form, owner, group, instance in the three slots, and a hard cut.

## Decision Outcome

Chosen: one form, and a hard cut.

* **The three slots carry owner, group and instance.** `agents.<verb>.<owner>.<group>.<instance>`. Each token is
  `[a-z][a-z0-9-]*`, 1 to 32 characters, no `--`, no trailing dash; the whole address is at most 64 characters. The flat
  key is `<owner>_<group>_<instance>`.
* **There is no default group.** `endpoint create` needs a group and a session needs `MASH_GROUP`. A missing group is a
  readable refusal, code `group_missing`.
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
* **The endpoint makes its own key in 0.5.5.** `endpoint create` takes the endpoint's public key, signed by the account
  signing key; no private key crosses to the central host. The group is never stored in configuration: it is given on
  each create and by `MASH_GROUP`. A group cannot be changed later (create a
  new endpoint, delete the old; its flat key is burned). An explicit same-host flag makes the key on the mashctl host; the
  endpoint-made key stays the default. A second mashctl machine is set up by `adopt` with the same key material; an own
  signing key in the account JWT is not supported (ADR 0059). This is the credential primitive of ADR 0136 lifted out of the
  list-set path.
* **There is one form, no legacy and no migration in mash.** `mashctl add`, legacy `reissue`, `agent_token`, the `cc` form,
  its parsers and record code, and the alias machinery are removed. 0.5.5 does not read, convert or tolerate an old
  record, address or credential. Moving the maintainer's own endpoints is a one-time script outside mash, owned by
  operations.

This amends ADR 0005 (subjects), 0006 (one account; owners are slot 1), 0011 (the record), 0074 and 0076 (their alias
machinery is removed), 0077 (the rule file's keys) and 0102 (the group is slot 2, not the owner).

### Consequences

* Good: one model; trust in the subject; no tag or template needed to say who belongs with whom; no legacy-aware reader
  and no one-use command to carry.
* Bad: a breaking change for every deployment: every identity changes and every creds file is replaced. Nothing in mash
  helps with that; the release note says so first.
* Bad: SAP clients that find agents by a wildcard on the harness slot find nothing in mash. This is documented.
* Bad: the first slot's meaning differs from SAP's SHOULD. The mitigation is documentation and truthful metadata.
* Neutral: the policy release must re-key the held policy stack by the new address.

### Confirmation

* A live proof on one local server (plan, section 2): create endpoints with an endpoint-made key, exchange mail across
  groups of one owner, revoke a key and see the live connection close and the reconnect refused, and delete an endpoint; then the same across three real hosts, over TLS, on a cluster, from
  two mashctl machines.
* Unit tests: the token rule and the 64-character bound, the flat key's unambiguity, `ValidateGrant` refusing a wildcard
  owner, the permission set for an `O.G.I` (no peer line reaches another owner), the refusals (missing group, taken
  address, burned flat key).
* A test fails the build if a subject, record field or error text still carries the removed legacy form or `agent_token`.

## Pros and Cons of the Options

### Keep `cc` in slot 1 and put group and instance behind it

* Good: no change to the first slot, so SAP's SHOULD is not departed from.
* Bad: the group and instance share one token, joined by a dash, and a wildcard cannot say "this group": trust does not
  follow the path.
* Bad: keeps the legacy form.

### Keep both forms, with the three-token form as the new default

* Good: no forced change.
* Bad: two models forever, which the maintainer ruled out; every command and every test carries both.

### One form, owner, group, instance, and a hard cut

* Good: trust follows the path; one model; no code that exists for a single use.
* Bad: a breaking change for every deployment, and a documented departure from SAP's SHOULD.

## More Information

[The plan](../plans/2026-11-17-endpoint-model-owner-group-instance.md) has the tables, the triage of the held policy stack
and the size. Decision 240, the no-default-group ruling and the hard-cut ruling are the maintainer's, 2026-11-16 and
2026-11-17.
