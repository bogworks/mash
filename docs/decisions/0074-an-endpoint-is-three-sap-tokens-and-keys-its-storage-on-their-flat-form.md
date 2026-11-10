---
status: proposed
date: 2026-11-10
decision-makers: Randy Bias (mash maintainer)
---

# An endpoint is three SAP tokens and keys its storage on their flat form

## Context and Problem Statement

An endpoint is today an owner and a name. The owner is one setting per deployment, the agent token is the constant `cc`
([ADR 0005](0005-subjects-follow-sap-hierarchy.md)), mail may carry a sixth, zone token
([ADR 0054](0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md)), and every stored identifier is keyed on
the name alone: the inbox stream, the reply prefix, the directory and lease keys, the user name. Two endpoints that share
a name under different owners therefore collide, and a deployment that wants its agent and owner tokens to differ per
endpoint cannot say so. The name rule ([ADR 0036](0036-names-are-lower-case-hyphenated-words-up-to-48-characters.md)) is
48 characters with no recorded reason. How does an endpoint carry SAP's three identity tokens, and what do its storage
keys look like?

## Decision Drivers

* SAP's subject is `agents.<verb>.<agent>.<owner>.<name>` on every verb; mash should carry each token as given and give
  none a meaning of its own.
* Two new-form endpoints that differ in any one token must never share a key.
* Endpoints that exist today must keep their subjects, streams and keys, so no stored mail moves.
* A subject, a stream name and a file name must stay safe for every value mash accepts.

## Considered Options

* An identity of three tokens, with a flat form that joins them with underscores (chosen).
* Keep `(owner, name)` and add the agent token as a per-deployment setting.
* Encode the three tokens in one dash-joined name.

## Decision Outcome

An endpoint's identity is the three tokens `Agent`, `Owner` and `Name` (`sap.EndpointIdentity`). A new-form identity carries all
three as given, on every verb: `agents.mail.<agent>.<owner>.<name>`, with the agent token literal and never a wildcard
in grants or filters. Each token is lower-case ASCII letters, digits and single dashes, starts with a letter, does not end
with a dash, and is at most 32 characters; the two separators count towards a total of 64. These are mash's own bounds on
subjects and stream names, replacing the 48-character cap of ADR 0036 for new-form identities. mash does not read the
tokens as anything but SAP's agent, owner and name, and holds no list of agent names.

Where one flat identifier is needed (stream, consumer scope, key, reply prefix, user name, file name) it is the three
tokens joined by underscores. No token contains an underscore, so the mapping is injective and reverses by splitting.

A legacy identity is `(cc, <owner>, <name>)` under today's rule (48 characters for owner and name). It keeps its subjects
and its keys exactly, with the bare name as its flat form, so nothing stored for it moves. The legacy flat form is the bare name, so two legacy identities with the same name under
different owners still share it, exactly as today; that limitation stays until the legacy form is removed. This type
covers the unzoned legacy subjects; the zoned mail form of ADR 0054 stays as it is. This keeps legacy identities
working while new-form ones are introduced; removing them is a later decision.

This record is proposed. It becomes accepted with the change that makes new-form identities usable; the removal of the
legacy forms and the zone token, which supersedes the layout parts of ADR 0005 and ADR 0054, is a later record.

### Consequences

* Good, because two new-form endpoints that differ in one token never share a key.
* Good, because the legacy layout is untouched, so no mail is lost or moved.
* Good, because mash stays mechanism: it validates token syntax and nothing about what a token means.
* Bad, because mash carries two identity forms until the legacy form is removed.
* Bad, because the 32-character token bound is new and is not a reading of any stored data.

### Confirmation

`internal/sap/identity_test.go`: token vectors, the 64-character total bound, the flat form's injectivity (exhaustive over
a small alphabet and a seeded property test), the round trip, and legacy identities producing today's subjects, filter
and key. `internal/sap/golden_test.go` and the other characterisation tests pin today's legacy identifiers and validity.

## Pros and Cons of the Options

### An identity of three tokens, with a flat form (chosen)

* Good, because it matches SAP's subject and keeps the tokens separate.
* Bad, because every key derived from the name must move to the flat form for new-form identities.

### Keep `(owner, name)` and add the agent token as a setting

* Good, because it changes little.
* Bad, because the owner and agent stay one value per deployment, which is what the change is meant to lift.

### Encode the three tokens in one dash-joined name

* Good, because a name stays one string.
* Bad, because dashes are legal inside every token, so the name cannot be split back into its parts.

## More Information

* Code: `internal/sap/identity.go`.
* Related: [ADR 0005](0005-subjects-follow-sap-hierarchy.md), [ADR 0036](0036-names-are-lower-case-hyphenated-words-up-to-48-characters.md), [ADR 0054](0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md).
