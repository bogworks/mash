---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (mash maintainer)
---

# The directory record says what an endpoint does with a description and capabilities

## Context and Problem Statement

A directory record already carries a role (one string, 64 characters), a host, up to 8 labels and a note. That is enough
to say who an endpoint is, but not what it can do. Finding the endpoint for a task means reading free text with a grep,
and an endpoint that does several things has nowhere to say so. `mash who` now filters (issue #100, phase 1), so the
record needs something to filter on.

## Decision Drivers

* An endpoint can have several capabilities, and a caller wants to ask for one by name, exactly.
* A sentence of explanation needs more than 64 characters.
* Older mash versions must keep reading and writing records, and `set-meta` must not lose fields it does not know.
* Identity on record stays set by the admin: a stranger's mail must not be able to claim a capability.

## Considered Options

* Labels only (one value per key, 8 keys).
* A single free-text field only.
* Two new optional fields: `description` and `caps`.
* Endpoints advertise themselves.
* A role-lookup verb.

## Decision Outcome

Chosen option: two new optional record fields, because capabilities are a list of exact words (so they can be matched,
not guessed) and a description is prose, and the two have different limits.

* **`description`**: free text, at most 256 characters, printable, no tab or newline.
* **`caps`**: at most 8 words, each `[a-z][a-z0-9_.-]{0,31}`, no repeats, stored sorted.
* Both are set by the admin: `provision --describe TEXT --cap C...`; `set-meta --describe TEXT|-`, `--cap C` to add,
  `--uncap C` to remove and `--cap -` to clear. The limits are checked after the merge, as for labels, and `set-meta`
  keeps every stored field it does not know.
* `who --cap C` is an exact, repeatable filter whose values all have to be present; `--match` also searches the
  description and capabilities; `--long` and `--json` show both, and the `who` tool takes `cap`.

### Consequences

* Good, because an agent or script finds an endpoint by capability in one call, with no grep.
* Good, because the change is additive: older readers ignore the fields, and a record they rewrite through `set-meta`
  of a newer mash keeps them.
* Good, because the admin remains the only writer, so a capability on record is as trustworthy as the role.
* Bad, because the admin has to keep the words current and nothing checks that an endpoint does what its record says.
* Bad, because capability words are free-form: two admins can spell the same thing differently.

### Confirmation

Unit tests cover the validators, `provision` and `set-meta` (set, clear, add, remove, the limit after the merge, unknown
fields kept), the filter and the renderers; the CLI and MCP tests run `who --cap` against a live directory.

## Pros and Cons of the Options

### Labels only

* Good, because it needs no record change.
* Bad, because a label holds one value per key and there are only 8 keys, so a list of capabilities eats the budget.
* Bad, because a value is 64 characters, too short for a description.

### A single free-text field only

* Good, because it is one field and one limit.
* Bad, because a caller can only substring-match it, so `nats` also finds `no-nats`, and exact lookup is impossible.

### Two new optional fields

* Good, because each has the limits that suit it, and capabilities can be matched exactly.
* Good, because it is additive and backward compatible.
* Bad, because the record grows two more fields to document and keep valid.

### Endpoints advertise themselves

* Good, because the record would always be current.
* Bad, because an endpoint would need a new permission to write its own directory record.
* Bad, because it blurs admin-set identity with a self-declared claim, so mail from a stranger could say it handles
  anything. Rejected for now; it can come back as its own ADR if the admin-set record proves not to be enough.

### A role-lookup verb

* Good, because "find the endpoint for X" would be one command.
* Bad, because a verb must choose one of several matches, and that choice is the caller's policy. `who --cap X --online
  --names` already gives the candidates.

## More Information

* Issue #100. Code: `internal/store` (Record, ValidCaps, ValidDescription), `internal/admin` (Meta, MetaUpdate),
  `internal/endpoint/whofilter.go`. Guide: "Directory metadata" in `docs/operations.md`.
