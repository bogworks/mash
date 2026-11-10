---
status: accepted
date: 2026-11-10
decision-makers: Randy Bias (mash maintainer)
---

# A three-token record may carry the legacy identity it answers to as an alias

## Context and Problem Statement

During the transition window a new-form endpoint (three SAP tokens, [ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md)) also answers to one legacy identity, so a sender that still addresses the legacy name reaches it. The directory record of the new-form endpoint says nothing about that. Admission cannot then tell that two new-form records would answer to one legacy name, or that a legacy endpoint shares subjects with a record's alias, and a reader cannot show which legacy name an endpoint answers to. Where is the alias recorded, who may write it, and how does admission treat it?

## Decision Drivers

* Identity is security-sensitive: two records must never answer to one legacy name, and no endpoint may extend its own reach.
* Readers from before the change must keep working.
* The alias exists only for the window and must be easy to remove with the legacy form.

## Considered Options

* An optional `alias` object on the new-form directory record (chosen).
* A separate directory bucket or key for aliases.
* Derive the alias from naming convention at read time.

## Decision Outcome

This amends [ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md) by adding one optional field to the directory record.

A new-form record may carry `alias`, an object `{"owner": ..., "name": ...}`: the legacy identity it answers to during the window.

* **Who writes it.** Only provisioning, with admin credentials. An endpoint never writes its own alias.
* **Validation at provisioning.** The owner and name pass the legacy name rule; a legacy record with exactly that owner and name exists and is unzoned; no other new-form record claims it (one alias per legacy identity, one per record); the alias owner differs from the record's own owner.
* **Admission.** The alias's subjects are part of the identity. `store.AdmitWithAlias` applies every rule of `store.Admit` to the identity and, with an alias, also refuses: an alias that names no legacy endpoint; an alias another record already claims; an alias that overlaps a new-form identity. Any identity that overlaps the alias of an existing record is refused, except the legacy endpoint that alias names. `store.Admit` is the same check with no alias.
* **Reader rules.** `ParseRecord` accepts `alias` on a new-form record only, and only when it is a valid legacy identity with an owner different from the record's own; on a legacy record it is an error. `Record.AliasIdentity` returns the legacy identity. Unknown fields stay ignored, so an older reader ignores `alias`. The field is optional and omitted when empty, so a record without it marshals as before.
* **Display.** `mash who --long` shows `alias OWNER/NAME`; `who --json` has an `alias` object only for a record that carries one, so its shape is unchanged for every other record ([ADR 0049](0049-who-filters-and-its-json-form-are-a-stable-shared-contract.md)).
* **Removal.** The field and its reading are removed in the step that removes the legacy form.

### Consequences

* Good, because admission can prove that no two records answer to one legacy name.
* Good, because the schema change is additive and readers from before it are unaffected.
* Bad, because the directory record carries a transitional field until the legacy form is removed.

### Confirmation

`internal/store` tests: record round trips, legacy records marshalling byte-identically, an older reader ignoring the field, and the admission matrix for each rule. `cmd/mash` tests: the `who` text and JSON forms, with the Phase 0 golden tests untouched.

## Pros and Cons of the Options

### An optional `alias` on the record (chosen)

* Good, because the alias lives with the identity it belongs to and is removed with it.
* Bad, because it is one more field in the record.

### A separate bucket or key for aliases

* Good, because the record schema stays as it is.
* Bad, because two stores must agree, and a failure between writes leaves an alias without a record.

### Derive the alias at read time

* Good, because nothing new is stored.
* Bad, because the legacy name is a choice, not a function of the new identity.

## More Information

* Code: `internal/store/store.go`, `internal/store/admit.go`.
* Related: [ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md), [ADR 0049](0049-who-filters-and-its-json-form-are-a-stable-shared-contract.md).
