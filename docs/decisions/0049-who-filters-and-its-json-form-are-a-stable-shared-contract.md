---
status: accepted
date: 2026-11-01
decision-makers: Randy Bias (mash maintainer)
---

# `who` filters and its JSON form are a stable, shared contract

## Context and Problem Statement

Finding an endpoint meant `mash who | grep -i -E 'nats|ops'`. `who` printed every endpoint, had no machine-readable form,
and the MCP `who` tool took no arguments, so an agent searched by running a shell or reading the whole directory (issue #100).
The directory record already carries `role`, `host`, `label`s and a `note` (set by the admin); what was missing was a way to
ask for a subset and to read the answer without parsing columns.

## Decision Drivers

* An agent must be able to ask "who matches X, online?" in one tool call, with no shell.
* The text output that scripts already parse must not change.
* The CLI and the MCP tool must answer the same question the same way.
* No change to the directory record or to permissions in this step.

## Considered Options

* Leave `who` as it is and document `grep`.
* Add filters and a JSON form to the CLI only.
* Add the same filters and a JSON form to the CLI and the MCP tool, through one shared filter.

## Decision Outcome

Chosen option: one shared filter, `endpoint.WhoFilter`, used by `mash who` and by the MCP `who` tool, because a second
implementation would drift and an agent should not need the shell.

* Filters AND together: `--name`, `--role` and `--match` are case-insensitive substrings; `--owner` is exact; `--label K`
  (key present) or `--label K=V` (exact, case-sensitive value), repeatable; `--online` or `--offline`. `--match` searches
  name, role, note and label keys and values.
* `--names` prints one name per line; `--json` prints an array of objects with the fields `name`, `owner`, `online`,
  `last_seen`, `role`, `host`, `note`, `labels` and, with `--long`, `lease`. Empty fields are omitted except `name`, `owner`
  and `online`. The field names are a contract: a field is added, never renamed or removed, within a minor release line.
* A filter with no match prints nothing (`[]` for JSON) and exits 0; a bad flag exits 2.
* The MCP tool takes the same filters as optional parameters (`match`, `name`, `role`, `label`, `online`, `format`);
  a call without arguments behaves as before.
* The default text output with no flags is unchanged.

### Consequences

* Good, because one call replaces a `grep`, for people and for agents.
* Good, because scripts get a documented JSON form instead of parsing tab columns.
* Bad, because the JSON field names are now a compatibility promise.

### Confirmation

Unit tests of the filter, the flag parser and the renderers; a CLI and MCP test against a test directory
(`cmd/mash/whocli_test.go`).

## Pros and Cons of the Options

### Leave `who` as it is and document `grep`

* Good, because nothing changes.
* Bad, because an agent has no shell-free way to search, and every caller parses columns.

### Filters and JSON in the CLI only

* Good, because it is the smaller change.
* Bad, because the agent's tool, the case that prompted the issue, would still list everything.

### The same filters and JSON through one shared filter

* Good, because both surfaces answer identically.
* Bad, because the filter semantics become a contract to keep stable.

## More Information

* Issue #100. Phase 2 (a description and capability words in the directory record) has its own ADR.
* Code: `internal/endpoint/whofilter.go`, `cmd/mash/whofilter.go`.
