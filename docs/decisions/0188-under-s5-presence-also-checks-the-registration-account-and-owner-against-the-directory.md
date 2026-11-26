---
status: accepted
date: 2026-11-26
decision-makers: Randy Bias (mash maintainer)
---

# Under S5 presence also checks the registration's account and owner against the directory

Amends: ADR-0066

## Context and Problem Statement

[ADR 0066](0066-mash-serve-asks-who-is-online-when-someone-asks-instead-of-listening-to-every-heartbeat.md) says a
registration in a presence answer counts "only when its identity signature verifies and its key is the one the
directory has for that name". Nothing else in the registration is compared with the directory: a registration that
names another account, another owner, or a status subject as its prompt endpoint counts as long as the signature holds
and the key matches. REQ-016 and amendment 5 (i) of the SDK adoption contract ask for more. What does a presence answer
have to show before an endpoint counts as online?

## Decision Drivers

* An endpoint counts as online only when the directory vouches for the registration, not when someone holds a valid
  signature over whatever they chose to claim.
* A registration under another owner token must not mark this owner's endpoint online.
* A record that holds no account vouches for no account.
* 0.5.x discovery does not change before the 0.6.0 switch-on.

## Considered Options

* Keep ADR 0066's rule: signature and directory key only.
* Add the account, owner and verb checks to presence, behind the S5 hook (chosen).

## Decision Outcome

Chosen: under S5 the account and owner checks join presence, together with the verb check. A registration is verified
only when all of these hold:

* the identity signature verifies over the user_nkey, account, agent, owner and prompt subject (as in ADR 0066);
* the directory has a record for the address the subject names;
* the record's own User key is the registration's user_nkey (the key the record holds now, not one it held before it
  was replaced);
* the record's account is not empty and equals the registration's account;
* `metadata.owner` equals the record's owner;
* the subject's verb is `prompt`, so a signed status, heartbeat or mail subject does not mark an agent online.

An answer that carries a registration failing any of these is counted once as not verified. The step runs outside the
endpoint's lock on a copy of the directory record, and the record is read again before the last-seen time is set.

The checks sit in `internal/sapsdk/discovery` (`Verify`) and run through the S5 hook in `internal/endpoint`. The hook
is nil in 0.5.x, so presence there is ADR 0066's own; the rule takes effect at the 0.6.0 switch-on.

This amends the sentence of ADR 0066 quoted above. The rest of ADR 0066 stands.

### Consequences

* Good, because a registration that the directory does not vouch for, in account, owner or verb, no longer counts
  as online.
* Good, because a replaced key stops counting at once: the record's own key is compared, not an index.
* Bad, because at switch-on a registration under another owner token stops counting, and a foreign agent is counted as
  not verified.
* Bad, because a record with no account never verifies.
* Neutral, because 0.5.x is unchanged: the hook is nil in production until the switch-on.

### Confirmation

* `internal/sapsdk/discovery/discovery_test.go`: `TestVerify` (a signed status, heartbeat or mail subject, another
  owner, another account and a replaced key are each not verified) and `TestVerifyRecordWithoutAccount`.
* `internal/endpoint`: `TestS5ReplacedKeyDoesNotVerify`, `TestS5PresenceRechecksTheRecordAfterVerify`, and the S5
  acceptance tests (`TestS5AT015_OwnerDifferenceRefused`, `TestS5Off_DiscoveryIsMains` for the inert case).

## Pros and Cons of the Options

### Keep ADR 0066's rule

* Good, because nothing changes.
* Bad, because a valid signature over a claimed account, owner or verb is enough to count.

### Add the checks behind the S5 hook

* Good, because the directory vouches for every part of the registration that counts.
* Good, because 0.5.x does not change.
* Bad, because a stricter rule can refuse a registration that counted before, at switch-on.

## More Information

* Amends [ADR 0066](0066-mash-serve-asks-who-is-online-when-someone-asks-instead-of-listening-to-every-heartbeat.md).
* REQ-016 and amendment 5 (i) of the SDK adoption contract (TO-12).
* Code: `internal/sapsdk/discovery/discovery.go` (`Verify`), `internal/endpoint/s5hook.go` (`applyPresenceS5`).
