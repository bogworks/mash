---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# A deny rule that selects nobody is an error unless the rule says `allow_empty`; `add` and `reissue` gate on the file's tests

## Context and Problem Statement

[ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md) makes the policy file
the one source of live permissions. Three paths let a file that should be refused still change them (issue #180):

1. Only `mashctl policy apply` ran the file's `tests:`. `mashctl add` and `mashctl reissue` mint a user JWT whose
   exception tags take effect when the user connects, with no apply, so a file whose tests fail could still produce
   live permissions.
2. A `from` or `to` selector that matches no endpoint was a warning. Under `default: allow` a misspelled label in a
   deny rule selects nobody, the pair falls through to allow, and `apply` accepted the file when it had no tests.
3. `policy check` and `label` accepted zones of 33 to 48 characters (the endpoint-name rule), while `add`, `set-zone`
   and `apply` refuse them because `sap.MaxZoneLen` is 32 ([ADR 0054](0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md)).
   A `label` edit could leave a file `apply` refuses.

`mashctl`'s command line is frozen, so any override must live in the policy file.

## Decision Drivers

* A deny rule exists to stop mail; one that stops nothing is almost always a typo, and under `default: allow` it fails open.
* Every verb that mints credentials or changes live permissions from the file should refuse the same files.
* An intentionally empty group (a zone with no endpoints yet) must stay expressible.
* `check` must agree with what the verbs that follow it accept.
* No new verb or flag on `mashctl`.

## Considered Options

* Keep the warning, and tell people to write tests.
* Make a zero-match deny selector an error, with an optional `allow_empty: true` key on the rule as the override; gate
  `add` and `reissue` on the tests as `apply` does; validate the zone with `sap.ValidZone`.
* The same, with the override as a `mashctl` flag.

## Decision Outcome

Chosen option: the second.

* `policy.Check` reports an error, not a warning, when a deny rule's `from` or `to` selector matches no declared
  endpoint. The message names the rule, the selector and the line, keeps the "(zone X has no endpoints)" hint, and says
  `set allow_empty: true on the rule if the group is intentionally empty`. A rule with `allow_empty: true` gets the
  warning again. An allow rule keeps the warning: a selector that matches nobody there removes access, which fails
  closed. The key is carried through the file like `log`, and has no effect on the compiled permissions.
* `add` and `reissue` run the same test gate as `apply` (one shared helper, the same `FAIL` lines and the same
  `refusing: N of M policy tests in FILE fail (...)` message) on the exact bytes they mint from, after `policy.Parse`
  has run `Check` on them, and before any key, record or creds file is made or changed.
* `policy check` validates the zone label value with `sap.ValidZone` (1 to 32 characters). Endpoint names stay 1 to 48.

### Consequences

* Good: a misspelled deny selector is caught by `check`, `apply`, `add` and `reissue` alike.
* Good: a file whose tests fail cannot mint a credential.
* Good: `check` and `label` no longer accept a zone that `add`, `set-zone` and `apply` refuse.
* Bad: a file that had a deny rule matching nobody now fails to parse until the rule is fixed or marked `allow_empty: true`.
* Bad: `add` and `reissue` of a file with failing tests now refuse where they used to succeed.

### Confirmation

* Policy package tests: a deny zero-match is an error, an allow zero-match and a deny with `allow_empty` are warnings,
  `allow_empty` survives a file edit, a 32-character zone passes and a 33-character one is refused.
* `mashctl` tests: `add` and `reissue` of a file whose tests fail change nothing and exit 1, and succeed once the tests pass.

## Pros and Cons of the Options

### Keep the warning

* Good, because no existing file changes meaning.
* Bad, because the warning is easy to miss and a no-test file under `default: allow` fails open.
* Bad, because `add` and `reissue` stay outside the gate.

### Error with `allow_empty` in the file, a shared gate, `sap.ValidZone`

* Good, because the override is visible in the reviewed file, next to the rule it excuses.
* Good, because one gate means one answer to "may this file change permissions".
* Bad, because existing files with such a rule need an edit.

### Override as a `mashctl` flag

* Good, because the file stays untouched.
* Bad, because it adds a flag to a frozen command line, and the override would not travel with the file or be reviewed with it.

## More Information

Amends [ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md) (the check's
errors and warnings) and relies on the zone token of [ADR 0054](0054-mail-subjects-carry-a-zone-token-legacy-subjects-stay-valid.md);
their files are unchanged. See `docs/policy-file.md`.
