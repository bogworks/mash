---
status: accepted
date: 2026-11-02
decision-makers: Randy Bias (mash maintainer)
---

# Every zone key carries both tag templates and every user the `deny:_none` placeholder

## Context and Problem Statement

[ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md) compiles exceptions to
user tags expanded by two templates in the zone key, `agents.mail.*.<owner>.{{tag(allow)}}` (publish allow) and
`agents.mail.*.<owner>.{{tag(deny)}}` (publish deny). The first compile added the deny template to a zone's key only
when some pair of the zone needed deny encoding, and gave the users of such a zone the placeholder tag `deny:0`.
nats-server refuses to authenticate a user with no deny tag when the key has the deny template in a deny list. So the
first deny exception in a zone changed the zone key and made every user of the zone stale: the whole zone had to be
re-signed for one exception.

## Decision Drivers

* A policy edit should re-sign as few users as it can.
* nats-server fails a user with no `deny:` tag under a key with the deny template; this cannot be configured away.
* A placeholder must address nothing.

## Considered Options

* Keep the deny template and the placeholder only for zones that need them (the previous behaviour).
* Every zone key carries both templates from its first compile, and every user carries the placeholder `deny:_none`.

## Decision Outcome

Chosen option: every zone key carries both templates always, and every compiled endpoint carries the tag `deny:_none`
besides its real `allow:` and `deny:` tags. `_none` is a valid subject token and no endpoint or zone name can be
`_none` (names start with a letter), so `agents.mail.*.<owner>._none` addresses nothing. A tag template expands to one
line per tag value, so a user with `deny:_none` and `deny:billing.hr-agent` gets two deny lines. The first deny
exception of a zone changes only the user JWTs of the endpoints it names. This replaces the placeholder `deny:0` of
ADR 0053's first implementation.

### Consequences

* Good: the zone key never changes because an exception appears; `reissue needed` names only the endpoints whose tags
  change.
* Good: scoped users still carry an empty permission block of their own; `mint` asserts it.
* Bad: every user JWT is one tag (about 30 bytes) larger, and every zone key one deny line larger.
* Bad: a deployment minted under the earlier compile has stale zone keys and users, and the ORDER matters: first run
  `mashctl reissue --changed` (every user gains `deny:_none`; under the old zone key, which has no deny template, the
  extra tag is harmless), and only then `mashctl policy apply` (the new key carries the deny template, and a user
  without a deny tag fails to authenticate once it does). Applying first would lock out every not-yet-reissued user
  when it next reconnects.

### Confirmation

* The policy live test connects users of a zone with no deny exception, adds one for a single endpoint, and checks the
  zone key template is unchanged and only that endpoint's tags differ.

## Pros and Cons of the Options

### Templates and placeholder only where a zone needs them

* Good, because users of a zone without deny exceptions carry no extra tag.
* Bad, because the first deny exception changes the zone key and makes every user of the zone stale.

### Both templates on every key, the placeholder on every user

* Good, because a zone key is stable from its first compile, and an exception re-signs only the endpoints it names.
* Bad, because every user JWT and every zone key is slightly larger.

## More Information

Amends [ADR 0053](0053-who-may-mail-whom-is-a-policy-file-compiled-by-mashctl-into-nats-permissions.md) for the tag
templates and the placeholder; its file is unchanged. The sizes are in `docs/policy-file.md`.
