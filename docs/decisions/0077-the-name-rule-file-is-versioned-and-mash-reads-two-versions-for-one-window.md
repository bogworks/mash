---
status: accepted
date: 2026-11-10
decision-makers: Randy Bias (mash maintainer)
---

# The name-rule file is versioned, and mash reads two versions for one window

## Context and Problem Statement

A deployment may need to refuse some identities that mash's own syntax accepts, for example a retired agent token. mash
must not hard-code what a token means, so the refusal comes from a file the deployment supplies. Two sides depend on
that file's format: mash, which reads it, and the tooling that writes it. If either changes the format alone, endpoints
fail to start or, worse, start without the rule. Where is the format defined, and how does it change?

## Decision Drivers

* mash applies rules as given and gives no token a meaning of its own.
* A rule that fails to load must stop the endpoint, never silently reopen what it closed.
* The writer and the reader change on different schedules, so there must be a window in which both versions work.

## Considered Options

* A versioned JSON file, defined in `docs/name-rule.md`, read at start, with a two-version compatibility window (chosen).
* A rule held in the directory or the policy file.
* No version: readers ignore unknown keys.

## Decision Outcome

The format is published as `docs/name-rule.md`, titled `name-rule@1`. mash owns the schema and its compatibility; the
deployment owns the content. The file is named by `MASH_NAME_RULE_FILE` or `name_rule_file`, read once at start, with the
environment winning over the file key. An unreadable or invalid file, an unknown key, an unsupported version, or a
required rule that is missing, refuses start. `mash status` shows the path, the first 12 hex digits of the SHA-256 of
the file and the rule count, or says that no rule is set.

mash reads version n and n-1 of the rule file for one compatibility window. The window is the release that introduces
n plus the next minor release; after that, n-1 refuses start with a named error. Any other version refuses start. Only
version 1 exists today.

Unknown keys are errors, so a format change that adds a key raises the version. The directory and the policy file are
not used: anyone who can write the store could change the first, and the second is tied to open questions about zones.

### Consequences

* Good, because the format has one definition that both sides cite.
* Good, because a bad or missing file stops start and cannot reopen a closed form.
* Good, because the digest in `status` lets a check confirm every endpoint loaded the expected file.
* Bad, because a change takes a restart.
* Bad, because every format change that adds a key needs a version and a window.

### Confirmation

The loader's tests (a later change) cover each condition in "What refuses start", the whole-token wrapping, the status
line, and version n-1 and unknown-version handling. This record is docs only; `docs/name-rule.md` is the normative text.

## Pros and Cons of the Options

### A versioned JSON file read at start (chosen)

* Good, because it is simple, auditable and tied to a restart.
* Bad, because a change needs a restart.

### A rule held in the directory or the policy file

* Good, because a change would not need a restart.
* Bad, because anyone who can write the store could change the rule.

### No version, ignore unknown keys

* Good, because the writer can add keys freely.
* Bad, because an old reader would silently ignore a rule it does not understand.

## More Information

* Format: `docs/name-rule.md`.
* Related: [ADR 0074](0074-an-endpoint-is-three-sap-tokens-and-keys-its-storage-on-their-flat-form.md).
