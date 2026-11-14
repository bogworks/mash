---
status: accepted
date: 2026-11-14
decision-makers: Randy Bias (mash maintainer)
---

# The AT-011 test and its record live in internal/policyapply

Amends: ADR-0097

## Context and Problem Statement

[ADR 0097](0097-a-template-change-on-a-scoped-signing-key-closes-open-connections-and-binds-the-reconnect.md) names the test `TestAT011TemplateChangeReachesOpenConnections` in `internal/policy`, its record `internal/policy/testdata/at011-result.json`, and the update command `MASH_AT011_UPDATE=1 go test -run TestAT011 ./internal/policy/`. Zone removal ([ADR 0091](0091-there-are-no-zones-mail-keeps-the-five-token-subject-and-the-address-is-the-fence.md), step Z3) deleted the policy package's compiler. The test needs a nats-server and no policy code, so it moved unchanged, and the paths in ADR 0097 no longer exist.

## Decision Drivers

* A record must name files that exist, and the release check must keep proving the property.

## Considered Options

* Leave ADR 0097's paths as they are.
* Move the test and record and amend ADR 0097 with the new paths.

## Decision Outcome

The test is `internal/policyapply/at011_test.go` and its record is `internal/policyapply/testdata/at011-result.json`. The update command is `MASH_AT011_UPDATE=1 go test -run TestAT011 ./internal/policyapply/`. The test stays `required` in `scripts/releasecheck/policytests.txt`. The property ADR 0097 records, that a template change on a scoped signing key closes open connections and binds the reconnect, is unchanged; only the location is. Where this ADR and ADR 0097 differ on a path or the command, this one governs. ADR 0097's file is not edited.

Chosen option: move the test and record and amend ADR 0097 with the new paths.

### Consequences

* Good: the paths and the command in the record point at files that exist.
* Good: the release check keeps requiring the test, so the property stays proven.
* Neutral: a reader who starts from ADR 0097 must follow its amendment to find the test.

### Confirmation

`scripts/releasecheck/policytests.txt` lists `AT-011  ./internal/policyapply  TestAT011TemplateChangeReachesOpenConnections  required`, which the release check runs.

## Pros and Cons of the Options

### Leave ADR 0097's paths as they are

* Bad, because they point at files that no longer exist.

### Move and amend (chosen)

* Good, because ADR 0097 stays immutable and the new paths are on record.

## More Information

* Amends [ADR 0097](0097-a-template-change-on-a-scoped-signing-key-closes-open-connections-and-binds-the-reconnect.md); its file is unchanged.
