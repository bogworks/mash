---
status: accepted
date: 2026-11-25
decision-makers: Randy Bias (mash maintainer)
---

# `who --json` prints every field, null when unset, and fields are only added

Amends: ADR-0049

## Context and Problem Statement

[ADR 0049](0049-who-filters-and-its-json-form-are-a-stable-shared-contract.md) says `who --json` omits empty fields except
`name`, `owner` and `online`. The code never did: `mash who --json` has printed every field for every endpoint, an unset
`role`, `host` or `description` as `null`, an empty `capability` as `[]` and empty `labels` as `{}`
(`TestWhoJSONCarriesEveryField`). The field names in the ADR (`name`, `last_seen`, `note`, `lease`) also differ from the
shipped ones (`address`, `online`, `role`, `host`, `description`, `capability`, `labels`). The spec rule "In --json, one
name for one fact" (#1042) says an unset field is null and never left out, with `who` as its one exception. What does
`who --json` promise?

## Decision Drivers

* A script must see the same keys on every run.
* The shipped output is what scripts already read; changing it breaks them.
* ADR 0049's promise that a field is added and never renamed or removed is the part callers rely on.

## Considered Options

* Keep ADR 0049's omission clause and change the code to omit empty fields.
* Record that `who --json` prints every field, null when unset, and keep the rule's exception to the name `capability`.

## Decision Outcome

Chosen option: record what the code does. `who --json` prints every field for every endpoint; an unset scalar is `null`,
an empty list `[]` and an empty map `{}`. A field is added, never renamed or removed, as ADR 0049 says. The list key stays
`capability` (singular), the one exception to the rule that a list's key is plural. ADR 0049's clause "Empty fields are
omitted except `name`, `owner` and `online`" no longer holds.

### Consequences

* Good, because `who --json` follows the same rule as every other command: the keys are the same on every run.
* Good, because no script that reads `who --json` today breaks.
* Bad, because the key is the one list that is singular, kept for compatibility.

### Confirmation

`TestWhoJSONCarriesEveryField` (`internal/cmdcore/cmds/who_test.go`) pins every field and its null; the `who` x-json and the
`--json` case in `spec/mash.json` agree (`TestXJSONAgreesWithXOutput`).

## Pros and Cons of the Options

### Keep ADR 0049's omission clause and change the code

* Good, because the text and the code agree again.
* Bad, because it changes output that scripts read today, and the keys then differ from run to run.

### Record what the code does

* Good, because nothing breaks and the keys are fixed.
* Bad, because ADR 0049's omission clause is amended.

## More Information

* Amends ADR 0049 for its omission clause only; the rest of ADR 0049 stands.
* Issue #1042, the rule in `spec/rules.json` ("In --json, one name for one fact").
