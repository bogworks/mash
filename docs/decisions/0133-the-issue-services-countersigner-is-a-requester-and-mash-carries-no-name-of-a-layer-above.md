---
status: proposed
date: 2026-11-15
decision-makers: Randy Bias (mash maintainer)
---

# The issue service's countersigner is a requester and mash carries no name of a layer above

Amends: ADR-0104

## Context and Problem Statement

[ADR 0104](0104-an-issue-service-signs-a-credential-for-a-public-key-a-wrangler-countersigned.md) lets the issue service
sign a credential for a public key that a registered key countersigned. It called that key by a name taken from a layer
above mash. In mash the thing is small and has nothing to do with that layer: a registered public key that may ask the
service to sign a credential for a public key it holds, for one host and one owner group. The name was a field of the
request, a verb of `mashctl`, the name of a Go package and of a key store file, and it appeared in error texts, docs and
test fixtures. A few other words of the same layer (its words for a group of agents, for a unit of work and for the
product that wraps mash) were left in comments, test names and one decoder's package comment.

The scrub list (`scripts/scrubcheck/words.txt`) guards the repository's history and portability; it does not scan
shipped Go sources and docs for the words of a layer above. [ADR 0122](0122-applying-a-list-set-checks-that-an-added-address-has-its-inbox-and-makes-nothing.md) is the last decision that touched the
countersigner's inputs and kept the former name. What should the countersigner be called, and what stops the words from
coming back?

## Decision Drivers

* mash works alone and must not depend on, or carry the vocabulary of, anything that wraps it.
* A flag, subject, field, key, error text or test fixture that carries such a word is a defect.
* Nothing is deployed, so there is nothing to migrate and no old name needs to keep working.
* The signed bytes of an issue request are a security surface: renaming must not change them.
* A rule that only a reviewer remembers will be broken; it needs a test.

## Considered Options

* Rename the concept to requester everywhere, drop the other words, and add a test that refuses them.
* Rename the field and verb only, and leave comments and fixtures.
* Keep the former name and explain it in the glossary.

## Decision Outcome

Chosen option: "Rename the concept to requester everywhere, drop the other words, and add a test that refuses them",
because it is the only option that leaves no word of a layer above in shipped code, and the test keeps it so.

* The countersigner is a **requester**. The request field is `requester` (the requester's public key, replacing the
  former field), the verb is `mashctl requester init --test-account | add --host H --group G --key PUB | list | remove
  --host H --group G`, the Go package is `internal/requesterreg` (`RequesterRegistry`), the key store file is
  `requesters.json`, and the refusal for an unknown key reads `that requester key is not registered for this host and
  owner group`.
* The proof prefix stays `mash-issue-v2` and the canonical bytes keep their layout: the prefix never contained the
  former name, so a signature is exactly as valid as before.
* Comments, test names, fixtures and the non-ADR docs lose the other words: a live connection, a test, a peer of the same
  owner. The package comment of `policydoc` says the list-set document is the one value per account that an outside
  system may submit and mash applies, and that its schema value and field names are mash's own.
* `internal/release/forbidden_words_test.go` fails when any of the former names appears as a whole word in a non-test Go
  file or in the docs, outside the ADRs, plans, specs, evidence, the change log and its fragments, and the scrub list.
* ADRs are history and are not edited; ADR 0104 keeps its name and text.

### Consequences

* Good: mash's source and docs read as mash's own; a request, a verb or a file with the former name is refused or absent.
* Good: the guard turns the standing rule into a failing test.
* Bad: any operator tooling written against the former field, verb or file name must change. There is none deployed.
* Bad: the guard knows the words it forbids, so the test file itself, and the ADRs that record the history, are exempt.

### Confirmation

* `internal/release/forbidden_words_test.go` scans every tracked non-test Go file and every doc that is not history.
* `cmd/mashctl` tests drive `mashctl requester` and assert the new texts; the issue tests assert the `requester` field and
  the unchanged canonical bytes (`mash-issue-v2`).
* `go run ./scripts/adrlint docs` and `go test ./internal/release/` pass on the change.

## Pros and Cons of the Options

### Rename everywhere and add a test

* Good: no word remains; the rule is enforced.
* Bad: one breaking change of a field, a verb and a file name, which costs nothing while nothing is deployed.

### Rename the field and verb only

* Good: smaller diff.
* Bad: the words stay in comments and fixtures and are copied from there into the next change.

### Keep the former name

* Good: no change.
* Bad: it keeps a word of a layer above in mash, against the maintainer's ruling.

## More Information

* Amends [ADR 0104](0104-an-issue-service-signs-a-credential-for-a-public-key-a-wrangler-countersigned.md): only the name
  of the countersigner; what the service signs, and why, is unchanged.
* Related: [ADR 0122](0122-applying-a-list-set-checks-that-an-added-address-has-its-inbox-and-makes-nothing.md).
